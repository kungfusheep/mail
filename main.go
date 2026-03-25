package main

import (
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/provider"
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
}

// ui state
var (
	// display slices derived from app state
	folderNames  []string
	threadRows   []ThreadRow
	folderSel    int
	threadSel    int

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
	composeModeStr string
	editor         *compose.Editor
	replyThreadID  string
	replyMsg       *provider.Message
)

type ThreadRow struct {
	ThreadIdx int
	MsgIdx    int    // -1 for thread header, >= 0 for message
	Label     string
	Detail    string
	Date      string
	Unread    bool
	Expanded  bool
}

func main() {
	app := NewApp()

	seedFakeData()
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
			HBox(
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
			resetCompose()
			app.Go("compose")
		}).
		Handle("r", func() {
			if threadSel >= 0 && threadSel < len(threadRows) {
				row := threadRows[threadSel]
				if row.MsgIdx < 0 && row.ThreadIdx < len(state.threads) {
					setupReply(state.threads[row.ThreadIdx])
					app.Go("compose")
				}
			}
		})

	// compose view — editor first, omnibox for actions
	editor = compose.NewEditor(compose.NewDocument(), "")
	editor.SetSize(80, 24)
	composeModeStr = editor.Mode().String()
	composeSubject = ""

	// derive subject from editor content on every change
	editor.OnChange = func() {
		if s := editor.Subject(); s != "" {
			composeSubject = s
		}
		composeModeStr = editor.Mode().String()
	}

	composeStatusLine := ""
	updateComposeStatus := func() {
		composeModeStr = editor.Mode().String()
		subj := composeSubject
		if subj == "" {
			subj = "(no subject — use # heading)"
		}
		to := composeTo
		if to == "" {
			to = "(no recipient — :to to set)"
		}
		composeStatusLine = fmt.Sprintf("to: %s    subject: %s", to, subj)
	}

	showOmnibox := false
	omniboxInput := ""
	omniboxError := ""

	execOmniCommand := func(cmd string) {
		parts := strings.SplitN(strings.TrimSpace(cmd), " ", 2)
		action := parts[0]
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}

		switch action {
		case "send", "s":
			if composeTo == "" {
				omniboxError = "set recipient first — :to <address>"
				return
			}
			sendMessage(app)
			showOmnibox = false
			return
		case "to":
			if arg == "" {
				omniboxError = "usage: :to <address>"
				return
			}
			composeTo = arg
		case "cc":
			if arg == "" {
				omniboxError = "usage: :cc <address>"
				return
			}
			composeCC = arg
		case "subject", "sub":
			if arg == "" {
				omniboxError = "usage: :subject <text>"
				return
			}
			composeSubject = arg
		case "discard", "q":
			resetCompose()
			showOmnibox = false
			app.Go("main")
			return
		default:
			omniboxError = fmt.Sprintf("unknown: %s (try: send, to, cc, subject, discard)", action)
			return
		}
		showOmnibox = false
		omniboxError = ""
		updateComposeStatus()
	}

	app.View("compose",
		VBox(
			// editor takes all available space
			LayerView(editor.Layer()).Grow(1),

			// status line at bottom
			HBox.Gap(2)(
				Text(&composeModeStr).Bold(),
				Text(&composeStatusLine).Dim(),
			),

			// omnibox overlay at bottom
			If(&showOmnibox).Then(
				HBox(
					Text(":"),
					Text(&omniboxInput),
				),
			),
			If(&showOmnibox).Then(
				If(&omniboxError).Eq("").
					Then(Text("send  to <addr>  cc <addr>  subject <text>  discard").Dim()).
					Else(Text(&omniboxError).Bold()),
			),
		),
	).NoCounts().
		// omnibox trigger
		Handle(":", func() {
			if editor.Mode() == compose.ModeNormal && !showOmnibox {
				showOmnibox = true
				omniboxInput = ""
				omniboxError = ""
			}
		}).
		// vim normal mode
		Handle("i", func() {
			if showOmnibox {
				return
			}
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsert()
				updateComposeStatus()
			}
		}).
		Handle("a", func() {
			if showOmnibox {
				return
			}
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsertAfter()
				updateComposeStatus()
			}
		}).
		Handle("A", func() {
			if showOmnibox {
				return
			}
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsertLineEnd()
				updateComposeStatus()
			}
		}).
		Handle("I", func() {
			if showOmnibox {
				return
			}
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsertLineStart()
				updateComposeStatus()
			}
		}).
		Handle("o", func() {
			if showOmnibox {
				return
			}
			if editor.Mode() == compose.ModeNormal {
				editor.OpenBelow()
				updateComposeStatus()
			}
		}).
		Handle("O", func() {
			if showOmnibox {
				return
			}
			if editor.Mode() == compose.ModeNormal {
				editor.OpenAbove()
				updateComposeStatus()
			}
		}).
		Handle("<Escape>", func() {
			if showOmnibox {
				showOmnibox = false
				omniboxError = ""
				return
			}
			editor.EnterNormal()
			updateComposeStatus()
		}).
		Handle("h", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.SetCursor(editor.Left(1))
		}).
		Handle("l", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.SetCursor(editor.Right(1))
		}).
		Handle("k", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.SetCursor(editor.Up(1))
		}).
		Handle("j", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.SetCursor(editor.Down(1))
		}).
		Handle("w", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.SetCursor(editor.NextWordStart(1))
		}).
		Handle("b", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.SetCursor(editor.PrevWordStart(1))
		}).
		Handle("e", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.SetCursor(editor.NextWordEnd(1))
		}).
		Handle("u", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.Undo()
		}).
		Handle("<C-r>", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.Redo()
		}).
		Handle("dd", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.DeleteLine()
		}).
		Handle("x", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.DeleteChar()
		}).
		Handle("p", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.Put()
		}).
		Handle("P", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.PutBefore()
		}).
		Handle("J", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.JoinLines()
		}).
		Handle("<C-d>", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.ScrollHalfPageDown()
		}).
		Handle("<C-u>", func() {
			if showOmnibox || editor.Mode() != compose.ModeNormal {
				return
			}
			editor.ScrollHalfPageUp()
		})

	// route unmatched keys to editor (insert mode) or omnibox
	if router, ok := app.ViewRouter("compose"); ok {
		router.HandleUnmatched(func(k riffkey.Key) bool {
			// omnibox input
			if showOmnibox {
				if k.Special == riffkey.SpecialBackspace {
					if len(omniboxInput) > 0 {
						omniboxInput = omniboxInput[:len(omniboxInput)-1]
					}
					omniboxError = ""
					app.RequestRender()
					return true
				}
				if k.Special == riffkey.SpecialEnter {
					execOmniCommand(omniboxInput)
					app.RequestRender()
					return true
				}
				if k.Rune != 0 && k.Mod == 0 && unicode.IsPrint(k.Rune) {
					omniboxInput += string(k.Rune)
					omniboxError = ""
					app.RequestRender()
					return true
				}
				return false
			}

			// editor insert mode
			if editor.Mode() != compose.ModeInsert {
				return false
			}
			if k.Paste != "" {
				editor.InsertText(k.Paste)
				editor.UpdateDisplay()
				updateComposeStatus()
				app.RequestRender()
				return true
			}
			if k.Special == riffkey.SpecialBackspace {
				editor.Backspace()
				editor.UpdateDisplay()
				updateComposeStatus()
				app.RequestRender()
				return true
			}
			if k.Special == riffkey.SpecialEnter {
				editor.NewLine()
				editor.UpdateDisplay()
				updateComposeStatus()
				app.RequestRender()
				return true
			}
			if k.Rune != 0 && k.Mod == 0 && unicode.IsPrint(k.Rune) {
				editor.InsertChar(k.Rune)
				editor.UpdateDisplay()
				updateComposeStatus()
				app.RequestRender()
				return true
			}
			return false
		})
	}

	// resize handler for editor
	app.OnResize(func(w, h int) {
		editor.SetSize(w, h-3) // subtract status line + omnibox
		editor.UpdateDisplay()
	})

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
	folder := state.folders[state.activeFolder]
	for i, t := range state.threads {
		// filter by folder (in real app, provider does this)
		if !threadInFolder(t, folder.ID) {
			continue
		}
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

func threadInFolder(t provider.Thread, folderID string) bool {
	if len(t.Messages) == 0 {
		return false
	}
	for _, label := range t.Messages[0].Labels {
		if label == folderID {
			return true
		}
	}
	return false
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
			loadPreview(t.Messages[row.MsgIdx])
			pane = 2
			updateBorders()
		}
		return
	}
	// thread header — expand or show first message preview
	handleThreadToggle()
	t := state.threads[row.ThreadIdx]
	if len(t.Messages) > 0 {
		loadPreview(t.Messages[len(t.Messages)-1])
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

func loadPreview(msg provider.Message) {
	state.previewLines = nil
	state.previewLines = append(state.previewLines,
		fmt.Sprintf("From: %s", msg.From.String()),
		fmt.Sprintf("To: %s", formatAddresses(msg.To)),
		fmt.Sprintf("Date: %s", msg.Date.Format("2 Jan 2006 15:04")),
		fmt.Sprintf("Subject: %s", msg.Subject),
		"",
	)
	// use text body for now — w3m preview comes later
	for _, line := range strings.Split(msg.TextBody, "\n") {
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
	editor = compose.NewEditor(compose.NewDocument(), "")
	editor.SetSize(80, 24)
	editor.EnterInsert()
	composeModeStr = editor.Mode().String()
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
	_ = msg
	statusText = fmt.Sprintf("sent to %s (no provider)", composeTo)
	resetCompose()
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

// fake data — realistic mailbox content

func seedFakeData() {
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
					From: provider.Address{Name: "Alice Chen", Email: "alice@example.com"},
					To:   []provider.Address{{Name: "Team", Email: "team@example.com"}},
					Subject: "production deploy blocked on failing e2e tests",
					Date:    now.Add(-2 * time.Hour),
					TextBody: "hey team,\n\nthe e2e suite is failing on the checkout flow tests.\nlooks like the session token changes from last week broke the auth fixture.\n\ni've pinned the deploy pipeline until we fix this. can someone\ntake a look at the fixture setup in test/e2e/auth_helper.go?\n\nthanks,\nalice",
					Read: true, MessageID: "<m1a@example.com>",
				},
				{
					ID: "m1b", ThreadID: "t1", Labels: []string{"INBOX"},
					From: provider.Address{Name: "Bob Kumar", Email: "bob@example.com"},
					To:   []provider.Address{{Name: "Team", Email: "team@example.com"}},
					Subject: "re: production deploy blocked on failing e2e tests",
					Date:    now.Add(-90 * time.Minute),
					TextBody: "i can take a look. the session token format changed from\njwt to opaque tokens — the fixture is still generating jwts.\n\nshould be a quick fix, i'll push a branch in 30 min.\n\nbob",
					Read: true, InReplyTo: "<m1a@example.com>",
					MessageID: "<m1b@example.com>",
				},
				{
					ID: "m1c", ThreadID: "t1", Labels: []string{"INBOX"},
					From: provider.Address{Name: "Carol Zhang", Email: "carol@example.com"},
					To:   []provider.Address{{Name: "Team", Email: "team@example.com"}},
					Subject: "re: production deploy blocked on failing e2e tests",
					Date:    now.Add(-12 * time.Minute),
					TextBody: "bob's fix looks good. i ran the full suite locally and\neverything passes now. unblocking the deploy.\n\ncarol",
					Read: false, InReplyTo: "<m1b@example.com>",
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
					From: provider.Address{Name: "Dave Park", Email: "dave@example.com"},
					To:   []provider.Address{{Name: "Engineering", Email: "eng@example.com"}},
					Subject: "weekly engineering sync — march 24",
					Date:    now.Add(-3 * time.Hour),
					TextBody: "notes from today's sync:\n\n- api gateway migration is 80% complete\n- new hire onboarding starts next monday\n- reminder: code freeze for v2.4 is thursday\n\naction items:\n1. finish gateway canary rollout (eve)\n2. update runbook for new auth flow (bob)\n3. schedule load test for wednesday (alice)\n\ndave",
					Read: true, MessageID: "<m2a@example.com>",
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
					From: provider.Address{Name: "Eve Santos", Email: "eve@example.com"},
					To:   []provider.Address{{Name: "Backend", Email: "backend@example.com"}},
					Subject: "api rate limiting — proposal for v2",
					Date:    now.Add(-26 * time.Hour),
					TextBody: "i've been working on a new rate limiting approach for the api.\n\ncurrent system: fixed window, 1000 req/min per api key\nproposed: sliding window with token bucket, configurable per endpoint\n\nthe main wins:\n- burst tolerance without long-term abuse\n- per-endpoint granularity (search can be tighter than reads)\n- better observability — we can track token consumption patterns\n\ndraft doc is here. thoughts?\n\neve",
					Read: true, MessageID: "<m3a@example.com>",
				},
				{
					ID: "m3b", ThreadID: "t3", Labels: []string{"INBOX"},
					From: provider.Address{Name: "Alice Chen", Email: "alice@example.com"},
					To:   []provider.Address{{Name: "Backend", Email: "backend@example.com"}},
					Subject: "re: api rate limiting — proposal for v2",
					Date:    now.Add(-20 * time.Hour),
					TextBody: "this looks solid. one concern: the token bucket refill rate\nneeds to account for our bursty enterprise clients. some of\nthem legitimately spike to 5x normal during their batch jobs.\n\ncan we add a \"burst multiplier\" config per api key tier?\n\nalice",
					Read: true, InReplyTo: "<m3a@example.com>",
					MessageID: "<m3b@example.com>",
				},
				{
					ID: "m3c", ThreadID: "t3", Labels: []string{"INBOX"},
					From: provider.Address{Name: "Frank Liu", Email: "frank@example.com"},
					To:   []provider.Address{{Name: "Backend", Email: "backend@example.com"}},
					Subject: "re: api rate limiting — proposal for v2",
					Date:    now.Add(-5 * time.Hour),
					TextBody: "+1 on the burst multiplier idea. also we should make sure\nthe 429 response includes a retry-after header with the\nactual token refill time, not just a generic \"try later\".\n\nfrank",
					Read: false, InReplyTo: "<m3b@example.com>",
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
					From: provider.Address{Name: "Office Supplies Co", Email: "orders@officesupplies.example.com"},
					To:   []provider.Address{{Email: "pete@example.com"}},
					Subject: "office furniture order confirmation",
					Date:    now.Add(-1 * 24 * time.Hour),
					TextBody: "your order #OSC-7234 has been confirmed.\n\nitems:\n- standing desk frame (black) x1\n- monitor arm (dual) x1\n- desk mat (dark grey, 90x40) x1\n\nestimated delivery: march 28\n\nthank you for your order.",
					Read: true, MessageID: "<m4a@officesupplies.example.com>",
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
					From: provider.Address{Name: "Grace Torres", Email: "grace@example.com"},
					To:   []provider.Address{{Name: "Engineering", Email: "eng@example.com"}},
					Subject: "security audit results — q1 2026",
					Date:    now.Add(-2 * 24 * time.Hour),
					TextBody: "hi all,\n\nthe q1 security audit is complete. summary:\n\n- 0 critical findings\n- 2 high (both in legacy auth, already patched)\n- 5 medium (dependency updates, tracking in jira)\n- 12 low/informational\n\nfull report attached. the two high findings were:\n1. session tokens not invalidated on password change\n2. csrf token reuse across form submissions\n\nboth patches are deployed as of yesterday.\n\ngrace",
					Read: true, MessageID: "<m5a@example.com>",
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
					From: provider.Address{Email: "pete@example.com"},
					To:   []provider.Address{{Name: "Dave Park", Email: "dave@example.com"}},
					Subject: "re: lunch wednesday?",
					Date:    now.Add(-4 * time.Hour),
					TextBody: "yeah sounds good, the ramen place on 5th? 12:30?\n\npete",
					Read: true, MessageID: "<m6a@example.com>",
				},
			},
		},
	}
}
