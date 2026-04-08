package mailbox

import (
	"fmt"
	"strings"
	"time"

	"github.com/kungfusheep/mail/cache"
	imapprov "github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/preview"
	"github.com/kungfusheep/mail/provider"
)

type Mailbox struct {
	cache *cache.Cache
	imap  *imapprov.IMAP

	folders []provider.Folder
	active  int

	FolderNames []string
	CanonEnd    int

	Threads    []provider.Thread
	ThreadRows []ThreadRow

	PreviewLines []string
}

func New(c *cache.Cache) *Mailbox {
	return &Mailbox{cache: c}
}

func (m *Mailbox) SetIMAP(imap *imapprov.IMAP) {
	m.imap = imap
}

// folders

func (m *Mailbox) LoadFolders() {
	if m.cache == nil {
		return
	}
	folders, err := m.cache.GetFolders()
	if err == nil && len(folders) > 0 {
		m.folders = folders
	}
}

func (m *Mailbox) SyncFolders() error {
	if m.imap == nil {
		return fmt.Errorf("not connected")
	}
	folders, err := m.imap.ListFolders()
	if err != nil {
		return err
	}
	m.folders = folders
	if m.cache != nil {
		m.cache.PutFolders(folders)
	}
	return nil
}

func (m *Mailbox) BuildFolderDisplay(labelsOpen bool) {
	m.FolderNames = nil

	seen := make(map[string]bool)
	var ordered []provider.Folder

	for _, id := range canonicalOrder {
		for _, f := range m.folders {
			if f.ID == id && !seen[f.ID] {
				seen[f.ID] = true
				ordered = append(ordered, f)
			}
		}
	}
	var custom []provider.Folder
	for _, f := range m.folders {
		if !seen[f.ID] {
			if strings.HasPrefix(f.ID, "[Gmail]/") {
				continue
			}
			seen[f.ID] = true
			custom = append(custom, f)
		}
	}

	m.folders = ordered

	for _, f := range m.folders {
		name := f.Name
		if display, ok := canonicalFolderNames[f.ID]; ok {
			name = display
		}
		if f.Unread > 0 {
			name = fmt.Sprintf("%s (%d)", name, f.Unread)
		}
		m.FolderNames = append(m.FolderNames, name)
	}

	m.CanonEnd = len(m.FolderNames)

	if len(custom) > 0 {
		if labelsOpen {
			m.FolderNames = append(m.FolderNames, "▾ Labels")
		} else {
			m.FolderNames = append(m.FolderNames, "▸ Labels")
		}
		if labelsOpen {
			m.folders = append(m.folders, custom...)
			for _, f := range custom {
				name := f.Name
				if f.Unread > 0 {
					name = fmt.Sprintf("  %s (%d)", name, f.Unread)
				} else {
					name = "  " + name
				}
				m.FolderNames = append(m.FolderNames, name)
			}
		}
	}
}

func (m *Mailbox) SelectFolder(idx int) {
	if idx < len(m.folders) {
		m.active = idx
	}
}

func (m *Mailbox) ActiveFolderID() string {
	if m.active < len(m.folders) {
		return m.folders[m.active].ID
	}
	return ""
}

func (m *Mailbox) FolderCount() int {
	return len(m.folders)
}

func (m *Mailbox) FolderName(idx int) string {
	if idx < len(m.folders) {
		return m.folders[idx].Name
	}
	return ""
}

// threads

func (m *Mailbox) LoadThreads() {
	if m.cache == nil || m.ActiveFolderID() == "" {
		return
	}
	threads, err := m.cache.GetThreads(m.ActiveFolderID(), 25)
	if err == nil {
		m.Threads = threads
	}
}

func (m *Mailbox) SyncThreads() error {
	if m.imap == nil {
		return fmt.Errorf("not connected")
	}
	id := m.ActiveFolderID()
	if id == "" {
		return nil
	}
	result, err := m.imap.ListThreads(provider.ListOptions{
		Folder:     id,
		MaxResults: 25,
	})
	if err != nil {
		return err
	}
	if m.cache != nil {
		for _, t := range result.Threads {
			m.cache.PutThread(id, t)
		}
	}
	m.LoadThreads()
	return nil
}

func (m *Mailbox) BuildThreadDisplay() {
	m.ThreadRows = nil
	for i, t := range m.Threads {
		m.ThreadRows = append(m.ThreadRows, ThreadRow{
			ThreadIdx: i,
			MsgIdx:    -1,
			Label:     truncate(t.Subject, 35),
			Detail:    fmt.Sprintf("[%d]", len(t.Messages)),
			Date:      relativeTime(t.Date),
			Unread:    t.Unread > 0,
		})
	}
}

func (m *Mailbox) ToggleThread(sel int) {
	if sel < 0 || sel >= len(m.ThreadRows) {
		return
	}
	row := &m.ThreadRows[sel]
	if row.MsgIdx >= 0 {
		return
	}

	row.Expanded = !row.Expanded
	t := m.Threads[row.ThreadIdx]

	if row.Expanded {
		var msgRows []ThreadRow
		for j, msg := range t.Messages {
			msgRows = append(msgRows, ThreadRow{
				ThreadIdx: row.ThreadIdx,
				MsgIdx:    j,
				Label:     truncate(msg.From.Email, 25),
				Detail:    truncate(msg.Subject, 20),
				Date:      relativeTime(msg.Date),
				Unread:    !msg.Read,
			})
		}
		after := make([]ThreadRow, len(m.ThreadRows[sel+1:]))
		copy(after, m.ThreadRows[sel+1:])
		m.ThreadRows = append(m.ThreadRows[:sel+1], msgRows...)
		m.ThreadRows = append(m.ThreadRows, after...)
	} else {
		end := sel + 1
		for end < len(m.ThreadRows) && m.ThreadRows[end].MsgIdx >= 0 {
			end++
		}
		m.ThreadRows = append(m.ThreadRows[:sel+1], m.ThreadRows[end:]...)
	}
}

func (m *Mailbox) SelectedMessage(sel int) *provider.Message {
	if sel < 0 || sel >= len(m.ThreadRows) {
		return nil
	}
	row := m.ThreadRows[sel]
	if row.MsgIdx < 0 {
		return nil
	}
	if row.ThreadIdx < len(m.Threads) && row.MsgIdx < len(m.Threads[row.ThreadIdx].Messages) {
		msg := m.Threads[row.ThreadIdx].Messages[row.MsgIdx]
		return &msg
	}
	return nil
}

func (m *Mailbox) SelectedThread(sel int) *provider.Thread {
	if sel < 0 || sel >= len(m.ThreadRows) {
		return nil
	}
	row := m.ThreadRows[sel]
	if row.ThreadIdx < len(m.Threads) {
		return &m.Threads[row.ThreadIdx]
	}
	return nil
}

func (m *Mailbox) LastMessage(sel int) *provider.Message {
	t := m.SelectedThread(sel)
	if t == nil || len(t.Messages) == 0 {
		return nil
	}
	msg := t.Messages[len(t.Messages)-1]
	return &msg
}

// preview

func (m *Mailbox) LoadPreview(msg provider.Message, width int) {
	if msg.TextBody == "" && msg.HTMLBody == "" && m.imap != nil {
		full, err := m.imap.GetMessage(msg.ID)
		if err == nil {
			msg = full
		}
	}

	cols := 72
	if width > 0 {
		cols = width/2 - 4
		if cols < 40 {
			cols = 40
		}
	}

	m.PreviewLines = nil
	m.PreviewLines = append(m.PreviewLines,
		fmt.Sprintf("From: %s", msg.From.String()),
		fmt.Sprintf("To: %s", formatAddresses(msg.To)),
		fmt.Sprintf("Date: %s", msg.Date.Format("2 Jan 2006 15:04")),
		fmt.Sprintf("Subject: %s", msg.Subject),
		"",
	)

	body := msg.TextBody
	if msg.HTMLBody != "" {
		body = preview.RenderHTML(msg.HTMLBody, msg.TextBody, cols)
	} else if strings.Contains(body, "<p>") || strings.Contains(body, "<br") || strings.Contains(body, "<div") {
		body = preview.RenderHTML(body, "", cols)
	}
	for _, line := range strings.Split(body, "\n") {
		m.PreviewLines = append(m.PreviewLines, line)
	}
}

// commands

func (m *Mailbox) ProcessPendingCommands() {
	if m.cache == nil || m.imap == nil {
		return
	}
	cmds, err := m.cache.PendingCommands()
	if err != nil || len(cmds) == 0 {
		return
	}
	for _, cmd := range cmds {
		var cmdErr error
		switch cmd.Action {
		case "mark_read":
			cmdErr = m.imap.MarkRead([]string{cmd.TargetID}, true)
		case "mark_unread":
			cmdErr = m.imap.MarkRead([]string{cmd.TargetID}, false)
		case "star":
			cmdErr = m.imap.Star([]string{cmd.TargetID}, true)
		case "unstar":
			cmdErr = m.imap.Star([]string{cmd.TargetID}, false)
		case "delete":
			cmdErr = m.imap.Delete([]string{cmd.TargetID})
		case "archive":
			cmdErr = m.imap.Archive([]string{cmd.TargetID})
		case "move":
			if folder, ok := cmd.Params["folder"]; ok {
				cmdErr = m.imap.Move([]string{cmd.TargetID}, folder)
			}
		}
		if cmdErr != nil {
			m.cache.UpdateCommandStatus(cmd.ID, "failed", cmdErr.Error())
		} else {
			m.cache.UpdateCommandStatus(cmd.ID, "synced", "")
		}
	}
	m.cache.ClearSyncedCommands()
}

// canonical folder ordering

var canonicalFolderNames = map[string]string{
	"INBOX":             "Inbox",
	"[Gmail]/Sent Mail": "Sent",
	"[Gmail]/Drafts":    "Drafts",
	"[Gmail]/Starred":   "Starred",
	"[Gmail]/Trash":     "Trash",
	"[Gmail]/Spam":      "Spam",
	"[Gmail]/All Mail":  "Archive",
	"Sent Messages":     "Sent",
	"Deleted Messages":  "Trash",
	"Drafts":            "Drafts",
}

var canonicalOrder = []string{
	"INBOX",
	"[Gmail]/Sent Mail", "Sent Messages",
	"[Gmail]/Drafts", "Drafts",
	"[Gmail]/Starred",
	"[Gmail]/Trash", "Deleted Messages",
}

// helpers

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

func formatAddresses(addrs []provider.Address) string {
	var parts []string
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

// ThreadRow is a display row — either a thread header or an expanded message
type ThreadRow struct {
	ThreadIdx int
	MsgIdx    int // -1 for thread header, >= 0 for message
	Label     string
	Detail    string
	Date      string
	Unread    bool
	Expanded  bool
}
