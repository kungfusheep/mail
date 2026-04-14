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
	email string

	folders []provider.Folder
	active  int

	folderNames []string
	canonEnd    int

	threads    []provider.Thread
	threadRows []ThreadRow

	previewLines []string
	previewText  string
}

// read-only pointers for glyph view binding
func (m *Mailbox) FolderNames() *[]string   { return &m.folderNames }
func (m *Mailbox) ThreadRows() *[]ThreadRow { return &m.threadRows }
func (m *Mailbox) PreviewLines() *[]string  { return &m.previewLines }
func (m *Mailbox) CanonEnd() int            { return m.canonEnd }
func (m *Mailbox) FolderLen() int           { return len(m.folderNames) }
func (m *Mailbox) ThreadLen() int           { return len(m.threadRows) }
func (m *Mailbox) PreviewText() *string     { return &m.previewText }

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

func New(c *cache.Cache, email string) *Mailbox {
	return &Mailbox{cache: c, email: email}
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

func (m *Mailbox) SyncSent() {
	if m.imap == nil || m.cache == nil {
		return
	}
	sentFolders := []string{"[Google Mail]/Sent Mail", "[Gmail]/Sent Mail", "Sent Messages"}
	for _, sf := range sentFolders {
		result, err := m.imap.ListThreads(provider.ListOptions{Folder: sf, MaxResults: 25})
		if err != nil {
			continue
		}
		// fetch full body for each sent message (we're still in the Sent folder)
		for _, t := range result.Threads {
			for _, msg := range t.Messages {
				if msg.TextBody == "" && msg.HTMLBody == "" {
					full, err := m.imap.GetMessage(msg.ID)
					if err == nil {
						msg = full
					}
				}
				msg.Read = true
				m.cache.PutSentMessage(msg)
			}
		}
		// re-select original folder
		m.imap.ListThreads(provider.ListOptions{Folder: m.ActiveFolderID(), MaxResults: 1})
		return
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

	// merge cached sent messages into threads for complete conversations
	if m.cache != nil {
		result.Threads = m.mergeWithSentMessages(result.Threads)
		m.cache.ReplaceThreads(id, result.Threads)
	}
	m.LoadThreads()
	return nil
}

func (m *Mailbox) mergeWithSentMessages(threads []provider.Thread) []provider.Thread {
	sent, err := m.cache.GetSentMessages(50)
	if err != nil || len(sent) == 0 {
		return threads
	}

	// build set of known MessageIDs across all threads
	known := make(map[string]bool)
	for _, t := range threads {
		for _, msg := range t.Messages {
			if msg.MessageID != "" {
				known[msg.MessageID] = true
			}
		}
	}

	// match sent messages to threads by InReplyTo OR normalized subject
	for i := range threads {
		threadSubj := normalizeSubject(threads[i].Subject)
		for j := range sent {
			if known[sent[j].MessageID] {
				continue
			}
			sentSubj := normalizeSubject(sent[j].Subject)

			matched := false
			// check InReplyTo links
			for _, msg := range threads[i].Messages {
				if msg.InReplyTo == sent[j].MessageID || sent[j].InReplyTo == msg.MessageID {
					matched = true
					break
				}
			}
			// fallback: subject match within 7 days of thread date
			if !matched && sentSubj != "" && sentSubj == threadSubj {
				diff := threads[i].Date.Sub(sent[j].Date)
				if diff < 0 {
					diff = -diff
				}
				if diff < 7*24*time.Hour {
					matched = true
				}
			}
			if matched {
				threads[i].Messages = append(threads[i].Messages, sent[j])
				known[sent[j].MessageID] = true
			}
		}
	}

	merged := 0
	for _, t := range threads {
		if len(t.Messages) > 1 {
			merged++
			log.Printf("merge: thread %q now has %d messages", t.Subject, len(t.Messages))
		}
	}
	log.Printf("merge: %d sent messages checked, %d threads enriched", len(sent), merged)

	// sort messages within each thread chronologically
	for i := range threads {
		sort.Slice(threads[i].Messages, func(a, b int) bool {
			return threads[i].Messages[a].Date.Before(threads[i].Messages[b].Date)
		})
		// update thread metadata
		if len(threads[i].Messages) > 0 {
			threads[i].Date = threads[i].Messages[len(threads[i].Messages)-1].Date
		}
	}

	return threads
}

func (m *Mailbox) BuildThreadDisplay() {
	m.threadRows = nil
	for i, t := range m.threads {
		sender := ""
		if len(t.Participants) > 0 {
			var names []string
			for _, p := range t.Participants {
				if p.Name != "" {
					names = append(names, p.Name)
				} else {
					names = append(names, p.Email)
				}
			}
			sender = strings.Join(names, " · ")
		} else if len(t.Messages) > 0 {
			from := t.Messages[0].From
			if from.Name != "" {
				sender = from.Name
			} else {
				sender = from.Email
			}
		}
		m.threadRows = append(m.threadRows, ThreadRow{
			ThreadIdx: i,
			MsgIdx:    -1,
			Label:     t.Subject,
			Sender:    sender,
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
	t := m.threads[row.ThreadIdx]
	// TODO: re-enable when we have real threading
	// if len(t.Messages) <= 1 {
	// 	return
	// }

	row.Expanded = !row.Expanded
	row.Grouped = row.Expanded
	t = m.threads[row.ThreadIdx]

	if row.Expanded {
		var msgRows []ThreadRow
		for j := len(t.Messages) - 1; j >= 0; j-- {
			msg := t.Messages[j]
			name := msg.From.Email
			if msg.From.Name != "" {
				name = msg.From.Name
			}
			msgRows = append(msgRows, ThreadRow{
				ThreadIdx: row.ThreadIdx,
				MsgIdx:    j,
				Label:     name,
				Date:      relativeTime(msg.Date),
				Unread:    !msg.Read,
				Grouped:   true,
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

func (m *Mailbox) LoadConversation(sel int, width int) {
	t := m.SelectedThread(sel)
	if t == nil || len(t.Messages) == 0 {
		return
	}

	cols := 72
	if width > 0 {
		cols = width/2 - 4
		if cols < 40 {
			cols = 40
		}
	}

	myEmail := m.email

	var lines []string
	for i, msg := range t.Messages {
		msg = m.resolveBody(msg, cols)

		// sender — "You" for sent messages, name for received
		from := msg.From.Email
		if msg.From.Name != "" {
			from = msg.From.Name
		}
		if myEmail != "" && strings.EqualFold(msg.From.Email, myEmail) {
			from = "You"
		}

		// header with date
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("%s · %s", from, msg.Date.Format("2 Jan 15:04")))
		lines = append(lines, "")

		// body — strip quoted text since we show each message separately
		body := msg.TextBody
		if msg.HTMLBody != "" {
			body = preview.RenderHTML(msg.HTMLBody, msg.TextBody, cols)
		} else if strings.Contains(body, "<p>") || strings.Contains(body, "<br") || strings.Contains(body, "<div") {
			body = preview.RenderHTML(body, "", cols)
		}
		body = preview.Sanitize(body)
		body = preview.StripQuoted(body)
		body = strings.TrimSpace(body)
		if body != "" {
			for _, line := range strings.Split(body, "\n") {
				lines = append(lines, "  "+line)
			}
		}

		// separator between messages
		if i < len(t.Messages)-1 {
			lines = append(lines, "")
			// lines = append(lines, strings.Repeat("·", min(cols/2, 20)))
		}
	}
	lines = append(lines, "")

	m.previewLines = lines
	m.previewText = strings.Join(lines, "\n")
}

func (m *Mailbox) resolveBody(msg provider.Message, cols int) provider.Message {
	if msg.TextBody == "" && msg.HTMLBody == "" {
		if m.cache != nil && msg.MessageID != "" {
			sent, err := m.cache.GetSentMessages(100)
			if err == nil {
				for _, s := range sent {
					if s.MessageID == msg.MessageID && (s.TextBody != "" || s.HTMLBody != "") {
						msg.TextBody = s.TextBody
						msg.HTMLBody = s.HTMLBody
						break
					}
				}
			}
		}
		if msg.TextBody == "" && msg.HTMLBody == "" && m.imap != nil {
			full, err := m.imap.GetMessage(msg.ID)
			if err == nil {
				msg = full
			}
		}
	}
	return msg
}

func (m *Mailbox) LoadPreview(msg provider.Message, width int) {
	if msg.TextBody == "" && msg.HTMLBody == "" {
		// check cache for sent message body first
		if m.cache != nil && msg.MessageID != "" {
			sent, err := m.cache.GetSentMessages(100)
			if err == nil {
				for _, s := range sent {
					if s.MessageID == msg.MessageID && (s.TextBody != "" || s.HTMLBody != "") {
						msg.TextBody = s.TextBody
						msg.HTMLBody = s.HTMLBody
						break
					}
				}
			}
		}
		// fall back to IMAP fetch
		if msg.TextBody == "" && msg.HTMLBody == "" && m.imap != nil {
			full, err := m.imap.GetMessage(msg.ID)
			if err == nil {
				msg = full
			}
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
	body = preview.Sanitize(body)
	for _, line := range strings.Split(body, "\n") {
		m.previewLines = append(m.previewLines, line)
	}
	m.previewText = strings.Join(m.previewLines, "\n")
}

// actions — each returns an undo closure + description

func (m *Mailbox) Archive(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	dest := m.folderIDByDisplayName("Archive")
	if dest == "" {
		log.Println("archive: no archive folder found")
		return nil, ""
	}
	thread, folder := *t, m.ActiveFolderID()
	cmdID := m.queueCommand("move", t.ID, map[string]string{"folder": dest})
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
	dest := m.folderIDByDisplayName("Trash")
	if dest == "" {
		log.Println("delete: no trash folder found")
		return nil, ""
	}
	thread, folder := *t, m.ActiveFolderID()
	cmdID := m.queueCommand("move", t.ID, map[string]string{"folder": dest})
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

// folderIDByDisplayName returns the raw IMAP folder ID for a canonical display name
// (e.g. "Trash" → "[Google Mail]/Bin", "Archive" → "[Google Mail]/All Mail")
func (m *Mailbox) folderIDByDisplayName(display string) string {
	for _, f := range m.folders {
		if canonicalDisplayName(f.ID) == display {
			return f.ID
		}
	}
	return ""
}

// helpers

func normalizeSubject(s string) string {
	s = strings.TrimSpace(s)
	for {
		lower := strings.ToLower(s)
		if strings.HasPrefix(lower, "re:") {
			s = strings.TrimSpace(s[3:])
		} else if strings.HasPrefix(lower, "fwd:") {
			s = strings.TrimSpace(s[4:])
		} else if strings.HasPrefix(lower, "fw:") {
			s = strings.TrimSpace(s[3:])
		} else {
			break
		}
	}
	return strings.ToLower(s)
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
	Sender    string
	Date      string
	Unread    bool
	Expanded  bool
	Selected  bool
	Grouped   bool
}

func (m *Mailbox) SetSelected(sel int) {
	for i := range m.threadRows {
		m.threadRows[i].Selected = i == sel
	}
}
