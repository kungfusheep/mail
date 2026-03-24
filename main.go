package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/provider"
)

var (
	folders      []string
	folderSel    int
	folderUnread []string

	threads   []ThreadRow
	threadSel int

	previewLines []string
	statusText   string

	pane  int // 0=folders, 1=threads, 2=preview
	frame int

	// pane border colours — white when focused, dim otherwise
	folderBorder  = White
	threadBorder  = BrightBlack
	previewBorder = BrightBlack

	// compose state
	composeTo      string
	composeCC      string
	composeSubject string
	composeModeStr string
	editor         *compose.Editor
	replyThreadID  string
	replyMsg       *provider.Message
)

type ThreadRow struct {
	Thread    provider.Thread
	Expanded  bool
	IsMessage bool
	MessageID string
	Depth     int
	Label     string
	Date      string
	Unread    bool
}

func main() {
	app := NewApp()

	statusText = "loading..."

	folders = []string{"Inbox", "Starred", "Sent", "Drafts", "Trash"}
	folderUnread = []string{"3", "", "", "1", ""}
	refreshThreadList()

	selectedStyle := Style{Attr: AttrInverse}

	app.View("main",
		VBox(
			// top bar
			HBox.Gap(1)(
				Text("mail").Bold(),
				Space(),
				Spinner(&frame).Frames(SpinnerDots),
			),

			// three-pane layout
			HBox(
				// folders
				VBox.Grow(1).Border(BorderRounded).BorderFG(&folderBorder).Title("Folders")(
					List(&folders).
						Selection(&folderSel).
						SelectedStyle(selectedStyle).
						Marker("  ").
						Render(func(f *string) any {
							idx := -1
							for i := range folders {
								if &folders[i] == f {
									idx = i
									break
								}
							}
							unread := ""
							if idx >= 0 && idx < len(folderUnread) {
								unread = folderUnread[idx]
							}
							if unread != "" {
								return HBox(
									Text(f),
									Space(),
									Text(&folderUnread[idx]).Bold(),
								)
							}
							return Text(f)
						}),
				),

				// threads
				VBox.Grow(2).Border(BorderRounded).BorderFG(&threadBorder).Title("Threads")(
					List(&threads).
						Selection(&threadSel).
						SelectedStyle(selectedStyle).
						Marker("  ").
						Render(func(row *ThreadRow) any {
							if row.IsMessage {
								return HBox.Gap(1)(
									SpaceW(2),
									Text(&row.Label).Dim(),
									Space(),
									Text(&row.Date).Dim(),
								)
							}
							count := fmt.Sprintf("[%d]", len(row.Thread.Messages))
							if row.Unread {
								return HBox.Gap(1)(
									Text(&row.Label).Bold(),
									Space(),
									Text(count),
									Text(&row.Date).Dim(),
								)
							}
							return HBox.Gap(1)(
								Text(&row.Label),
								Space(),
								Text(count).Dim(),
								Text(&row.Date).Dim(),
							)
						}),
				),

				// preview
				VBox.Grow(3).Border(BorderRounded).BorderFG(&previewBorder).Title("Preview")(
					ForEach(&previewLines, func(line *string) any {
						return Text(line)
					}),
				),
			),

			// status bar
			HBox.Gap(1)(
				Text(&statusText).Dim(),
				Space(),
				Text("q quit  c compose  r reply  a archive  d delete  / search").Dim(),
			),
		),
	).NoCounts().
		Handle("q", app.Stop).
		Handle("j", func() {
			switch pane {
			case 0:
				if folderSel < len(folders)-1 {
					folderSel++
				}
			case 1:
				if threadSel < len(threads)-1 {
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
				statusText = fmt.Sprintf("folder: %s", folders[folderSel])
				pane = 1
				updateBorders()
			case 1:
				if threadSel >= 0 && threadSel < len(threads) {
					row := threads[threadSel]
					if !row.IsMessage {
						toggleThread(threadSel)
					} else {
						loadPreview(row.MessageID)
						pane = 2
						updateBorders()
					}
				}
			}
		}).
		Handle("<Escape>", func() {
			if pane > 0 {
				pane--
				updateBorders()
			}
		}).
		Handle("o", func() {
			if pane == 1 && threadSel >= 0 && threadSel < len(threads) {
				row := threads[threadSel]
				if !row.IsMessage {
					toggleThread(threadSel)
				}
			}
		}).
		Handle("c", func() {
			resetCompose()
			app.Go("compose")
		}).
		Handle("r", func() {
			if threadSel >= 0 && threadSel < len(threads) {
				row := threads[threadSel]
				if !row.IsMessage && len(row.Thread.Messages) > 0 {
					setupReply(row.Thread)
					app.Go("compose")
				}
			}
		})

	// compose view
	editor = compose.NewEditor()
	editor.SetSize(80, 24)
	composeModeStr = editor.Mode().String()

	editor.OnChange = func() {
		composeModeStr = editor.Mode().String()
	}

	var composeForm *FormC
	composeForm = Form.LabelBold().OnSubmit(func() {
		if composeForm.ValidateAll() {
			sendMessage(app)
		}
	})(
		Field("To", Input(&composeTo).Placeholder("recipient@example.com").Validate(VRequired)),
		Field("Cc", Input(&composeCC).Placeholder("cc (optional)")),
		Field("Subject", Input(&composeSubject).Placeholder("subject").Validate(VRequired)),
	)

	app.View("compose",
		VBox(
			HBox.Gap(1)(
				Text("compose").Bold(),
				Space(),
				Text(&composeModeStr).Dim(),
			),
			composeForm,
			VBox.Grow(1).Border(BorderRounded).BorderFG(White).Title("Body")(
				LayerView(editor.Layer()),
			),
			Text("esc normal  i insert  ctrl-s send  ctrl-q discard").Dim(),
		),
	).NoCounts().
		Handle("<C-s>", func() {
			if composeTo != "" && composeSubject != "" {
				sendMessage(app)
			} else {
				statusText = "to and subject are required"
			}
		}).
		Handle("<C-q>", func() {
			resetCompose()
			app.Go("main")
		}).
		Handle("i", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsert()
				composeModeStr = editor.Mode().String()
			}
		}).
		Handle("a", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsertAfter()
				composeModeStr = editor.Mode().String()
			}
		}).
		Handle("A", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsertLineEnd()
				composeModeStr = editor.Mode().String()
			}
		}).
		Handle("I", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.EnterInsertLineStart()
				composeModeStr = editor.Mode().String()
			}
		}).
		Handle("o", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.OpenBelow()
				composeModeStr = editor.Mode().String()
			}
		}).
		Handle("O", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.OpenAbove()
				composeModeStr = editor.Mode().String()
			}
		}).
		Handle("<Escape>", func() {
			editor.EnterNormal()
			composeModeStr = editor.Mode().String()
		}).
		Handle("h", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.SetCursor(editor.Left(1))
			}
		}).
		Handle("l", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.SetCursor(editor.Right(1))
			}
		}).
		Handle("k", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.SetCursor(editor.Up(1))
			}
		}).
		Handle("j", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.SetCursor(editor.Down(1))
			}
		}).
		Handle("w", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.SetCursor(editor.WordForward())
			}
		}).
		Handle("b", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.SetCursor(editor.WordBackward())
			}
		}).
		Handle("e", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.SetCursor(editor.WordEnd())
			}
		}).
		Handle("u", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.Undo()
			}
		}).
		Handle("<C-r>", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.Redo()
			}
		}).
		Handle("dd", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.DeleteLine()
			}
		}).
		Handle("x", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.DeleteChar()
			}
		}).
		Handle("p", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.Put()
			}
		}).
		Handle("P", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.PutBefore()
			}
		}).
		Handle("J", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.JoinLines()
			}
		}).
		Handle("<C-d>", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.ScrollHalfPageDown()
			}
		}).
		Handle("<C-u>", func() {
			if editor.Mode() == compose.ModeNormal {
				editor.ScrollHalfPageUp()
			}
		})

	// spinner
	go func() {
		for range time.Tick(80 * time.Millisecond) {
			frame++
			app.RequestRender()
		}
	}()

	statusText = "ready — no provider connected"
	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
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

func refreshThreadList() {
	threads = nil
	now := time.Now()
	samples := []struct {
		subject string
		from    string
		count   int
		unread  bool
	}{
		{"deployment pipeline failing", "alice@example.com", 4, true},
		{"weekly sync notes", "bob@example.com", 2, false},
		{"re: api design review", "carol@example.com", 7, true},
		{"lunch tomorrow?", "dave@example.com", 1, false},
		{"security audit results", "eve@example.com", 3, true},
	}

	for i, s := range samples {
		t := provider.Thread{
			ID:      fmt.Sprintf("thread_%d", i),
			Subject: s.subject,
			Date:    now.Add(-time.Duration(i) * time.Hour),
			Unread:  boolToInt(s.unread),
		}
		for j := range s.count {
			t.Messages = append(t.Messages, provider.Message{
				ID:       fmt.Sprintf("msg_%d_%d", i, j),
				ThreadID: t.ID,
				From:     provider.Address{Email: s.from},
				Subject:  s.subject,
				Date:     now.Add(-time.Duration(i*10+j) * time.Minute),
				TextBody: fmt.Sprintf("message %d in thread about %s", j+1, s.subject),
			})
		}
		threads = append(threads, ThreadRow{
			Thread: t,
			Label:  truncate(s.subject, 30),
			Date:   relativeTime(t.Date),
			Unread: s.unread,
		})
	}
}

func toggleThread(idx int) {
	if idx < 0 || idx >= len(threads) {
		return
	}
	row := &threads[idx]
	if row.IsMessage {
		return
	}

	row.Expanded = !row.Expanded

	if row.Expanded {
		var msgRows []ThreadRow
		for _, m := range row.Thread.Messages {
			msgRows = append(msgRows, ThreadRow{
				IsMessage: true,
				MessageID: m.ID,
				Label:     truncate(m.From.Email, 20),
				Date:      relativeTime(m.Date),
				Depth:     1,
				Unread:    !m.Read,
			})
		}
		after := make([]ThreadRow, len(threads[idx+1:]))
		copy(after, threads[idx+1:])
		threads = append(threads[:idx+1], msgRows...)
		threads = append(threads, after...)
	} else {
		end := idx + 1
		for end < len(threads) && threads[end].IsMessage {
			end++
		}
		threads = append(threads[:idx+1], threads[end:]...)
	}
}

func loadPreview(messageID string) {
	previewLines = []string{
		"message: " + messageID,
		"",
		"(preview will render here)",
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func formatSubject(s string) string {
	s = strings.TrimPrefix(s, "re: ")
	s = strings.TrimPrefix(s, "Re: ")
	return s
}

func resetCompose() {
	composeTo = ""
	composeCC = ""
	composeSubject = ""
	replyThreadID = ""
	replyMsg = nil
	editor = compose.NewEditor()
	editor.SetSize(80, 24)
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

	// TODO: wire to actual provider.Send() when connected
	_ = msg
	statusText = fmt.Sprintf("message to %s queued (no provider connected)", composeTo)
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
