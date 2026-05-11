package main

import (
	"fmt"
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
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/mailcommands"
	"github.com/kungfusheep/mail/mailruntime"
	"github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/transition"
	"github.com/kungfusheep/mail/ui"
	"github.com/kungfusheep/riffkey"
)

func undoMessage(desc string) string {
	switch {
	case strings.HasPrefix(desc, "deleted "):
		return "restored " + strings.TrimPrefix(desc, "deleted ")
	case strings.HasPrefix(desc, "archived "):
		return "restored " + strings.TrimPrefix(desc, "archived ")
	default:
		return "undid " + desc
	}
}

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

	// inbox view state
	type undoItem struct {
		run     func()
		desc    string
		message string
	}

	var (
		folderSel        int
		threadSel        int
		labelsOpen       bool
		helpOpen         bool
		helpRef          NodeRef
		frame            int
		folderTitle      = "Inbox"
		searchQuery      string
		threadUnreadText string
		statusVisible    bool

		// pane styles — active uses FG, inactive uses dim
		folderStyle     = Style{FG: t.Dim}
		threadStyle     = Style{FG: t.FG}
		previewStyle    = Style{FG: t.Dim}
		folderListStyle = Style{FG: t.Dim}
		folderSelStyle  = Style{FG: t.Dim}
		threadListStyle = Style{FG: t.FG}
		pane            = 1

		undoStack []undoItem
	)

	statusFeed := ui.NewFeed(time.Now)
	updateStatusOverlay := func() {
		statusFeed.Update()
		items := statusFeed.Items()
		statusVisible = len(*items) > 0
	}
	notify := func(text string) {
		statusFeed.Push(text)
		updateStatusOverlay()
		app.RequestRender()
	}

	updateFocus := func() {
		folderStyle = Style{FG: t.Dim}
		threadStyle = Style{FG: t.Dim}
		folderListStyle = Style{FG: t.Dim}
		folderSelStyle = Style{FG: t.Dim}
		threadListStyle = Style{FG: t.Dim}
		previewStyle = Style{FG: t.Dim}
		switch pane {
		case 0:
			folderStyle = Style{FG: t.FG}
			folderListStyle = Style{FG: t.FG}
			folderSelStyle = Style{FG: t.Bright}
		case 1:
			threadStyle = Style{FG: t.FG}
			threadListStyle = Style{FG: t.FG}
		case 2:
			previewStyle = Style{FG: t.FG}
		}
	}

	updateThreadHeader := func() {
		unread := mb.ActiveFolderUnread()
		if unread <= 0 {
			threadUnreadText = ""
			return
		}
		threadUnreadText = fmt.Sprintf("%d unread", unread)
	}
	updateThreadHeader()

	// shimmer catalogue — navigate with , and . (prev/next). Label shows
	// in the status bar. Only the active variant is included in the view
	// tree (via If), so inactive ones pay no per-frame cost.
	var (
		// wormholeSpeed float64 = 6.0
		shimmerIdx   int = 0
		shimmerLabel     = ""

		// wormhole family toggles
		// onWormhole, onWStreaks, onWPulse, onWDepth, onWLayered, onWShear, onWLab, onSilEcho bool
	)
	// focused on wormhole family while we iterate; other effects are preserved
	// in effects.go but removed from the view template below.
	variantNames := []string{
		"wormhole (base)",
		"wormhole warp",
		"wormhole drag",
		"wormhole surge",
		"wormhole core",
		"wormhole turbulence (baseline)",
		"wormhole lab (iterating)",
		"silhouette echo",
	}

	// applyVariant := func() {
	// 	onWormhole, onWStreaks, onWPulse, onWDepth, onWLayered, onWShear, onWLab, onSilEcho = false, false, false, false, false, false, false, false
	// 	shimmerLabel = variantNames[shimmerIdx]
	// 	wormholeSpeed = 6.0
	// 	switch shimmerIdx {
	// 	case 0:
	// 		onWormhole = true
	// 	case 1:
	// 		onWStreaks = true
	// 	case 2:
	// 		onWPulse = true
	// 	case 3:
	// 		onWDepth = true
	// 	case 4:
	// 		onWLayered = true
	// 	case 5:
	// 		onWShear = true
	// 	case 6:
	// 		onWLab = true
	// 	case 7:
	// 		onSilEcho = true
	// 	}
	// }
	// applyVariant()

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
	comp := composeview.Setup(app, editor, mb, smtpClient, db, notify, &frame, composeTransition, t)

	var convView *ScrollViewC
	var loadPreview func()
	var rt *mailruntime.Runtime

	loadPreview = func() {
		mb.LoadConversation(threadSel, func() {
			if convView != nil {
				convView.Refresh()
			}
			app.RequestRender()
		})
		if convView != nil {
			convView.Refresh()
		}
		app.RequestRender()
	}

	handleEnter := func() {
		// Drafts folder: Enter resumes the draft in the composer rather than
		// opening a preview — the thread is a draft-in-progress, not a
		// conversation to read. Thread id in this folder is the stable
		// local draft id; ResumeDraft reads straight from the drafts table.
		if mb.ActiveFolderCanonical() == "Drafts" {
			if t := mb.SelectedThread(threadSel); t != nil {
				comp.ResumeDraft(t.ID)
			}
			return
		}
		if msg := mb.SelectedMessage(threadSel); msg != nil {
			mb.LoadPreview(*msg, app.Size().Width)
			mb.MarkRead(threadSel)
			updateThreadHeader()
			pane = 2
			updateFocus()
			return
		}
		mb.ToggleThread(threadSel)
		mb.MarkRead(threadSel)
		updateThreadHeader()
		loadPreview()
	}

	pushUndo := func(undo func(), desc string) {
		if undo != nil {
			undoStack = append(undoStack, undoItem{
				run:     undo,
				desc:    desc,
				message: undoMessage(desc),
			})
			notify(desc + " — u to undo")
			return
		}
		if desc != "" {
			notify(desc)
		}
	}

	clampThreadSel := func() {
		if threadSel >= mb.ThreadLen() {
			threadSel = mb.ThreadLen() - 1
		}
		if threadSel < 0 {
			threadSel = 0
		}
		mb.SetSelected(threadSel)
	}

	loadFolder := func() {
		if folderSel == mb.CanonEnd() {
			return
		}
		actualIdx := folderSel
		if folderSel > mb.CanonEnd() {
			actualIdx = folderSel - 2
		}
		if actualIdx >= mb.FolderCount() {
			return
		}
		mb.SelectFolder(actualIdx)
		mb.LoadThreads()
		mb.BuildThreadDisplay()
		threadSel = 0
		mb.SetSelected(0)
		updateThreadHeader()
		loadPreview()
		undoStack = nil
		folderTitle = mb.FolderName(folderSel)
		if rt != nil {
			rt.WatchActiveFolder()
			rt.SyncActiveFolder()
		}
	}

	startSearch := func() {
		searchQuery = ""
		app.HideCursor()
		app.PushView("search")
	}

	var (
		omniboxOpen    bool
		omniboxEmpty   = true
		omniboxMaxRows = 6
		omniboxItems   []mailcommands.Command
		omniboxList    *FilterListC[mailcommands.Command]
	)

	updateOmniboxLayout := func(width, height int) {
		h := height * 6 / 10
		if h < 10 {
			h = 10
		}
		if h > height-4 {
			h = height - 4
		}
		if h < 6 {
			h = 6
		}
		// Each command row is a bordered two-line VBox, so it consumes four
		// rows plus the parent gap. Use the 60% screen-height band only as a
		// max-row budget; the omnibox background itself sizes to visible rows.
		rows := (h - 6) / 5
		if rows < 1 {
			rows = 1
		}
		omniboxMaxRows = rows
		_ = width
	}

	size := app.Size()
	updateOmniboxLayout(size.Width, size.Height)
	updateStatusOverlay()

	refreshOmniboxState := func() {
		omniboxEmpty = omniboxList == nil || omniboxList.Filter().Len() == 0
	}

	app.OnBeforeRender(func() {
		if omniboxOpen {
			refreshOmniboxState()
		}
	})

	app.OnResize(func(width, height int) {
		updateOmniboxLayout(width, height)
		updateStatusOverlay()
		if omniboxList != nil {
			omniboxList.MaxVisible(omniboxMaxRows)
		}
	})

	openOmnibox := func() {
		if omniboxOpen {
			return
		}
		if omniboxList != nil {
			omniboxList.Clear()
		}
		refreshOmniboxState()
		omniboxOpen = true
		app.HideCursor()
		app.RequestRender()
	}

	closeOmnibox := func() {
		if !omniboxOpen {
			return
		}
		if omniboxList != nil {
			omniboxList.Clear()
		}
		refreshOmniboxState()
		omniboxOpen = false
		app.HideCursor()
		app.RequestRender()
	}

	moveOmnibox := func(delta int) {
		if omniboxList == nil || omniboxList.Filter().Len() == 0 {
			return
		}
		before := omniboxList.Selected()
		if delta > 0 {
			omniboxList.SelectNext()
			if omniboxList.Selected() == before {
				for range omniboxList.Filter().Len() {
					omniboxList.SelectPrev()
				}
			}
			return
		}
		omniboxList.SelectPrev()
		if omniboxList.Selected() == before {
			for range omniboxList.Filter().Len() {
				omniboxList.SelectNext()
			}
		}
	}

	pageOmnibox := func(delta int) {
		if omniboxList == nil || omniboxList.Filter().Len() == 0 {
			return
		}
		if delta > 0 {
			omniboxList.PageDown()
		} else {
			omniboxList.PageUp()
		}
	}

	firstOmnibox := func() {
		if omniboxList == nil {
			return
		}
		for range omniboxList.Filter().Len() {
			omniboxList.SelectPrev()
		}
	}

	lastOmnibox := func() {
		if omniboxList == nil {
			return
		}
		for range omniboxList.Filter().Len() {
			omniboxList.SelectNext()
		}
	}

	threadAction := func(label string, fn func()) {
		if mb.ThreadLen() == 0 {
			notify(label + ": no thread selected")
			return
		}
		fn()
	}

	omniboxItems = mailcommands.Build(mailcommands.Actions{
		ComposeNew:  func() { comp.Open() },
		ResumeDraft: func() { comp.ResumeLast() },
		ReplySelected: func() {
			threadAction("reply", func() {
				if t := mb.SelectedThread(threadSel); t != nil {
					if row := mb.ThreadRowAt(threadSel); row != nil && row.MsgIdx < 0 && mb.ActiveFolderCanonical() == "Drafts" {
						comp.ResumeDraft(t.ID)
						return
					}
					comp.Open()
					comp.SetupReply(*t)
				}
			})
		},
		RefreshMail: func() {
			notify("syncing...")
			if rt != nil {
				rt.SyncActiveFolder()
			}
		},
		ToggleFolders: func() {
			labelsOpen = !labelsOpen
			mb.BuildFolderDisplay(labelsOpen)
			if folderSel >= mb.FolderLen() {
				folderSel = mb.FolderLen() - 1
			}
			if folderSel < 0 {
				folderSel = 0
			}
			updateThreadHeader()
			notify("folders toggled")
		},
		FocusFolders: func() {
			pane = 0
			updateFocus()
		},
		FocusThreads: func() {
			pane = 1
			updateFocus()
		},
		FocusPreview: func() {
			pane = 2
			updateFocus()
		},
		SearchMail:   startSearch,
		OpenSelected: handleEnter,
		ArchiveSelected: func() {
			threadAction("archive", func() {
				pushUndo(mb.Archive(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				updateThreadHeader()
				loadPreview()
			})
		},
		DeleteSelected: func() {
			threadAction("delete", func() {
				pushUndo(mb.Delete(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				updateThreadHeader()
				loadPreview()
			})
		},
		ToggleStar: func() {
			threadAction("star", func() {
				pushUndo(mb.ToggleStar(threadSel))
			})
		},
		ToggleRead: func() {
			threadAction("read", func() {
				pushUndo(mb.ToggleRead(threadSel))
				mb.BuildFolderDisplay(labelsOpen)
				updateThreadHeader()
			})
		},
		UndoLast: func() {
			if len(undoStack) == 0 {
				notify("nothing to undo")
				return
			}
			item := undoStack[len(undoStack)-1]
			item.run()
			undoStack = undoStack[:len(undoStack)-1]
			clampThreadSel()
			mb.BuildFolderDisplay(labelsOpen)
			updateThreadHeader()
			loadPreview()
			notify(item.message)
		},
		ShowKeyboardHelp: func() { helpOpen = true },
		Quit:             func() { app.Stop() },
	})
	omniboxList = FilterList(&omniboxItems, func(cmd *mailcommands.Command) string {
		return cmd.Label + " " + cmd.Description + " " + cmd.Key + " " + cmd.Section
	}).
		Placeholder("type a command").
		MaxVisible(omniboxMaxRows).
		Marker("  ").
		Style(Style{BG: t.BG}).
		SelectedStyle(Style{FG: t.Bright, BG: t.SelBG}).
		Render(func(cmd *mailcommands.Command) Component {
			return VBox.PaddingVH(1, 2)(
				HBox(
					Text(&cmd.Label).FG(t.Bright),
					Space(),
					Text(&cmd.Key).FG(t.Subtle),
				),
				HBox(
					Text(&cmd.Section).FG(t.Accent).Width(12),
					Text(&cmd.Description).FG(t.Subtle),
				),
			)
		})
	refreshOmniboxState()

	execOmnibox := func() {
		if omniboxList == nil {
			return
		}
		cmd := omniboxList.Selected()
		if cmd == nil {
			return
		}
		action := cmd.Action
		closeOmnibox()
		if action != nil {
			action()
		}
	}

	fade := Animate
	accentMarker := Style{FG: t.Accent}

	// kv backs the help modal's key→description rows; IIFEs in the template
	// spread this into a ForEach for rendering.
	type kv struct{ key, desc string }

	// peakColor := Hex(0x242424)

	var omniboxRef NodeRef

	app.View("main",
		VBox.PaddingTRBL(0, 2, 0, 2)(

			ScreenEffect(composeTransition.SourceEffect()),
			ScreenEffect(composeTransition.TargetEffect()),

			HBox.Grow(1).Gap(4)(

				// left folder nav
				VBox.Grow(1).PaddingTRBL(1, 0, 0, 0).CascadeStyle(&folderStyle)(
					HBox(
						Text("mail").FG(t.Bright).Bold(),
						SpaceW(2),
						Text(&folderTitle).FG(t.Subtle),
						SpaceW(2),
						Text("·").FG(t.Muted),
						SpaceW(2),
						Text(&shimmerLabel).FG(t.Accent).Italic(),
					),
					SpaceH(2),
					List(mb.FolderNames()).
						Selection(&folderSel).
						Style(fade(&folderListStyle)).
						SelectedStyle(fade(&folderSelStyle)).
						Marker("● ").MarkerStyle(accentMarker),
				),

				// threads list
				VBox.Grow(3).Fill(t.ThreadBG).PaddingTRBL(1, 0, 0, 0).CascadeStyle(&threadStyle)(
					HBox(
						SpaceW(3),
						Text(&folderTitle).FG(t.Accent).Bold(),
						SpaceW(1),
						Text(&threadUnreadText).FG(t.Subtle),
						Space(),
						Text("Newest ▾").FG(t.Subtle),
						SpaceW(2),
					),
					SpaceH(2),
					List(mb.ThreadRows()).
						Selection(&threadSel).
						Style(fade(&threadListStyle)).
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
				),

				// preview window
				VBox.Grow(3).PaddingTRBL(1, 0, 0, 0).CascadeStyle(&previewStyle)(
					ScrollView.Grow(1).Ref(func(sv *ScrollViewC) {
						convView = sv
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
				),
			),

			// notifications
			If(&statusVisible).Then(
				Overlay.BottomRight().Offset(-2, -1)(
					VBox.Width(44).FitContent().Gap(1)(
						ForEach(statusFeed.Items(), func(item *ui.Notification) Component {
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
			If(&omniboxOpen).Then(
				Overlay.Centered()(
					VBox.
						Width(86).
						FitContent().
						Fill(t.BG).
						PaddingTRBL(1, 2, 1, 2).
						Opacity(In(1).Out(Animate.Duration(500*time.Millisecond)(0.0))).
						NodeRef(&omniboxRef)(
						On.Modal(
							Key("<CR>", execOmnibox),
							Key("<Enter>", execOmnibox),
							Key("<Esc>", closeOmnibox),
							Key("<C-c>", closeOmnibox),
							Key("<C-j>", func() { moveOmnibox(1) }),
							Key("<Down>", func() { moveOmnibox(1) }),
							Key("<Tab>", func() { moveOmnibox(1) }),
							Key("<C-n>", func() { moveOmnibox(1) }),
							Key("<C-k>", func() { moveOmnibox(-1) }),
							Key("<Up>", func() { moveOmnibox(-1) }),
							Key("<S-Tab>", func() { moveOmnibox(-1) }),
							Key("<C-p>", func() { moveOmnibox(-1) }),
							Key("<C-d>", func() { pageOmnibox(1) }),
							Key("<C-u>", func() { pageOmnibox(-1) }),
							Key("g", firstOmnibox),
							Key("G", lastOmnibox),
						),
						HBox(
							Text("mail").FG(t.Bright).Bold(),
							SpaceW(1),
							Text("commands").FG(t.Subtle),
							Space(),
							Text("j/k").FG(t.Muted),
							SpaceW(2),
							Text("<esc>").FG(t.Muted),
						),
						SpaceH(1),
						omniboxList,
						If(&omniboxEmpty).Then(
							VBox.Fill(t.BG).PaddingTRBL(1, 1, 1, 1)(
								Text("no commands").FG(t.Subtle),
							),
						),
						ScreenEffect(
							SEDropShadow().Focus(&omniboxRef).Strength(0.3),
							SEVignette().Smooth().Dodge(&omniboxRef).Strength(
								In(Animate(0.3)).Out(Animate.Duration(500*time.Millisecond)(0.0)),
							),
						),
					),
				),
			),

			// help modal — ? toggles. Vignette subtly darkens the rest of
			// the screen; the modal itself is dodged so it stays crisp.
			If(&helpOpen).Then(
				Overlay.Centered()(
					VBox.
						Width(56).
						Fill(t.BG).
						PaddingVH(1, 2).
						NodeRef(&helpRef).
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
							).Dodge(&helpRef).Smooth(),
							SEDropShadow().Focus(&helpRef),
						),
					),
				),
			),
		),
	).NoCounts().
		Handle("q", app.Stop).
		Handle("?", func() { helpOpen = !helpOpen }).
		Handle("<C-p>", openOmnibox).
		Handle("<Space>", openOmnibox).
		Handle(",", func() {
			shimmerIdx = (shimmerIdx - 1 + len(variantNames)) % len(variantNames)
			// applyVariant()
			notify("shimmer: " + shimmerLabel)
		}).
		Handle(".", func() {
			shimmerIdx = (shimmerIdx + 1) % len(variantNames)

			notify("shimmer: " + shimmerLabel)
		}).
		Handle("j", func() {
			switch pane {
			case 0:
				if folderSel < mb.FolderLen()-1 {
					folderSel++
					loadFolder()
				}
			case 1:
				if threadSel < mb.ThreadLen()-1 {
					threadSel++
					mb.SetSelected(threadSel)
					loadPreview()
				}
			case 2:
				if convView != nil {
					convView.Layer().ScrollDown(1)
				}
			}
		}).
		Handle("k", func() {
			switch pane {
			case 0:
				if folderSel > 0 {
					folderSel--
					loadFolder()
				}
			case 1:
				if threadSel > 0 {
					threadSel--
					mb.SetSelected(threadSel)
					loadPreview()
				}
			case 2:
				if convView != nil {
					convView.Layer().ScrollUp(1)
				}
			}
		}).
		Handle("l", func() {
			if pane < 2 {
				pane++
				updateFocus()
			}
		}).
		Handle("h", func() {
			if pane > 0 {
				pane--
				updateFocus()
			}
		}).
		Handle("<Tab>", func() {
			pane = (pane + 1) % 3
			updateFocus()
		}).
		Handle("<S-Tab>", func() {
			pane = (pane + 2) % 3
			updateFocus()
		}).
		Handle("<Enter>", func() {
			switch pane {
			case 0:
				if folderSel == mb.CanonEnd() {
					labelsOpen = !labelsOpen
					mb.BuildFolderDisplay(labelsOpen)
					updateThreadHeader()
					break
				}
				pane = 1
				updateFocus()
			case 1:
				handleEnter()
			}
		}).
		Handle("<Escape>", func() {
			if helpOpen {
				helpOpen = false
				return
			}
			if pane > 0 {
				pane--
				updateFocus()
			}
		}).
		Handle("o", func() {
			if pane == 1 {
				mb.ToggleThread(threadSel)
			}
		}).
		Handle("c", func() { comp.Open() }).
		Handle("C", func() { comp.ResumeLast() }).
		Handle("r", func() {
			if t := mb.SelectedThread(threadSel); t != nil {
				if row := mb.ThreadRowAt(threadSel); row != nil && row.MsgIdx < 0 {
					// Drafts folder: r resumes the draft (muscle-memory
					// consistency with Enter) rather than starting a
					// nonsensical reply to your own draft.
					if mb.ActiveFolderCanonical() == "Drafts" {
						comp.ResumeDraft(t.ID)
						return
					}
					comp.Open()
					comp.SetupReply(*t)
				}
			}
		}).
		Handle("a", func() {
			if pane == 1 {
				pushUndo(mb.Archive(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				updateThreadHeader()
				loadPreview()
			}
		}).
		Handle("d", func() {
			if pane == 1 {
				pushUndo(mb.Delete(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				updateThreadHeader()
				loadPreview()
			}
		}).
		Handle("s", func() {
			if pane == 1 {
				pushUndo(mb.ToggleStar(threadSel))
			}
		}).
		Handle("e", func() {
			if pane == 1 {
				pushUndo(mb.ToggleRead(threadSel))
				mb.BuildFolderDisplay(labelsOpen)
				updateThreadHeader()
			}
		}).
		Handle("u", func() {
			if pane == 1 && len(undoStack) > 0 {
				item := undoStack[len(undoStack)-1]
				item.run()
				undoStack = undoStack[:len(undoStack)-1]
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				updateThreadHeader()
				loadPreview()
				if len(undoStack) > 0 {
					notify(fmt.Sprintf("%s — %d undoable", item.message, len(undoStack)))
				} else {
					notify(item.message)
				}
			}
		}).
		Handle("/", func() {
			startSearch()
		})

	app.View("search",
		VBox(
			HBox(
				Text("/").FG(t.Bright).Bold(),
				Text(&searchQuery),
			),
		),
	).
		Handle("<CR>", func() {
			q := searchQuery
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			if q == "" {
				return
			}
			results, err := db.Search(q, 50)
			if err != nil {
				notify(fmt.Sprintf("search: %v", err))
				return
			}
			mb.SetSearchResults(results)
			mb.BuildThreadDisplay()
			threadSel = 0
			mb.SetSelected(0)
			updateThreadHeader()
			pane = 1
			updateFocus()
			notify(fmt.Sprintf("search: %q (%d)", q, len(results)))
		}).
		Handle("<Esc>", func() {
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
		}).
		Handle("<BS>", func() {
			if len(searchQuery) > 0 {
				runes := []rune(searchQuery)
				searchQuery = string(runes[:len(runes)-1])
			}
		}).
		NoCounts()

	if searchRouter, ok := app.ViewRouter("search"); ok {
		searchRouter.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				searchQuery += string(k.Rune)
				app.RequestRender()
				return true
			}
			return false
		})
	}

	rt = mailruntime.New(db, mb, mailruntime.Config{
		Backend: !offline,
		IMAP:    cfg,
	}, mailruntime.Callbacks{
		Status: func(text string) {
			notify(text)
		},
		FoldersChanged: func() {
			mb.BuildFolderDisplay(labelsOpen)
			if draftsID := mb.FolderIDByDisplayName("Drafts"); draftsID != "" {
				db.SetDraftsLabel(draftsID)
			}
			updateThreadHeader()
		},
		ThreadsChanged: func() {
			mb.BuildFolderDisplay(labelsOpen)
			mb.BuildThreadDisplay()
			clampThreadSel()
			updateThreadHeader()
			if loadPreview != nil {
				loadPreview()
			}
		},
		Render: app.RequestRender,
	})
	rt.Start()
	defer rt.Close()

	// spinner
	go func() {
		for range time.Tick(80 * time.Millisecond) {
			frame++
			updateStatusOverlay()
			app.RequestRender()
		}
	}()

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}
