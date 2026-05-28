package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/composeview"
	"github.com/kungfusheep/mail/effects"
	"github.com/kungfusheep/mail/helpdialog"
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/mailruntime"
	"github.com/kungfusheep/mail/omnibox"
	"github.com/kungfusheep/mail/senderid"
	"github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/transition"
	"github.com/kungfusheep/mail/ui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "cache" {
		if err := runCacheCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

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
	model.ThemeName = "dark"
	mb.SetNotifiers(model.Notify, model.NotifyError)

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
		ApplyTheme: func(name string, palette theme.Theme) {
			model.ThemeName = name
			model.ApplyTheme(palette)
			composeTransition.SetColors(palette.BG, Hex(0x3a3a3a))
			editor.SetTheme(theme.ComposeTheme(palette))
			app.RequestRender()
		},
	})

	app.OnBeforeRender(func() {
		model.ProcessPending()
		omniBox.BeforeRender()
		model.UpdatePreviewScroll()
	})

	app.OnResize(func(width, height int) {
		omniBox.Resize(width, height)
		model.UpdateStatusOverlay()
	})

	shade := Animate.Duration(380 * time.Millisecond).Ease(EaseOutCubic)

	folderShade := effects.NewFocusShade(&model.FolderPaneRef).
		Strength(In(shade(0.38)).Out(shade(0.0))).
		Dodge(&model.HelpRef, omniBox.Ref())

	threadShade := effects.NewFocusShade(&model.ThreadPaneRef).
		Strength(In(shade(0.38)).Out(shade(0.0))).
		Dodge(&model.HelpRef, omniBox.Ref())

	app.View("main",
		VBox.PaddingTRBL(0, 0, 0, 2)(

			ScreenEffect(composeTransition.SourceEffect()),
			ScreenEffect(composeTransition.TargetEffect()),

			If(&model.Pane).Ne(mailbox.FolderPane).Then(
				ScreenEffect(folderShade),
			),
			If(&model.Pane).Ne(mailbox.ThreadPane).Then(
				ScreenEffect(threadShade),
			),

			HBox.Grow(1).Gap(4)(

				// left folder nav
				VBox.Grow(1).PaddingTRBL(1, 0, 0, 0).NodeRef(&model.FolderPaneRef).CascadeStyle(&model.FolderStyle)(
					HBox(
						Text("mail").Bold(),
						SpaceW(2),
						Text(&model.FolderTitle).Dim(),
					),
					SpaceH(2),
					List(mb.FolderNames()).
						Selection(&model.FolderSel).
						Style(&model.FolderListStyle).
						SelectedStyle(&model.FolderSelStyle).
						Marker("● "),
					If(&model.Pane).Eq(mailbox.FolderPane).Then(
						On(
							Key("j", model.FolderDown),
							Key("k", model.FolderUp),
							Key("<Enter>", model.EnterFolder),
						),
					),
				),

				// threads list
				VBox.Grow(3).Fill(&model.ThreadBG).PaddingTRBL(1, 0, 0, 0).NodeRef(&model.ThreadPaneRef).CascadeStyle(&model.ThreadStyle)(
					HBox(
						SpaceW(3),
						Text(&model.FolderTitle).FG(&model.Accent).Bold(),
						SpaceW(1),
						Text(&model.ThreadUnreadText).Dim(),
						Space(),
						Text("Newest ▾").Dim(),
						SpaceW(2),
					),
					SpaceH(2),
					List(mb.ThreadRows()).
						Selection(&model.ThreadSel).
						Style(&model.ThreadListStyle).
						SelectedStyle(Style{}).
						Marker("  ").
						Render(func(row *mailbox.ThreadRow) Component {
							itemBG := If(&row.Selected).Then(&model.SelBG).Else(
								If(&row.Grouped).
									Then(&model.GroupBG).
									Else(&model.ThreadBG),
							)
							return VBox.Fill(&model.ThreadBG).PaddingTRBL(0, 1, 0, 0)(
								If(&row.HasGroup).Then(
									VBox.Fill(&model.ThreadBG).PaddingTRBL(1, 0, 0, 1)(
										Text(&row.GroupLabel).FG(&model.Accent).Dim().Bold(),
									),
								),
								VBox.Fill(itemBG).PaddingVH(1, 2)(
									HBox(
										If(&row.Unread).Then(Text("●").FG(&model.Accent)).Else(Text(" ")),
										SpaceW(1),
										HBox.Grow(1)(
											Text(&row.Label).Style(
												If(&row.Unread).
													Then(Style{Attr: AttrBold}).
													Else(Style{})),
											SpaceW(1),
											If(&row.Starred).Then(Text("★").FG(&model.Accent)),
										),
										SpaceW(2),
										Text(&row.Date).Dim(),
									),
									HBox(
										SpaceW(2),
										If(&row.HasSenderColor).Then(
											HBox(
												Text("▐").Style(&row.SenderStyle),
												SpaceW(1),
											),
										),
										Text(&row.Sender).Dim(),
										SpaceW(2),
										If(&row.HasDraft).Then(Text("draft").FG(&model.Accent).Italic()),
									),
									If(&row.HasAttachments).Then(
										HBox.Gap(1).Fill(itemBG).PaddingTRBL(0, 0, 0, 2)(
											ForEach(&row.Attachments, func(chip *mailbox.AttachmentChip) Component {
												return HBox.Width(22).Border(BorderSoft).BorderFG(
													If(&row.Selected).Then(&model.SelBG).Else(
														If(&row.Grouped).Then(&model.GroupBG).Else(&model.ThreadBG),
													),
												).Fill(attachmentFill(&chip.FillName, t)).PaddingVH(0, 1)(
													Text(&chip.Icon),
													SpaceW(1),
													Text(&chip.Filename),
												)
											}),
											If(&row.HasAttachmentOverflow).Then(
												HBox.Width(5).Border(BorderSoft).BorderFG(
													If(&row.Selected).Then(&model.SelBG).Else(
														If(&row.Grouped).Then(&model.GroupBG).Else(&model.ThreadBG),
													),
												).Fill(&model.GroupBG).PaddingVH(0, 1)(
													Text(&row.AttachmentOverflow).Bold(),
												),
											),
										),
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
				VBox.Grow(3).PaddingTRBL(1, 0, 0, 0).NodeRef(&model.PreviewPaneRef).CascadeStyle(&model.PreviewStyle)(

					ScrollView.
						Grow(1).
						Fill(&model.BG).
						Scrollbar().
						ScrollbarTrackStyle(&model.PreviewScrollTrack).
						ScrollbarThumbStyle(&model.PreviewScrollThumb).
						ScrollbarOpacity(Animate(If(&model.PreviewScrollVisible).Then(1.0).Else(0.0))).
						Ref(func(sv *ScrollViewC) {
							model.SetConversationView(sv)
						})(
						SpaceH(2),
						ForEach(mb.ConversationMessages(), func(msg *mailbox.ConversationMessage) Component {
							return VBox(
								If(&msg.ThreadScanMode).Then(
									VBox(
										HBox(
											If(&msg.HasSenderColor).Then(
												HBox(
													Text("▐").Style(&msg.SenderStyle),
													SpaceW(1),
												),
											),
											Text(&msg.Sender).FG(&model.Bright).Bold(),
											SpaceW(2),
											Text(&msg.Date).Dim(),
										),
										If(&msg.HasScanSubject).Then(
											Text(&msg.ScanSubjectLine).FG(&model.Subtle),
										),
									),
								).Else(
									VBox(
										If(&msg.HasSubject).Then(
											Text(&msg.Subject).FG(&model.Bright).Bold(),
										),
										headerMetaRow("at", &msg.Date, model),
										HBox(
											Text("from").FG(&model.Dim),
											SpaceW(1),
											If(&msg.HasSenderColor).Then(
												HBox(
													Text("▐").Style(&msg.SenderStyle),
													SpaceW(1),
												),
											),
											Text(&msg.FromLine).FG(&model.Subtle),
											If(&msg.HasUnsubscribe).Then(SpaceW(1)),
											If(&msg.HasUnsubscribe).Then(Rich(&msg.UnsubscribeChip)),
										),
										If(&msg.HasTo).Then(headerMetaRow("to", &msg.ToLine, model)),
										If(&msg.HasCC).Then(headerMetaRow("cc", &msg.CCLine, model)),
										If(&msg.HasBCC).Then(headerMetaRow("bcc", &msg.BCCLine, model)),
									),
								),
								If(&msg.HasAttachments).Then(
									VBox.PaddingTRBL(1, 0, 0, 0).Gap(1)(
										ForEach(&msg.Attachments, func(attachment *mailbox.AttachmentRow) Component {
											return HBox.Border(BorderSoft).BorderFG(&model.BG).Fill(attachmentFill(&attachment.Filename, t)).PaddingVH(0, 1)(
												Rich(&attachment.Display),
											)
										}),
									),
								),
								SpaceH(1),
								VBox(
									ForEach(&msg.BodyBlocks, func(block *mailbox.PreviewBodyBlock) Component {
										return previewBodyBlock(block)
									}),
								),
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

			model.Compose.InlineView(&model.PreviewPaneRef),

			// notifications
			If(&model.StatusVisible).Then(
				Overlay.BottomRight().Offset(-2, -1)(
					VBox.Width(49).Gap(1)(
						ForEach(model.StatusItems(), func(item *ui.Notification) Component {
							return notificationRow(item, model)
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
				Text("/").FG(&model.Bright).Bold(),
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
		ThreadsChanged: model.QueueThreadsChanged,
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

func notificationRow(item *ui.Notification, model *mailbox.UI) Component {
	return HBox.Width(49).Opacity(&item.Opacity)(
		Space(),
		Text("● ").FG(
			Match(&item.Kind,
				Eq(ui.NotificationSuccess, &model.Success),
				Eq(ui.NotificationWarning, &model.Warning),
				Eq(ui.NotificationError, &model.Error),
				Eq(ui.NotificationAction, &model.Accent),
			).Default(&model.Info),
		),
		Text(&item.Text).FG(&model.Bright),
	)
}

func runCacheCommand(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mail cache sender <inspect|clear|refresh> <domain-or-email>")
	}
	switch args[0] {
	case "sender":
		return runCacheSenderCommand(args[1:])
	default:
		return fmt.Errorf("unknown cache command %q", args[0])
	}
}

func runCacheSenderCommand(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: mail cache sender <inspect|clear|refresh> <domain-or-email>")
	}
	action := args[0]
	domain := normalizeSenderDomain(args[1])
	if domain == "" {
		return fmt.Errorf("sender domain is empty")
	}

	db, err := openCache()
	if err != nil {
		return err
	}
	defer db.Close()

	switch action {
	case "inspect":
		identity, ok, err := db.SenderIdentity(domain)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Printf("%s: no cached sender identity\n", domain)
			return nil
		}
		printSenderIdentity(identity)
		return nil
	case "clear":
		if err := db.DeleteSenderIdentity(domain); err != nil {
			return err
		}
		fmt.Printf("%s: sender identity cache cleared\n", domain)
		return nil
	case "refresh":
		if err := db.DeleteSenderIdentity(domain); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		identity := senderid.Enricher{}.Enrich(ctx, domain)
		if identity.Domain == "" {
			return fmt.Errorf("%s: sender identity refresh failed", domain)
		}
		if err := db.PutSenderIdentity(identity); err != nil {
			return err
		}
		printSenderIdentity(identity)
		return nil
	default:
		return fmt.Errorf("unknown sender cache command %q", action)
	}
}

func openCache() (*cache.Cache, error) {
	if cachePath := os.Getenv("MAIL_CACHE_PATH"); cachePath != "" {
		return cache.NewAt(cachePath)
	}
	return cache.New()
}

func normalizeSenderDomain(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "@") {
		return senderid.DomainFromEmail(raw)
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
		raw = parsed.Host
	}
	raw = strings.TrimPrefix(raw, "www.")
	raw = strings.TrimSuffix(raw, ".")
	return raw
}

func printSenderIdentity(identity cache.SenderIdentity) {
	fmt.Printf("domain: %s\n", identity.Domain)
	fmt.Printf("display name: %s\n", identity.DisplayName)
	fmt.Printf("icon url: %s\n", identity.IconURL)
	fmt.Printf("theme colour: %s\n", identity.ThemeColor)
	fmt.Printf("bimi logo url: %s\n", identity.BIMILogoURL)
	fmt.Printf("source: %s\n", identity.Source)
	fmt.Printf("confidence: %d\n", identity.Confidence)
	fmt.Printf("colour checked: %s\n", formatCLITime(identity.ColorCheckedAt))
	fmt.Printf("updated: %s\n", formatCLITime(identity.UpdatedAt))
}

func formatCLITime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func previewBodyBlock(block *mailbox.PreviewBodyBlock) Component {
	return previewBodyBlockFrame(block, &block.Style)
}

func previewBodyBlockFrame(block *mailbox.PreviewBodyBlock, style *Style) Component {
	return VBox.CascadeStyle(style)(
		If(&block.HasSpaceBefore).Then(
			SpaceH(1),
		),
		If(&block.HasExtraSpaceBefore).Then(
			SpaceH(1),
		),
		Rich(&block.Spans),
	)
}

func headerMetaRow(label string, value *string, model *mailbox.UI) Component {
	return HBox(
		Text(label).FG(&model.Dim),
		SpaceW(1),
		Text(value).FG(&model.Subtle),
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
		Where(func(name string) bool { return attachmentNameHas(name, ".ics") }, tone(Hex(0x5fae95))),
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
