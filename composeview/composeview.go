package composeview

import (
	"fmt"
	"log"
	"strings"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/provider"
	"github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/transition"
	"github.com/kungfusheep/riffkey"
)

type Controls struct {
	Open        func()
	SetupReply  func(provider.Thread)
	ResumeLast  func()
	ResumeDraft func(threadID string)
}

func cursorColor(ed *compose.Editor) Color {
	t := ed.Theme()
	switch ed.Mode() {
	case compose.ModeInsert:
		return t.Cursor.Insert
	case compose.ModeVisual:
		return t.Cursor.Visual
	default:
		return t.Cursor.Normal
	}
}

func Setup(app *App, ed *compose.Editor, mb *mailbox.Mailbox, smtpClient *smtp.SMTP, db *cache.Cache, statusText *string, frame *int, tr *transition.Transition, palette theme.Theme) Controls {
	var to, cc, subject string
	var replyMsg *provider.Message

	var fieldTo, fieldCC, fieldSubject InputState
	var fieldFocus FocusGroup
	var focused bool
	labelTo, labelCC, labelSub := palette.Muted, palette.Muted, palette.Muted
	var toFieldRef, ccFieldRef NodeRef
	var contactResults []string
	var contactSel int
	var showContacts bool
	var showSending bool
	var sendingStatus string
	var composeActive bool

	var searchQuery, searchPrompt string
	var searchFwd bool

	var currentDraftID string
	var draftSaveTimer *time.Timer
	var draftTouched bool

	var pendingCursorShow bool

	tr.CursorOverlay(func() (int, int, Color, bool) {
		if !composeActive || focused {
			return 0, 0, Color{}, false
		}
		x, y := ed.CursorScreenPos()
		return x, y, cursorColor(ed), true
	})

	reset := func() {
		to, cc, subject = "", "", ""
		replyMsg = nil
		fieldTo.Clear()
		fieldCC.Clear()
		fieldSubject.Clear()
		fieldFocus.Current = -1
		focused = false
		ed.ResetEmpty()
		currentDraftID = ""
		draftTouched = false
	}

	snapshotDraft := func() cache.Draft {
		return cache.Draft{
			ThreadID: currentDraftID,
			To:       fieldTo.Value,
			Cc:       fieldCC.Value,
			Subject:  fieldSubject.Value,
			Body:     ed.Markdown(),
		}
	}

	saveDraft := func() {
		if db == nil {
			return
		}
		d := snapshotDraft()
		bodyLen := len(d.Body)
		if err := db.PutDraft(d); err != nil {
			log.Printf("saveDraft: PutDraft failed: %v", err)
			return
		}
		log.Printf("saveDraft: wrote thread=%q subject=%q bodyLen=%d isEmpty=%v", d.ThreadID, d.Subject, bodyLen, d.IsEmpty())
	}

	scheduleDraftSave := func() {
		if !composeActive || db == nil {
			return
		}
		draftTouched = true
		if draftSaveTimer != nil {
			draftSaveTimer.Stop()
		}
		draftSaveTimer = time.AfterFunc(time.Second, saveDraft)
	}

	loadDraft := func(threadID string) bool {
		if db == nil {
			return false
		}
		d, found, err := db.GetDraft(threadID)
		if err != nil || !found {
			return false
		}
		to = d.To
		cc = d.Cc
		subject = d.Subject
		fieldTo.Value = d.To
		fieldTo.Cursor = len(d.To)
		fieldCC.Value = d.Cc
		fieldCC.Cursor = len(d.Cc)
		fieldSubject.Value = d.Subject
		fieldSubject.Cursor = len(d.Subject)
		ed.LoadMarkdown(d.Body)
		return true
	}

	send := func() {
		log.Printf("sendMessage: to=%q cc=%q subject=%q", to, cc, subject)
		if smtpClient == nil {
			log.Println("sendMessage: no smtp configured")
			return
		}
		msg := provider.Message{
			To:       provider.ParseAddressList(to),
			CC:       provider.ParseAddressList(cc),
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
			err := smtpClient.Send(&msg)
			showSending = false
			if err != nil {
				log.Printf("sendMessage: failed: %v", err)
				*statusText = fmt.Sprintf("send failed: %v", err)
				app.RequestRender()
				return
			}
			msg.Date = time.Now()
			msg.Read = true
			if db != nil {
				db.PutSentMessage(msg)
				db.DeleteDraft(currentDraftID)
				go mb.ProcessPendingCommands()
			}
			log.Printf("sendMessage: sent to %s (msgid=%s)", to, msg.MessageID)
			*statusText = fmt.Sprintf("sent to %s", to)
			composeActive = false
			reset()
			app.HideCursor()
			app.Go("main")
			app.RequestRender()
		}()
	}

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

	app.View("compose",
		VBox(
			ScreenEffect(tr.SourceEffect()),
			ScreenEffect(tr.TargetEffect()),
			LayerView(ed.Layer()).Grow(1),

			VBox(
				SpaceH(1),
				HBox(Space(), VBox.Width(64)(
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
					VBox.Border(BorderRounded).BorderFG(palette.Muted)(
						List(&contactResults).
							Selection(&contactSel).
							SelectedStyle(Style{Attr: AttrInverse}).
							MaxVisible(6),
					),
				),
			),

			If(&showSending).Then(
				Overlay.Centered().Backdrop().BackdropFG(palette.BG)(
					VBox.Border(BorderRounded).BorderFG(palette.Muted).Width(40)(
						SpaceH(1),
						HBox(
							Space(),
							Spinner(frame).Frames(SpinnerDots).FG(palette.Subtle),
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

	ed.Layer().Render = func() {
		w := ed.Layer().ViewportWidth()
		h := ed.Layer().ViewportHeight()
		if w > 0 && h > 0 {
			ed.SetSize(w, h)
			if pendingCursorShow {
				if tr.Complete() {
					pendingCursorShow = false
					ed.Refresh()
				} else {
					ed.UpdateDisplay()
					app.HideCursor()
				}
			} else {
				ed.UpdateDisplay()
			}
		}
	}
	ed.Layer().AlwaysRender = true

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !composeActive || !draftTouched {
				continue
			}
			saveDraft()
			mb.ProcessPendingCommands()
		}
	}()

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

	if router, ok := app.ViewRouter("compose"); ok {
		exitCompose := func() {
			if draftSaveTimer != nil {
				draftSaveTimer.Stop()
			}
			if draftTouched {
				saveDraft()
				go mb.ProcessPendingCommands()
			}
			composeActive = false
			reset()
			app.HideCursor()
			tr.Start()
			app.Go("main")
		}

		router.Handle("<C-q>", func(_ riffkey.Match) { exitCompose() })

		router.Handle("<Esc>", func(_ riffkey.Match) {
			ed.ExitDialogueIfEmpty()
			exitCompose()
		})

		fieldStates := []*InputState{&fieldTo, &fieldCC, &fieldSubject}
		labels := []*Color{&labelTo, &labelCC, &labelSub}

		syncLabels := func() {
			for i, l := range labels {
				if focused && fieldFocus.Current == i {
					*l = palette.Bright
				} else {
					*l = palette.Muted
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
					scheduleDraftSave()
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

		compose.RegisterNormalMode(router, app, ed,
			func() { enterInsertMode() },
			func() { compose.RegisterVisualMode(app, ed) },
		)

		router.AddOnAfter(func() {
			if !composeActive {
				return
			}
			ed.Refresh()
			if pendingCursorShow && !tr.Complete() {
				app.HideCursor()
			}
			if focused {
				app.HideCursor()
			}
			scheduleDraftSave()
		})
	}

	return Controls{
		Open: func() {
			reset()
			id, err := cache.NewDraftID()
			if err != nil {
				log.Printf("Open: NewDraftID failed: %v", err)
				return
			}
			currentDraftID = id
			ed.SetTypewriterMode(true)
			composeActive = true
			pendingCursorShow = true
			tr.Start()
			app.Go("compose")
		},
		SetupReply: func(thread provider.Thread) {
			currentDraftID = thread.ID
			lastMsg := thread.Messages[len(thread.Messages)-1]
			replyMsg = &lastMsg

			if loadDraft(thread.ID) {
				return
			}

			to = lastMsg.From.String()
			s := lastMsg.Subject
			if !strings.HasPrefix(strings.ToLower(s), "re:") {
				s = "Re: " + s
			}
			subject = s

			body := lastMsg.TextBody
			if body == "" && lastMsg.HTMLBody != "" {
				body = lastMsg.HTMLBody
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
		ResumeLast: func() {
			if db == nil {
				return
			}
			d, found, err := db.GetLastDraft()
			if err != nil || !found {
				*statusText = "no drafts to resume"
				app.RequestRender()
				return
			}
			reset()
			currentDraftID = d.ThreadID
			to = d.To
			cc = d.Cc
			subject = d.Subject
			fieldTo.Value = d.To
			fieldTo.Cursor = len(d.To)
			fieldCC.Value = d.Cc
			fieldCC.Cursor = len(d.Cc)
			fieldSubject.Value = d.Subject
			fieldSubject.Cursor = len(d.Subject)
			ed.LoadMarkdown(d.Body)

			if d.ThreadID != "" {
				if t, err := db.GetThread(d.ThreadID); err == nil && len(t.Messages) > 0 {
					lastMsg := t.Messages[len(t.Messages)-1]
					replyMsg = &lastMsg
				}
			}

			ed.SetTypewriterMode(true)
			composeActive = true
			pendingCursorShow = true
			tr.Start()
			app.Go("compose")
		},
		ResumeDraft: func(threadID string) {
			if db == nil {
				return
			}
			d, found, err := db.GetDraft(threadID)
			if err != nil || !found {
				*statusText = "draft not found"
				app.RequestRender()
				return
			}
			reset()
			currentDraftID = d.ThreadID
			to = d.To
			cc = d.Cc
			subject = d.Subject
			fieldTo.Value = d.To
			fieldTo.Cursor = len(d.To)
			fieldCC.Value = d.Cc
			fieldCC.Cursor = len(d.Cc)
			fieldSubject.Value = d.Subject
			fieldSubject.Cursor = len(d.Subject)
			ed.LoadMarkdown(d.Body)

			if t, err := db.GetThread(d.ThreadID); err == nil && len(t.Messages) > 0 {
				lastMsg := t.Messages[len(t.Messages)-1]
				replyMsg = &lastMsg
			}

			ed.SetTypewriterMode(true)
			composeActive = true
			pendingCursorShow = true
			tr.Start()
			app.Go("compose")
		},
	}
}
