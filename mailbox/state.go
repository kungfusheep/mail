package mailbox

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/preview"
	"github.com/kungfusheep/mail/provider"
)

type State struct {
	cache *cache.Cache
	imap  *imap.IMAP
	email string

	linkOpener LinkOpener
	notify     func(string)
	notifyErr  func(string)

	attachmentOpener  LinkOpener
	attachmentFetcher func(AttachmentRow) ([]byte, error)
	messageFetcher    func(string) (provider.Message, error)

	folders []provider.Folder
	active  int

	folderNames []string
	canonEnd    int

	// displayMu serialises mutations to threads / threadRows. Actions run on
	// the UI goroutine; the cache-publish subscriber runs separately. Without
	// this both paths race through LoadThreads + BuildThreadDisplay and can
	// leave the display in an inconsistent (out-of-date-order) state.
	displayMu  sync.Mutex
	threads    []provider.Thread
	threadRows []ThreadRow
	selected   int

	previewLines []string
	previewText  string

	// conversation + guards for the preview pane. The async body-fetch
	// goroutine spawned from LoadConversation shares this slice with
	// the synchronous path that gets called on every folder/row change
	// — without serialisation you get a classic slice-header race
	// (goroutine reads len, main resets slice, goroutine writes past
	// end, panic). convEpoch is bumped on every LoadConversation so a
	// late-returning goroutine can tell its own render was superseded
	// and bail out cleanly.
	convMu       sync.Mutex
	convEpoch    int64
	conversation []ConversationMessage
}

// read-only pointers for glyph view binding

func (m *State) FolderNames() *[]string                       { return &m.folderNames }
func (m *State) ThreadRows() *[]ThreadRow                     { return &m.threadRows }
func (m *State) PreviewLines() *[]string                      { return &m.previewLines }
func (m *State) CanonEnd() int                                { return m.canonEnd }
func (m *State) FolderLen() int                               { return len(m.folderNames) }
func (m *State) ThreadLen() int                               { return len(m.threadRows) }
func (m *State) Selected() int                                { return m.ClampSelection(m.selected) }
func (m *State) PreviewText() *string                         { return &m.previewText }
func (m *State) ConversationMessages() *[]ConversationMessage { return &m.conversation }

func (m *State) ThreadRowAt(sel int) *ThreadRow {
	if sel >= 0 && sel < len(m.threadRows) {
		return &m.threadRows[sel]
	}
	return nil
}

// SetSearchResults replaces threads with search results from cache
func (m *State) SetSearchResults(results []provider.Thread) {
	m.threads = results
	m.BuildThreadDisplay()
}

func NewState(c *cache.Cache, email string) *State {
	return &State{cache: c, email: email}
}

func (m *State) SetLinkOpener(open LinkOpener) {
	m.linkOpener = open
}

func (m *State) SetAttachmentOpener(open LinkOpener) {
	m.attachmentOpener = open
}

func (m *State) SetNotifiers(info, err func(string)) {
	m.notify = info
	m.notifyErr = err
}

func (m *State) OpenLink(href string) {
	m.notifyInfo("opening link...")
	if m.linkOpener == nil {
		log.Printf("no link opener configured for %q", href)
		m.notifyError("link opener not configured")
		return
	}
	if err := m.linkOpener(href); err != nil {
		log.Printf("open link %q: %v", href, err)
		m.notifyError(fmt.Sprintf("open link: %v", err))
	}
}

func (m *State) notifyInfo(text string) {
	if m.notify != nil {
		m.notify(text)
	}
}

func (m *State) notifyError(text string) {
	if m.notifyErr != nil {
		m.notifyErr(text)
		return
	}
	m.notifyInfo(text)
}

func (m *State) SetIMAP(imapClient *imap.IMAP) {
	m.imap = imapClient
}

// folders

func (m *State) LoadFolders() {
	if m.cache == nil {
		return
	}
	folders, err := m.cache.GetFolders()
	if err == nil && len(folders) > 0 {
		m.folders = folders
	}
}

func (m *State) SyncFolders() error {
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
	if m.cache != nil {
		m.cache.PutFolders(folders)
	}
	m.LoadFolders()
	return nil
}

// displayFolders is the ordered folder list used for index lookups from the view.
// it's rebuilt each time BuildFolderDisplay is called, without mutating the source folders.
var displayFolders []provider.Folder

func (m *State) BuildFolderDisplay(labelsOpen bool) {
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

func (m *State) displayFolder(idx int) *provider.Folder {
	if idx < len(displayFolders) {
		return &displayFolders[idx]
	}
	return nil
}

func (m *State) SelectFolder(idx int) {
	if idx < len(displayFolders) {
		m.active = idx
	}
}

func (m *State) ActiveFolderID() string {
	if m.active < len(displayFolders) {
		return displayFolders[m.active].ID
	}
	return ""
}

func (m *State) ActiveFolderUnread() int {
	id := m.ActiveFolderID()
	if id == "" {
		return 0
	}
	for _, f := range m.folders {
		if f.ID == id {
			return f.Unread
		}
	}
	if m.active < len(displayFolders) && displayFolders[m.active].ID == id {
		return displayFolders[m.active].Unread
	}
	return 0
}

// ActiveFolderCanonical returns the canonical display name of the active
// folder (e.g. "Inbox", "Drafts", "Trash") or "" if the folder isn't one
// of the well-known system folders. Callers use this to special-case
// behaviour by folder kind, without coupling to raw IMAP folder IDs.
func (m *State) ActiveFolderCanonical() string {
	return canonicalDisplayName(m.ActiveFolderID())
}

// Watch subscribes the mailbox to a cache label and keeps its thread
// list in lockstep with cache mutations. On every publish we rerun
// LoadThreads + BuildThreadDisplay (the invariant: threadRows mirrors
// cache state for the active folder) and then call onRefresh for any
// app-level work that needs to happen too — re-selecting, reloading
// the preview, requesting a render.
//
// Returned func unsubscribes. Callers hold onto it to swap watchers
// when the active folder changes.
func (m *State) Watch(label string, onRefresh func()) func() {
	if m.cache == nil || label == "" {
		return func() {}
	}
	ch, unsub := m.cache.Subscribe(label)
	go func() {
		for range ch {
			m.LoadThreads()
			m.BuildThreadDisplay()
			if onRefresh != nil {
				onRefresh()
			}
		}
	}()
	return unsub
}

func (m *State) FolderCount() int {
	return len(displayFolders)
}

func (m *State) FolderName(idx int) string {
	if idx < len(displayFolders) {
		return displayFolders[idx].Name
	}
	return ""
}

// threads

func (m *State) LoadThreads() {
	if m.cache == nil || m.ActiveFolderID() == "" {
		return
	}
	// Drafts folder is a direct projection of the local drafts table —
	// the threads table is bypassed entirely so local edits show up the
	// moment PutDraft returns, without needing to round-trip through IMAP.
	if m.ActiveFolderCanonical() == "Drafts" {
		drafts, err := m.cache.ListDrafts()
		if err != nil {
			return
		}
		m.displayMu.Lock()
		m.threads = draftsAsThreads(drafts)
		m.displayMu.Unlock()
		m.updateActiveFolderUnread()
		return
	}
	threads, err := m.cache.GetThreads(m.ActiveFolderID(), 25)
	if err != nil {
		return
	}
	m.displayMu.Lock()
	m.threads = threads
	m.displayMu.Unlock()
	m.updateActiveFolderUnread()
}

func (m *State) updateActiveFolderUnread() {
	id := m.ActiveFolderID()
	if id == "" {
		return
	}
	unread := 0
	for _, t := range m.threads {
		unread += t.Unread
	}
	for i := range m.folders {
		if m.folders[i].ID == id {
			m.folders[i].Unread = unread
			return
		}
	}
}

// draftsAsThreads projects the local drafts table into the provider.Thread
// shape the UI renders. Keeps the Drafts folder view driven by one source
// of truth (the drafts table) — every save updates it, every frame reads
// from it, no intermediate snapshot to go stale.
func draftsAsThreads(drafts []cache.Draft) []provider.Thread {
	threads := make([]provider.Thread, 0, len(drafts))
	for _, d := range drafts {
		subject := d.Subject
		if subject == "" {
			subject = "(no subject)"
		}
		to := provider.ParseAddressList(d.To)
		msg := provider.Message{
			ID:       d.ThreadID,
			ThreadID: d.ThreadID,
			To:       to,
			CC:       provider.ParseAddressList(d.Cc),
			BCC:      provider.ParseAddressList(d.Bcc),
			Subject:  d.Subject,
			TextBody: d.Body,
			Date:     d.UpdatedAt,
			Read:     true,
		}
		threads = append(threads, provider.Thread{
			ID:           d.ThreadID,
			Subject:      subject,
			Snippet:      snippet(d.Body),
			Messages:     []provider.Message{msg},
			Date:         d.UpdatedAt,
			Participants: to,
		})
	}
	return threads
}

func snippet(body string) string {
	const max = 120
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, "\n", " ")
	if len(body) <= max {
		return body
	}
	return body[:max]
}

func (m *State) SyncSent() {
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
		m.imap.SelectFolder(m.ActiveFolderID())
		return
	}
}

func (m *State) SyncThreads() error {
	if m.imap == nil {
		return fmt.Errorf("not connected")
	}
	id := m.ActiveFolderID()
	if id == "" {
		return nil
	}
	log.Printf("SyncThreads: fetching folder=%q", id)
	result, err := m.imap.ListThreads(provider.ListOptions{
		Folder:     id,
		MaxResults: 25,
	})
	if err != nil {
		log.Printf("SyncThreads: %q failed: %v", id, err)
		return err
	}
	log.Printf("SyncThreads: %q returned %d threads", id, len(result.Threads))
	m.applySyncResult(id, result.Threads)
	return nil
}

// applySyncResult routes an IMAP sync result to the right storage path
// based on the SOURCE folder it came from. Gating on the source (not the
// current view) matters because a sync can resolve after the user has
// switched folders — a Starred fetch landing while we're on Drafts must
// go through the threads table, not be fed to reconcileDrafts which
// would adopt every message as a fake "server draft".
func (m *State) applySyncResult(folderID string, threads []provider.Thread) {
	if m.cache == nil || folderID == "" {
		return
	}
	if canonicalDisplayName(folderID) == "Drafts" {
		m.reconcileDrafts(threads)
		m.LoadThreads()
		return
	}
	threads = m.mergeWithSentMessages(threads)
	m.preserveCachedBodies(folderID, threads)
	m.cache.ReplaceThreads(folderID, threads)
	m.LoadThreads()
}

// reconcileDrafts brings the local drafts table in line with what the
// server has in its Drafts folder. Three cases:
//
//   - server UID matches a local row's remote_uid → already tracked, leave
//     it alone (local edits take precedence over server state by design).
//   - server UID has no local match → adopt: create a local row with a
//     fresh stable id so the user can edit it here.
//   - local row's remote_uid is absent from the server list → the draft was
//     deleted elsewhere, prune locally.
//
// This runs inside SyncThreads on the Drafts folder and replaces the
// previous ReplaceThreads path for that folder.
func (m *State) reconcileDrafts(serverThreads []provider.Thread) {
	serverUIDs := make(map[string]provider.Message)
	for _, t := range serverThreads {
		for _, msg := range t.Messages {
			if msg.ID != "" {
				serverUIDs[msg.ID] = msg
			}
		}
	}

	localUIDs, err := m.cache.DraftRemoteUIDs()
	if err != nil {
		log.Printf("reconcileDrafts: DraftRemoteUIDs failed: %v", err)
		return
	}

	// Adopt unknowns + backfill empty bodies on already-tracked rows.
	// ListThreads returns headers only for Gmail's Drafts folder, so we
	// fetch the full message per UID to capture body + full recipient
	// lists — otherwise the list renders but the preview pane is empty.
	// The connection is already SELECTed on Drafts from the enclosing
	// ListThreads call.
	for uid, msg := range serverUIDs {
		// case 1: already tracked → only fetch if body is missing
		if existing, found, err := m.cache.FindDraftByRemoteUID(uid); err == nil && found {
			if existing.Body != "" {
				continue
			}
			full, gerr := m.imap.GetMessage(uid)
			if gerr != nil {
				log.Printf("reconcileDrafts: backfill GetMessage uid=%s failed: %v", uid, gerr)
				continue
			}
			body := full.TextBody
			if body == "" && full.HTMLBody != "" {
				body = full.HTMLBody
			}
			if err := m.cache.BackfillDraftContent(existing.ThreadID,
				addrsToString(full.To), addrsToString(full.CC), addrsToString(full.BCC),
				full.Subject, body, full.Date); err != nil {
				log.Printf("reconcileDrafts: backfill failed thread=%s: %v", existing.ThreadID, err)
				continue
			}
			log.Printf("reconcileDrafts: backfilled thread=%s from uid=%s bodyLen=%d", existing.ThreadID, uid, len(body))
			continue
		}
		// case 2: unknown → adopt with a fresh stable id
		newID, err := cache.NewDraftID()
		if err != nil {
			log.Printf("reconcileDrafts: NewDraftID failed: %v", err)
			continue
		}
		full, gerr := m.imap.GetMessage(uid)
		if gerr != nil {
			log.Printf("reconcileDrafts: GetMessage uid=%s failed: %v (adopting with headers only)", uid, gerr)
		} else {
			msg = full
		}
		body := msg.TextBody
		if body == "" && msg.HTMLBody != "" {
			body = msg.HTMLBody
		}
		_ = m.cache.SeedDraft(cache.Draft{
			ThreadID:  newID,
			To:        addrsToString(msg.To),
			Cc:        addrsToString(msg.CC),
			Bcc:       addrsToString(msg.BCC),
			Subject:   msg.Subject,
			Body:      body,
			RemoteUID: uid,
			UpdatedAt: msg.Date,
		})
		log.Printf("reconcileDrafts: adopted server uid=%s as thread=%s subject=%q bodyLen=%d date=%s", uid, newID, msg.Subject, len(body), msg.Date.Format(time.RFC3339))
	}

	// prune: any local row whose remote copy is gone. "" remote_uid means
	// never-synced local draft — don't touch those.
	for uid := range localUIDs {
		if _, ok := serverUIDs[uid]; !ok {
			if err := m.cache.DeleteDraftByRemoteUID(uid); err == nil {
				log.Printf("reconcileDrafts: pruned local row for gone uid=%s", uid)
			}
		}
	}
}

func addrsToString(addrs []provider.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

// preserveCachedBodies copies previously-fetched message bodies from the
// cache onto fresh threads so they aren't lost on re-sync.
func (m *State) preserveCachedBodies(folder string, threads []provider.Thread) {
	old, err := m.cache.GetThreads(folder, 100)
	if err != nil || len(old) == 0 {
		return
	}

	// build lookup: message ID → cached body
	type body struct{ text, html string }
	bodies := make(map[string]body)
	attachments := make(map[string][]provider.Attachment)
	for _, t := range old {
		for _, msg := range t.Messages {
			if msg.MessageID != "" && (msg.TextBody != "" || msg.HTMLBody != "") {
				bodies[msg.MessageID] = body{msg.TextBody, msg.HTMLBody}
			}
			if msg.MessageID != "" && len(msg.Attachments) > 0 {
				attachments[msg.MessageID] = msg.Attachments
			}
		}
	}

	for i := range threads {
		for j := range threads[i].Messages {
			msg := &threads[i].Messages[j]
			if msg.TextBody == "" && msg.HTMLBody == "" && msg.MessageID != "" {
				if b, ok := bodies[msg.MessageID]; ok {
					msg.TextBody = b.text
					msg.HTMLBody = b.html
				}
			}
			if len(msg.Attachments) == 0 && msg.MessageID != "" {
				if a, ok := attachments[msg.MessageID]; ok {
					msg.Attachments = a
				}
			}
		}
	}
}

func (m *State) mergeWithSentMessages(threads []provider.Thread) []provider.Thread {
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

func (m *State) BuildThreadDisplay() {
	m.displayMu.Lock()
	defer m.displayMu.Unlock()
	m.threadRows = nil
	lastGroup := ""
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
		starred := false
		for _, msg := range t.Messages {
			if msg.Starred {
				starred = true
				break
			}
		}
		hasDraft := false
		// In the Drafts folder every row is already a draft; the inline
		// "draft" tag is a reply-draft indicator for other folders only.
		if m.cache != nil && m.ActiveFolderCanonical() != "Drafts" {
			hasDraft = m.cache.HasDraft(t.ID)
		}
		chips, overflow := threadAttachmentChips(t.Messages, 2)
		group := dateGroup(t.Date)
		groupLabel := ""
		if group != lastGroup {
			groupLabel = group
			lastGroup = group
		}
		m.threadRows = append(m.threadRows, ThreadRow{
			ThreadIdx:             i,
			MsgIdx:                -1,
			Label:                 t.Subject,
			Sender:                sender,
			Date:                  relativeTime(t.Date),
			GroupLabel:            groupLabel,
			HasGroup:              groupLabel != "",
			Unread:                t.Unread > 0,
			Starred:               starred,
			HasDraft:              hasDraft,
			Attachments:           chips,
			HasAttachments:        len(chips) > 0,
			AttachmentOverflow:    overflow,
			HasAttachmentOverflow: overflow != "",
		})
	}
	m.applySelected()
}

func (m *State) ToggleThread(sel int) {
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
			chips, overflow := threadAttachmentChips([]provider.Message{msg}, 2)
			msgRows = append(msgRows, ThreadRow{
				ThreadIdx:             row.ThreadIdx,
				MsgIdx:                j,
				Label:                 name,
				Date:                  relativeTime(msg.Date),
				Unread:                !msg.Read,
				Starred:               msg.Starred,
				Attachments:           chips,
				HasAttachments:        len(chips) > 0,
				AttachmentOverflow:    overflow,
				HasAttachmentOverflow: overflow != "",
				Grouped:               true,
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

func (m *State) SelectedMessage(sel int) *provider.Message {
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

func (m *State) SelectedThread(sel int) *provider.Thread {
	if sel < 0 || sel >= len(m.threadRows) {
		return nil
	}
	row := m.threadRows[sel]
	if row.ThreadIdx < len(m.threads) {
		return &m.threads[row.ThreadIdx]
	}
	return nil
}

func (m *State) LastMessage(sel int) *provider.Message {
	t := m.SelectedThread(sel)
	if t == nil || len(t.Messages) == 0 {
		return nil
	}
	msg := t.Messages[len(t.Messages)-1]
	return &msg
}

// preview

// LoadConversation populates the conversation slice from the selected thread.
// Bodies available in cache are included immediately. Missing bodies are
// fetched from IMAP in the background; onUpdate is called when they arrive.
//
// Design: the expensive work (DB lookup via resolveCachedBody, HTML
// rendering via renderBody) runs WITHOUT the lock on a local slice, so
// the UI thread is never blocked waiting for another caller's build.
// Only the epoch bump and the final slice swap are under the mutex. The
// convEpoch counter lets late-returning async fetch goroutines notice
// their render was superseded and skip the writeback.
func (m *State) LoadConversation(sel int, onUpdate func()) {
	t := m.SelectedThread(sel)

	// Claim an epoch up front so any later-returning async goroutine
	// from our own call can identify itself as "still current".
	m.convMu.Lock()
	m.convEpoch++
	epoch := m.convEpoch
	m.convMu.Unlock()

	if t == nil || len(t.Messages) == 0 {
		m.convMu.Lock()
		if m.convEpoch == epoch {
			m.conversation = m.conversation[:0]
		}
		m.convMu.Unlock()
		return
	}

	// Build the new conversation slice into a LOCAL variable — no lock
	// held during resolveCachedBody / renderBody. Other goroutines can
	// read or rebuild m.conversation freely while we prepare this one.
	local := make([]ConversationMessage, 0, len(t.Messages))
	var needFetch []int
	var calendarTargets []calendarAttachmentTarget
	for i := range t.Messages {
		msg := t.Messages[i]
		if msg.TextBody == "" && msg.HTMLBody == "" {
			msg = m.resolveCachedBody(msg)
			if msg.TextBody != t.Messages[i].TextBody || msg.HTMLBody != t.Messages[i].HTMLBody {
				t.Messages[i].TextBody = msg.TextBody
				t.Messages[i].HTMLBody = msg.HTMLBody
			}
		}

		from := msg.From.Email
		if msg.From.Name != "" {
			from = msg.From.Name
		}
		isMe := m.email != "" && strings.EqualFold(msg.From.Email, m.email)
		if isMe {
			from = "You"
		}

		doc := m.renderDocument(msg)
		local = append(local, ConversationMessage{
			Sender:         from,
			Date:           msg.Date.Format("2 Jan 15:04"),
			Attachments:    m.attachmentRows(msg),
			HasAttachments: len(msg.Attachments) > 0,
			Body:           doc.PlainText(),
			BodySpans:      doc.GlyphSpansWithLinks(m.OpenLink),
			Segments:       doc.Segments(),
			IsMe:           isMe,
		})

		if msg.TextBody == "" && msg.HTMLBody == "" {
			needFetch = append(needFetch, i)
		} else if needsCalendarAttachmentParts(msg) {
			needFetch = append(needFetch, i)
		}
	}
	calendarTargets = calendarAttachmentTargets(local)

	// Publish the built slice — tiny critical section, just a pointer
	// swap. If a newer LoadConversation has already started, discard
	// our work; theirs wins.
	m.convMu.Lock()
	superseded := m.convEpoch != epoch
	if !superseded {
		m.conversation = local
	}
	m.convMu.Unlock()
	if superseded {
		return
	}
	m.enrichCalendarAttachments(epoch, calendarTargets, onUpdate)

	// async fetch missing bodies from IMAP.
	//
	// Drafts folder is a projection of the local drafts table — the
	// thread.Messages[i].ID is a stable draft id ("draft-<hex>"), not an
	// IMAP UID, so GetMessage would misparse it as UID 0 and fetch
	// nothing. Bodies for drafts are filled by reconcile's backfill path.
	// Also avoids corrupting the threads table via the PutThread call
	// below with a synthesised drafts projection.
	if len(needFetch) > 0 && (m.imap != nil || m.messageFetcher != nil) && onUpdate != nil && m.ActiveFolderCanonical() != "Drafts" {
		thread := t
		folder := m.ActiveFolderID()
		targets := append([]int(nil), needFetch...)
		go func() {
			if m.messageFetcher == nil && folder != "" {
				if err := m.imap.SelectFolder(folder); err != nil {
					log.Printf("conversation: failed to select %s: %v", folder, err)
					return
				}
			}
			changed := false
			for _, i := range targets {
				full, err := m.fetchMessage(thread.Messages[i].ID)
				if err != nil {
					log.Printf("conversation: fetch message %s: %v", thread.Messages[i].ID, err)
					continue
				}
				if full.TextBody != "" || full.HTMLBody != "" {
					thread.Messages[i].TextBody = full.TextBody
					thread.Messages[i].HTMLBody = full.HTMLBody
				}
				if len(full.Attachments) > 0 {
					thread.Messages[i].Attachments = full.Attachments
				}
				changed = true
			}
			if !changed {
				return
			}
			if m.cache != nil {
				m.cache.PutThread(*thread)
			}
			// Write back under the same lock, and only if our render is
			// still current. A later LoadConversation (folder switch,
			// different thread) bumps convEpoch; we bail without touching
			// the slice that belongs to someone else now.
			m.convMu.Lock()
			if m.convEpoch != epoch {
				m.convMu.Unlock()
				return
			}
			for _, i := range targets {
				if i >= len(m.conversation) {
					continue
				}
				doc := m.renderDocument(thread.Messages[i])
				m.conversation[i].Attachments = m.attachmentRows(thread.Messages[i])
				m.conversation[i].HasAttachments = len(thread.Messages[i].Attachments) > 0
				m.conversation[i].Body = doc.PlainText()
				m.conversation[i].BodySpans = doc.GlyphSpansWithLinks(m.OpenLink)
				m.conversation[i].Segments = doc.Segments()
			}
			calendarTargets := calendarAttachmentTargets(m.conversation)
			m.convMu.Unlock()
			onUpdate()
			m.enrichCalendarAttachments(epoch, calendarTargets, onUpdate)
		}()
	}
}

func (m *State) fetchMessage(id string) (provider.Message, error) {
	if m.messageFetcher != nil {
		return m.messageFetcher(id)
	}
	if m.imap == nil {
		return provider.Message{}, fmt.Errorf("not connected")
	}
	var lastErr error
	for _, folder := range m.fetchFolderCandidates(id) {
		if folder != "" {
			if err := m.imap.SelectFolder(folder); err != nil {
				lastErr = err
				continue
			}
		}
		msg, err := m.imap.GetMessage(id)
		if err == nil {
			return msg, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return provider.Message{}, lastErr
	}
	return provider.Message{}, fmt.Errorf("no folder candidates")
}

func needsCalendarAttachmentParts(msg provider.Message) bool {
	for _, attachment := range msg.Attachments {
		if isCalendarAttachment(attachment.Filename, attachment.ContentType) && len(attachment.Part) == 0 {
			return true
		}
	}
	return false
}

func (m *State) fetchFolderCandidates(threadID string) []string {
	var folders []string
	add := func(folder string) {
		if folder == "" {
			return
		}
		for _, existing := range folders {
			if existing == folder {
				return
			}
		}
		folders = append(folders, folder)
	}

	add(m.ActiveFolderID())
	if m.cache != nil && threadID != "" {
		for _, label := range m.cache.ThreadLabels(threadID) {
			add(label)
		}
	}
	add(m.FolderIDByDisplayName("Archive"))
	return folders
}

type calendarAttachmentTarget struct {
	ThreadID        string
	MessageIndex    int
	AttachmentIndex int
	Row             AttachmentRow
}

func calendarAttachmentTargets(messages []ConversationMessage) []calendarAttachmentTarget {
	var targets []calendarAttachmentTarget
	for msgIdx := range messages {
		for attachmentIdx := range messages[msgIdx].Attachments {
			row := messages[msgIdx].Attachments[attachmentIdx]
			if !row.Calendar || row.MessageID == "" || len(row.Part) == 0 {
				continue
			}
			targets = append(targets, calendarAttachmentTarget{
				ThreadID:        row.ThreadID,
				MessageIndex:    msgIdx,
				AttachmentIndex: attachmentIdx,
				Row:             row,
			})
		}
	}
	return targets
}

func (m *State) enrichCalendarAttachments(epoch int64, targets []calendarAttachmentTarget, onUpdate func()) {
	if len(targets) == 0 || onUpdate == nil || (m.imap == nil && m.attachmentFetcher == nil) {
		return
	}
	go func() {
		changed := false
		for _, target := range targets {
			data, err := m.fetchAttachment(target.Row)
			if err != nil {
				log.Printf("calendar attachment: fetch %s: %v", target.Row.Filename, err)
				continue
			}
			summary, ok := parseCalendarSummary(string(data))
			if !ok {
				continue
			}

			m.convMu.Lock()
			if m.convEpoch != epoch {
				m.convMu.Unlock()
				return
			}
			if target.MessageIndex >= len(m.conversation) ||
				target.AttachmentIndex >= len(m.conversation[target.MessageIndex].Attachments) {
				m.convMu.Unlock()
				continue
			}
			row := &m.conversation[target.MessageIndex].Attachments[target.AttachmentIndex]
			row.CalendarTitle = summary.Title
			row.CalendarWhen = summary.When
			row.Display = m.attachmentDisplay(*row)
			m.convMu.Unlock()
			changed = true
		}
		if changed {
			onUpdate()
		}
	}()
}

func (m *State) resolveCachedBody(msg provider.Message) provider.Message {
	if m.cache == nil || msg.MessageID == "" {
		return msg
	}
	sent, err := m.cache.GetSentMessages(100)
	if err != nil {
		return msg
	}
	for _, s := range sent {
		if s.MessageID == msg.MessageID && (s.TextBody != "" || s.HTMLBody != "") {
			msg.TextBody = s.TextBody
			msg.HTMLBody = s.HTMLBody
			return msg
		}
	}
	return msg
}

// cacheMessageBody writes a fetched body back to the thread in memory and cache.
func (m *State) cacheMessageBody(msg provider.Message) {
	for i := range m.threads {
		for j := range m.threads[i].Messages {
			if m.threads[i].Messages[j].ID == msg.ID {
				m.threads[i].Messages[j].TextBody = msg.TextBody
				m.threads[i].Messages[j].HTMLBody = msg.HTMLBody
				if m.cache != nil {
					m.cache.PutThread(m.threads[i])
				}
				return
			}
		}
	}
}

func (m *State) renderBody(msg provider.Message) string {
	return m.renderDocument(msg).PlainText()
}

func (m *State) renderDocument(msg provider.Message) preview.Document {
	body := msg.TextBody
	if msg.HTMLBody != "" {
		return preview.ParseHTML(msg.HTMLBody, msg.TextBody)
	} else if strings.Contains(body, "<p>") || strings.Contains(body, "<br") || strings.Contains(body, "<div") {
		doc := preview.ParseHTML(body, "")
		if len(doc.Blocks) > 0 {
			return doc
		}
		body = preview.RenderHTML(body, "", 72)
	}
	return preview.ParseText(body)
}

func (m *State) LoadPreview(msg provider.Message, width int) {
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
		// fall back to IMAP fetch, cache the result
		if msg.TextBody == "" && msg.HTMLBody == "" && m.imap != nil {
			full, err := m.imap.GetMessage(msg.ID)
			if err == nil {
				msg = full
				m.cacheMessageBody(msg)
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

	for _, line := range wrapPreviewLines(m.renderDocument(msg).PlainText(), cols) {
		m.previewLines = append(m.previewLines, line)
	}
	m.previewText = strings.Join(m.previewLines, "\n")
}

func wrapPreviewLines(body string, cols int) []string {
	if cols <= 0 {
		cols = 72
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapPreviewLine(line, cols)...)
	}
	return out
}

func wrapPreviewLine(line string, cols int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}

	var out []string
	var current strings.Builder
	for _, word := range words {
		if current.Len() == 0 {
			current.WriteString(word)
			continue
		}
		if current.Len()+1+len(word) > cols {
			out = append(out, current.String())
			current.Reset()
			current.WriteString(word)
			continue
		}
		current.WriteByte(' ')
		current.WriteString(word)
	}
	out = append(out, current.String())
	return out
}

// actions — each returns an undo closure + description.

func (m *State) Archive(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	dest := m.FolderIDByDisplayName("Archive")
	if dest == "" {
		log.Println("archive: no archive folder found")
		return nil, "archive unavailable: no archive folder"
	}
	thread, folder := *t, m.ActiveFolderID()
	m.queueMoveCommands(t, folder, dest)
	m.cache.RemoveThreadFromLabel(t.ID, folder)
	m.cache.AddThreadToLabel(t.ID, dest)
	m.LoadThreads()
	m.BuildThreadDisplay()
	m.SetSelected(sel)

	return func() {
		m.cache.PutThread(thread)
		m.cache.RemoveThreadFromLabel(thread.ID, dest)
		m.cache.AddThreadToLabel(thread.ID, folder)
		m.queueMoveCommands(&thread, dest, folder)
		m.LoadThreads()
		m.BuildThreadDisplay()
		m.SetSelected(sel)
	}, fmt.Sprintf("archived '%s'", truncate(thread.Subject, 30))
}

func (m *State) Delete(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	dest := m.FolderIDByDisplayName("Trash")
	if dest == "" {
		log.Println("delete: no trash folder found")
		return nil, "delete unavailable: no trash folder"
	}
	thread, folder := *t, m.ActiveFolderID()
	m.queueMoveCommands(t, folder, dest)
	m.cache.RemoveThreadFromLabel(t.ID, folder)
	m.cache.AddThreadToLabel(t.ID, dest)
	m.LoadThreads()
	m.BuildThreadDisplay()
	m.SetSelected(sel)

	return func() {
		m.cache.PutThread(thread)
		m.cache.RemoveThreadFromLabel(thread.ID, dest)
		m.cache.AddThreadToLabel(thread.ID, folder)
		m.queueMoveCommands(&thread, dest, folder)
		m.LoadThreads()
		m.BuildThreadDisplay()
		m.SetSelected(sel)
	}, fmt.Sprintf("deleted '%s'", truncate(thread.Subject, 30))
}

func (m *State) queueMoveCommands(t *provider.Thread, source, dest string) []string {
	var ids []string
	for _, msg := range t.Messages {
		if msg.ID == "" {
			continue
		}
		params := map[string]string{
			"folder": dest,
			"source": source,
		}
		if msg.MessageID != "" {
			params["message_id"] = msg.MessageID
		}
		ids = append(ids, m.queueCommand("move", msg.ID, params))
	}
	if len(ids) == 0 && t.ID != "" {
		ids = append(ids, m.queueCommand("move", t.ID, map[string]string{
			"folder": dest,
			"source": source,
		}))
	}
	return ids
}

func (m *State) ToggleStar(sel int) (undo func(), desc string) {
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
	thread := *t
	m.cache.PutThread(*t)
	m.updateActiveFolderUnread()
	m.BuildThreadDisplay()
	m.SetSelected(sel)

	return func() {
		for _, id := range cmdIDs {
			m.cancelCommand(id)
		}
		for i := range thread.Messages {
			thread.Messages[i].Starred = before[i]
			t.Messages[i].Starred = before[i]
		}
		m.cache.PutThread(thread)
		m.updateActiveFolderUnread()
		m.BuildThreadDisplay()
		m.SetSelected(sel)
	}, "toggled star"
}

func (m *State) ToggleRead(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil {
		return nil, ""
	}
	beforeUnread := t.Unread
	beforeRead := make([]bool, len(t.Messages))
	markRead := t.Unread > 0

	var cmdIDs []string
	for i, msg := range t.Messages {
		beforeRead[i] = msg.Read
		if markRead && !msg.Read {
			cmdIDs = append(cmdIDs, m.queueCommand("mark_read", msg.ID, nil))
		} else if !markRead {
			cmdIDs = append(cmdIDs, m.queueCommand("mark_unread", msg.ID, nil))
		}
		t.Messages[i].Read = markRead
	}
	if markRead {
		t.Unread = 0
	} else {
		t.Unread = len(t.Messages)
	}
	thread := *t
	m.cache.PutThread(*t)
	m.updateActiveFolderUnread()
	m.BuildThreadDisplay()
	m.SetSelected(sel)

	desc = "marked read"
	if !markRead {
		desc = "marked unread"
	}
	return func() {
		for _, id := range cmdIDs {
			m.cancelCommand(id)
		}
		thread.Unread = beforeUnread
		t.Unread = beforeUnread
		for i := range thread.Messages {
			thread.Messages[i].Read = beforeRead[i]
			t.Messages[i].Read = beforeRead[i]
		}
		m.cache.PutThread(thread)
		m.updateActiveFolderUnread()
		m.BuildThreadDisplay()
		m.SetSelected(sel)
	}, desc
}

func (m *State) MarkRead(sel int) (undo func(), desc string) {
	t := m.SelectedThread(sel)
	if t == nil || t.Unread == 0 {
		return nil, ""
	}
	beforeUnread := t.Unread
	beforeRead := make([]bool, len(t.Messages))

	var cmdIDs []string
	for i, msg := range t.Messages {
		beforeRead[i] = msg.Read
		if !msg.Read {
			cmdIDs = append(cmdIDs, m.queueCommand("mark_read", msg.ID, nil))
		}
		t.Messages[i].Read = true
	}
	t.Unread = 0
	thread := *t
	m.cache.PutThread(*t)
	m.LoadThreads()
	m.BuildThreadDisplay()

	return func() {
		for _, id := range cmdIDs {
			m.cancelCommand(id)
		}
		thread.Unread = beforeUnread
		for i := range thread.Messages {
			thread.Messages[i].Read = beforeRead[i]
		}
		m.cache.PutThread(thread)
		m.LoadThreads()
		m.BuildThreadDisplay()
	}, "marked read"
}

func (m *State) queueCommand(action, targetID string, params map[string]string) string {
	id := fmt.Sprintf("%s-%s-%d", action, targetID, time.Now().UnixNano())
	if m.cache == nil {
		return id
	}
	if params == nil {
		params = map[string]string{}
	}
	switch action {
	case "mark_read", "mark_unread", "star", "unstar", "move":
		if _, ok := params["source"]; !ok {
			params["source"] = m.ActiveFolderID()
		}
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

func (m *State) cancelCommand(id string) {
	if m.cache != nil {
		m.cache.DeleteCommand(id)
	}
}

// commands

// draftToMessage builds a provider.Message from a cache.Draft for the IMAP
// SaveDraft path. Recipients are parsed from the comma-separated fields the
// compose UI stores; threading headers come from the thread the draft is
// attached to (if any), resolved at send time via replyMsg rather than here.
func draftToMessage(d cache.Draft) provider.Message {
	return provider.Message{
		To:       provider.ParseAddressList(d.To),
		CC:       provider.ParseAddressList(d.Cc),
		BCC:      provider.ParseAddressList(d.Bcc),
		Subject:  d.Subject,
		TextBody: d.Body,
	}
}

func (m *State) ProcessPendingCommands() {
	if m.cache == nil {
		return
	}
	cmds, err := m.cache.PendingCommands()
	if err != nil || len(cmds) == 0 {
		return
	}
	cmds = m.compactMoveCommands(cmds)
	if m.imap == nil || len(cmds) == 0 {
		return
	}
	folder := m.ActiveFolderID()
	// restore primary connection to the user's active folder after the loop.
	// sync_draft / delete_draft internally SELECT [Gmail]/Drafts, and we must
	// not leave the connection pointing there — subsequent UID-based ops
	// (MarkRead, Star, preview fetches) would otherwise target the wrong
	// folder and corrupt state.
	defer func() {
		if folder != "" {
			_ = m.imap.SelectFolder(folder)
		}
	}()
	for _, cmd := range cmds {
		source := cmd.Params["source"]
		if source == "" {
			source = folder
		}
		// before each UID-dependent op, ensure we're SELECTed on the folder
		// the UID is valid in. For user actions (mark_read etc.) that means
		// the active folder the command was queued against. sync_draft and
		// delete_draft manage their own SELECT.
		switch cmd.Action {
		case "mark_read", "mark_unread", "star", "unstar", "move":
			if source != "" {
				_ = m.imap.SelectFolder(source)
			}
		}
		var cmdErr error
		dest := ""
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
			if f, ok := cmd.Params["folder"]; ok {
				dest = f
				cmdErr = m.imap.MoveMessage(source, f, cmd.TargetID, cmd.Params["message_id"])
			}
		case "sync_draft":
			draftsFolder := m.FolderIDByDisplayName("Drafts")
			if draftsFolder == "" {
				cmdErr = fmt.Errorf("no drafts folder")
				log.Printf("sync_draft: no Drafts folder resolved (folders loaded: %d)", len(m.folders))
				break
			}
			d, found, gerr := m.cache.GetDraft(cmd.TargetID)
			if gerr != nil {
				cmdErr = gerr
				break
			}
			if !found {
				log.Printf("sync_draft: draft %q vanished before processing", cmd.TargetID)
				break
			}
			msg := draftToMessage(d)
			log.Printf("sync_draft: pushing thread=%q prevUID=%q subject=%q", d.ThreadID, d.RemoteUID, d.Subject)
			newUID, serr := m.imap.SaveDraft(draftsFolder, msg, d.RemoteUID)
			if serr != nil {
				cmdErr = serr
				log.Printf("sync_draft: failed: %v", serr)
				break
			}
			log.Printf("sync_draft: ok thread=%q newUID=%s", d.ThreadID, newUID)
			cmdErr = m.cache.UpdateDraftRemoteUID(d.ThreadID, newUID)
		case "delete_draft":
			draftsFolder := m.FolderIDByDisplayName("Drafts")
			if draftsFolder == "" {
				cmdErr = fmt.Errorf("no drafts folder")
				break
			}
			log.Printf("delete_draft: expunging UID=%s", cmd.TargetID)
			cmdErr = m.imap.DeleteDraft(draftsFolder, cmd.TargetID)
			if cmdErr != nil {
				log.Printf("delete_draft: failed: %v", cmdErr)
			}
		}
		result := "ok"
		errMsg := ""
		if cmdErr != nil {
			result = "failed"
			errMsg = cmdErr.Error()
			m.cache.UpdateCommandStatus(cmd.ID, "failed", errMsg)
		} else {
			m.cache.UpdateCommandStatus(cmd.ID, "synced", "")
		}
		m.cache.LogWrite(cmd.Action, cmd.TargetID, folder, dest, result, errMsg)
	}
	m.cache.ClearSyncedCommands()
}

func (m *State) compactMoveCommands(cmds []cache.Command) []cache.Command {
	type moveKey struct {
		messageID string
		targetID  string
	}

	alive := make([]bool, len(cmds))
	for i := range alive {
		alive[i] = true
	}
	open := make(map[moveKey]int)

	for i, cmd := range cmds {
		if cmd.Action != "move" {
			continue
		}
		key := moveKey{
			messageID: cmd.Params["message_id"],
			targetID:  cmd.TargetID,
		}
		if key.messageID == "" {
			key.targetID = cmd.TargetID
		}

		if prevIdx, ok := open[key]; ok && alive[prevIdx] {
			prev := cmds[prevIdx]
			if prev.Params["source"] == cmd.Params["folder"] && prev.Params["folder"] == cmd.Params["source"] {
				alive[prevIdx] = false
				alive[i] = false
				if m.cache != nil {
					m.cache.DeleteCommands(prev.ID, cmd.ID)
				}
				delete(open, key)
				continue
			}
		}
		open[key] = i
	}

	out := make([]cache.Command, 0, len(cmds))
	for i, cmd := range cmds {
		if alive[i] {
			out = append(out, cmd)
		}
	}
	return out
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

// FolderIDByDisplayName returns the raw IMAP folder ID for a canonical display
// name (e.g. "Trash" → "[Google Mail]/Bin", "Archive" → "[Google Mail]/All
// Mail"). Some accounts expose duplicate canonical folders — notably Gmail
// accounts have both "[Gmail]/Drafts" and "[Google Mail]/Drafts" — so we
// pick the one with the most messages, matching BuildFolderDisplay's
// dedup rule. Ties fall to the first one encountered.
func (m *State) FolderIDByDisplayName(display string) string {
	var bestID string
	bestTotal := -1
	for _, f := range m.folders {
		if canonicalDisplayName(f.ID) != display {
			continue
		}
		if f.Total > bestTotal {
			bestTotal = f.Total
			bestID = f.ID
		}
	}
	return bestID
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

func dateGroup(t time.Time) string {
	now := time.Now()
	year, month, day := now.Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	d := t.In(now.Location())
	date := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, now.Location())

	switch {
	case !date.Before(today):
		return "TODAY"
	case date.Equal(today.AddDate(0, 0, -1)):
		return "YESTERDAY"
	case date.After(today.AddDate(0, 0, -7)):
		return "THIS WEEK"
	default:
		return "EARLIER"
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
type ConversationMessage struct {
	Sender         string
	Date           string
	Attachments    []AttachmentRow
	HasAttachments bool
	Body           string
	BodySpans      []glyph.Span
	Segments       []preview.Segment
	IsMe           bool
}

type AttachmentRow struct {
	Icon          string
	Filename      string
	ContentType   string
	Size          int64
	Metadata      string
	Calendar      bool
	CalendarTitle string
	CalendarWhen  string
	ThreadID      string
	MessageID     string
	Part          []int
	Encoding      string
	Display       []glyph.Span
}

type AttachmentChip struct {
	Icon     string
	Filename string
	FillName string
}

func (m *State) attachmentRows(msg provider.Message) []AttachmentRow {
	rows := make([]AttachmentRow, 0, len(msg.Attachments))
	for _, a := range msg.Attachments {
		name := a.Filename
		if name == "" {
			name = "attachment"
		}
		row := AttachmentRow{
			Icon:        provider.AttachmentIcon(name, a.ContentType),
			Filename:    name,
			ContentType: a.ContentType,
			Size:        a.Size,
			Metadata:    attachmentMetadata(name, a.ContentType, a.Size),
			Calendar:    isCalendarAttachment(name, a.ContentType),
			ThreadID:    msg.ThreadID,
			MessageID:   msg.ID,
			Part:        append([]int(nil), a.Part...),
			Encoding:    a.Encoding,
		}
		row.Display = m.attachmentDisplay(row)
		rows = append(rows, row)
	}
	return rows
}

func (m *State) attachmentDisplay(row AttachmentRow) []glyph.Span {
	if row.Calendar {
		title := row.CalendarTitle
		if title == "" {
			title = "calendar invite"
		}
		secondary := row.CalendarWhen
		if secondary == "" && row.Filename != "" && row.Filename != "attachment" {
			secondary = row.Filename
		}
		spans := []glyph.Span{
			{Text: row.Icon},
			{Text: " "},
			{Text: title, Style: glyph.Style{Attr: glyph.AttrBold}, OnSelect: func() { m.OpenAttachment(row) }},
		}
		if secondary != "" {
			spans = append(spans,
				glyph.Span{Text: "  "},
				glyph.Span{Text: secondary, Style: glyph.Style{Attr: glyph.AttrDim}},
			)
		}
		return spans
	}

	spans := []glyph.Span{
		{Text: row.Icon},
		{Text: " "},
		{Text: row.Filename, Style: glyph.Style{Attr: glyph.AttrBold}, OnSelect: func() { m.OpenAttachment(row) }},
	}
	if row.Metadata != "" {
		spans = append(spans,
			glyph.Span{Text: "  "},
			glyph.Span{Text: row.Metadata, Style: glyph.Style{Attr: glyph.AttrDim}},
		)
	}
	return spans
}

func isCalendarAttachment(filename, contentType string) bool {
	name := strings.ToLower(filename)
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "text/calendar") || strings.HasSuffix(name, ".ics")
}

func attachmentMetadata(filename, contentType string, size int64) string {
	parts := make([]string, 0, 2)
	if kind := attachmentKind(filename, contentType); kind != "" {
		parts = append(parts, kind)
	}
	if formatted := attachmentSize(size); formatted != "" {
		parts = append(parts, formatted)
	}
	return strings.Join(parts, " · ")
}

func attachmentKind(filename, contentType string) string {
	if dot := strings.LastIndex(filename, "."); dot >= 0 && dot < len(filename)-1 {
		return strings.ToLower(filename[dot+1:])
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if slash := strings.LastIndex(contentType, "/"); slash >= 0 && slash < len(contentType)-1 {
		return strings.TrimPrefix(contentType[slash+1:], "x-")
	}
	return contentType
}

func attachmentSize(size int64) string {
	if size <= 0 {
		return ""
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	if size < unit*unit {
		return fmt.Sprintf("%d KB", (size+unit/2)/unit)
	}
	if size < unit*unit*unit {
		return formatAttachmentUnit(float64(size)/(unit*unit), "MB")
	}
	return formatAttachmentUnit(float64(size)/(unit*unit*unit), "GB")
}

func formatAttachmentUnit(value float64, suffix string) string {
	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, suffix)
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

type ThreadRow struct {
	ThreadIdx             int
	MsgIdx                int // -1 for thread header, >= 0 for message
	Label                 string
	Sender                string
	Date                  string
	GroupLabel            string
	HasGroup              bool
	Unread                bool
	Starred               bool
	HasDraft              bool
	Attachments           []AttachmentChip
	HasAttachments        bool
	AttachmentOverflow    string
	HasAttachmentOverflow bool
	Expanded              bool
	Selected              bool
	Grouped               bool
}

func threadAttachmentChips(messages []provider.Message, limit int) ([]AttachmentChip, string) {
	if limit <= 0 {
		return nil, ""
	}
	total := 0
	chips := make([]AttachmentChip, 0, limit)
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		for _, attachment := range msg.Attachments {
			total++
			if len(chips) >= limit {
				continue
			}
			name := attachment.Filename
			if name == "" {
				name = "attachment"
			}
			chips = append(chips, AttachmentChip{
				Icon:     provider.AttachmentIcon(name, attachment.ContentType),
				Filename: truncate(name, 18),
				FillName: name,
			})
		}
	}
	if total > len(chips) {
		return chips, fmt.Sprintf("+%d", total-len(chips))
	}
	return chips, ""
}

func (m *State) SetSelected(sel int) {
	m.selected = m.ClampSelection(sel)
	m.applySelected()
}

func (m *State) applySelected() {
	sel := m.ClampSelection(m.selected)
	for i := range m.threadRows {
		m.threadRows[i].Selected = i == sel
	}
}

func (m *State) ClampSelection(sel int) int {
	if len(m.threadRows) == 0 {
		return 0
	}
	if sel >= len(m.threadRows) {
		return len(m.threadRows) - 1
	}
	if sel < 0 {
		return 0
	}
	return sel
}
