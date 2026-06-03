package composeview

import (
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/omnibox"
	"github.com/kungfusheep/mail/provider"
	"github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/transition"
	"github.com/kungfusheep/riffkey"
)

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

func replySubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

func forwardSubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		return subject
	}
	return "Fwd: " + subject
}

func replyAllHeaders(msg provider.Message, myEmail string) (string, string) {
	seen := make(map[string]bool)
	add := func(list *[]provider.Address, addr provider.Address) {
		email := strings.ToLower(strings.TrimSpace(addr.Email))
		if email == "" || email == strings.ToLower(strings.TrimSpace(myEmail)) || seen[email] {
			return
		}
		seen[email] = true
		*list = append(*list, addr)
	}

	var to, cc []provider.Address
	if !strings.EqualFold(msg.From.Email, myEmail) {
		add(&to, msg.From)
	}
	for _, addr := range msg.To {
		if len(to) == 0 {
			add(&to, addr)
		} else {
			add(&cc, addr)
		}
	}
	for _, addr := range msg.CC {
		add(&cc, addr)
	}
	return addressesString(to), addressesString(cc)
}

func addressesString(addrs []provider.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if text := strings.TrimSpace(addr.String()); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ", ")
}

func forwardedBody(msg provider.Message) string {
	body := msg.TextBody
	if body == "" && msg.HTMLBody != "" {
		body = msg.HTMLBody
	}
	var b strings.Builder
	b.WriteString("---------- Forwarded message ----------\n")
	if msg.From.Email != "" {
		b.WriteString("From: ")
		b.WriteString(msg.From.String())
		b.WriteByte('\n')
	}
	if !msg.Date.IsZero() {
		b.WriteString("Date: ")
		b.WriteString(msg.Date.Format("2 Jan 2006 15:04"))
		b.WriteByte('\n')
	}
	if msg.Subject != "" {
		b.WriteString("Subject: ")
		b.WriteString(msg.Subject)
		b.WriteByte('\n')
	}
	if len(msg.To) > 0 {
		b.WriteString("To: ")
		b.WriteString(addressesString(msg.To))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(body)
	return b.String()
}

func quotedDocument(body string) *compose.Document {
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
	return doc
}

func Setup(app *App, ed *compose.Editor, mb *mailbox.State, smtpClient *smtp.SMTP, db *cache.Cache, notify func(string), frame *int, tr *transition.Transition, palette theme.Theme, signature string, editorOmni *omnibox.OmniBox) mailbox.ComposeControls {
	if notify == nil {
		notify = func(string) {}
	}
	signature = strings.TrimRight(signature, "\r\n")
	inlineEd := compose.NewEditor(compose.NewDocument(), "")
	inlineEd.SetTheme(theme.ComposeTheme(palette))
	inlineEd.SetApp(app)

	var to, cc, subject string
	var attachments []provider.Attachment
	var attachmentRows []string
	var hasAttachments bool
	var replyMsg *provider.Message

	var fieldTo, fieldCC, fieldSubject InputState
	var fieldFocus FocusGroup
	var focused bool
	labelFrom, labelTo, labelCC, labelSub := palette.Muted, palette.Muted, palette.Muted, palette.Muted
	var toFieldRef, ccFieldRef NodeRef
	var contactResults []string
	var contactSel int
	var showContacts bool
	var showSending bool
	var sendingStatus string
	var composeActive bool
	var inlineActive bool
	var inlineRouterActive bool

	var searchQuery, searchPrompt string
	var searchFwd bool
	var attachPath string

	var currentDraftID string
	var draftSaveTimer *time.Timer
	var draftTouched bool

	var pendingCursorShow bool

	syncLabels := func() {
		labelFrom = palette.Muted
		for i, l := range []*Color{&labelTo, &labelCC, &labelSub} {
			if focused && fieldFocus.Current == i {
				*l = palette.Bright
			} else {
				*l = palette.Muted
			}
		}
	}

	applyPalette := func(next theme.Theme) {
		palette = next
		ed.SetTheme(theme.ComposeTheme(palette))
		inlineEd.SetTheme(theme.ComposeTheme(palette))
		syncLabels()
		app.RequestRender()
	}

	setHeaderFields := func(nextTo, nextCC, nextSubject string) {
		to = nextTo
		cc = nextCC
		subject = nextSubject
		fieldTo.Value = nextTo
		fieldTo.Cursor = len(nextTo)
		fieldCC.Value = nextCC
		fieldCC.Cursor = len(nextCC)
		fieldSubject.Value = nextSubject
		fieldSubject.Cursor = len(nextSubject)
	}

	repairMissingReplyHeaders := func(msg provider.Message) bool {
		changed := false
		if strings.TrimSpace(fieldTo.Value) == "" {
			to = msg.From.String()
			fieldTo.Value = to
			fieldTo.Cursor = len(to)
			changed = true
		}
		if strings.TrimSpace(fieldSubject.Value) == "" {
			subject = replySubject(msg.Subject)
			fieldSubject.Value = subject
			fieldSubject.Cursor = len(subject)
			changed = true
		}
		return changed
	}

	tr.CursorOverlay(func() (int, int, Color, bool) {
		if !composeActive || focused {
			return 0, 0, Color{}, false
		}
		x, y := ed.CursorScreenPos()
		return x, y, cursorColor(ed), true
	})

	syncInlineCursor := func() {
		if !inlineActive {
			inlineEd.Layer().HideCursor()
			return
		}
		x, y := inlineEd.CursorScreenPos()
		inlineEd.Layer().SetCursor(x, y)
		inlineEd.Layer().SetCursorStyle(CursorBlock)
		inlineEd.Layer().ShowCursor()
		app.SetCursorColor(cursorColor(inlineEd))
		app.HideCursor()
	}

	reset := func() {
		replyMsg = nil
		attachments = nil
		attachmentRows = nil
		hasAttachments = false
		setHeaderFields("", "", "")
		fieldFocus.Current = -1
		focused = false
		ed.ResetEmpty()
		currentDraftID = ""
		draftTouched = false
	}

	resetWithSignature := func(target *compose.Editor) {
		if signature == "" {
			target.ResetEmpty()
			return
		}
		target.LoadMarkdown(signature)
	}

	signedBody := func(body string) string {
		body = strings.TrimLeft(body, "\r\n")
		if signature == "" {
			return body
		}
		if body == "" {
			return signature
		}
		return signature + "\n\n" + body
	}

	activeEditor := func() *compose.Editor {
		if inlineActive {
			return inlineEd
		}
		return ed
	}

	snapshotDraft := func() cache.Draft {
		return cache.Draft{
			ThreadID: currentDraftID,
			To:       fieldTo.Value,
			Cc:       fieldCC.Value,
			Subject:  fieldSubject.Value,
			Body:     activeEditor().Markdown(),
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
		if (!composeActive && !inlineActive) || db == nil {
			return
		}
		draftTouched = true
		if draftSaveTimer != nil {
			draftSaveTimer.Stop()
		}
		draftSaveTimer = time.AfterFunc(time.Second, saveDraft)
	}

	loadDraftInto := func(threadID string, target *compose.Editor) bool {
		if db == nil {
			return false
		}
		d, found, err := db.GetDraft(threadID)
		if err != nil || !found {
			return false
		}
		setHeaderFields(d.To, d.Cc, d.Subject)
		target.LoadMarkdown(d.Body)
		return true
	}

	loadDraft := func(threadID string) bool {
		return loadDraftInto(threadID, ed)
	}

	sendFrom := func(sendEd *compose.Editor, onSent func()) {
		log.Printf("sendMessage: to=%q cc=%q subject=%q", to, cc, subject)
		if smtpClient == nil {
			log.Println("sendMessage: no smtp configured")
			notify("send unavailable: smtp not configured")
			app.RequestRender()
			return
		}
		msg := provider.Message{
			To:          provider.ParseAddressList(to),
			CC:          provider.ParseAddressList(cc),
			Subject:     subject,
			HTMLBody:    sendEd.ToHTML(),
			TextBody:    sendEd.ToPlainText(),
			Attachments: append([]provider.Attachment(nil), attachments...),
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
				notify(fmt.Sprintf("send failed: %v", err))
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
			notify(fmt.Sprintf("sent to %s", to))
			if onSent != nil {
				onSent()
			}
			app.RequestRender()
		}()
	}

	send := func() {
		sendFrom(ed, func() {
			composeActive = false
			reset()
			app.HideCursor()
			app.Go("main")
		})
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
					HBox.Gap(1)(
						Text("FROM").FG(&labelFrom),
						Text(mb.Email()).FG(&palette.Subtle),
					),
					HBox.Gap(1).NodeRef(&toFieldRef)(
						Text("TO").FG(&labelTo),
						Input().Field(&fieldTo).FocusGroup(&fieldFocus, 0).
							Placeholder("·····").PlaceholderStyle(Style{Attr: AttrDim}),
					),
					HBox.Gap(1).NodeRef(&ccFieldRef)(
						Text("CC").FG(&labelCC),
						Input().Field(&fieldCC).FocusGroup(&fieldFocus, 1).
							Placeholder("·····").PlaceholderStyle(Style{Attr: AttrDim}),
					),
					HBox.Gap(1)(
						Text("SUBJECT").FG(&labelSub),
						Input().Field(&fieldSubject).FocusGroup(&fieldFocus, 2).
							Placeholder("·····").PlaceholderStyle(Style{Attr: AttrDim}),
					),
					If(&hasAttachments).Then(
						VBox.PaddingTRBL(1, 0, 0, 0)(
							ForEach(&attachmentRows, func(row *string) Component {
								return Text(row).Dim()
							}),
						),
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
			editorOmniboxView(editorOmni),
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

	inlineEd.Layer().Render = func() {
		w := inlineEd.Layer().ViewportWidth()
		h := inlineEd.Layer().ViewportHeight()
		if w > 0 && h > 0 {
			inlineEd.SetSize(w, h)
			inlineEd.UpdateDisplay()
			syncInlineCursor()
		}
	}
	inlineEd.Layer().AlwaysRender = true

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

	app.View("compose-attach",
		VBox(
			HBox(
				Text("attach ").Bold(),
				Text(&attachPath),
			),
		),
	).
		Handle("<CR>", func() {
			path := attachPath
			attachPath = ""
			app.ShowCursor()
			app.PopView()
			attachment, err := localAttachment(path)
			if err != nil {
				notify(fmt.Sprintf("attach: %v", err))
				app.RequestRender()
				return
			}
			attachments = append(attachments, attachment)
			attachmentRows = append(attachmentRows, composeAttachmentRow(attachment))
			hasAttachments = len(attachmentRows) > 0
			notify(fmt.Sprintf("attached %s", attachment.Filename))
			app.RequestRender()
		}).
		Handle("<Esc>", func() {
			attachPath = ""
			app.ShowCursor()
			app.PopView()
		}).
		Handle("<BS>", func() {
			if len(attachPath) > 0 {
				runes := []rune(attachPath)
				attachPath = string(runes[:len(runes)-1])
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

	if attachRouter, ok := app.ViewRouter("compose-attach"); ok {
		attachRouter.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				attachPath += string(k.Rune)
				app.RequestRender()
				return true
			}
			return false
		})
	}

	var demoteFullReply func()

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
		router.Handle("<C-o>", func(_ riffkey.Match) { demoteFullReply() })
		router.Handle(":inline<CR>", func(_ riffkey.Match) { demoteFullReply() })
		router.Handle(":attach<CR>", func(_ riffkey.Match) {
			attachPath = ""
			app.HideCursor()
			app.PushView("compose-attach")
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
		registerEditorOmnibox(app, editorOmni, router, ed, notify, scheduleDraftSave)

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

	var closeInlineReply func(save bool)
	closeInlineReply = func(save bool) {
		if !inlineActive {
			return
		}
		if save && draftTouched {
			saveDraft()
			go mb.ProcessPendingCommands()
		}
		inlineActive = false
		inlineEd.Layer().HideCursor()
		if inlineRouterActive {
			app.Pop()
			inlineRouterActive = false
		}
		reset()
		app.HideCursor()
		app.RequestRender()
	}

	var enterInlineInsert func()
	var pushInlineRouter func()

	promoteInlineReply := func() {
		if !inlineActive {
			return
		}
		if inlineRouterActive {
			app.Pop()
			inlineRouterActive = false
		}
		ed.LoadMarkdown(inlineEd.Markdown())
		inlineEd.Layer().HideCursor()
		ed.SetTypewriterMode(true)
		composeActive = true
		inlineActive = false
		pendingCursorShow = true
		tr.Start()
		app.Go("compose")
	}

	demoteFullReply = func() {
		if !composeActive || replyMsg == nil {
			return
		}
		if inlineRouterActive {
			app.Pop()
			inlineRouterActive = false
		}
		inlineEd.LoadMarkdown(ed.Markdown())
		inlineEd.SetTypewriterMode(false)
		ed.Layer().HideCursor()
		composeActive = false
		inlineActive = true
		pendingCursorShow = false
		pushInlineRouter()
		inlineEd.EnterInsert()
		enterInlineInsert()
		inlineEd.UpdateDisplay()
		syncInlineCursor()
		app.Go("main")
		app.RequestRender()
	}

	enterInlineInsert = func() {
		compose.RegisterInsertMode(app, inlineEd, func() {
			syncInlineCursor()
			scheduleDraftSave()
			app.RequestRender()
		})
	}

	pushInlineRouter = func() {
		if inlineRouterActive {
			return
		}
		r := riffkey.NewRouter().Name("inline-reply")
		r.Handle("<Esc>", func(_ riffkey.Match) { closeInlineReply(true) })
		r.Handle("<C-q>", func(_ riffkey.Match) { closeInlineReply(true) })
		r.Handle("<C-s>", func(_ riffkey.Match) {
			if strings.TrimSpace(to) != "" {
				sendFrom(inlineEd, func() {
					inlineActive = false
					inlineEd.Layer().HideCursor()
					if inlineRouterActive {
						app.Pop()
						inlineRouterActive = false
					}
					reset()
					app.HideCursor()
				})
			}
		})
		r.Handle("<C-o>", func(_ riffkey.Match) { promoteInlineReply() })
		registerEditorOmnibox(app, editorOmni, r, inlineEd, notify, func() {
			syncInlineCursor()
			scheduleDraftSave()
		})
		compose.RegisterNormalMode(r, app, inlineEd,
			enterInlineInsert,
			func() { compose.RegisterVisualMode(app, inlineEd) },
		)
		r.AddOnAfter(func() {
			if !inlineActive {
				return
			}
			inlineEd.UpdateDisplay()
			syncInlineCursor()
			scheduleDraftSave()
			app.RequestRender()
		})
		app.Push(r)
		inlineRouterActive = true
	}

	setupInlineReply := func(thread provider.Thread) {
		reset()
		currentDraftID = thread.ID
		lastMsg := thread.Messages[len(thread.Messages)-1]
		replyMsg = &lastMsg

		if loadDraftInto(thread.ID, inlineEd) {
			if repairMissingReplyHeaders(lastMsg) {
				saveDraft()
			}
		} else {
			setHeaderFields(lastMsg.From.String(), "", replySubject(lastMsg.Subject))
			resetWithSignature(inlineEd)
		}

		inlineEd.SetTypewriterMode(false)
		inlineActive = true
		draftTouched = false
		pushInlineRouter()
		inlineEd.EnterInsert()
		enterInlineInsert()
		inlineEd.UpdateDisplay()
		syncInlineCursor()
		app.RequestRender()
	}

	return mailbox.ComposeControls{
		Open: func() {
			reset()
			id, err := cache.NewDraftID()
			if err != nil {
				log.Printf("Open: NewDraftID failed: %v", err)
				return
			}
			currentDraftID = id
			resetWithSignature(ed)
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
				if repairMissingReplyHeaders(lastMsg) {
					saveDraft()
				}
				return
			}

			setHeaderFields(lastMsg.From.String(), "", replySubject(lastMsg.Subject))
			resetWithSignature(ed)
		},
		SetupReplyAll: func(thread provider.Thread) {
			currentDraftID = thread.ID
			lastMsg := thread.Messages[len(thread.Messages)-1]
			replyMsg = &lastMsg

			if loadDraft(thread.ID) {
				return
			}

			replyTo, replyCC := replyAllHeaders(lastMsg, mb.Email())
			setHeaderFields(replyTo, replyCC, replySubject(lastMsg.Subject))
			resetWithSignature(ed)
		},
		SetupForward: func(thread provider.Thread) {
			lastMsg := thread.Messages[len(thread.Messages)-1]
			replyMsg = nil
			setHeaderFields("", "", forwardSubject(lastMsg.Subject))
			doc := quotedDocument(signedBody(forwardedBody(lastMsg)))
			ed.ResetDocument(doc)
		},
		OpenInlineReply: setupInlineReply,
		ToggleInline:    demoteFullReply,
		ApplyTheme:      applyPalette,
		InlineView: func(previewRef *NodeRef) Component {
			return If(&inlineActive).Then(
				Overlay.OnTop(previewRef)(
					VBox(
						Space().Grow(1),
						HBox.PaddingTRBL(0, 2, 1, 2)(
							VBox.Width(72).Border(BorderSoft).BorderFG(&palette.Muted).Fill(&palette.BG).PaddingTRBL(1, 1, 1, 1)(
								HBox(
									Text("reply").FG(&palette.Bright).Bold(),
									SpaceW(1),
									Text(&to).FG(&palette.Subtle),
									Space(),
									Text("i edit  c-s send  c-o full  esc close").FG(&palette.Muted),
								),
								SpaceH(1),
								LayerView(inlineEd.Layer()).Height(7),
							),
						),
					),
				),
			)
		},
		ResumeLast: func() {
			if db == nil {
				return
			}
			d, found, err := db.GetLastDraft()
			if err != nil || !found {
				notify("no drafts to resume")
				app.RequestRender()
				return
			}
			reset()
			currentDraftID = d.ThreadID
			setHeaderFields(d.To, d.Cc, d.Subject)
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
				notify("draft not found")
				app.RequestRender()
				return
			}
			reset()
			currentDraftID = d.ThreadID
			setHeaderFields(d.To, d.Cc, d.Subject)
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

func localAttachment(path string) (provider.Attachment, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return provider.Attachment{}, fmt.Errorf("missing path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return provider.Attachment{}, err
	}
	if info.IsDir() {
		return provider.Attachment{}, fmt.Errorf("%s is a directory", path)
	}
	name := filepath.Base(path)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return provider.Attachment{
		Filename:    name,
		ContentType: contentType,
		Size:        info.Size(),
		LocalPath:   path,
	}, nil
}

func composeAttachmentRow(attachment provider.Attachment) string {
	return fmt.Sprintf("%s %s", provider.AttachmentIcon(attachment.Filename, attachment.ContentType), attachment.Filename)
}
