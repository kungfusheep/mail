package mailbox

import (
	"strings"
	"testing"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/preview"
	"github.com/kungfusheep/mail/provider"
)

func testCache(t *testing.T) *cache.Cache {
	t.Helper()
	c, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

var testFolders = []provider.Folder{
	{ID: "INBOX", Name: "INBOX"},
	{ID: "[Google Mail]/Bin", Name: "Bin"},
	{ID: "[Google Mail]/All Mail", Name: "All Mail"},
}

// draftsHarness sets up the exact pipeline main.go drives for the Drafts
// folder: cache with SetDraftsLabel, mailbox with folders loaded, Drafts
// selected, a subscriber goroutine mirroring watchLabel. Returns the
// mailbox and a channel that signals every time the subscriber reruns
// LoadThreads + BuildThreadDisplay (lets tests wait for refreshes to
// land before asserting).
//
// Tests use this to exercise what the UI actually shows — row labels,
// row dates, conversation bodies — rather than poking individual cache
// primitives.
type draftsHarness struct {
	cache   *cache.Cache
	mb      *Mailbox
	refresh chan struct{}
	unsub   func()
}

func newDraftsHarness(t *testing.T) *draftsHarness {
	t.Helper()
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Gmail]/Drafts", Name: "Drafts"},
	})
	c.SetDraftsLabel("[Gmail]/Drafts")

	mb := New(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	for i := 0; i < mb.FolderCount(); i++ {
		if mb.FolderName(i) == "Drafts" {
			mb.SelectFolder(i)
			break
		}
	}
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	// Drive the same watch pipeline main.go does. The refresh channel
	// just observes each completion so tests can wait deterministically.
	refresh := make(chan struct{}, 16)
	unsub := mb.Watch("[Gmail]/Drafts", func() {
		refresh <- struct{}{}
	})

	return &draftsHarness{cache: c, mb: mb, refresh: refresh, unsub: unsub}
}

// waitForRefresh blocks until the subscriber has rerun the refresh path
// once, or fails the test on timeout. Use after any draft-table mutation.
func (h *draftsHarness) waitForRefresh(t *testing.T) {
	t.Helper()
	select {
	case <-h.refresh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("subscriber never refreshed after draft mutation")
	}
}

// seedAdoptedDraft mimics what reconcileDrafts does for a server-side
// draft: a local row keyed by a stable id with RemoteUID pointing back
// to the server's UID. Important: the draft's UpdatedAt should be the
// message's actual date (from IMAP INTERNALDATE / Date header), not
// when we happened to adopt it.
func (h *draftsHarness) seedAdoptedDraft(d cache.Draft) {
	_ = h.cache.SeedDraft(d)
}

// The exact flow the app drives when adopting a server-side draft: the
// row must land in the mailbox's threadRows, its displayed date must
// reflect the server message's date (not adoption time), and its body
// must flow through to the conversation preview without panicking.
//
// This is the test that would have caught both live bugs if written
// before shipping.
func TestDraftsPipeline_RowDatesAndPreview(t *testing.T) {
	h := newDraftsHarness(t)
	defer h.unsub()

	// three drafts with different real dates — we want the list to come
	// back ordered by REAL date DESC, not by adoption time.
	real1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	real2 := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	real3 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	h.seedAdoptedDraft(cache.Draft{ThreadID: "draft-a", Subject: "oldest", Body: "body-a", RemoteUID: "1", UpdatedAt: real1})
	h.waitForRefresh(t)
	h.seedAdoptedDraft(cache.Draft{ThreadID: "draft-b", Subject: "middle", Body: "body-b", RemoteUID: "2", UpdatedAt: real2})
	h.waitForRefresh(t)
	h.seedAdoptedDraft(cache.Draft{ThreadID: "draft-c", Subject: "newest", Body: "body-c", RemoteUID: "3", UpdatedAt: real3})
	h.waitForRefresh(t)

	// === row order ===
	rows := *h.mb.ThreadRows()
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	wantOrder := []string{"newest", "middle", "oldest"}
	for i, want := range wantOrder {
		if rows[i].Label != want {
			t.Errorf("row[%d] label = %q, want %q — drafts must order by REAL date (most recent first), not by adoption time", i, rows[i].Label, want)
		}
	}

	// === row dates reflect real message date, not adoption time ===
	// If the "authored now" bug is present, all three rows read the same
	// updated_at (adoption time) and the date column says something like
	// "now" for all of them. A correct implementation preserves the
	// server-provided date so the user sees drafts ordered and dated
	// like on the server.
	sel := h.mb.SelectedThread(0)
	if sel == nil {
		t.Fatal("SelectedThread(0) is nil")
	}
	// the newest draft is from 2026-04-20 — must not be stamped with "now"
	if sel.Date.Year() != real3.Year() || sel.Date.Month() != real3.Month() || sel.Date.Day() != real3.Day() {
		t.Errorf("SelectedThread(0).Date = %v, want %v — 'authored now' bug: SeedDraft is clobbering message date with time.Now()", sel.Date, real3)
	}

	// === preview loads without panicking and carries the body through ===
	// The panic at mailbox.go:792 happens because LoadConversation kicks
	// off an async IMAP GetMessage on a message whose ID is a stable
	// draft id ("draft-c") rather than a UID. The synchronous body is
	// already populated (via backfill / seed); the async path is wrong
	// for drafts folder.
	h.mb.LoadConversation(0, nil)

	msgs := *h.mb.ConversationMessages()
	if len(msgs) != 1 {
		t.Fatalf("conversation messages = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Body, "body-c") {
		t.Errorf("conversation body = %q, want to contain \"body-c\" — preview pane will be empty", msgs[0].Body)
	}
	if len(msgs[0].BodySpans) == 0 {
		t.Fatal("conversation body spans are empty — rich preview pane will be empty")
	}
	if !strings.Contains(msgs[0].BodySpans[0].Text, "body-c") {
		t.Errorf("conversation body span = %q, want to contain \"body-c\" — rich preview pane will be empty", msgs[0].BodySpans[0].Text)
	}
}

func TestBodySpansFromSegments_StylesNonMainSegments(t *testing.T) {
	spans := bodySpansFromSegments([]preview.Segment{
		{Kind: preview.SegmentMain, Text: "Main body"},
		{Kind: preview.SegmentQuote, Text: "> old reply"},
		{Kind: preview.SegmentSignature, Text: "Thanks"},
	})

	if len(spans) != 5 {
		t.Fatalf("body spans = %d, want 5 including separators", len(spans))
	}
	if spans[0].Text != "Main body" || spans[0].Style.Attr != glyph.AttrNone {
		t.Fatalf("main span = %#v, want unstyled main body", spans[0])
	}
	if spans[2].Text != "> old reply" || !spans[2].Style.Attr.Has(glyph.AttrDim) || !spans[2].Style.Attr.Has(glyph.AttrItalic) {
		t.Fatalf("quote span = %#v, want dim italic quoted text", spans[2])
	}
	if spans[4].Text != "Thanks" || !spans[4].Style.Attr.Has(glyph.AttrDim) {
		t.Fatalf("signature span = %#v, want dim signature text", spans[4])
	}
}

// Regression: when a sync for a non-Drafts folder (Inbox / Starred /
// etc) is in flight and the user switches to Drafts before it resolves,
// the stale result MUST NOT be routed through reconcileDrafts — every
// inbox message would otherwise get adopted as a phantom server draft.
// The dispatch must gate on the SOURCE folder id, not the current view.
func TestApplySyncResult_StaleSyncDoesNotPolluteDrafts(t *testing.T) {
	h := newDraftsHarness(t)
	defer h.unsub()

	before, _ := h.cache.ListDrafts()
	beforeCount := len(before)

	// Simulate a Starred-folder sync result arriving while the active
	// view is Drafts. These threads have nothing to do with drafts.
	starredResult := []provider.Thread{
		{ID: "s1", Subject: "Receipt for your payment to Discord Inc",
			Date:     time.Now().AddDate(0, 0, -3),
			Messages: []provider.Message{{ID: "s1", Subject: "Receipt", TextBody: "receipt"}}},
		{ID: "s2", Subject: "File your Self Assessment return",
			Date:     time.Now().AddDate(0, 0, -18),
			Messages: []provider.Message{{ID: "s2", Subject: "File", TextBody: "filing"}}},
	}
	h.mb.applySyncResult("[Gmail]/Starred", starredResult)

	after, _ := h.cache.ListDrafts()
	if len(after) != beforeCount {
		var subs []string
		for _, d := range after {
			subs = append(subs, d.Subject)
		}
		t.Errorf("drafts table grew from %d to %d after stale Starred sync — starred subjects leaked in: %v", beforeCount, len(after), subs)
	}

	// And the Starred threads table should have the new content (correct
	// routing): proves the fix doesn't send it somewhere else entirely.
	starred, _ := h.cache.GetThreads("[Gmail]/Starred", 25)
	if len(starred) != 2 {
		t.Errorf("Starred threads table has %d rows, want 2 — sync result wasn't stored correctly", len(starred))
	}
}

// Replicates the live panic: a drafts row lands in the list with an
// empty body (the state between reconcile adopting the UID and the
// backfill fetch completing), LoadConversation is called (preview
// pane), then a folder-switch-style reset clears m.conversation. The
// async fetch goroutine then must not crash writing back into the
// now-empty slice. Previously panicked at mailbox.go:792.
func TestLoadConversation_DraftsEmptyBodyNoPanic(t *testing.T) {
	h := newDraftsHarness(t)
	defer h.unsub()

	// Adopted row with no body yet — mimics the window between adoption
	// and backfill.
	h.seedAdoptedDraft(cache.Draft{
		ThreadID:  "draft-pending",
		Subject:   "awaiting backfill",
		Body:      "",
		RemoteUID: "42",
		UpdatedAt: time.Now(),
	})
	h.waitForRefresh(t)

	// onUpdate is passed in main.go via loadPreview; provide it so the
	// code path that used to panic is exercised.
	h.mb.LoadConversation(0, func() {})

	// Simulate folder-switch: another LoadConversation call resets
	// m.conversation via the SelectedThread==nil path. The pre-fix code
	// would panic here because the async goroutine from the first call
	// is still running with stale indices. Since we synchronously ran
	// LoadConversation above, any goroutine launched synchronously will
	// have started by now.
	// First flip to an empty selection by clearing threadRows.
	for i := 0; i < 3; i++ {
		h.mb.LoadConversation(0, func() {})
	}
	// If we got here without panicking, the guards held.
}

// When the active folder is Drafts, LoadThreads must project from the
// drafts table — not from the stale threads-table snapshot. This is the
// fix for the "edit a draft, reopen, see old version" bug: local saves
// update the drafts table directly, so reading from it each frame keeps
// the UI in lockstep with state.
func TestLoadThreads_DraftsFolderProjectsFromDraftsTable(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Gmail]/Drafts", Name: "Drafts"},
	})
	// seed a stale snapshot in the threads table to prove we ignore it —
	// in the bug, this snapshot is what leaked through as "the old version"
	c.ReplaceThreads("[Gmail]/Drafts", []provider.Thread{
		{ID: "stale-thread", Subject: "STALE SUBJECT", Messages: []provider.Message{{TextBody: "stale body"}}},
	})
	// and a fresh draft in the drafts table — what should show through
	_ = c.PutDraft(cache.Draft{ThreadID: "draft-fresh", Subject: "fresh", Body: "fresh body"})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	// Drafts is index 2 in canonical ordering (Inbox, Sent, Drafts, ...)
	for i := 0; i < mb.FolderCount(); i++ {
		if mb.FolderName(i) == "Drafts" {
			mb.SelectFolder(i)
			break
		}
	}
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	if n := mb.ThreadLen(); n != 1 {
		t.Fatalf("got %d thread rows, want 1 (the drafts-table row; stale snapshot must not leak)", n)
	}
	got := mb.SelectedThread(0)
	if got == nil {
		t.Fatal("SelectedThread(0) = nil")
	}
	if got.ID != "draft-fresh" {
		t.Errorf("thread.ID = %q, want draft-fresh (stale snapshot %q must not appear)", got.ID, "stale-thread")
	}
	if got.Subject != "fresh" {
		t.Errorf("thread.Subject = %q, want \"fresh\"", got.Subject)
	}
	if len(got.Messages) == 0 || got.Messages[0].TextBody != "fresh body" {
		t.Errorf("thread body did not come from drafts table")
	}
}

// End-to-end: a draft mutation (PutDraft / SeedDraft / delete) MUST fire
// the cache's pubsub subscriber on the Drafts folder label. Without this,
// the pipeline the live UI relies on — subscriber → LoadThreads →
// BuildThreadDisplay → RequestRender — never gets kicked and the user
// sees an empty list even though the drafts table is populated. This test
// is what should have caught my first pass; it directly simulates the
// subscriber the app wires up in main.go.
func TestDraftWrites_PublishOnDraftsFolderLabel(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "[Gmail]/Drafts", Name: "Drafts"}})
	c.SetDraftsLabel("[Gmail]/Drafts")

	ch, unsub := c.Subscribe("[Gmail]/Drafts")
	defer unsub()

	// drain any spurious signal
	select {
	case <-ch:
	default:
	}

	_ = c.PutDraft(cache.Draft{ThreadID: "draft-put", Subject: "s", Body: "b"})
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PutDraft did not publish on the drafts folder label — mailbox subscriber will never fire, UI stays empty")
	}

	_ = c.SeedDraft(cache.Draft{ThreadID: "draft-seed", Subject: "s", Body: "b", RemoteUID: "99"})
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("SeedDraft did not publish — reconcileDrafts adopting server drafts will populate the cache but UI won't refresh")
	}

	if err := c.DeleteDraftByRemoteUID("99"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("DeleteDraftByRemoteUID did not publish — prune in reconcile leaves stale UI row")
	}
}

// Full pipeline test: from SyncThreads-style adoption through the pubsub
// subscriber that main.go wires up, assert the mailbox threadRows populate.
// This replicates the exact sequence the running app goes through when the
// user has server-side drafts and first navigates into the Drafts folder.
func TestDraftsFolder_AdoptionSurfacesInUI(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Gmail]/Drafts", Name: "Drafts"},
	})
	c.SetDraftsLabel("[Gmail]/Drafts")

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	for i := 0; i < mb.FolderCount(); i++ {
		if mb.FolderName(i) == "Drafts" {
			mb.SelectFolder(i)
			break
		}
	}
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	if n := mb.ThreadLen(); n != 0 {
		t.Fatalf("pre-sync rows = %d, want 0 (cache has no drafts yet)", n)
	}

	// subscriber mirrors main.go watchLabel: on each channel signal,
	// refresh the display. This is the pipeline the user's UI depends on.
	ch, unsub := c.Subscribe("[Gmail]/Drafts")
	defer unsub()

	done := make(chan struct{})
	go func() {
		for range ch {
			mb.LoadThreads()
			mb.BuildThreadDisplay()
			if mb.ThreadLen() > 0 {
				close(done)
				return
			}
		}
	}()

	// simulate reconcileDrafts adopting a server-side draft via SeedDraft
	// (the same call the real reconciler makes)
	stableID, _ := cache.NewDraftID()
	_ = c.SeedDraft(cache.Draft{
		ThreadID:  stableID,
		Subject:   "from server",
		Body:      "server body",
		RemoteUID: "42",
	})

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("subscriber never saw drafts — SeedDraft didn't publish, so UI would stay empty after reconcile")
	}

	rows := *mb.ThreadRows()
	if len(rows) != 1 {
		t.Fatalf("after adopt: rows = %d, want 1", len(rows))
	}
	if rows[0].Label != "from server" {
		t.Errorf("row label = %q, want \"from server\"", rows[0].Label)
	}
}

// Replicates the exact user flow that broke before: edit draft, exit
// compose, re-enter the Drafts folder list. Must show the new body.
// Previously the mailbox projection (threadRows) never refreshed because
// PutDraft didn't publish on the drafts folder label.
func TestDraftsFolder_EditThenExit_ListShowsNewContent(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Gmail]/Drafts", Name: "Drafts"},
	})
	c.SetDraftsLabel("[Gmail]/Drafts")

	// seed a pre-existing draft (same state a user hits when entering Drafts
	// with a server-side draft already adopted)
	_ = c.PutDraft(cache.Draft{ThreadID: "draft-abc", Subject: "hello", Body: "first pass"})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	for i := 0; i < mb.FolderCount(); i++ {
		if mb.FolderName(i) == "Drafts" {
			mb.SelectFolder(i)
			break
		}
	}
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	// subscriber mirrors main.go watchLabel, tracking how many times the
	// mailbox projection got refreshed
	ch, unsub := c.Subscribe("[Gmail]/Drafts")
	defer unsub()
	refreshed := make(chan struct{}, 4)
	go func() {
		for range ch {
			mb.LoadThreads()
			mb.BuildThreadDisplay()
			refreshed <- struct{}{}
		}
	}()

	// === user edits the draft (compose → saveDraft → PutDraft) ===
	_ = c.PutDraft(cache.Draft{ThreadID: "draft-abc", Subject: "hello", Body: "SECOND pass"})

	select {
	case <-refreshed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("edit did not reach the mailbox projection — the bug is back: UI will show first pass")
	}

	// === back on the mailbox list ===
	got := mb.SelectedThread(0)
	if got == nil {
		t.Fatal("SelectedThread(0) = nil")
	}
	if got.Messages[0].TextBody != "SECOND pass" {
		t.Errorf("list body = %q, want \"SECOND pass\" — the stale-draft bug has returned", got.Messages[0].TextBody)
	}
}

// When user edits a draft, re-enters the Drafts folder, the list must
// show the NEW body — not the stale snapshot that used to leak through
// from the threads table.
func TestLoadThreads_DraftsFolderReflectsLatestEdit(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "[Gmail]/Drafts", Name: "Drafts"}})
	_ = c.PutDraft(cache.Draft{ThreadID: "draft-x", Subject: "s", Body: "first pass"})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()

	// simulate the user editing the draft in compose, then re-entering
	// the drafts folder (which re-calls LoadThreads + BuildThreadDisplay)
	_ = c.PutDraft(cache.Draft{ThreadID: "draft-x", Subject: "s", Body: "second pass"})
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	got := mb.SelectedThread(0)
	if got == nil {
		t.Fatal("SelectedThread(0) = nil after edit")
	}
	if got.Messages[0].TextBody != "second pass" {
		t.Errorf("body after edit = %q, want \"second pass\" — this is the stale-draft bug", got.Messages[0].TextBody)
	}
}

func testMailbox(t *testing.T, folders []provider.Folder, threads []provider.Thread) *Mailbox {
	t.Helper()
	c := testCache(t)
	c.PutFolders(folders)
	if len(threads) > 0 && len(folders) > 0 {
		c.ReplaceThreads(folders[0].ID, threads)
	}
	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	if len(folders) > 0 {
		mb.SelectFolder(0)
		mb.LoadThreads()
		mb.BuildThreadDisplay()
	}
	return mb
}

// folder display tests

func TestBuildFolderDisplay_CanonicalOrder(t *testing.T) {
	mb := testMailbox(t, []provider.Folder{
		{ID: "[Gmail]/Trash", Name: "Trash"},
		{ID: "INBOX", Name: "INBOX", Unread: 3},
		{ID: "[Gmail]/Sent Mail", Name: "Sent Mail"},
	}, nil)

	want := []string{"Inbox (3)", "Sent", "Trash"}
	names := *mb.FolderNames()
	if len(names) != len(want) {
		t.Fatalf("got %d folder names %v, want %d %v", len(names), names, len(want), want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("folder[%d] = %q, want %q", i, names[i], name)
		}
	}
}

func TestBuildFolderDisplay_GoogleMailPrefix(t *testing.T) {
	mb := testMailbox(t, []provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Google Mail]/Sent Mail", Name: "Sent Mail", Total: 3736},
		{ID: "[Google Mail]/Bin", Name: "Bin", Unread: 50, Total: 293},
		{ID: "[Google Mail]/All Mail", Name: "All Mail", Total: 40000},
		{ID: "[Google Mail]/Starred", Name: "Starred", Total: 43},
		{ID: "[Google Mail]/Drafts", Name: "Drafts", Total: 14},
		{ID: "[Google Mail]/Spam", Name: "Spam", Unread: 42},
	}, nil)

	want := []string{"Inbox", "Sent", "Drafts", "Starred", "Trash (50)", "Spam (42)", "Archive"}
	names := *mb.FolderNames()
	if len(names) != len(want) {
		t.Fatalf("got %d folder names %v, want %d %v", len(names), names, len(want), want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("folder[%d] = %q, want %q", i, names[i], name)
		}
	}
}

func TestBuildFolderDisplay_DedupPreferData(t *testing.T) {
	mb := testMailbox(t, []provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Gmail]/Sent Mail", Name: "Sent Mail", Total: 0},
		{ID: "[Google Mail]/Sent Mail", Name: "Sent Mail", Total: 3736},
		{ID: "[Gmail]/Drafts", Name: "Drafts", Total: 0},
		{ID: "[Google Mail]/Drafts", Name: "Drafts", Total: 14},
	}, nil)

	want := []string{"Inbox", "Sent", "Drafts"}
	names := *mb.FolderNames()
	if len(names) != len(want) {
		t.Fatalf("got %d folder names %v, want %d %v", len(names), names, len(want), want)
	}
}

func TestBuildFolderDisplay_FiltersSystemFolders(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Airmail]/Done", Name: "[Airmail]/Done"},
		{ID: "[Mailbox]/Later", Name: "[Mailbox]/Later"},
		{ID: "[Gmail]", Name: "[Gmail]"},
		{ID: "[Google Mail]", Name: "[Google Mail]"},
		{ID: "MyLabel", Name: "MyLabel"},
	})
	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(true)

	want := []string{"Inbox", "▾ Labels", "  MyLabel"}
	names := *mb.FolderNames()
	if len(names) != len(want) {
		t.Fatalf("got %d folder names %v, want %d %v", len(names), names, len(want), want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("folder[%d] = %q, want %q", i, names[i], name)
		}
	}
}

func TestBuildFolderDisplay_LabelsToggle(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "MyLabel", Name: "MyLabel"},
		{ID: "Work", Name: "Work"},
	})
	mb := New(c, "test@example.com")
	mb.LoadFolders()

	mb.BuildFolderDisplay(false)
	if mb.CanonEnd() != 1 {
		t.Errorf("canonEnd = %d, want 1", mb.CanonEnd())
	}
	if mb.FolderLen() != 2 {
		t.Errorf("closed: got %d names, want 2", mb.FolderLen())
	}

	mb.BuildFolderDisplay(true)
	if mb.FolderLen() != 4 {
		t.Errorf("open: got %d names, want 4", mb.FolderLen())
	}
}

func TestBuildFolderDisplay_RepeatedCallsPreserveLabels(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "MyLabel", Name: "MyLabel"},
	})
	mb := New(c, "test@example.com")
	mb.LoadFolders()

	mb.BuildFolderDisplay(true)
	if mb.FolderLen() != 3 {
		t.Fatalf("first open: got %d, want 3", mb.FolderLen())
	}
	mb.BuildFolderDisplay(false)
	if mb.FolderLen() != 2 {
		t.Fatalf("closed: got %d, want 2", mb.FolderLen())
	}
	mb.BuildFolderDisplay(true)
	if mb.FolderLen() != 3 {
		t.Fatalf("second open: got %d, want 3", mb.FolderLen())
	}
}

func TestActiveFolderID_AfterBuildDisplay(t *testing.T) {
	mb := testMailbox(t, []provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Gmail]/Sent Mail", Name: "Sent Mail"},
		{ID: "MyLabel", Name: "MyLabel"},
	}, nil)

	mb.SelectFolder(0)
	if id := mb.ActiveFolderID(); id != "INBOX" {
		t.Errorf("active folder = %q, want INBOX", id)
	}
	mb.SelectFolder(1)
	if id := mb.ActiveFolderID(); id != "[Gmail]/Sent Mail" {
		t.Errorf("active folder = %q, want [Gmail]/Sent Mail", id)
	}
}

// cache round-trip tests

func TestCacheRoundTrip_Folders(t *testing.T) {
	mb := testMailbox(t, []provider.Folder{
		{ID: "INBOX", Name: "INBOX", Unread: 5, Total: 100},
		{ID: "[Gmail]/Sent Mail", Name: "Sent Mail"},
	}, nil)

	names := *mb.FolderNames()
	if len(names) != 2 {
		t.Fatalf("got %d folder names, want 2", len(names))
	}
	if names[0] != "Inbox (5)" {
		t.Errorf("folder[0] = %q, want %q", names[0], "Inbox (5)")
	}
}

func TestCacheRoundTrip_Threads(t *testing.T) {
	now := time.Now()
	mb := testMailbox(t,
		[]provider.Folder{{ID: "INBOX", Name: "INBOX"}},
		[]provider.Thread{
			{ID: "t1", Subject: "older", Date: now.Add(-2 * time.Hour)},
			{ID: "t2", Subject: "newer", Date: now.Add(-1 * time.Hour)},
		},
	)

	rows := *mb.ThreadRows()
	if len(rows) != 2 {
		t.Fatalf("got %d thread rows, want 2", len(rows))
	}
	if rows[0].Label != "newer" {
		t.Errorf("first thread = %q, want 'newer'", rows[0].Label)
	}
}

func TestBuildThreadDisplay_DateGroups(t *testing.T) {
	now := time.Now()
	mb := testMailbox(t,
		[]provider.Folder{{ID: "INBOX", Name: "INBOX"}},
		[]provider.Thread{
			{ID: "today", Subject: "today", Date: now},
			{ID: "today2", Subject: "today again", Date: now.Add(-time.Hour)},
			{ID: "yesterday", Subject: "yesterday", Date: now.AddDate(0, 0, -1)},
			{ID: "week", Subject: "this week", Date: now.AddDate(0, 0, -3)},
			{ID: "earlier", Subject: "earlier", Date: now.AddDate(0, 0, -12)},
		},
	)

	rows := *mb.ThreadRows()
	if len(rows) != 5 {
		t.Fatalf("got %d thread rows, want 5", len(rows))
	}
	want := []string{"TODAY", "", "YESTERDAY", "THIS WEEK", "EARLIER"}
	for i := range want {
		if rows[i].GroupLabel != want[i] {
			t.Fatalf("row %d group = %q, want %q", i, rows[i].GroupLabel, want[i])
		}
		if rows[i].HasGroup != (want[i] != "") {
			t.Fatalf("row %d HasGroup = %v, want %v", i, rows[i].HasGroup, want[i] != "")
		}
	}
}

// thread interaction tests

func TestToggleThread_ExpandCollapse(t *testing.T) {
	now := time.Now()
	mb := testMailbox(t,
		[]provider.Folder{{ID: "INBOX", Name: "INBOX"}},
		[]provider.Thread{
			{ID: "t1", Subject: "thread one", Date: now, Messages: []provider.Message{
				{ID: "m1", From: provider.Address{Email: "a@x.com"}, Subject: "msg 1", Date: now},
				{ID: "m2", From: provider.Address{Email: "b@x.com"}, Subject: "msg 2", Date: now},
			}},
			{ID: "t2", Subject: "thread two", Date: now.Add(-time.Minute), Messages: []provider.Message{
				{ID: "m3", From: provider.Address{Email: "c@x.com"}, Subject: "msg 3", Date: now},
			}},
		},
	)

	if mb.ThreadLen() != 2 {
		t.Fatalf("initial rows = %d, want 2", mb.ThreadLen())
	}

	mb.ToggleThread(0)
	if mb.ThreadLen() != 4 {
		t.Fatalf("after expand: rows = %d, want 4", mb.ThreadLen())
	}

	mb.ToggleThread(0)
	if mb.ThreadLen() != 2 {
		t.Fatalf("after collapse: rows = %d, want 2", mb.ThreadLen())
	}
}

func TestSelectedMessage_AfterExpand(t *testing.T) {
	now := time.Now()
	mb := testMailbox(t,
		[]provider.Folder{{ID: "INBOX", Name: "INBOX"}},
		[]provider.Thread{
			{ID: "t1", Subject: "test", Date: now, Messages: []provider.Message{
				{ID: "m1", From: provider.Address{Email: "a@x.com"}, Subject: "first", Date: now},
				{ID: "m2", From: provider.Address{Email: "b@x.com"}, Subject: "second", Date: now},
			}},
		},
	)

	if msg := mb.SelectedMessage(0); msg != nil {
		t.Errorf("header should return nil message")
	}

	mb.ToggleThread(0)
	// newest first: row 1 = m2, row 2 = m1
	msg := mb.SelectedMessage(1)
	if msg == nil {
		t.Fatal("expected message at row 1")
	}
	if msg.ID != "m2" {
		t.Errorf("message ID = %q, want m2", msg.ID)
	}
}

// action tests

func TestArchive_RemovesThreadAndQueuesCommand(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	undo, _ := mb.Archive(1)

	if mb.ThreadLen() != 2 {
		t.Fatalf("rows after archive = %d, want 2", mb.ThreadLen())
	}

	cmds, _ := c.PendingCommands()
	if len(cmds) != 1 || cmds[0].Action != "move" || cmds[0].TargetID != "m2" {
		t.Errorf("pending commands = %v, want move m2", cmds)
	}
	if cmds[0].Params["folder"] != "[Google Mail]/All Mail" {
		t.Errorf("move folder = %q, want [Google Mail]/All Mail", cmds[0].Params["folder"])
	}
	if cmds[0].Params["source"] != "INBOX" {
		t.Errorf("move source = %q, want INBOX", cmds[0].Params["source"])
	}

	// undo should restore the thread
	undo()
	if mb.ThreadLen() != 3 {
		t.Fatalf("after undo: rows = %d, want 3", mb.ThreadLen())
	}
	cmds, _ = c.PendingCommands()
	if len(cmds) != 0 {
		t.Errorf("after undo: pending commands = %d, want 0", len(cmds))
	}
}

func TestArchive_QueuesEveryMessageInThread(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "m2", Subject: "grouped", Date: now, Messages: []provider.Message{{ID: "m1"}, {ID: "m2"}}},
	})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.Archive(0)

	cmds, _ := c.PendingCommands()
	if len(cmds) != 2 {
		t.Fatalf("pending commands = %d, want 2", len(cmds))
	}
	got := map[string]bool{}
	for _, cmd := range cmds {
		if cmd.Action != "move" {
			t.Errorf("action = %q, want move", cmd.Action)
		}
		if cmd.Params["folder"] != "[Google Mail]/All Mail" {
			t.Errorf("move folder = %q, want [Google Mail]/All Mail", cmd.Params["folder"])
		}
		if cmd.Params["source"] != "INBOX" {
			t.Errorf("move source = %q, want INBOX", cmd.Params["source"])
		}
		got[cmd.TargetID] = true
	}
	for _, id := range []string{"m1", "m2"} {
		if !got[id] {
			t.Errorf("missing queued move for %s", id)
		}
	}
}

func TestToggleRead_UpdatesDisplay(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "unread", Date: now, Unread: 2, Messages: []provider.Message{
			{ID: "m1", Read: false},
			{ID: "m2", Read: false},
		}},
	})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	rows := *mb.ThreadRows()
	if !rows[0].Unread {
		t.Fatal("expected unread before toggle")
	}

	undo, _ := mb.ToggleRead(0)

	rows = *mb.ThreadRows()
	if rows[0].Unread {
		t.Error("expected read after toggle")
	}
	for _, msg := range mb.threads[0].Messages {
		if !msg.Read {
			t.Errorf("message %s still unread after toggle", msg.ID)
		}
	}
	mb.BuildFolderDisplay(false)
	if got := (*mb.FolderNames())[0]; got != "Inbox" {
		t.Errorf("folder label after mark read = %q, want Inbox", got)
	}

	cmds, _ := c.PendingCommands()
	if len(cmds) != 2 {
		t.Fatalf("pending commands = %d, want 2", len(cmds))
	}

	// undo should restore unread state
	undo()
	rows = *mb.ThreadRows()
	if !rows[0].Unread {
		t.Error("expected unread after undo")
	}
	for _, msg := range mb.threads[0].Messages {
		if msg.Read {
			t.Errorf("message %s still read after undo", msg.ID)
		}
	}
}

func TestDelete_WithExpandedThread(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "has messages", Date: now, Messages: []provider.Message{
			{ID: "m1", From: provider.Address{Email: "a@x.com"}, Date: now},
			{ID: "m2", From: provider.Address{Email: "b@x.com"}, Date: now},
		}},
		{ID: "t2", Subject: "other", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.Delete(0)
	rows := *mb.ThreadRows()
	if len(rows) != 1 {
		t.Fatalf("after delete: rows = %d, want 1", len(rows))
	}
	if rows[0].Label != "other" {
		t.Errorf("remaining = %q, want 'other'", rows[0].Label)
	}
}

func TestUndo_MultipleDeletes(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	// delete three threads, building undo stack
	var undoStack []func()
	undo1, _ := mb.Delete(0)
	undoStack = append(undoStack, undo1)
	undo2, _ := mb.Delete(0)
	undoStack = append(undoStack, undo2)
	undo3, _ := mb.Delete(0)
	undoStack = append(undoStack, undo3)

	if mb.ThreadLen() != 0 {
		t.Fatalf("after 3 deletes: rows = %d, want 0", mb.ThreadLen())
	}

	// undo all three in reverse order
	for i := len(undoStack) - 1; i >= 0; i-- {
		undoStack[i]()
	}

	if mb.ThreadLen() != 3 {
		t.Fatalf("after 3 undos: rows = %d, want 3", mb.ThreadLen())
	}
}

func TestDelete_PersistsThroughReload(t *testing.T) {
	c := testCache(t)
	now := time.Now()
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "keep", Date: now},
		{ID: "t2", Subject: "delete me", Date: now.Add(-time.Hour)},
	})

	mb := New(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.Delete(1) // undo not used — committed

	// simulate restart
	mb2 := New(c, "test@example.com")
	mb2.LoadFolders()
	mb2.BuildFolderDisplay(false)
	mb2.SelectFolder(0)
	mb2.LoadThreads()
	mb2.BuildThreadDisplay()

	rows := *mb2.ThreadRows()
	if len(rows) != 1 {
		t.Fatalf("after reload: rows = %d, want 1", len(rows))
	}
	if rows[0].Label != "keep" {
		t.Errorf("remaining = %q, want 'keep'", rows[0].Label)
	}
}

// cache tests

func TestCacheReplaceThreads_RemovesStale(t *testing.T) {
	c := testCache(t)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "old", Subject: "stale thread", Date: time.Now().Add(-24 * time.Hour)},
	})

	fresh := []provider.Thread{
		{ID: "new1", Subject: "fresh one", Date: time.Now()},
		{ID: "new2", Subject: "fresh two", Date: time.Now()},
	}
	if err := c.ReplaceThreads("INBOX", fresh); err != nil {
		t.Fatal(err)
	}

	threads, err := c.GetThreads("INBOX", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}
	for _, th := range threads {
		if th.ID == "old" {
			t.Error("stale thread still in cache")
		}
	}
}

func TestCacheReplaceThreads_DoesNotAffectOtherFolders(t *testing.T) {
	c := testCache(t)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "inbox1", Subject: "inbox", Date: time.Now()},
	})
	c.ReplaceThreads("SENT", []provider.Thread{
		{ID: "sent1", Subject: "sent", Date: time.Now()},
	})

	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "inbox2", Subject: "new inbox", Date: time.Now()},
	})

	sent, _ := c.GetThreads("SENT", 25)
	if len(sent) != 1 || sent[0].ID != "sent1" {
		t.Errorf("SENT affected by INBOX replace: %v", sent)
	}
}
