package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/composeview"
	"github.com/kungfusheep/mail/helpdialog"
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
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

	mb := mailbox.NewState(db, email)
	mb.SetLinkOpener(mailbox.OpenSystemURL)
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
	model := mailbox.NewUI(mailbox.UIConfig{
		App:   app,
		Cache: db,
		State: mb,
		Theme: t,
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
	comp := composeview.Setup(app, editor, mb, smtpClient, db, model.NotifyCompose, &model.Frame, composeTransition, t)
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
					If(&model.Pane).Eq(mailbox.FolderPane).Then(
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
					If(&model.Pane).Eq(mailbox.ThreadPane).Then(
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
							Key("g", model.ThreadTop),
							Key("G", model.ThreadBottom),
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
								If(&msg.HasAttachments).Then(
									VBox.PaddingTRBL(1, 0, 0, 0).Gap(1)(
										ForEach(&msg.Attachments, func(attachment *mailbox.AttachmentRow) Component {
											return HBox.Border(BorderSoft).BorderFG(t.BG).Fill(attachmentFill(&attachment.Filename, t)).PaddingVH(0, 1)(
												Text(&attachment.Icon).FG(t.Bright),
												SpaceW(1),
												Text(&attachment.Filename).FG(t.Bright).Bold(),
											)
										}),
									),
								),
								SpaceH(1),
								Rich(&msg.BodySpans),
								SpaceH(1),
							)
						}),
					),

					If(&model.Pane).Eq(mailbox.PreviewPane).Then(
						On(
							Key("j", model.PreviewDown),
							Key("k", model.PreviewUp),
							Key("d", model.PreviewHalfPageDown),
							Key("u", model.PreviewHalfPageUp),
							Key("g", model.PreviewTop),
							Key("G", model.PreviewBottom),
						),
					),
				),
			),

			// notifications
			If(&model.StatusVisible).Then(
				Overlay.BottomRight().Offset(-2, -1)(
					VBox.Width(49).Gap(1)(
						ForEach(model.StatusItems(), func(item *ui.Notification) Component {
							return notificationRow(item, t)
						}),
					),
				),
			),

			// omnibox
			omniBox.View(),

			helpdialog.Mailbox(&model.HelpOpen, &model.HelpRef, t),
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
				Key(";", app.EnterJumpMode),
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
		Status:         model.NotifyRuntime,
		FoldersChanged: model.FoldersChanged,
		ThreadsChanged: model.ThreadsChanged,
		Render:         app.RequestRender,
	})
	model.SetRuntime(rt.WatchActiveFolder, rt.SyncActiveFolder)
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

func notificationRow(item *ui.Notification, t theme.Theme) Component {
	return HBox.Width(49).Opacity(&item.Opacity)(
		Space(),
		Text("● ").FG(
			Match(&item.Kind,
				Eq(ui.NotificationSuccess, t.Success),
				Eq(ui.NotificationWarning, t.Warning),
				Eq(ui.NotificationError, t.Error),
				Eq(ui.NotificationAction, t.Accent),
			).Default(t.Info),
		),
		Text(&item.Text).FG(t.Bright),
	)
}

func attachmentFill(filename *string, t theme.Theme) *MatchC[string] {
	return attachmentTone(filename, ReadableTint(t.BG, t.Subtle, t.Bright, 4.5, 0.20), func(base Color) Color {
		return ReadableTint(t.BG, base, t.Bright, 4.5, 0.40)
	})
}

func attachmentTone(filename *string, fallback Color, tone func(Color) Color) *MatchC[string] {
	return Match(filename,
		Where(func(name string) bool { return attachmentNameHas(name, ".pdf") }, tone(Hex(0xd65f5f))),
		Where(func(name string) bool { return attachmentNameHasAny(name, ".doc", ".docx") }, tone(Hex(0x5f8fd6))),
		Where(func(name string) bool { return attachmentNameHasAny(name, ".xls", ".xlsx", ".csv") }, tone(Hex(0x6fbf7a))),
		Where(func(name string) bool { return attachmentNameHasAny(name, ".ppt", ".pptx") }, tone(Hex(0xd68a5f))),
		Where(func(name string) bool { return attachmentNameHasAny(name, ".png", ".jpg", ".jpeg", ".gif", ".webp") }, tone(Hex(0x8f7ad6))),
		Where(func(name string) bool {
			return attachmentNameHasAny(name, ".mp3", ".wav", ".m4a", ".mov", ".mp4", ".mkv")
		}, tone(Hex(0x63bfc7))),
		Where(func(name string) bool { return attachmentNameHasAny(name, ".zip", ".tar", ".gz") }, tone(Hex(0xc99b55))),
	).Default(fallback)
}

func attachmentNameHas(name, suffix string) bool {
	return strings.HasSuffix(strings.ToLower(name), suffix)
}

func attachmentNameHasAny(name string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if attachmentNameHas(name, suffix) {
			return true
		}
	}
	return false
}
