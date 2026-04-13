package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/contacts"
	imapprov "github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/provider"
	smtpprov "github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/riffkey"
)

type AppTheme struct {
	BG      Color
	Bright  Color // selected item, emphasis
	FG      Color // active pane content
	Subtle  Color // dates, senders, secondary info
	Dim     Color // inactive pane content
	Muted   Color // help bar, status
	Accent  Color
	SelBG   Color
	GroupBG Color
}

var themeDark = AppTheme{
	BG:      Hex(0x1a1a1a),
	Bright:  Hex(0xeeeeee),
	FG:      Hex(0xb0b0b0),
	Subtle:  Hex(0x777777),
	Dim:     Hex(0x565656),
	Muted:   Hex(0x3a3a3a),
	Accent:  Hex(0xe60012),
	SelBG:   Hex(0x2e2e2e),
	GroupBG: Hex(0x242424),
}

var themeLight = AppTheme{
	BG:      Hex(0xf6f6f6),
	Bright:  Hex(0x111111),
	FG:      Hex(0x333333),
	Subtle:  Hex(0x777777),
	Dim:     Hex(0xaaaaaa),
	Muted:   Hex(0xcccccc),
	Accent:  Hex(0xe60012),
	SelBG:   Hex(0xe8e8e8),
	GroupBG: Hex(0xeeeeee),
}

func main() {
	logFile, err := os.OpenFile(
		filepath.Join(os.TempDir(), "mail.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644,
	)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}
	log.Println("starting mail")

	app := NewApp()
	app.SetDefaultStyle(Style{FG: themeDark.FG, BG: themeDark.BG})

	db, err := cache.New()
	if err != nil {
		log.Fatal(err)
	}

	mb := mailbox.New(db)

	cfg, err := imapprov.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	smtp := smtpprov.New(smtpprov.Config{
		Server:   cfg.SMTPServer,
		Email:    cfg.Email,
		Password: cfg.Password,
	})

	// load from cache
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.SetSelected(0)

	t := themeDark
	_ = themeLight

	// inbox view state
	var (
		folderSel  int
		threadSel  int
		labelsOpen  bool
		frame       int
		statusText  = "Inbox"
		searchQuery string

		// pane styles — active uses FG, inactive uses dim
		folderStyle     = Style{FG: t.Dim}
		threadStyle     = Style{FG: t.FG}
		previewStyle    = Style{FG: t.Dim}
		folderListStyle  = Style{FG: t.Dim}
		folderSelStyle   = Style{FG: t.Dim}
		threadListStyle = Style{FG: t.FG}
		pane            = 1

		undoStack []func()
	)

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

	// compose view
	editor := compose.NewEditor(compose.NewDocument(), "")
	editor.SetApp(app)
	editor.StartSpellResultWorker(app.RequestRender)
	comp := setupComposeView(app, editor, mb, smtp, db, &statusText, &frame)

	// connect and sync in background
	go func() {
		imap := imapprov.New(cfg)
		if err := imap.Authenticate(); err != nil {
			statusText = fmt.Sprintf("imap: %v", err)
			app.RequestRender()
			return
		}
		mb.SetIMAP(imap)
		log.Println("imap: authenticated")

		if err := mb.SyncFolders(); err != nil {
			statusText = fmt.Sprintf("sync: %v", err)
			app.RequestRender()
			return
		}
		mb.BuildFolderDisplay(labelsOpen)
		app.RequestRender()

		if err := mb.SyncThreads(); err != nil {
			statusText = fmt.Sprintf("sync: %v", err)
		}
		mb.BuildThreadDisplay()
		mb.SetSelected(threadSel)
		app.RequestRender()

		go mb.ProcessPendingCommands()
		go cacheContacts(db)
	}()

	syncThreadsFromNetwork := func() {
		if err := mb.SyncThreads(); err != nil {
			statusText = fmt.Sprintf("sync: %v", err)
		}
		mb.BuildThreadDisplay()
		mb.SetSelected(threadSel)
		app.RequestRender()
	}

	handleEnter := func() {
		if msg := mb.SelectedMessage(threadSel); msg != nil {
			mb.LoadPreview(*msg, app.Size().Width)
			mb.MarkRead(threadSel)
			pane = 2
			updateFocus()
			return
		}
		mb.ToggleThread(threadSel)
		mb.MarkRead(threadSel)
		if msg := mb.LastMessage(threadSel); msg != nil {
			mb.LoadPreview(*msg, app.Size().Width)
		}
	}

	pushUndo := func(undo func(), desc string) {
		if undo != nil {
			undoStack = append(undoStack, undo)
			statusText = desc + " — u to undo"
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
			actualIdx = folderSel - 1
		}
		if actualIdx >= mb.FolderCount() {
			return
		}
		mb.SelectFolder(actualIdx)
		mb.LoadThreads()
		mb.BuildThreadDisplay()
		threadSel = 0
		mb.SetSelected(0)
		undoStack = nil
		statusText = mb.FolderName(folderSel)
		go syncThreadsFromNetwork()
	}

	previewTV := TextView(mb.PreviewText()).Grow(1)
	smooth := Animate.Duration(400 * time.Millisecond).Ease(EaseOutCubic)
	accentMarker := Style{FG: t.Accent}

	app.View("main",
		VBox.PaddingTRBL(1, 2, 0, 2)(
			HBox(
				Text("mail").FG(t.Bright).Bold(),
				SpaceW(2),
				Text(&statusText).FG(t.Subtle),
			),
			SpaceH(1),
			HBox.Grow(1).Gap(4)(
				VBox.Grow(1).CascadeStyle(&folderStyle)(
					HRule(), SpaceH(1),
					List(mb.FolderNames()).
						Selection(&folderSel).
						Style(smooth(&folderListStyle)).
						SelectedStyle(smooth(&folderSelStyle)).
						Marker("● ").MarkerStyle(accentMarker),
				),
				VBox.Grow(3).CascadeStyle(&threadStyle)(
					HRule(), SpaceH(1),
					List(mb.ThreadRows()).
						Selection(&threadSel).
						Style(smooth(&threadListStyle)).
						SelectedStyle(Style{}).
						Marker("  ").
						Render(func(row *mailbox.ThreadRow) any {
							itemBG := smooth(If(&row.Selected).Then(t.SelBG).Else(
								If(&row.Grouped).
									Then(t.GroupBG).
									Else(t.BG),
							))
							return VBox.Fill(itemBG).Border(BorderSoft).BorderFG(itemBG)(
								HBox(
									If(&row.Unread).Then(Text("●").FG(t.Accent)).Else(Text(" ")),
									SpaceW(1),
									HBox.Grow(1)(
										Text(&row.Label).Style(
											If(&row.Unread).
												Then(Style{Attr: AttrBold}).
												Else(Style{})),
									),
									SpaceW(2),
									Text(&row.Date).Dim(),
								),
								HBox(
									SpaceW(2),
									Text(&row.Sender).Dim(),
								),
							)
						}),
				),
				VBox.Grow(3).CascadeStyle(&previewStyle)(
					HRule(), SpaceH(1),
					previewTV,
				),
			),
			SpaceH(1),
			Text("q quit · j/k nav · h/l pane · enter open · c compose · r reply · a archive · d delete · u undo · / search").FG(t.Muted),
		),
	).NoCounts().
		Handle("q", app.Stop).
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
				}
			case 2:
				previewTV.Layer().ScrollDown(1)
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
				}
			case 2:
				previewTV.Layer().ScrollUp(1)
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
					break
				}
				pane = 1
				updateFocus()
			case 1:
				handleEnter()
			}
		}).
		Handle("<Escape>", func() {
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
		Handle("c", func() {
			comp.Open()
		}).
		Handle("r", func() {
			if t := mb.SelectedThread(threadSel); t != nil {
				if row := mb.ThreadRowAt(threadSel); row != nil && row.MsgIdx < 0 {
					comp.Open()
					comp.SetupReply(*t)
				}
			}
		}).
		Handle("a", func() {
			if pane == 1 {
				pushUndo(mb.Archive(threadSel))
				clampThreadSel()
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("d", func() {
			if pane == 1 {
				pushUndo(mb.Delete(threadSel))
				clampThreadSel()
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("s", func() {
			if pane == 1 {
				pushUndo(mb.ToggleStar(threadSel))
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("e", func() {
			if pane == 1 {
				pushUndo(mb.ToggleRead(threadSel))
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("u", func() {
			if pane == 1 && len(undoStack) > 0 {
				undoStack[len(undoStack)-1]()
				undoStack = undoStack[:len(undoStack)-1]
				clampThreadSel()
				if len(undoStack) > 0 {
					statusText = fmt.Sprintf("%d undoable — u to undo", len(undoStack))
				} else {
					statusText = "undone"
				}
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("/", func() {
			searchQuery = ""
			app.HideCursor()
			app.PushView("search")
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
				statusText = fmt.Sprintf("search: %v", err)
				return
			}
			mb.SetSearchResults(results)
			mb.BuildThreadDisplay()
			threadSel = 0
			mb.SetSelected(0)
			pane = 1
			updateFocus()
			statusText = fmt.Sprintf("search: %q (%d)", q, len(results))
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

	// spinner
	go func() {
		for range time.Tick(80 * time.Millisecond) {
			frame++
			app.RequestRender()
		}
	}()

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}

type composeControls struct {
	Open       func()
	SetupReply func(provider.Thread)
}

func setupComposeView(app *App, ed *compose.Editor, mb *mailbox.Mailbox, smtp *smtpprov.SMTP, db *cache.Cache, statusText *string, frame *int) composeControls {
	// compose data
	var to, cc, subject string
	var replyMsg *provider.Message

	// compose view state
	var fieldTo, fieldCC, fieldSubject InputState
	var fieldFocus FocusGroup
	var focused bool
	labelTo, labelCC, labelSub := BrightBlack, BrightBlack, BrightBlack
	var toFieldRef, ccFieldRef NodeRef
	var contactResults []string
	var contactSel int
	var showContacts bool
	var showDiscard, showSending bool
	var sendingStatus string

	// compose search state
	var searchQuery, searchPrompt string
	var searchFwd bool

	reset := func() {
		to, cc, subject = "", "", ""
		replyMsg = nil
		fieldTo.Clear()
		fieldCC.Clear()
		fieldSubject.Clear()
		fieldFocus.Current = -1
		focused = false
		ed.ResetDocument(compose.NewDocument())
	}

	send := func() {
		log.Printf("sendMessage: to=%q cc=%q subject=%q", to, cc, subject)
		if smtp == nil {
			log.Println("sendMessage: no smtp configured")
			return
		}
		msg := provider.Message{
			To:       parseRecipients(to),
			CC:       parseRecipients(cc),
			Subject:  subject,
			HTMLBody: ed.ToHTML(),
			TextBody: ed.ToPlainText(),
		}
		if replyMsg != nil {
			msg.InReplyTo = replyMsg.MessageID
			msg.References = append(replyMsg.References, replyMsg.MessageID)
		}
		showSending = true
		sendingStatus = "sending..."
		app.RequestRender()

		go func() {
			log.Println("sendMessage: sending via smtp...")
			err := smtp.Send(msg)
			showSending = false
			if err != nil {
				log.Printf("sendMessage: failed: %v", err)
				*statusText = fmt.Sprintf("send failed: %v", err)
				app.RequestRender()
				return
			}
			log.Printf("sendMessage: sent to %s", to)
			*statusText = fmt.Sprintf("sent to %s", to)
			reset()
			app.HideCursor()
			app.Go("main")
			app.RequestRender()
		}()
	}

	// wire enterInsertMode for operator text object combos
	var enterInsertMode func()
	compose.SetEnterInsertMode(func(a *App, e *compose.Editor) {
		enterInsertMode()
	})

	enterInsertMode = func() {
		syncSubject := func() {
			b := ed.CurrentBlock()
			if b == nil {
				return
			}
			switch b.Type {
			case compose.BlockH1, compose.BlockH2, compose.BlockH3:
				subject = b.Text()
				fieldSubject.Value = subject
				fieldSubject.Cursor = len(subject)
			}
		}

		compose.RegisterInsertMode(app, ed, syncSubject)

		if r := app.Router(); r != nil {
			r.Handle("<C-s>", func(_ riffkey.Match) {
				ed.EnterNormal()
				app.Pop()
				if to != "" {
					send()
				}
			})
		}
	}

	// compose view layout
	app.View("compose",
		VBox(
			LayerView(ed.Layer()).Grow(1),

			VBox(
				SpaceH(1),
				HBox(Space(), VBox.Width(60)(
					HBox.Gap(1).NodeRef(&toFieldRef)(
						Text("TO").FG(&labelTo),
						TextInput{Field: &fieldTo, FocusGroup: &fieldFocus, FocusIndex: 0,
							Placeholder: "·····", PlaceholderStyle: Style{Attr: AttrDim}},
					),
					HBox.Gap(1).NodeRef(&ccFieldRef)(
						Text("CC").FG(&labelCC),
						TextInput{Field: &fieldCC, FocusGroup: &fieldFocus, FocusIndex: 1,
							Placeholder: "·····", PlaceholderStyle: Style{Attr: AttrDim}},
					),
					HBox.Gap(1)(
						Text("SUBJECT").FG(&labelSub),
						TextInput{Field: &fieldSubject, FocusGroup: &fieldFocus, FocusIndex: 2,
							Placeholder: "·····", PlaceholderStyle: Style{Attr: AttrDim}},
					),
				), Space()),
				SpaceH(1),
			),

			If(&showContacts).Then(
				Overlay.Above(&toFieldRef)(
					VBox.Border(BorderRounded).BorderFG(BrightBlack)(
						List(&contactResults).
							Selection(&contactSel).
							SelectedStyle(Style{Attr: AttrInverse}).
							MaxVisible(6),
					),
				),
			),

			If(&showDiscard).Then(
				Overlay.Centered().Backdrop().BackdropFG(BrightBlack)(
					VBox.Border(BorderRounded).BorderFG(BrightBlack).Width(40)(
						SpaceH(1),
						Text("discard changes?").Bold().Style(Style{Align: AlignCenter}).Width(38),
						SpaceH(1),
						HBox(
							Space(),
							Text("y").Bold(), Text(" discard").Dim(),
							SpaceW(4),
							Text("n").Bold(), Text(" cancel").Dim(),
							Space(),
						),
						SpaceH(1),
					),
				),
			),
			If(&showSending).Then(
				Overlay.Centered().Backdrop().BackdropFG(BrightBlack)(
					VBox.Border(BorderRounded).BorderFG(BrightBlack).Width(40)(
						SpaceH(1),
						HBox(
							Space(),
							Spinner(frame).Frames(SpinnerDots).FG(BrightBlack),
							SpaceW(1),
							Text(&sendingStatus).Style(Style{Align: AlignCenter}),
							Space(),
						),
						SpaceH(1),
					),
				),
			),
		),
	).NoCounts()

	// editor render callback
	ed.Layer().Render = func() {
		w := ed.Layer().ViewportWidth()
		h := ed.Layer().ViewportHeight()
		if w > 0 && h > 0 {
			ed.SetSize(w, h)
			ed.UpdateDisplay()
		}
	}
	ed.Layer().AlwaysRender = true

	// compose search view
	app.View("compose-search",
		VBox(
			HBox(
				Text(&searchPrompt).Bold(),
				Text(&searchQuery),
			),
		),
	).
		Handle("<CR>", func() {
			q := searchQuery
			fwd := searchFwd
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			if q != "" {
				ed.Search(q, fwd)
			}
			ed.Refresh()
		}).
		Handle("<Esc>", func() {
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			ed.Refresh()
		}).
		Handle("<BS>", func() {
			if len(searchQuery) > 0 {
				runes := []rune(searchQuery)
				searchQuery = string(runes[:len(runes)-1])
			}
		}).
		NoCounts()

	if searchRouter, ok := app.ViewRouter("compose-search"); ok {
		searchRouter.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				searchQuery += string(k.Rune)
				app.RequestRender()
				return true
			}
			return false
		})
	}

	// keybindings
	if router, ok := app.ViewRouter("compose"); ok {
		exitCompose := func() { reset(); app.HideCursor(); app.Go("main") }

		router.Handle("<C-q>", func(_ riffkey.Match) { exitCompose() })

		router.Handle("<Esc>", func(_ riffkey.Match) {
			ed.ExitDialogueIfEmpty()
			if !ed.Dirty() {
				exitCompose()
				return
			}
			showDiscard = true
			confirm := riffkey.NewRouter().Name("confirm-discard").NoCounts()
			dismiss := func() { showDiscard = false; app.Pop() }
			confirm.Handle("y", func(_ riffkey.Match) { dismiss(); exitCompose() })
			confirm.Handle("Y", func(_ riffkey.Match) { dismiss(); exitCompose() })
			confirm.Handle("<CR>", func(_ riffkey.Match) { dismiss(); exitCompose() })
			confirm.Handle("n", func(_ riffkey.Match) { dismiss() })
			confirm.Handle("N", func(_ riffkey.Match) { dismiss() })
			confirm.Handle("<Esc>", func(_ riffkey.Match) { dismiss() })
			confirm.AddOnAfter(func() { app.RequestRender() })
			app.Push(confirm)
		})

		// send panel
		fieldStates := []*InputState{&fieldTo, &fieldCC, &fieldSubject}
		labels := []*Color{&labelTo, &labelCC, &labelSub}

		syncLabels := func() {
			for i, l := range labels {
				if focused && fieldFocus.Current == i {
					*l = White
				} else {
					*l = BrightBlack
				}
			}
		}

		var lastContactQuery string

		searchContacts := func() {
			if fieldFocus.Current > 1 {
				showContacts = false
				return
			}
			query := fieldStates[fieldFocus.Current].Value
			if query == lastContactQuery {
				return
			}
			lastContactQuery = query
			if len(query) < 2 {
				showContacts = false
				contactResults = nil
				return
			}
			go func() {
				var results []provider.Address
				if db != nil {
					results, _ = db.SearchContacts(query)
				}
				contactResults = nil
				for _, r := range results {
					contactResults = append(contactResults, r.String())
				}
				contactSel = 0
				showContacts = len(contactResults) > 0
				app.RequestRender()
			}()
		}

		bindCurrentField := func(fr *riffkey.Router) {
			f := fieldStates[fieldFocus.Current]
			th := riffkey.NewTextHandler(&f.Value, &f.Cursor)
			fr.HandleUnmatched(func(k riffkey.Key) bool {
				handled := th.HandleKey(k)
				if handled {
					searchContacts()
				}
				return handled
			})
			fr.NoCounts()
			syncLabels()
			showContacts = false
			lastContactQuery = ""
		}

		exitFields := func() {
			to = fieldTo.Value
			cc = fieldCC.Value
			subject = fieldSubject.Value
			fieldFocus.Current = -1
			focused = false
			showContacts = false
			syncLabels()
			app.Pop()
			ed.Refresh()
		}

		router.Handle("<Tab>", func(_ riffkey.Match) {
			focused = true
			fieldFocus.Current = 0
			app.HideCursor()

			fr := riffkey.NewRouter().Name("send-fields")
			bindCurrentField(fr)

			fr.Handle("<Tab>", func(_ riffkey.Match) {
				next := fieldFocus.Current + 1
				if next >= len(fieldStates) {
					exitFields()
					return
				}
				fieldFocus.Current = next
				bindCurrentField(fr)
			})
			fr.Handle("<S-Tab>", func(_ riffkey.Match) {
				prev := fieldFocus.Current - 1
				if prev < 0 {
					exitFields()
					return
				}
				fieldFocus.Current = prev
				bindCurrentField(fr)
			})
			fr.Handle("<Esc>", func(_ riffkey.Match) {
				if showContacts {
					showContacts = false
					return
				}
				exitFields()
			})
			fr.Handle("<C-s>", func(_ riffkey.Match) {
				exitFields()
				if to != "" {
					send()
				}
			})
			fr.Handle("<CR>", func(_ riffkey.Match) {
				if showContacts && contactSel >= 0 && contactSel < len(contactResults) {
					fieldStates[fieldFocus.Current].Value = contactResults[contactSel]
					fieldStates[fieldFocus.Current].Cursor = len(contactResults[contactSel])
					showContacts = false
					return
				}
				next := fieldFocus.Current + 1
				if next >= len(fieldStates) {
					exitFields()
					return
				}
				fieldFocus.Current = next
				bindCurrentField(fr)
			})
			fr.Handle("<Down>", func(_ riffkey.Match) {
				if showContacts && contactSel < len(contactResults)-1 {
					contactSel++
				}
			})
			fr.Handle("<Up>", func(_ riffkey.Match) {
				if showContacts && contactSel > 0 {
					contactSel--
				}
			})

			fr.AddOnAfter(func() { app.RequestRender() })
			app.Push(fr)
		})

		router.Handle("<C-s>", func(_ riffkey.Match) {
			if to != "" {
				send()
			}
		})
		router.Handle(":send<CR>", func(_ riffkey.Match) {
			if to != "" {
				send()
			}
		})
		router.Handle(":s<CR>", func(_ riffkey.Match) {
			if to != "" {
				send()
			}
		})

		// search
		composeStartSearch := func(forward bool) {
			if forward {
				searchPrompt = "/"
			} else {
				searchPrompt = "?"
			}
			searchQuery = ""
			searchFwd = forward
			app.HideCursor()
			app.PushView("compose-search")
		}
		router.Handle("/", func(_ riffkey.Match) { composeStartSearch(true) })
		router.Handle("?", func(_ riffkey.Match) { composeStartSearch(false) })

		// pure editor keybindings
		compose.RegisterNormalMode(router, app, ed,
			func() { enterInsertMode() },
			func() { compose.RegisterVisualMode(app, ed) },
		)

		router.AddOnAfter(func() {
			ed.Refresh()
			if focused {
				app.HideCursor()
			}
		})
	}

	return composeControls{
		Open: func() {
			reset()
			ed.SetTypewriterMode(true)
			app.Go("compose")
			ed.Refresh()
		},
		SetupReply: func(thread provider.Thread) {
			lastMsg := thread.Messages[len(thread.Messages)-1]
			replyMsg = &lastMsg
			to = lastMsg.From.String()
			s := lastMsg.Subject
			if !strings.HasPrefix(strings.ToLower(s), "re:") {
				s = "Re: " + s
			}
			subject = s

			// build document with quoted original text
			body := lastMsg.TextBody
			if body == "" && lastMsg.HTMLBody != "" {
				body = lastMsg.HTMLBody // will be plain enough for quoting
			}
			doc := compose.NewDocument()
			doc.Blocks = []compose.Block{
				{Type: compose.BlockParagraph, Runs: []compose.Run{{Text: ""}}},
				{Type: compose.BlockParagraph, Runs: []compose.Run{{Text: ""}}},
			}
			for _, line := range strings.Split(body, "\n") {
				doc.Blocks = append(doc.Blocks, compose.Block{
					Type: compose.BlockQuote,
					Runs: []compose.Run{{Text: line}},
				})
			}
			ed.ResetDocument(doc)
		},
	}
}

func parseRecipients(s string) []provider.Address {
	if s == "" {
		return nil
	}
	var addrs []provider.Address
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.LastIndex(part, "<"); idx >= 0 {
			name := strings.TrimSpace(part[:idx])
			email := strings.TrimRight(part[idx+1:], ">")
			email = strings.TrimSpace(email)
			addrs = append(addrs, provider.Address{Name: name, Email: email})
		} else {
			addrs = append(addrs, provider.Address{Email: part})
		}
	}
	return addrs
}

func cacheContacts(db *cache.Cache) {
	if db == nil {
		return
	}
	log.Println("contacts: loading from macOS...")
	all := contacts.All()
	log.Printf("contacts: loaded %d, caching", len(all))
	if len(all) > 0 {
		db.PutContacts(all)
	}
}
