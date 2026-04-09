package mailbox

import (
	"fmt"
	"log"
	"sort"
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

	folderNames []string
	canonEnd    int

	threads    []provider.Thread
	threadRows []ThreadRow

	previewLines []string
}

// read-only pointers for glyph view binding
func (m *Mailbox) FolderNames() *[]string  { return &m.folderNames }
func (m *Mailbox) ThreadRows() *[]ThreadRow { return &m.threadRows }
func (m *Mailbox) PreviewLines() *[]string  { return &m.previewLines }
func (m *Mailbox) CanonEnd() int            { return m.canonEnd }
func (m *Mailbox) FolderLen() int           { return len(m.folderNames) }
func (m *Mailbox) ThreadLen() int           { return len(m.threadRows) }

func (m *Mailbox) ThreadRowAt(sel int) *ThreadRow {
	if sel >= 0 && sel < len(m.threadRows) {
		return &m.threadRows[sel]
	}
	return nil
}

// SetSearchResults replaces threads with search results from cache
func (m *Mailbox) SetSearchResults(results []provider.Thread) {
	m.threads = results
	m.BuildThreadDisplay()
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
	for _, f := range folders {
		log.Printf("folder: id=%q name=%q unread=%d total=%d", f.ID, f.Name, f.Unread, f.Total)
	}
	m.folders = folders
	if m.cache != nil {
		m.cache.PutFolders(folders)
	}
	return nil
}

// displayFolders is the ordered folder list used for index lookups from the view.
// it's rebuilt each time BuildFolderDisplay is called, without mutating the source folders.
var displayFolders []provider.Folder

func (m *Mailbox) BuildFolderDisplay(labelsOpen bool) {
	m.folderNames = nil
	displayFolders = nil

	// collect canonical folders, dedup by display name (prefer the one with more messages)
	type ranked struct {
		folder provider.Folder
		rank   int
	}
	bestCanon := make(map[string]ranked) // display name → best folder
	var custom []provider.Folder

	for _, f := range m.folders {
		rank := canonicalRank(f.ID)
		if rank >= 0 {
			display := canonicalDisplayName(f.ID)
			existing, exists := bestCanon[display]
			if !exists || f.Total > existing.folder.Total {
				bestCanon[display] = ranked{folder: f, rank: rank}
			}
			continue
		}
		// skip empty system folders from other clients
		if strings.HasPrefix(f.ID, "[Gmail]") ||
			strings.HasPrefix(f.ID, "[Google Mail]") ||
			strings.HasPrefix(f.ID, "[Airmail]") ||
			strings.HasPrefix(f.ID, "[Mailbox]") {
			continue
		}
		custom = append(custom, f)
	}

	// sort canonical by rank
	var ordered []ranked
	for _, r := range bestCanon {
		ordered = append(ordered, r)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].rank < ordered[j].rank
	})

	for _, r := range ordered {
		displayFolders = append(displayFolders, r.folder)
		name := canonicalDisplayName(r.folder.ID)
		if r.folder.Unread > 0 {
			name = fmt.Sprintf("%s (%d)", name, r.folder.Unread)
		}
		m.folderNames = append(m.folderNames, name)
	}

	m.canonEnd = len(m.folderNames)

	if len(custom) > 0 {
		if labelsOpen {
			m.folderNames = append(m.folderNames, "▾ Labels")
		} else {
			m.folderNames = append(m.folderNames, "▸ Labels")
		}
		if labelsOpen {
			displayFolders = append(displayFolders, custom...)
			for _, f := range custom {
				name := f.Name
				if f.Unread > 0 {
					name = fmt.Sprintf("  %s (%d)", name, f.Unread)
				} else {
					name = "  " + name
				}
				m.folderNames = append(m.folderNames, name)
			}
		}
	}
}

func (m *Mailbox) displayFolder(idx int) *provider.Folder {
	if idx < len(displayFolders) {
		return &displayFolders[idx]
	}
	return nil
}

func (m *Mailbox) SelectFolder(idx int) {
	if idx < len(displayFolders) {
		m.active = idx
	}
}

func (m *Mailbox) ActiveFolderID() string {
	if m.active < len(displayFolders) {
		return displayFolders[m.active].ID
	}
	return ""
}

func (m *Mailbox) FolderCount() int {
	return len(displayFolders)
}

func (m *Mailbox) FolderName(idx int) string {
	if idx < len(displayFolders) {
		return displayFolders[idx].Name
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
		m.threads = threads
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
		m.cache.ReplaceThreads(id, result.Threads)
	}
	m.LoadThreads()
	return nil
}

func (m *Mailbox) BuildThreadDisplay() {
	m.threadRows = nil
	for i, t := range m.threads {
		m.threadRows = append(m.threadRows, ThreadRow{
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
	if sel < 0 || sel >= len(m.threadRows) {
		return
	}
	row := &m.threadRows[sel]
	if row.MsgIdx >= 0 {
		return
	}

	row.Expanded = !row.Expanded
	t := m.threads[row.ThreadIdx]

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
		after := make([]ThreadRow, len(m.threadRows[sel+1:]))
		copy(after, m.threadRows[sel+1:])
		m.threadRows = append(m.threadRows[:sel+1], msgRows...)
		m.threadRows = append(m.threadRows, after...)
	} else {
		end := sel + 1
		for end < len(m.threadRows) && m.threadRows[end].MsgIdx >= 0 {
			end++
		}
		m.threadRows = append(m.threadRows[:sel+1], m.threadRows[end:]...)
	}
}

func (m *Mailbox) SelectedMessage(sel int) *provider.Message {
	if sel < 0 || sel >= len(m.threadRows) {
		return nil
	}
	row := m.threadRows[sel]
	if row.MsgIdx < 0 {
		return nil
	}
	if row.ThreadIdx < len(m.threads) && row.MsgIdx < len(m.threads[row.ThreadIdx].Messages) {
		msg := m.threads[row.ThreadIdx].Messages[row.MsgIdx]
		return &msg
	}
	return nil
}

func (m *Mailbox) SelectedThread(sel int) *provider.Thread {
	if sel < 0 || sel >= len(m.threadRows) {
		return nil
	}
	row := m.threadRows[sel]
	if row.ThreadIdx < len(m.threads) {
		return &m.threads[row.ThreadIdx]
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

	m.previewLines = nil
	m.previewLines = append(m.previewLines,
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
		m.previewLines = append(m.previewLines, line)
	}
}

// actions — each returns an undo closure + description

func (m *Mailbox) Archive(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	thread, folder := *t, m.ActiveFolderID()
	cmdID := m.queueCommand("archive", t.ID, nil)
	m.cache.DeleteThread(t.ID)
	m.LoadThreads()
	m.BuildThreadDisplay()

	return func() {
		m.cancelCommand(cmdID)
		m.cache.PutThread(folder, thread)
		m.LoadThreads()
		m.BuildThreadDisplay()
	}, fmt.Sprintf("archived '%s'", truncate(thread.Subject, 30))
}

func (m *Mailbox) Delete(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	thread, folder := *t, m.ActiveFolderID()
	cmdID := m.queueCommand("delete", t.ID, nil)
	m.cache.DeleteThread(t.ID)
	m.LoadThreads()
	m.BuildThreadDisplay()

	return func() {
		m.cancelCommand(cmdID)
		m.cache.PutThread(folder, thread)
		m.LoadThreads()
		m.BuildThreadDisplay()
	}, fmt.Sprintf("deleted '%s'", truncate(thread.Subject, 30))
}

func (m *Mailbox) ToggleStar(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	before := make([]bool, len(t.Messages))
	var cmdIDs []string
	for i, msg := range t.Messages {
		before[i] = msg.Starred
		if msg.Starred {
			cmdIDs = append(cmdIDs, m.queueCommand("unstar", msg.ID, nil))
			t.Messages[i].Starred = false
		} else {
			cmdIDs = append(cmdIDs, m.queueCommand("star", msg.ID, nil))
			t.Messages[i].Starred = true
		}
	}
	folder := m.ActiveFolderID()
	thread := *t
	m.cache.PutThread(folder, *t)
	m.LoadThreads()
	m.BuildThreadDisplay()

	return func() {
		for _, id := range cmdIDs {
			m.cancelCommand(id)
		}
		for i := range thread.Messages {
			thread.Messages[i].Starred = before[i]
		}
		m.cache.PutThread(folder, thread)
		m.LoadThreads()
		m.BuildThreadDisplay()
	}, "toggled star"
}

func (m *Mailbox) ToggleRead(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	folder := m.ActiveFolderID()
	beforeUnread := t.Unread
	markRead := t.Unread > 0

	var cmdIDs []string
	for _, msg := range t.Messages {
		if markRead && !msg.Read {
			cmdIDs = append(cmdIDs, m.queueCommand("mark_read", msg.ID, nil))
		} else if !markRead {
			cmdIDs = append(cmdIDs, m.queueCommand("mark_unread", msg.ID, nil))
		}
	}
	if markRead {
		t.Unread = 0
	} else {
		t.Unread = len(t.Messages)
	}
	thread := *t
	m.cache.PutThread(folder, *t)
	m.LoadThreads()
	m.BuildThreadDisplay()

	desc = "marked read"
	if !markRead {
		desc = "marked unread"
	}
	return func() {
		for _, id := range cmdIDs {
			m.cancelCommand(id)
		}
		thread.Unread = beforeUnread
		m.cache.PutThread(folder, thread)
		m.LoadThreads()
		m.BuildThreadDisplay()
	}, desc
}

func (m *Mailbox) MarkRead(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil || t.Unread == 0 {
		return nil, ""
	}
	folder := m.ActiveFolderID()
	beforeUnread := t.Unread

	var cmdIDs []string
	for _, msg := range t.Messages {
		if !msg.Read {
			cmdIDs = append(cmdIDs, m.queueCommand("mark_read", msg.ID, nil))
		}
	}
	t.Unread = 0
	thread := *t
	m.cache.PutThread(folder, *t)
	m.LoadThreads()
	m.BuildThreadDisplay()

	return func() {
		for _, id := range cmdIDs {
			m.cancelCommand(id)
		}
		thread.Unread = beforeUnread
		m.cache.PutThread(folder, thread)
		m.LoadThreads()
		m.BuildThreadDisplay()
	}, "marked read"
}

func (m *Mailbox) queueCommand(action, targetID string, params map[string]string) string {
	id := fmt.Sprintf("%s-%s-%d", action, targetID, time.Now().UnixNano())
	if m.cache == nil {
		return id
	}
	m.cache.PutCommand(cache.Command{
		ID:        id,
		Action:    action,
		TargetID:  targetID,
		Params:    params,
		Status:    "pending",
		CreatedAt: time.Now(),
	})
	return id
}

func (m *Mailbox) cancelCommand(id string) {
	if m.cache != nil {
		m.cache.DeleteCommand(id)
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

// canonicalSuffixes maps the folder suffix (after [Gmail]/ or [Google Mail]/)
// to its display name. standalone folder names are also included.
var canonicalSuffixes = map[string]string{
	"Sent Mail": "Sent",
	"Drafts":    "Drafts",
	"Starred":   "Starred",
	"Trash":     "Trash",
	"Bin":       "Trash",
	"Spam":      "Spam",
	"All Mail":  "Archive",
}

var canonicalStandalone = map[string]string{
	"INBOX":            "Inbox",
	"Sent Messages":    "Sent",
	"Deleted Messages": "Trash",
	"Drafts":           "Drafts",
}

// canonicalDisplayName returns the display name for a folder, or empty if not canonical
func canonicalDisplayName(id string) string {
	if name, ok := canonicalStandalone[id]; ok {
		return name
	}
	for _, prefix := range []string{"[Gmail]/", "[Google Mail]/"} {
		if strings.HasPrefix(id, prefix) {
			suffix := strings.TrimPrefix(id, prefix)
			if name, ok := canonicalSuffixes[suffix]; ok {
				return name
			}
		}
	}
	return ""
}

// canonicalRank returns a sort rank for canonical folders, or -1 if not canonical
func canonicalRank(id string) int {
	order := []string{"Inbox", "Sent", "Drafts", "Starred", "Trash", "Spam", "Archive"}
	name := canonicalDisplayName(id)
	if name == "" {
		return -1
	}
	for i, o := range order {
		if name == o {
			return i
		}
	}
	return -1
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
