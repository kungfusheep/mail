package main

import (
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/composeview"
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/mailboxmodel"
	"github.com/kungfusheep/mail/mailruntime"
	"github.com/kungfusheep/mail/omnibox"
	"github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/transition"
	"github.com/kungfusheep/mail/ui"
)

func main() {
	logFile, err := os.OpenFile(
		filepath.Join(os.TempDir(), "mail.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644,
	)
	if err == nil {
		log.SetOutput(logFile)
		// redirect stderr to the log so panics/runtime errors don't corrupt the TUI
		syscall.Dup2(int(logFile.Fd()), int(os.Stderr.Fd()))
		defer logFile.Close()
	}
	log.Println("starting mail")

	themet := theme.Dark()

	app := NewApp()
	app.SetDefaultStyle(Style{FG: themet.FG, BG: themet.BG})

	var db *cache.Cache
	if cachePath := os.Getenv("MAIL_CACHE_PATH"); cachePath != "" {
		db, err = cache.NewAt(cachePath)
	} else {
		db, err = cache.New()
	}
	if err != nil {
		log.Fatal(err)
	}
	// sweep any leftover empty-body drafts from earlier writes
	_ = db.GCEmptyDrafts()

	offline := os.Getenv("MAIL_OFFLINE") == "1"
	email := os.Getenv("MAIL_EMAIL")
	var cfg imap.Config
	if !offline {
		cfg, err = imap.LoadConfig()
		if err != nil {
			log.Fatal(err)
		}
		email = cfg.Email
	}
	if email == "" {
		email = "me@example.test"
	}

	mb := mailbox.New(db, email)
	var smtpClient *smtp.SMTP
	if !offline {
		smtpClient = smtp.New(smtp.Config{
			Server:   cfg.SMTPServer,
			Email:    cfg.Email,
			Password: cfg.Password,
		})
	}

	// load from cache
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	// Tell the cache which IMAP folder id represents Drafts — draft-table
	// writes publish on that label so the mailbox subscriber refreshes the
	// UI. Without this wiring the drafts view silently goes stale.
	if draftsID := mb.FolderIDByDisplayName("Drafts"); draftsID != "" {
		db.SetDraftsLabel(draftsID)
	}
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.SetSelected(0)
	mb.LoadConversation(0, nil)

	t := themet
	model := mailboxmodel.New(mailboxmodel.Config{
		App:     app,
		Cache:   db,
		Mailbox: mb,
		Theme:   t,
	})

	// continuous frame requests for time-based animation
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			app.RequestRender()
		}
	}()

	// transition: mailbox → compose ("settle and rise")
	composeTransition := transition.New(940*time.Millisecond, t.BG, Hex(0x3a3a3a))

	// compose view — editor theme matches mail palette so the compose view and
	// mailbox share the same dark background. Earlier we left Background unset
	// because it seemed to trigger a top-row-loss bug; that turned out to be
	// the emoji/wide-rune width issue (fixed in glyph).
	composeTheme := theme.ComposeTheme(t)

	editor := compose.NewEditor(compose.NewDocument(), "")
	editor.SetTheme(composeTheme)
	editor.SetApp(app)
	editor.StartSpellResultWorker(app.RequestRender)
	comp := composeview.Setup(app, editor, mb, smtpClient, db, model.Notify, &model.Frame, composeTransition, t)
	model.SetCompose(comp)

	var rt *mailruntime.Runtime

	omniBox := omnibox.New(omnibox.Config{
		App:   app,
		Theme: t,
		Model: model,
	})

	app.OnBeforeRender(omniBox.BeforeRender)

	app.OnResize(func(width, height int) {
		omniBox.Resize(width, height)
		model.UpdateStatusOverlay()
	})

	fade := Animate
	accentMarker := Style{FG: t.Accent}

	// kv backs the help modal's key→description rows; IIFEs in the template
	// spread this into a ForEach for rendering.
	type kv struct{ key, desc string }

	// peakColor := Hex(0x242424)

	app.View("main",
		VBox.PaddingTRBL(0, 2, 0, 2)(

			ScreenEffect(composeTransition.SourceEffect()),
			ScreenEffect(composeTransition.TargetEffect()),

			HBox.Grow(1).Gap(4)(

				// left folder nav
				VBox.Grow(1).PaddingTRBL(1, 0, 0, 0).CascadeStyle(&model.FolderStyle)(
					HBox(
						Text("mail").FG(t.Bright).Bold(),
						SpaceW(2),
						Text(&model.FolderTitle).FG(t.Subtle),
					),
					SpaceH(2),
					List(mb.FolderNames()).
						Selection(&model.FolderSel).
						Style(fade(&model.FolderListStyle)).
						SelectedStyle(fade(&model.FolderSelStyle)).
						Marker("● ").MarkerStyle(accentMarker),
					If(&model.Pane).Eq(mailboxmodel.FolderPane).Then(
						On(
							Key("j", model.FolderDown),
							Key("k", model.FolderUp),
							Key("<Enter>", model.EnterFolder),
						),
					),
				),

				// threads list
				VBox.Grow(3).Fill(t.ThreadBG).PaddingTRBL(1, 0, 0, 0).CascadeStyle(&model.ThreadStyle)(
					HBox(
						SpaceW(3),
						Text(&model.FolderTitle).FG(t.Accent).Bold(),
						SpaceW(1),
						Text(&model.ThreadUnreadText).FG(t.Subtle),
						Space(),
						Text("Newest ▾").FG(t.Subtle),
						SpaceW(2),
					),
					SpaceH(2),
					List(mb.ThreadRows()).
						Selection(&model.ThreadSel).
						Style(fade(&model.ThreadListStyle)).
						SelectedStyle(Style{}).
						Marker("  ").
						Render(func(row *mailbox.ThreadRow) Component {
							itemBG := If(&row.Selected).Then(t.SelBG).Else(
								If(&row.Grouped).
									Then(t.GroupBG).
									Else(t.ThreadBG),
							)
							return VBox.Fill(t.ThreadBG).PaddingTRBL(0, 1, 0, 0)(
								If(&row.HasGroup).Then(
									VBox.Fill(t.ThreadBG).PaddingTRBL(1, 0, 0, 1)(
										Text(&row.GroupLabel).FG(t.Accent).Dim().Bold(),
									),
								),
								VBox.Fill(itemBG).PaddingVH(1, 2)(
									HBox(
										If(&row.Unread).Then(Text("●").FG(t.Accent)).Else(Text(" ")),
										SpaceW(1),
										HBox.Grow(1)(
											Text(&row.Label).Style(
												If(&row.Unread).
													Then(Style{Attr: AttrBold}).
													Else(Style{})),
											SpaceW(1),
											If(&row.Starred).Then(Text("★").FG(t.Accent)),
										),
										SpaceW(2),
										Text(&row.Date).Dim(),
									),
									HBox(
										SpaceW(2),
										Text(&row.Sender).Dim(),
										SpaceW(2),
										If(&row.HasDraft).Then(Text("draft").FG(t.Accent).Italic()),
									),
								),
							)
						}),
					If(&model.Pane).Eq(mailboxmodel.ThreadPane).Then(
						On(
							Key("j", model.ThreadDown),
							Key("k", model.ThreadUp),
							Key("<Enter>", model.Enter),
							Key("o", model.ToggleThread),
							Key("r", model.ReplySelected),
							Key("a", model.ArchiveSelected),
							Key("d", model.DeleteSelected),
							Key("s", model.ToggleStarSelected),
							Key("e", model.ToggleReadSelected),
							Key("u", model.UndoLast),
						),
					),
				),

				// preview window
				VBox.Grow(3).PaddingTRBL(1, 0, 0, 0).CascadeStyle(&model.PreviewStyle)(
					ScrollView.Grow(1).Ref(func(sv *ScrollViewC) {
						model.SetConversationView(sv)
					})(
						SpaceH(2),
						ForEach(mb.ConversationMessages(), func(msg *mailbox.ConversationMessage) Component {
							return VBox(
								HBox(
									Text(&msg.Sender).Style(
										If(&msg.IsMe).
											Then(Style{Attr: AttrBold, FG: t.Accent}).
											Else(Style{Attr: AttrBold}),
									),
									SpaceW(1),
									Text(&msg.Date).Dim(),
								),
								SpaceH(1),
								Rich(&msg.BodySpans),
								SpaceH(1),
							)
						}),
					),
					If(&model.Pane).Eq(mailboxmodel.PreviewPane).Then(
						On(
							Key("j", model.PreviewDown),
							Key("k", model.PreviewUp),
						),
					),
				),
			),

			// notifications
			If(&model.StatusVisible).Then(
				Overlay.BottomRight().Offset(-2, -1)(
					VBox.Width(44).FitContent().Gap(1)(
						ForEach(model.StatusItems(), func(item *ui.Notification) Component {
							return Text(&item.Text).
								FG(t.Bright).
								Opacity(&item.Opacity).
								Width(44).
								Style(Style{Align: AlignRight})
						}),
					),
				),
			),

			// omnibox
			omniBox.View(),

			// help modal — ? toggles. Vignette subtly darkens the rest of
			// the screen; the modal itself is dodged so it stays crisp.
			If(&model.HelpOpen).Then(
				Overlay.Centered()(
					VBox.
						Width(56).
						Fill(t.BG).
						PaddingVH(1, 2).
						NodeRef(&model.HelpRef).
						Opacity(
							In(Animate(1.0)).Out(Animate(0)),
						).
						Gap(1)(
						Text("keyboard").FG(t.Bright).Bold(),
						HBox(
							func() Component {
								rows := []kv{
									{"j / k", "up / down"},
									{"h / l", "pane left / right"},
									{"tab", "next pane"},
									{"enter", "open"},
									{"o", "expand thread"},
									{"/", "search"},
								}
								return VBox.Grow(3)(
									Text("navigate").FG(t.Subtle),
									ForEach(&rows, func(r *kv) Component {
										return HBox.Gap(2)(Text(&r.key).FG(t.FG).Width(8), Text(&r.desc).FG(t.Subtle))
									}),
								)
							}(),
							func() Component {
								rows := []kv{
									{"c", "compose"},
									{"C", "resume draft"},
									{"r", "reply"},
									{"a", "archive"},
									{"d", "delete"},
									{"s", "star"},
									{"e", "toggle read"},
									{"u", "undo"},
								}
								return VBox.Grow(2)(
									Text("actions").FG(t.Subtle),
									ForEach(&rows, func(r *kv) Component {
										return HBox.Gap(2)(Text(&r.key).FG(t.FG).Width(3), Text(&r.desc).FG(t.Subtle))
									}),
								)
							}(),
						),
						ScreenEffect(
							SEVignette().Strength(
								In(
									Animate.From(0)(0.55),
								).Out(

									Animate(0),
								),
							).Dodge(&model.HelpRef).Smooth(),
							SEDropShadow().Focus(&model.HelpRef),
						),
					),
				),
			),
			On(
				Key("q", app.Stop),
				Key("?", model.ToggleKeyboardHelp),
				Key("<C-p>", omniBox.Open),
				Key("<Space>", omniBox.Open),
				Key("h", model.FocusLeft),
				Key("l", model.FocusRight),
				Key("<Tab>", model.FocusNext),
				Key("<S-Tab>", model.FocusPrev),
				Key("<Escape>", model.Escape),
				Key("c", model.ComposeNew),
				Key("C", model.ResumeDraft),
				Key("/", model.StartSearch),
			),
		),
	).NoCounts()

	app.View("search",
		VBox(
			HBox(
				Text("/").FG(t.Bright).Bold(),
				Text(&model.SearchQuery),
			),
			On(
				Key("<CR>", model.SubmitSearch),
				Key("<Esc>", model.CancelSearch),
				Key("<BS>", model.BackspaceSearch),
			),
		),
	).NoCounts()

	if searchRouter, ok := app.ViewRouter("search"); ok {
		searchRouter.HandleUnmatched(model.AppendSearchKey)
	}

	rt = mailruntime.New(db, mb, mailruntime.Config{
		Backend: !offline,
		IMAP:    cfg,
	}, mailruntime.Callbacks{
		Status:         model.Notify,
		FoldersChanged: model.FoldersChanged,
		ThreadsChanged: model.ThreadsChanged,
		Render:         app.RequestRender,
	})
	model.SetRuntime(rt)
	rt.Start()
	defer rt.Close()

	// spinner
	go func() {
		for range time.Tick(80 * time.Millisecond) {
			model.TickFrame()
		}
	}()

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}
