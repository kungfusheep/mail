package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/compose"
	imapprov "github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/preview"
	"github.com/kungfusheep/mail/provider"
	smtpprov "github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/riffkey"
)

// app state — single source of truth
var state struct {
	folders       []provider.Folder
	activeFolder  int
	threads       []provider.Thread
	activeThread  int
	activeMessage int
	previewLines  []string
	imap          *imapprov.IMAP
	smtp          *smtpprov.SMTP
	app           *App
}

// ui state
var (
	// display slices derived from app state
	folderNames []string
	threadRows  []ThreadRow
	folderSel   int
	threadSel   int

	statusText string
	pane       int // 0=folders, 1=threads, 2=preview
	frame      int

	folderBorder  = White
	threadBorder  = BrightBlack
	previewBorder = BrightBlack

	// compose
	composeTo      string
	composeCC      string
	composeSubject string
	editor         *compose.Editor
	replyThreadID  string
	replyMsg       *provider.Message

	// compose search
	composeSearchQuery  string
	composeSearchPrompt string
	composeSearchFwd    bool

	// compose ui state
	showDiscardPrompt bool
	sendPanelFocused  bool
	modalStyle        = Style{FG: Hex(0x2d2d2d), BG: Hex(0xfaf8f5)}

	// send panel fields
	fieldTo      InputState
	fieldCC      InputState
	fieldSubject InputState
	fieldFocus   FocusGroup
	labelTo      = Hex(0xcccccc)
	labelCC      = Hex(0xcccccc)
	labelSub     = Hex(0xcccccc)
)

type ThreadRow struct {
	ThreadIdx int
	MsgIdx    int // -1 for thread header, >= 0 for message
	Label     string
	Detail    string
	Date      string
	Unread    bool
	Expanded  bool
}

func main() {
	app := NewApp()
	state.app = app

	// connect to IMAP
	cfg, err := imapprov.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	state.imap = imapprov.New(cfg)
	if err := state.imap.Authenticate(); err != nil {
		log.Fatal(err)
	}
	state.smtp = smtpprov.New(smtpprov.Config{
		Server:   cfg.SMTPServer,
		Email:    cfg.Email,
		Password: cfg.Password,
	})

	loadFolders()
	syncFolderList()
	syncThreadList()

	selectedStyle := Style{Attr: AttrInverse}

	app.View("main",
		VBox(
			HBox.Gap(1)(
				Text("mail").Bold(),
				Space(),
				Text(&statusText).Dim(),
				Space(),
				Spinner(&frame).Frames(SpinnerDots),
			),
			HBox.Grow(1)(
				VBox.Grow(1).Border(BorderRounded).BorderFG(&folderBorder).Title("Folders")(
					List(&folderNames).
						Selection(&folderSel).
						SelectedStyle(selectedStyle).
						Marker("  "),
				),
				VBox.Grow(2).Border(BorderRounded).BorderFG(&threadBorder).Title("Threads")(
					List(&threadRows).
						Selection(&threadSel).
						SelectedStyle(selectedStyle).
						Marker("  ").
						Render(func(row *ThreadRow) any {
							if row.MsgIdx >= 0 {
								return HBox.Gap(1)(
									SpaceW(2),
									Text(&row.Label).Dim(),
									Space(),
									Text(&row.Date).Dim(),
								)
							}
							if row.Unread {
								return HBox.Gap(1)(
									Text(&row.Label).Bold(),
									Space(),
									Text(&row.Detail),
									Text(&row.Date).Dim(),
								)
							}
							return HBox.Gap(1)(
								Text(&row.Label),
								Space(),
								Text(&row.Detail).Dim(),
								Text(&row.Date).Dim(),
							)
						}),
				),
				VBox.Grow(3).Border(BorderRounded).BorderFG(&previewBorder).Title("Preview")(
					ForEach(&state.previewLines, func(line *string) any {
						return Text(line)
					}),
				),
			),
			Text("q quit  h/l pane  j/k nav  enter select/expand  o expand  c compose  r reply").Dim(),
		),
	).NoCounts().
		Handle("q", app.Stop).
		Handle("j", func() {
			switch pane {
			case 0:
				if folderSel < len(folderNames)-1 {
					folderSel++
				}
			case 1:
				if threadSel < len(threadRows)-1 {
					threadSel++
				}
			}
		}).
		Handle("k", func() {
			switch pane {
			case 0:
				if folderSel > 0 {
					folderSel--
				}
			case 1:
				if threadSel > 0 {
					threadSel--
				}
			}
		}).
		Handle("l", func() {
			if pane < 2 {
				pane++
				updateBorders()
			}
		}).
		Handle("h", func() {
			if pane > 0 {
				pane--
				updateBorders()
			}
		}).
		Handle("<Tab>", func() {
			pane = (pane + 1) % 3
			updateBorders()
		}).
		Handle("<S-Tab>", func() {
			pane = (pane + 2) % 3
			updateBorders()
		}).
		Handle("<Enter>", func() {
			switch pane {
			case 0:
				state.activeFolder = folderSel
				syncThreadList()
				threadSel = 0
				pane = 1
				updateBorders()
				statusText = state.folders[folderSel].Name
			case 1:
				handleThreadEnter()
			}
		}).
		Handle("<Escape>", func() {
			if pane > 0 {
				pane--
				updateBorders()
			}
		}).
		Handle("o", func() {
			if pane == 1 {
				handleThreadToggle()
			}
		}).
		Handle("c", func() {
			openCompose(app)
		}).
		Handle("r", func() {
			if threadSel >= 0 && threadSel < len(threadRows) {
				row := threadRows[threadSel]
				if row.MsgIdx < 0 && row.ThreadIdx < len(state.threads) {
					openCompose(app)
					setupReply(state.threads[row.ThreadIdx])
				}
			}
		})

	// compose view — editor first, omnibox for actions
	editor = compose.NewEditor(compose.NewDocument(), "")
	editor.SetApp(app)
	composeSubject = ""

	// wire enterInsertMode for operator text object combos
	compose.SetEnterInsertMode(func(a *App, e *compose.Editor) {
		composeEnterInsertMode(a, e)
	})

	// derive subject from editor content on every change

	// start spell check
	editor.StartSpellResultWorker(app.RequestRender)

	// compose view layout
	app.View("compose",
		VBox(
			LayerView(editor.Layer()).Grow(1),

			VBox.Fill(Hex(0xfaf8f5))(
				SpaceH(1),
				HBox(Space(), VBox.Width(60)(
					HBox.Gap(1)(
						Text("TO").FG(&labelTo),
						TextInput{Field: &fieldTo, FocusGroup: &fieldFocus, FocusIndex: 0,
							Placeholder: "·····", PlaceholderStyle: Style{FG: Hex(0xcccccc)},
							Style: Style{FG: Hex(0x2d2d2d)}},
					),
					HBox.Gap(1)(
						Text("CC").FG(&labelCC),
						TextInput{Field: &fieldCC, FocusGroup: &fieldFocus, FocusIndex: 1,
							Placeholder: "·····", PlaceholderStyle: Style{FG: Hex(0xcccccc)},
							Style: Style{FG: Hex(0x2d2d2d)}},
					),
					HBox.Gap(1)(
						Text("SUBJECT").FG(&labelSub),
						TextInput{Field: &fieldSubject, FocusGroup: &fieldFocus, FocusIndex: 2,
							Placeholder: "·····", PlaceholderStyle: Style{FG: Hex(0xcccccc)},
							Style: Style{FG: Hex(0x2d2d2d)}},
					),
				), Space()),
				SpaceH(1),
			),

			If(&showDiscardPrompt).Then(
				Overlay.Centered().Backdrop().BackdropFG(BrightBlack)(
					VBox.Border(BorderRounded).BorderFG(Hex(0xcccccc)).Fill(Hex(0xfaf8f5)).CascadeStyle(&modalStyle).Width(40)(
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
		),
	).NoCounts()

	// render callback — glyph calls this with correct viewport size
	editor.Layer().Render = func() {
		w := editor.Layer().ViewportWidth()
		h := editor.Layer().ViewportHeight()
		if w > 0 && h > 0 {
			editor.SetSize(w, h)
			editor.UpdateDisplay()
		}
	}
	editor.Layer().AlwaysRender = true

	// compose search view (for / and ?)
	app.View("compose-search",
		VBox(
			HBox(
				Text(&composeSearchPrompt).Bold(),
				Text(&composeSearchQuery),
			),
		),
	).
		Handle("<CR>", func() {
			q := composeSearchQuery
			fwd := composeSearchFwd
			composeSearchQuery = ""
			app.ShowCursor()
			app.PopView()
			if q != "" {
				editor.Search(q, fwd)
			}
			editor.Refresh()
		}).
		Handle("<Esc>", func() {
			composeSearchQuery = ""
			app.ShowCursor()
			app.PopView()
			editor.Refresh()
		}).
		Handle("<BS>", func() {
			if len(composeSearchQuery) > 0 {
				runes := []rune(composeSearchQuery)
				composeSearchQuery = string(runes[:len(runes)-1])
			}
		}).
		NoCounts()

	if searchRouter, ok := app.ViewRouter("compose-search"); ok {
		searchRouter.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				composeSearchQuery += string(k.Rune)
				app.RequestRender()
				return true
			}
			return false
		})
	}

	// wire all normal mode keybindings on the compose view's router
	if composeRouter, ok := app.ViewRouter("compose"); ok {
		setupComposeNormalMode(composeRouter, app, editor)
		composeRouter.AddOnAfter(func() {
			editor.Refresh()
			if sendPanelFocused {
				app.HideCursor()
			}
		})
	}

	// spinner
	go func() {
		for range time.Tick(80 * time.Millisecond) {
			frame++
			app.RequestRender()
		}
	}()

	statusText = "Inbox"
	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}

// data sync — derives display slices from app state

func loadFolders() {
	folders, err := state.imap.ListFolders()
	if err != nil {
		statusText = fmt.Sprintf("error: %v", err)
		return
	}
	state.folders = folders
}

func syncFolderList() {
	folderNames = nil
	for _, f := range state.folders {
		name := f.Name
		if f.Unread > 0 {
			name = fmt.Sprintf("%s (%d)", f.Name, f.Unread)
		}
		folderNames = append(folderNames, name)
	}
}

func syncThreadList() {
	threadRows = nil
	if len(state.folders) == 0 {
		return
	}
	folder := state.folders[state.activeFolder]

	result, err := state.imap.ListThreads(provider.ListOptions{
		Folder:     folder.ID,
		MaxResults: 25,
	})
	if err != nil {
		statusText = fmt.Sprintf("error: %v", err)
		return
	}
	state.threads = result.Threads

	for i, t := range state.threads {
		threadRows = append(threadRows, ThreadRow{
			ThreadIdx: i,
			MsgIdx:    -1,
			Label:     truncate(t.Subject, 35),
			Detail:    fmt.Sprintf("[%d]", len(t.Messages)),
			Date:      relativeTime(t.Date),
			Unread:    t.Unread > 0,
		})
	}
}


func handleThreadEnter() {
	if threadSel < 0 || threadSel >= len(threadRows) {
		return
	}
	row := threadRows[threadSel]
	if row.MsgIdx >= 0 {
		// message row — show preview
		t := state.threads[row.ThreadIdx]
		if row.MsgIdx < len(t.Messages) {
			loadPreview(state.app, t.Messages[row.MsgIdx])
			pane = 2
			updateBorders()
		}
		return
	}
	// thread header — expand or show first message preview
	handleThreadToggle()
	t := state.threads[row.ThreadIdx]
	if len(t.Messages) > 0 {
		loadPreview(state.app, t.Messages[len(t.Messages)-1])
	}
}

func handleThreadToggle() {
	if threadSel < 0 || threadSel >= len(threadRows) {
		return
	}
	row := &threadRows[threadSel]
	if row.MsgIdx >= 0 {
		return
	}

	row.Expanded = !row.Expanded
	t := state.threads[row.ThreadIdx]

	if row.Expanded {
		var msgRows []ThreadRow
		for j, m := range t.Messages {
			msgRows = append(msgRows, ThreadRow{
				ThreadIdx: row.ThreadIdx,
				MsgIdx:    j,
				Label:     truncate(m.From.Email, 25),
				Detail:    truncate(m.Subject, 20),
				Date:      relativeTime(m.Date),
				Unread:    !m.Read,
			})
		}
		after := make([]ThreadRow, len(threadRows[threadSel+1:]))
		copy(after, threadRows[threadSel+1:])
		threadRows = append(threadRows[:threadSel+1], msgRows...)
		threadRows = append(threadRows, after...)
	} else {
		end := threadSel + 1
		for end < len(threadRows) && threadRows[end].MsgIdx >= 0 {
			end++
		}
		threadRows = append(threadRows[:threadSel+1], threadRows[end:]...)
	}
}

func loadPreview(app *App, msg provider.Message) {
	// fetch full body if not already loaded
	if msg.TextBody == "" && msg.HTMLBody == "" && state.imap != nil {
		full, err := state.imap.GetMessage(msg.ID)
		if err == nil {
			msg = full
		}
	}

	// estimate preview pane width from terminal size
	// preview pane is Grow(3) out of total Grow(6) ≈ 50%, minus border/padding
	cols := 72
	s := app.Size()
	if s.Width > 0 {
		cols = s.Width/2 - 4
		if cols < 40 {
			cols = 40
		}
	}

	state.previewLines = nil
	state.previewLines = append(state.previewLines,
		fmt.Sprintf("From: %s", msg.From.String()),
		fmt.Sprintf("To: %s", formatAddresses(msg.To)),
		fmt.Sprintf("Date: %s", msg.Date.Format("2 Jan 2006 15:04")),
		fmt.Sprintf("Subject: %s", msg.Subject),
		"",
	)
	// render body — use w3m for html, detect html in misidentified text/plain
	body := msg.TextBody
	if msg.HTMLBody != "" {
		body = preview.RenderHTML(msg.HTMLBody, msg.TextBody, cols)
	} else if strings.Contains(body, "<p>") || strings.Contains(body, "<br") || strings.Contains(body, "<div") {
		body = preview.RenderHTML(body, "", cols)
	}
	for _, line := range strings.Split(body, "\n") {
		state.previewLines = append(state.previewLines, line)
	}
}

func formatAddresses(addrs []provider.Address) string {
	var parts []string
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

func updateBorders() {
	folderBorder = BrightBlack
	threadBorder = BrightBlack
	previewBorder = BrightBlack
	switch pane {
	case 0:
		folderBorder = White
	case 1:
		threadBorder = White
	case 2:
		previewBorder = White
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d"
		}
		return fmt.Sprintf("%dd", days)
	}
}

func resetCompose() {
	composeTo = ""
	composeCC = ""
	composeSubject = ""
	replyThreadID = ""
	replyMsg = nil
	fieldTo.Clear()
	fieldCC.Clear()
	fieldSubject.Clear()
	fieldFocus.Current = -1
	sendPanelFocused = false
	editor.ResetDocument(compose.NewDocument())
}

func openCompose(app *App) {
	resetCompose()
	// set typewriter mode before view activates — ensureCursorVisible
	// will centre correctly once the Layer gets its size on first render
	editor.SetTypewriterMode(true)
	app.Go("compose")
	editor.Refresh()
}

func setupReply(thread provider.Thread) {
	resetCompose()
	lastMsg := thread.Messages[len(thread.Messages)-1]
	replyMsg = &lastMsg
	replyThreadID = thread.ID
	composeTo = lastMsg.From.String()
	subject := lastMsg.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	composeSubject = subject
}

func sendMessage(app *App) {
	msg := provider.Message{
		To:       parseRecipients(composeTo),
		CC:       parseRecipients(composeCC),
		Subject:  composeSubject,
		HTMLBody: editor.ToHTML(),
		TextBody: editor.ToPlainText(),
	}
	if replyMsg != nil {
		msg.InReplyTo = replyMsg.MessageID
		msg.References = append(replyMsg.References, replyMsg.MessageID)
	}

	if state.smtp != nil {
		if err := state.smtp.Send(msg); err != nil {
			statusText = fmt.Sprintf("send failed: %v", err)
			return
		}
		statusText = fmt.Sprintf("sent to %s", composeTo)
	} else {
		statusText = "send failed: no smtp configured"
	}
	resetCompose()
	app.HideCursor()
	app.Go("main")
}

func parseRecipients(s string) []provider.Address {
	if s == "" {
		return nil
	}
	var addrs []provider.Address
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			addrs = append(addrs, provider.Address{Email: part})
		}
	}
	return addrs
}

// seedFakeData removed — using real IMAP data

func _seedFakeData() {
	now := time.Now()

	state.folders = []provider.Folder{
		{ID: "INBOX", Name: "Inbox", Unread: 3, Total: 47},
		{ID: "STARRED", Name: "Starred", Unread: 0, Total: 12},
		{ID: "SENT", Name: "Sent", Unread: 0, Total: 156},
		{ID: "DRAFT", Name: "Drafts", Unread: 0, Total: 2},
		{ID: "TRASH", Name: "Trash", Unread: 0, Total: 8},
	}

	state.threads = []provider.Thread{
		{
			ID: "t1", Subject: "production deploy blocked on failing e2e tests",
			Date: now.Add(-12 * time.Minute), Unread: 2,
			Participants: []provider.Address{
				{Name: "Alice Chen", Email: "alice@example.com"},
				{Name: "Bob Kumar", Email: "bob@example.com"},
				{Name: "Carol Zhang", Email: "carol@example.com"},
			},
			Messages: []provider.Message{
				{
					ID: "m1a", ThreadID: "t1", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Alice Chen", Email: "alice@example.com"},
					To:       []provider.Address{{Name: "Team", Email: "team@example.com"}},
					Subject:  "production deploy blocked on failing e2e tests",
					Date:     now.Add(-2 * time.Hour),
					TextBody: "hey team,\n\nthe e2e suite is failing on the checkout flow tests.\nlooks like the session token changes from last week broke the auth fixture.\n\ni've pinned the deploy pipeline until we fix this. can someone\ntake a look at the fixture setup in test/e2e/auth_helper.go?\n\nthanks,\nalice",
					Read:     true, MessageID: "<m1a@example.com>",
				},
				{
					ID: "m1b", ThreadID: "t1", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Bob Kumar", Email: "bob@example.com"},
					To:       []provider.Address{{Name: "Team", Email: "team@example.com"}},
					Subject:  "re: production deploy blocked on failing e2e tests",
					Date:     now.Add(-90 * time.Minute),
					TextBody: "i can take a look. the session token format changed from\njwt to opaque tokens — the fixture is still generating jwts.\n\nshould be a quick fix, i'll push a branch in 30 min.\n\nbob",
					Read:     true, InReplyTo: "<m1a@example.com>",
					MessageID: "<m1b@example.com>",
				},
				{
					ID: "m1c", ThreadID: "t1", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Carol Zhang", Email: "carol@example.com"},
					To:       []provider.Address{{Name: "Team", Email: "team@example.com"}},
					Subject:  "re: production deploy blocked on failing e2e tests",
					Date:     now.Add(-12 * time.Minute),
					TextBody: "bob's fix looks good. i ran the full suite locally and\neverything passes now. unblocking the deploy.\n\ncarol",
					Read:     false, InReplyTo: "<m1b@example.com>",
					MessageID: "<m1c@example.com>",
				},
			},
		},
		{
			ID: "t2", Subject: "weekly engineering sync — march 24",
			Date: now.Add(-3 * time.Hour), Unread: 0,
			Participants: []provider.Address{
				{Name: "Dave Park", Email: "dave@example.com"},
			},
			Messages: []provider.Message{
				{
					ID: "m2a", ThreadID: "t2", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Dave Park", Email: "dave@example.com"},
					To:       []provider.Address{{Name: "Engineering", Email: "eng@example.com"}},
					Subject:  "weekly engineering sync — march 24",
					Date:     now.Add(-3 * time.Hour),
					TextBody: "notes from today's sync:\n\n- api gateway migration is 80% complete\n- new hire onboarding starts next monday\n- reminder: code freeze for v2.4 is thursday\n\naction items:\n1. finish gateway canary rollout (eve)\n2. update runbook for new auth flow (bob)\n3. schedule load test for wednesday (alice)\n\ndave",
					Read:     true, MessageID: "<m2a@example.com>",
				},
			},
		},
		{
			ID: "t3", Subject: "api rate limiting — proposal for v2",
			Date: now.Add(-5 * time.Hour), Unread: 1,
			Participants: []provider.Address{
				{Name: "Eve Santos", Email: "eve@example.com"},
				{Name: "Alice Chen", Email: "alice@example.com"},
				{Name: "Frank Liu", Email: "frank@example.com"},
			},
			Messages: []provider.Message{
				{
					ID: "m3a", ThreadID: "t3", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Eve Santos", Email: "eve@example.com"},
					To:       []provider.Address{{Name: "Backend", Email: "backend@example.com"}},
					Subject:  "api rate limiting — proposal for v2",
					Date:     now.Add(-26 * time.Hour),
					TextBody: "i've been working on a new rate limiting approach for the api.\n\ncurrent system: fixed window, 1000 req/min per api key\nproposed: sliding window with token bucket, configurable per endpoint\n\nthe main wins:\n- burst tolerance without long-term abuse\n- per-endpoint granularity (search can be tighter than reads)\n- better observability — we can track token consumption patterns\n\ndraft doc is here. thoughts?\n\neve",
					Read:     true, MessageID: "<m3a@example.com>",
				},
				{
					ID: "m3b", ThreadID: "t3", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Alice Chen", Email: "alice@example.com"},
					To:       []provider.Address{{Name: "Backend", Email: "backend@example.com"}},
					Subject:  "re: api rate limiting — proposal for v2",
					Date:     now.Add(-20 * time.Hour),
					TextBody: "this looks solid. one concern: the token bucket refill rate\nneeds to account for our bursty enterprise clients. some of\nthem legitimately spike to 5x normal during their batch jobs.\n\ncan we add a \"burst multiplier\" config per api key tier?\n\nalice",
					Read:     true, InReplyTo: "<m3a@example.com>",
					MessageID: "<m3b@example.com>",
				},
				{
					ID: "m3c", ThreadID: "t3", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Frank Liu", Email: "frank@example.com"},
					To:       []provider.Address{{Name: "Backend", Email: "backend@example.com"}},
					Subject:  "re: api rate limiting — proposal for v2",
					Date:     now.Add(-5 * time.Hour),
					TextBody: "+1 on the burst multiplier idea. also we should make sure\nthe 429 response includes a retry-after header with the\nactual token refill time, not just a generic \"try later\".\n\nfrank",
					Read:     false, InReplyTo: "<m3b@example.com>",
					MessageID: "<m3c@example.com>",
				},
			},
		},
		{
			ID: "t4", Subject: "office furniture order confirmation",
			Date: now.Add(-1 * 24 * time.Hour), Unread: 0,
			Participants: []provider.Address{
				{Name: "Office Supplies Co", Email: "orders@officesupplies.example.com"},
			},
			Messages: []provider.Message{
				{
					ID: "m4a", ThreadID: "t4", Labels: []string{"INBOX"},
					From:     provider.Address{Name: "Office Supplies Co", Email: "orders@officesupplies.example.com"},
					To:       []provider.Address{{Email: "pete@example.com"}},
					Subject:  "office furniture order confirmation",
					Date:     now.Add(-1 * 24 * time.Hour),
					TextBody: "your order #OSC-7234 has been confirmed.\n\nitems:\n- standing desk frame (black) x1\n- monitor arm (dual) x1\n- desk mat (dark grey, 90x40) x1\n\nestimated delivery: march 28\n\nthank you for your order.",
					Read:     true, MessageID: "<m4a@officesupplies.example.com>",
				},
			},
		},
		{
			ID: "t5", Subject: "security audit results — q1 2026",
			Date: now.Add(-2 * 24 * time.Hour), Unread: 0,
			Participants: []provider.Address{
				{Name: "Grace Torres", Email: "grace@example.com"},
			},
			Messages: []provider.Message{
				{
					ID: "m5a", ThreadID: "t5", Labels: []string{"INBOX", "STARRED"},
					From:     provider.Address{Name: "Grace Torres", Email: "grace@example.com"},
					To:       []provider.Address{{Name: "Engineering", Email: "eng@example.com"}},
					Subject:  "security audit results — q1 2026",
					Date:     now.Add(-2 * 24 * time.Hour),
					TextBody: "hi all,\n\nthe q1 security audit is complete. summary:\n\n- 0 critical findings\n- 2 high (both in legacy auth, already patched)\n- 5 medium (dependency updates, tracking in jira)\n- 12 low/informational\n\nfull report attached. the two high findings were:\n1. session tokens not invalidated on password change\n2. csrf token reuse across form submissions\n\nboth patches are deployed as of yesterday.\n\ngrace",
					Read:     true, MessageID: "<m5a@example.com>",
				},
			},
		},
		// sent folder thread
		{
			ID: "t6", Subject: "re: lunch wednesday?",
			Date: now.Add(-4 * time.Hour), Unread: 0,
			Participants: []provider.Address{
				{Email: "pete@example.com"},
				{Name: "Dave Park", Email: "dave@example.com"},
			},
			Messages: []provider.Message{
				{
					ID: "m6a", ThreadID: "t6", Labels: []string{"SENT"},
					From:     provider.Address{Email: "pete@example.com"},
					To:       []provider.Address{{Name: "Dave Park", Email: "dave@example.com"}},
					Subject:  "re: lunch wednesday?",
					Date:     now.Add(-4 * time.Hour),
					TextBody: "yeah sounds good, the ramen place on 5th? 12:30?\n\npete",
					Read:     true, MessageID: "<m6a@example.com>",
				},
			},
		},
	}
}

// compose keybinding architecture — mirrors wed's main.go via cmd/test

func setupComposeNormalMode(router *riffkey.Router, app *App, ed *compose.Editor) {
	// exit compose
	exitCompose := func() { resetCompose(); app.HideCursor(); app.Go("main") }

	router.Handle("<C-q>", func(_ riffkey.Match) { exitCompose() })

	router.Handle("<Esc>", func(_ riffkey.Match) {
		ed.ExitDialogueIfEmpty()
		if !ed.Dirty() {
			exitCompose()
			return
		}
		showDiscardPrompt = true
		confirm := riffkey.NewRouter().Name("confirm-discard").NoCounts()
		dismiss := func() { showDiscardPrompt = false; app.Pop() }
		confirm.Handle("y", func(_ riffkey.Match) { dismiss(); exitCompose() })
		confirm.Handle("Y", func(_ riffkey.Match) { dismiss(); exitCompose() })
		confirm.Handle("<CR>", func(_ riffkey.Match) { dismiss(); exitCompose() })
		confirm.Handle("n", func(_ riffkey.Match) { dismiss() })
		confirm.Handle("N", func(_ riffkey.Match) { dismiss() })
		confirm.Handle("<Esc>", func(_ riffkey.Match) { dismiss() })
		confirm.AddOnAfter(func() { app.RequestRender() })
		app.Push(confirm)
	})

	// send panel — Tab focuses the fields, Esc/Enter/Tab-past-end returns to editor
	fieldStates := []*InputState{&fieldTo, &fieldCC, &fieldSubject}
	labels := []*Color{&labelTo, &labelCC, &labelSub}
	dimColor := Hex(0xcccccc)
	activeColor := Hex(0x2d2d2d)

	syncLabels := func() {
		for i, l := range labels {
			if sendPanelFocused && fieldFocus.Current == i {
				*l = activeColor
			} else {
				*l = dimColor
			}
		}
	}

	bindCurrentField := func(fr *riffkey.Router) {
		f := fieldStates[fieldFocus.Current]
		th := riffkey.NewTextHandler(&f.Value, &f.Cursor)
		fr.HandleUnmatched(th.HandleKey)
		fr.NoCounts()
		syncLabels()
	}

	exitFields := func() {
		composeTo = fieldTo.Value
		composeCC = fieldCC.Value
		composeSubject = fieldSubject.Value
		fieldFocus.Current = -1
		sendPanelFocused = false
		syncLabels()
		app.Pop()
		ed.Refresh()
	}

	router.Handle("<Tab>", func(_ riffkey.Match) {
		sendPanelFocused = true
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
		fr.Handle("<Esc>", func(_ riffkey.Match) { exitFields() })
		fr.Handle("<CR>", func(_ riffkey.Match) {
			// Enter on last field exits, otherwise advances
			next := fieldFocus.Current + 1
			if next >= len(fieldStates) {
				exitFields()
				return
			}
			fieldFocus.Current = next
			bindCurrentField(fr)
		})

		fr.AddOnAfter(func() { app.RequestRender() })
		app.Push(fr)
	})

	// send — Ctrl-S from editor or send panel
	router.Handle("<C-s>", func(_ riffkey.Match) {
		if composeTo != "" {
			sendMessage(app)
		}
	})

	// ex-style commands
	router.Handle(":send<CR>", func(_ riffkey.Match) {
		if composeTo != "" {
			sendMessage(app)
		}
	})
	router.Handle(":s<CR>", func(_ riffkey.Match) {
		if composeTo != "" {
			sendMessage(app)
		}
	})

	// movement
	router.Handle("h", func(m riffkey.Match) { ed.Left(m.Count) })
	router.Handle("l", func(m riffkey.Match) { ed.Right(m.Count) })
	router.Handle("j", func(m riffkey.Match) { ed.Down(m.Count) })
	router.Handle("k", func(m riffkey.Match) { ed.Up(m.Count) })
	router.Handle("<Left>", func(m riffkey.Match) { ed.Left(m.Count) })
	router.Handle("<Right>", func(m riffkey.Match) { ed.Right(m.Count) })
	router.Handle("<Down>", func(m riffkey.Match) { ed.Down(m.Count) })
	router.Handle("<Up>", func(m riffkey.Match) { ed.Up(m.Count) })
	router.Handle("gj", func(m riffkey.Match) { ed.BlockDown(m.Count) })
	router.Handle("gk", func(m riffkey.Match) { ed.BlockUp(m.Count) })
	router.Handle("0", func(_ riffkey.Match) { ed.LineStart() })
	router.Handle("$", func(_ riffkey.Match) { ed.LineEnd() })
	router.Handle("^", func(_ riffkey.Match) { ed.FirstNonBlank() })
	router.Handle("gg", func(_ riffkey.Match) { ed.DocStart() })
	router.Handle("G", func(_ riffkey.Match) { ed.DocEnd() })
	router.Handle("w", func(m riffkey.Match) { ed.NextWordStart(m.Count) })
	router.Handle("b", func(m riffkey.Match) { ed.PrevWordStart(m.Count) })
	router.Handle("e", func(m riffkey.Match) { ed.NextWordEnd(m.Count) })

	// scrolling
	router.Handle("<C-d>", func(_ riffkey.Match) { ed.ScrollHalfPageDown() })
	router.Handle("<C-u>", func(_ riffkey.Match) { ed.ScrollHalfPageUp() })
	router.Handle("<C-f>", func(_ riffkey.Match) { ed.ScrollPageDown() })
	router.Handle("<C-b>", func(_ riffkey.Match) { ed.ScrollPageUp() })
	router.Handle("<C-e>", func(_ riffkey.Match) { ed.ScrollLineDown() })
	router.Handle("<C-y>", func(_ riffkey.Match) { ed.ScrollLineUp() })
	router.Handle("zz", func(_ riffkey.Match) { ed.ScrollCenter() })
	router.Handle("zt", func(_ riffkey.Match) { ed.ScrollTop() })
	router.Handle("zb", func(_ riffkey.Match) { ed.ScrollBottom() })

	// view modes
	router.Handle("zT", func(_ riffkey.Match) { ed.ToggleTypewriterMode() })
	router.Handle("zf", func(_ riffkey.Match) { ed.ToggleFocusMode() })
	router.Handle("zF", func(_ riffkey.Match) { ed.CycleFocusScope() })
	router.Handle("gz", func(_ riffkey.Match) { ed.ToggleZenMode() })
	router.Handle("zr", func(_ riffkey.Match) { ed.ToggleRawMode() })
	router.Handle("\\t", func(_ riffkey.Match) { ed.ToggleTheme() })

	// spell check
	router.Handle("zg", func(_ riffkey.Match) { ed.AddWordToDictionary() })
	router.Handle("]e", func(_ riffkey.Match) { ed.NextMisspelled() })
	router.Handle("[e", func(_ riffkey.Match) { ed.PrevMisspelled() })

	// section navigation
	router.Handle("]S", func(_ riffkey.Match) { ed.NextSameLevel() })
	router.Handle("[S", func(_ riffkey.Match) { ed.PrevSameLevel() })

	// heading promote/demote
	router.Handle("g>", func(_ riffkey.Match) { ed.PromoteHeading() })
	router.Handle("g<", func(_ riffkey.Match) { ed.DemoteHeading() })

	// move blocks
	router.Handle("<A-j>", func(_ riffkey.Match) { ed.MoveBlockDown() })
	router.Handle("<A-k>", func(_ riffkey.Match) { ed.MoveBlockUp() })

	// visual mode
	router.Handle("v", func(_ riffkey.Match) { ed.EnterVisual(); composeSetupVisualRouter(app, ed) })
	router.Handle("V", func(_ riffkey.Match) { ed.EnterVisualLine(); composeSetupVisualRouter(app, ed) })
	router.Handle("<C-v>", func(_ riffkey.Match) { ed.EnterVisualBlock(); composeSetupVisualRouter(app, ed) })

	// insert mode
	router.Handle("i", func(_ riffkey.Match) { ed.EnterInsert(); composeEnterInsertMode(app, ed) })
	router.Handle("a", func(_ riffkey.Match) { ed.EnterInsertAfter(); composeEnterInsertMode(app, ed) })
	router.Handle("I", func(_ riffkey.Match) { ed.EnterInsertLineStart(); composeEnterInsertMode(app, ed) })
	router.Handle("A", func(_ riffkey.Match) { ed.EnterInsertLineEnd(); composeEnterInsertMode(app, ed) })
	router.Handle("o", func(_ riffkey.Match) { ed.OpenBelow(); composeEnterInsertMode(app, ed) })
	router.Handle("O", func(_ riffkey.Match) { ed.OpenAbove(); composeEnterInsertMode(app, ed) })

	// editing
	router.Handle("x", func(m riffkey.Match) {
		for range m.Count {
			ed.DeleteChar()
		}
	})
	replaceRouter := riffkey.NewRouter().Name("replace").NoCounts()
	replaceRouter.Handle("<Esc>", func(_ riffkey.Match) { app.Pop() })
	replaceRouter.HandleUnmatched(func(k riffkey.Key) bool {
		if k.Rune != 0 && k.Mod == 0 {
			ed.ReplaceChar(k.Rune)
			app.Pop()
			return true
		}
		return false
	})
	replaceRouter.AddOnAfter(func() { ed.Refresh() })
	router.Handle("r", func(_ riffkey.Match) { app.Push(replaceRouter) })

	router.Handle("dd", func(m riffkey.Match) {
		for range m.Count {
			ed.DeleteLine()
		}
	})
	router.Handle("dj", func(m riffkey.Match) {
		for range m.Count + 1 {
			ed.DeleteLine()
		}
	})
	router.Handle("dk", func(m riffkey.Match) {
		for range m.Count + 1 {
			if ed.Cursor().Block > 0 {
				ed.BlockUp(1)
			}
			ed.DeleteLine()
		}
	})
	router.Handle("D", func(_ riffkey.Match) {
		ed.Delete(compose.Range{Start: ed.Cursor(), End: compose.Pos{Block: ed.Cursor().Block, Col: ed.CurrentBlock().Length()}})
	})
	router.Handle("J", func(m riffkey.Match) {
		for range m.Count {
			ed.JoinLines()
		}
	})
	router.Handle("~", func(_ riffkey.Match) { ed.ToggleCase() })
	router.Handle("u", func(_ riffkey.Match) { ed.Undo() })
	router.Handle("<C-r>", func(_ riffkey.Match) { ed.Redo() })
	router.Handle("yy", func(_ riffkey.Match) { ed.Yank(ed.InnerBlock()) })
	router.Handle("p", func(_ riffkey.Match) { ed.Put() })
	router.Handle("P", func(_ riffkey.Match) { ed.PutBefore() })
	router.Handle("cc", func(_ riffkey.Match) { ed.Change(ed.InnerBlock()); composeEnterInsertMode(app, ed) })
	router.Handle("cj", func(m riffkey.Match) {
		for range m.Count {
			ed.DeleteLine()
		}
		ed.Change(ed.InnerBlock())
		composeEnterInsertMode(app, ed)
	})
	router.Handle("ck", func(m riffkey.Match) {
		for range m.Count {
			if ed.Cursor().Block > 0 {
				ed.BlockUp(1)
			}
			ed.DeleteLine()
		}
		ed.Change(ed.InnerBlock())
		composeEnterInsertMode(app, ed)
	})
	router.Handle("C", func(_ riffkey.Match) {
		ed.Change(compose.Range{Start: ed.Cursor(), End: compose.Pos{Block: ed.Cursor().Block, Col: ed.CurrentBlock().Length()}})
		composeEnterInsertMode(app, ed)
	})

	// f/F/t/T
	composeFindChar := func(action func(rune)) {
		cr := riffkey.NewRouter().Name("char").NoCounts()
		cr.Handle("<Esc>", func(_ riffkey.Match) { app.Pop() })
		cr.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				action(k.Rune)
				app.Pop()
				return true
			}
			return false
		})
		app.Push(cr)
	}
	router.Handle("f", func(_ riffkey.Match) { composeFindChar(ed.FindChar) })
	router.Handle("F", func(_ riffkey.Match) { composeFindChar(ed.FindCharBack) })
	router.Handle("t", func(_ riffkey.Match) { composeFindChar(ed.TillChar) })
	router.Handle("T", func(_ riffkey.Match) { composeFindChar(ed.TillCharBack) })
	router.Handle(";", func(_ riffkey.Match) { ed.RepeatFind() })
	router.Handle(",", func(_ riffkey.Match) { ed.RepeatFindReverse() })

	// marks
	composeMarkPrompt := func(action func(rune)) {
		mr := riffkey.NewRouter().Name("mark").NoCounts()
		mr.Handle("<Esc>", func(_ riffkey.Match) { app.Pop() })
		mr.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune >= 'a' && k.Rune <= 'z' && k.Mod == 0 {
				action(k.Rune)
				app.Pop()
				return true
			}
			return false
		})
		app.Push(mr)
	}
	router.Handle("m", func(_ riffkey.Match) { composeMarkPrompt(ed.SetMark) })
	router.Handle("'", func(_ riffkey.Match) { composeMarkPrompt(func(r rune) { ed.GotoMarkLine(r) }) })
	router.Handle("`", func(_ riffkey.Match) { composeMarkPrompt(func(r rune) { ed.GotoMark(r) }) })

	// search
	composeStartSearch := func(forward bool) {
		if forward {
			composeSearchPrompt = "/"
		} else {
			composeSearchPrompt = "?"
		}
		composeSearchQuery = ""
		composeSearchFwd = forward
		app.HideCursor()
		app.PushView("compose-search")
	}
	router.Handle("/", func(_ riffkey.Match) { composeStartSearch(true) })
	router.Handle("?", func(_ riffkey.Match) { composeStartSearch(false) })
	router.Handle("n", func(_ riffkey.Match) { ed.SearchNext() })
	router.Handle("N", func(_ riffkey.Match) { ed.SearchPrev() })
	router.Handle("*", func(_ riffkey.Match) {
		word := ed.InnerWord()
		if word.Start.Block == word.End.Block {
			if b := ed.CurrentBlock(); b != nil {
				runes := []rune(b.Text())
				if word.Start.Col < len(runes) && word.End.Col <= len(runes) {
					if p := string(runes[word.Start.Col:word.End.Col]); p != "" {
						ed.Search(p, true)
					}
				}
			}
		}
	})
	router.Handle("#", func(_ riffkey.Match) {
		word := ed.InnerWord()
		if word.Start.Block == word.End.Block {
			if b := ed.CurrentBlock(); b != nil {
				runes := []rune(b.Text())
				if word.Start.Col < len(runes) && word.End.Col <= len(runes) {
					if p := string(runes[word.Start.Col:word.End.Col]); p != "" {
						ed.Search(p, false)
					}
				}
			}
		}
	})

	// screen jumps
	router.Handle("H", func(_ riffkey.Match) { ed.GotoScreenTop() })
	router.Handle("M", func(_ riffkey.Match) { ed.GotoScreenMiddle() })
	router.Handle("L", func(_ riffkey.Match) { ed.GotoScreenBottom() })
	router.Handle("<C-o>", func(_ riffkey.Match) { ed.JumpBack() })
	router.Handle("<C-i>", func(_ riffkey.Match) { ed.JumpForward() })
	router.Handle("-", func(_ riffkey.Match) { ed.PrevLineFirstNonBlank() })
	router.Handle("+", func(_ riffkey.Match) { ed.NextLineFirstNonBlank() })
	router.Handle("<C-a>", func(_ riffkey.Match) { ed.IncrementNumber(1) })
	router.Handle("<C-x>", func(_ riffkey.Match) { ed.IncrementNumber(-1) })
	router.Handle(".", func(_ riffkey.Match) { ed.RepeatLastAction() })

	// sentences
	router.Handle(")", func(m riffkey.Match) {
		for range m.Count {
			ed.NextSentence()
		}
	})
	router.Handle("(", func(m riffkey.Match) {
		for range m.Count {
			ed.PrevSentence()
		}
	})
	router.Handle("gs)", func(_ riffkey.Match) { ed.SwapSentenceNext() })
	router.Handle("gs(", func(_ riffkey.Match) { ed.SwapSentencePrev() })

	// operator × text object combos
	compose.RegisterOperatorTextObjects(app, ed)

	// block type
	router.Handle("g0", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockParagraph) })
	router.Handle("g1", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH1) })
	router.Handle("g2", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH2) })
	router.Handle("g3", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH3) })
	router.Handle("g4", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH4) })
	router.Handle("g5", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH5) })
	router.Handle("g6", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH6) })
	router.Handle("g-", func(_ riffkey.Match) { ed.InsertDivider() })
	router.Handle("gyiw", func(_ riffkey.Match) { ed.YankStyle() })
	router.Handle("gpiw", func(_ riffkey.Match) { ed.PasteStyle(ed.InnerWord()) })

	// templates
	router.Handle("gth", func(_ riffkey.Match) { ed.CycleTemplate("headings") })
	router.Handle("gtq", func(_ riffkey.Match) { ed.CycleTemplate("quotes") })
	router.Handle("gtd", func(_ riffkey.Match) { ed.CycleTemplate("dividers") })
	router.Handle("gtl", func(_ riffkey.Match) { ed.CycleTemplate("lists") })
	router.Handle("gtc", func(_ riffkey.Match) { ed.CycleTemplate("code") })
	router.Handle("gtt", func(_ riffkey.Match) { ed.CycleTemplate("tables") })
	router.Handle("gt@", func(_ riffkey.Match) { ed.CycleTemplate("dialogue") })
	router.Handle("gBt", func(_ riffkey.Match) { ed.ApplyBundle("typewriter") })
	router.Handle("gBm", func(_ riffkey.Match) { ed.ApplyBundle("minimal") })
	router.Handle("gBa", func(_ riffkey.Match) { ed.ApplyBundle("academic") })
	router.Handle("gBc", func(_ riffkey.Match) { ed.ApplyBundle("creative") })

	// matchers
	compose.RegisterMatchers(app, ed)
}

func composeEnterInsertMode(app *App, ed *compose.Editor) {
	r := riffkey.NewRouter().Name("insert").NoCounts()

	r.Handle("<Esc>", func(_ riffkey.Match) {
		ed.EnterNormal()
		app.Pop()
	})
	r.Handle("<CR>", func(_ riffkey.Match) { ed.NewLine() })
	r.Handle("<S-CR>", func(_ riffkey.Match) { ed.YieldToNextSpeaker() })
	r.Handle("<C-n>", func(_ riffkey.Match) { ed.YieldToNextSpeaker() })
	r.Handle("<BS>", func(_ riffkey.Match) { ed.Backspace() })
	r.Handle("<Del>", func(_ riffkey.Match) { ed.DeleteChar() })
	r.Handle("<Left>", func(_ riffkey.Match) { ed.Left(1) })
	r.Handle("<Right>", func(_ riffkey.Match) { ed.Right(1) })
	r.Handle("<Up>", func(_ riffkey.Match) { ed.Up(1) })
	r.Handle("<Down>", func(_ riffkey.Match) { ed.Down(1) })
	r.Handle("<Space>", func(_ riffkey.Match) { ed.InsertChar(' ') })
	r.Handle("<C-w>", func(_ riffkey.Match) { ed.DeleteWordBack() })
	r.Handle("<C-u>", func(_ riffkey.Match) { ed.DeleteToLineStart() })

	r.Handle("<Tab>", func(_ riffkey.Match) {
		b := ed.CurrentBlock()
		if b != nil && b.Type == compose.BlockDialogue {
			ed.ToggleDialogueMode()
		} else {
			ed.InsertText("    ")
		}
	})

	syncSubject := func() {
		b := ed.CurrentBlock()
		if b == nil {
			return
		}
		switch b.Type {
		case compose.BlockH1, compose.BlockH2, compose.BlockH3:
			composeSubject = b.Text()
			fieldSubject.Value = composeSubject
			fieldSubject.Cursor = len(composeSubject)
		}
	}

	r.HandleUnmatched(func(k riffkey.Key) bool {
		if k.IsPaste() {
			ed.InsertText(k.Paste)
			ed.Refresh()
			syncSubject()
			return true
		}
		if k.Rune != 0 && k.Mod == 0 {
			ed.InsertChar(k.Rune)
			ed.Refresh()
			syncSubject()
			return true
		}
		return false
	})

	r.AddOnAfter(func() {
		ed.Refresh()
		syncSubject()
	})
	app.Push(r)
}

func composeSetupVisualRouter(app *App, ed *compose.Editor) {
	r := riffkey.NewRouter().Name("visual").NoCounts()

	r.Handle("<Esc>", func(_ riffkey.Match) { ed.ExitVisual(); app.Pop() })
	r.Handle("v", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == compose.VisualChar {
			ed.ExitVisual()
			app.Pop()
		} else {
			ed.SetVisualMode(compose.VisualChar)
		}
	})
	r.Handle("V", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == compose.VisualLine {
			ed.ExitVisual()
			app.Pop()
		} else {
			ed.SetVisualMode(compose.VisualLine)
		}
	})
	r.Handle("<C-v>", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == compose.VisualBlock {
			ed.ExitVisual()
			app.Pop()
		} else {
			ed.SetVisualMode(compose.VisualBlock)
		}
	})

	r.Handle("h", func(_ riffkey.Match) { ed.Left(1) })
	r.Handle("l", func(_ riffkey.Match) { ed.Right(1) })
	r.Handle("j", func(_ riffkey.Match) { ed.Down(1) })
	r.Handle("k", func(_ riffkey.Match) { ed.Up(1) })
	r.Handle("<Left>", func(_ riffkey.Match) { ed.Left(1) })
	r.Handle("<Right>", func(_ riffkey.Match) { ed.Right(1) })
	r.Handle("<Up>", func(_ riffkey.Match) { ed.Up(1) })
	r.Handle("<Down>", func(_ riffkey.Match) { ed.Down(1) })
	r.Handle("w", func(_ riffkey.Match) { ed.NextWordStart(1) })
	r.Handle("b", func(_ riffkey.Match) { ed.PrevWordStart(1) })
	r.Handle("e", func(_ riffkey.Match) { ed.NextWordEnd(1) })
	r.Handle("0", func(_ riffkey.Match) { ed.LineStart() })
	r.Handle("$", func(_ riffkey.Match) { ed.LineEnd() })
	r.Handle("^", func(_ riffkey.Match) { ed.FirstNonBlank() })
	r.Handle("gg", func(_ riffkey.Match) { ed.DocStart() })
	r.Handle("G", func(_ riffkey.Match) { ed.DocEnd() })
	r.Handle("o", func(_ riffkey.Match) { ed.SwapVisualEnds() })
	r.Handle("O", func(_ riffkey.Match) { ed.SwapVisualEnds() })

	compose.RegisterVisualTextObjects(r, ed)
	compose.RegisterVisualOperators(r, app, ed)

	r.Handle("~", func(_ riffkey.Match) { ed.ToggleCaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })
	r.Handle("U", func(_ riffkey.Match) { ed.UppercaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })
	r.Handle("u", func(_ riffkey.Match) { ed.LowercaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })

	r.AddOnAfter(func() { ed.Refresh() })
	app.Push(r)
}
