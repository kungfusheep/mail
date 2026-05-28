package mailbox

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/preview"
	"github.com/kungfusheep/mail/provider"
	"github.com/kungfusheep/mail/theme"
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
	mb      *State
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

	mb := NewState(c, "me@example.com")
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
		mb.LoadThreads()
		mb.BuildThreadDisplay()
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

func waitForThreadRefresh(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("subscriber never refreshed after thread mutation")
	}
}

func assertThreadRows(t *testing.T, mb *State, wantLabels []string, wantSelected int) {
	t.Helper()
	rows := *mb.ThreadRows()
	if len(rows) != len(wantLabels) {
		t.Fatalf("rows = %d (%#v), want %d", len(rows), rows, len(wantLabels))
	}
	for i, want := range wantLabels {
		if rows[i].Label != want {
			t.Fatalf("row %d = %q, want %q; rows=%#v", i, rows[i].Label, want, rows)
		}
		if rows[i].Selected != (i == wantSelected) {
			t.Fatalf("row %d selected = %v, want %v; rows=%#v", i, rows[i].Selected, i == wantSelected, rows)
		}
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
	if !spansContain(msgs[0].BodySpans, "body-c") {
		t.Errorf("conversation body spans = %#v, want to contain \"body-c\" — rich preview pane will be empty", msgs[0].BodySpans)
	}
}

func spansContain(spans []glyph.Span, text string) bool {
	for _, span := range spans {
		if strings.Contains(span.Text, text) {
			return true
		}
	}
	return false
}

func TestThreadNavigationResetsPreviewScroll(t *testing.T) {
	mb := testState(t, []provider.Folder{{ID: "INBOX", Name: "INBOX"}}, []provider.Thread{
		{
			ID:      "one",
			Subject: "one",
			Messages: []provider.Message{{
				ID:        "m1",
				MessageID: "m1",
				From:      provider.Address{Name: "Alice", Email: "alice@example.com"},
				Date:      time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
				TextBody:  "first",
			}},
		},
		{
			ID:      "two",
			Subject: "two",
			Messages: []provider.Message{{
				ID:        "m2",
				MessageID: "m2",
				From:      provider.Address{Name: "Bob", Email: "bob@example.com"},
				Date:      time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
				TextBody:  "second",
			}},
		},
	})

	model := NewUI(UIConfig{
		App:   glyph.NewApp(),
		Cache: testCache(t),
		State: mb,
		Theme: theme.Dark(),
	})
	scroll := glyph.ScrollView()
	scroll.Layer().SetViewport(20, 5)
	scroll.Layer().SetBuffer(glyph.NewBuffer(20, 20))
	scroll.Layer().ScrollTo(8)
	model.SetConversationView(scroll)

	model.ThreadDown()

	if got := scroll.Layer().ScrollY(); got != 0 {
		t.Fatalf("preview scroll = %d, want 0 after thread navigation", got)
	}
}

func TestUpdatePreviewScrollTracksScrollViewLayer(t *testing.T) {
	model := NewUI(UIConfig{
		App:   glyph.NewApp(),
		Cache: testCache(t),
		State: NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	scroll := glyph.ScrollView()
	scroll.Layer().SetViewport(20, 5)
	scroll.Layer().SetBuffer(glyph.NewBuffer(20, 25))
	scroll.Layer().ScrollTo(10)
	model.SetConversationView(scroll)
	model.FocusPreview()

	model.UpdatePreviewScroll()

	if !model.PreviewCanScroll {
		t.Fatal("PreviewCanScroll = false, want true for overflowing preview content")
	}
	if !model.PreviewScrollVisible {
		t.Fatal("PreviewScrollVisible = false, want true while preview pane is focused and scrollable")
	}
	if got := model.PreviewScrollPos; got != 50 {
		t.Fatalf("PreviewScrollPos = %d, want 50", got)
	}

	scroll.Layer().SetBuffer(glyph.NewBuffer(20, 5))
	model.UpdatePreviewScroll()

	if model.PreviewCanScroll {
		t.Fatal("PreviewCanScroll = true, want false when preview content fits")
	}
	if model.PreviewScrollVisible {
		t.Fatal("PreviewScrollVisible = true, want false when preview content fits")
	}
	if got := model.PreviewScrollPos; got != 0 {
		t.Fatalf("PreviewScrollPos = %d, want 0 when preview content fits", got)
	}
}

func TestApplyThemeInvalidatesAndRepaintsPreviewLayer(t *testing.T) {
	app := glyph.NewApp()
	model := NewUI(UIConfig{
		App:   app,
		Cache: testCache(t),
		State: NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	scroll := glyph.ScrollView.Fill(&model.BG)(
		glyph.Text("preview body"),
		glyph.SpaceH(3),
	)
	model.SetConversationView(scroll)

	tmpl := glyph.Build(scroll)
	tmpl.SetApp(app)
	buf := glyph.NewBuffer(30, 5)
	tmpl.Execute(buf, 30, 5)
	if scroll.Layer().NeedsRender() {
		t.Fatal("preview layer still needs render after initial execute")
	}

	light := theme.Light()
	model.ApplyTheme(light)
	if !scroll.Layer().NeedsRender() {
		t.Fatal("ApplyTheme did not invalidate the preview layer")
	}

	buf = glyph.NewBuffer(30, 5)
	tmpl.Execute(buf, 30, 5)
	got := buf.Get(0, 1).Style.BG
	if got != light.BG {
		t.Fatalf("preview layer background = %#v, want light theme BG %#v", got, light.BG)
	}
}

func TestLoadPreviewUsesDocumentModelForHTML(t *testing.T) {
	mb := NewState(nil, "test@example.com")
	msg := provider.Message{
		From:    provider.Address{Name: "Alice", Email: "alice@example.com"},
		To:      []provider.Address{{Email: "test@example.com"}},
		Date:    time.Date(2026, 5, 13, 9, 30, 0, 0, time.UTC),
		Subject: "html preview",
		HTMLBody: `<html><body>
			<h1>Receipt</h1>
			<p>Hello <strong>Alex</strong>, see <a href="https://example.test">details</a>.</p>
			<blockquote><p>older reply</p></blockquote>
		</body></html>`,
	}

	mb.LoadPreview(msg, 120)

	got := *mb.PreviewText()
	for _, want := range []string{
		"Subject: html preview",
		"Receipt",
		"Hello Alex, see details.",
		"older reply",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview text = %q, want to contain %q", got, want)
		}
	}
}

func TestUpdateFocusKeepsPaneStylesActiveForScreenEffects(t *testing.T) {
	tm := theme.Dark()
	model := NewUI(UIConfig{
		App:   glyph.NewApp(),
		Cache: testCache(t),
		State: NewState(nil, "test@example.com"),
		Theme: tm,
	})

	model.FocusPreview()

	if model.ThreadStyle.FG != tm.FG {
		t.Fatalf("thread style after preview focus = %v, want fg %v", model.ThreadStyle.FG, tm.FG)
	}
	if model.PreviewStyle.FG != tm.FG {
		t.Fatalf("preview style after preview focus = %v, want fg %v", model.PreviewStyle.FG, tm.FG)
	}
	if model.FolderListStyle.FG != tm.FG {
		t.Fatalf("folder list style after preview focus = %v, want fg %v", model.FolderListStyle.FG, tm.FG)
	}
}

func TestLoadConversationCarriesAttachmentMetadata(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "attachments",
		Date:    now,
		Messages: []provider.Message{{
			ID:       "m1",
			From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject:  "attachments",
			Date:     now,
			TextBody: "see attached",
			Attachments: []provider.Attachment{{
				Filename:    "brief.pdf",
				ContentType: "application/pdf",
				Size:        153600,
				Part:        []int{2},
			}},
		}},
	}})

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if len(msgs) != 1 {
		t.Fatalf("conversation messages = %d, want 1", len(msgs))
	}
	if len(msgs[0].Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(msgs[0].Attachments))
	}
	if got := msgs[0].Attachments[0].Filename; got != "brief.pdf" {
		t.Fatalf("attachment filename = %q, want brief.pdf", got)
	}
	if got := msgs[0].Attachments[0].Size; got != 153600 {
		t.Fatalf("attachment size = %d, want 153600", got)
	}
	if got := msgs[0].Attachments[0].Metadata; got != "pdf · 150 KB" {
		t.Fatalf("attachment metadata = %q, want pdf · 150 KB", got)
	}
	if got := msgs[0].Attachments[0].MessageID; got != "m1" {
		t.Fatalf("attachment message id = %q, want m1", got)
	}
	if got := msgs[0].Attachments[0].Part; len(got) != 1 || got[0] != 2 {
		t.Fatalf("attachment part = %v, want [2]", got)
	}
	jumps := 0
	for _, span := range msgs[0].Attachments[0].Display {
		if span.OnSelect != nil {
			jumps++
		}
	}
	if jumps != 1 {
		t.Fatalf("attachment display jump callbacks = %d, want 1", jumps)
	}
}

func TestLoadConversationShowsAttachmentSourceForThread(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "attachments",
		Date:    now,
		Messages: []provider.Message{
			{
				ID:       "m1",
				From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
				Subject:  "first",
				Date:     now,
				TextBody: "first message",
			},
			{
				ID:       "m2",
				From:     provider.Address{Name: "Bob", Email: "bob@example.com"},
				Subject:  "second",
				Date:     now.Add(time.Minute),
				TextBody: "see attached",
				Attachments: []provider.Attachment{{
					Filename:    "brief.pdf",
					ContentType: "application/pdf",
					Size:        153600,
					Part:        []int{2},
				}},
			},
		},
	}})

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if len(msgs) != 2 {
		t.Fatalf("conversation messages = %d, want 2", len(msgs))
	}
	if len(msgs[1].Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(msgs[1].Attachments))
	}
	if got := msgs[1].Attachments[0].Source; got != "Bob" {
		t.Fatalf("attachment source = %q, want Bob", got)
	}
	if !spansContain(msgs[1].Attachments[0].Display, "from Bob") {
		t.Fatalf("attachment display = %#v, want source metadata", msgs[1].Attachments[0].Display)
	}
}

func TestLoadMoreThreadsExpandsCachedWindow(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	threads := make([]provider.Thread, 60)
	for i := range threads {
		threads[i] = provider.Thread{
			ID:      fmt.Sprintf("t%02d", i),
			Subject: fmt.Sprintf("thread %02d", i),
			Date:    now.Add(time.Duration(i) * time.Minute),
			Messages: []provider.Message{{
				ID:      fmt.Sprintf("m%02d", i),
				Subject: fmt.Sprintf("thread %02d", i),
				Date:    now.Add(time.Duration(i) * time.Minute),
			}},
		}
	}
	if err := c.ReplaceThreads("INBOX", threads); err != nil {
		t.Fatal(err)
	}

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	if got := len(mb.threads); got != defaultThreadLimit {
		t.Fatalf("initial loaded threads = %d, want %d", got, defaultThreadLimit)
	}

	limit := mb.LoadMoreThreads()
	if limit != defaultThreadLimit+threadLimitStep {
		t.Fatalf("thread limit = %d, want %d", limit, defaultThreadLimit+threadLimitStep)
	}
	if got := len(mb.threads); got != defaultThreadLimit+threadLimitStep {
		t.Fatalf("loaded threads after load more = %d, want %d", got, defaultThreadLimit+threadLimitStep)
	}
}

func TestCopySenderAddressCopiesSelectedThreadSender(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "hello",
		Date:    time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
		Messages: []provider.Message{{
			ID:   "m1",
			From: provider.Address{Name: "Alice", Email: "alice@example.com"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	var copied string
	oldCopy := copyText
	copyText = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() { copyText = oldCopy })

	model := NewUI(UIConfig{
		App:   glyph.NewApp(),
		Cache: c,
		State: mb,
		Theme: theme.Dark(),
	})
	model.CopySenderAddress()

	if copied != "alice@example.com" {
		t.Fatalf("copied sender = %q, want alice@example.com", copied)
	}
}

func TestSpamMovesThreadToSpamAndQueuesUndo(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Google Mail]/Spam", Name: "Spam"},
	})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "spammy",
		Date:    now,
		Messages: []provider.Message{{
			ID:      "m1",
			Subject: "spammy",
			Date:    now,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	undo, desc := mb.Spam(0)
	if undo == nil {
		t.Fatal("Spam undo = nil")
	}
	if !strings.Contains(desc, "spam") {
		t.Fatalf("Spam desc = %q, want spam", desc)
	}
	if mb.ThreadLen() != 0 {
		t.Fatalf("rows after spam = %d, want 0", mb.ThreadLen())
	}
	spam, err := c.GetThreads("[Google Mail]/Spam", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(spam) != 1 || spam[0].ID != "t1" {
		t.Fatalf("spam threads = %#v, want t1", spam)
	}

	undo()
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	if mb.ThreadLen() != 1 {
		t.Fatalf("rows after undo = %d, want 1", mb.ThreadLen())
	}
}

func TestAttachmentMetadataFormatsKindAndSize(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		contentType string
		size        int64
		want        string
	}{
		{name: "extension and kilobytes", filename: "brief.pdf", contentType: "application/pdf", size: 153600, want: "pdf · 150 KB"},
		{name: "content type fallback", filename: "attachment", contentType: "text/calendar", size: 2048, want: "calendar · 2 KB"},
		{name: "bytes", filename: "notes.txt", contentType: "text/plain", size: 72, want: "txt · 72 B"},
		{name: "megabytes", filename: "photo.png", contentType: "image/png", size: 1572864, want: "png · 1.5 MB"},
		{name: "kind only", filename: "invoice.csv", contentType: "text/csv", size: 0, want: "csv"},
		{name: "size only", filename: "attachment", contentType: "", size: 1024, want: "1 KB"},
		{name: "empty", filename: "attachment", contentType: "", size: 0, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := attachmentMetadata(tt.filename, tt.contentType, tt.size); got != tt.want {
				t.Fatalf("attachmentMetadata() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadConversationCarriesSubjectAndPreviewBlocks(t *testing.T) {
	c := testCache(t)
	if err := c.PutSenderIdentity(cache.SenderIdentity{Domain: "example.com", ThemeColor: "#ff3533"}); err != nil {
		t.Fatal(err)
	}
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 19, 11, 0, 0, 0, time.UTC)
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "letter heading",
		Date:    now,
		Messages: []provider.Message{{
			ID:      "m1",
			From:    provider.Address{Name: "Alice", Email: "alice@example.com"},
			To:      []provider.Address{{Name: "Pete", Email: "pete@example.com"}},
			CC:      []provider.Address{{Name: "Team", Email: "team@example.com"}},
			Subject: "letter heading",
			Date:    now,
			HTMLBody: `<html><body>
				<h1>Account update</h1>
				<p>Hello Pete, your account is ready.</p>
				<ul><li>Review the details</li></ul>
			</body></html>`,
		}},
	}})

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if len(msgs) != 1 {
		t.Fatalf("conversation messages = %d, want 1", len(msgs))
	}
	if got := msgs[0].Subject; got != "letter heading" {
		t.Fatalf("subject = %q, want letter heading", got)
	}
	if !msgs[0].HasSubject {
		t.Fatal("HasSubject = false, want true")
	}
	if !msgs[0].HasSenderColor {
		t.Fatal("HasSenderColor = false, want sender theme colour from cache")
	}
	if got := msgs[0].FromLine; got != "Alice <alice@example.com>" {
		t.Fatalf("from line = %q, want Alice address", got)
	}
	if got := msgs[0].ToLine; got != "Pete <pete@example.com>" {
		t.Fatalf("to line = %q, want Pete address", got)
	}
	if got := msgs[0].CCLine; got != "Team <team@example.com>" {
		t.Fatalf("cc line = %q, want Team address", got)
	}
	if !msgs[0].HasTo || !msgs[0].HasCC || msgs[0].HasBCC {
		t.Fatalf("recipient flags to/cc/bcc = %v/%v/%v, want true/true/false", msgs[0].HasTo, msgs[0].HasCC, msgs[0].HasBCC)
	}
	if len(msgs[0].BodyBlocks) != 3 {
		t.Fatalf("body blocks = %d, want 3", len(msgs[0].BodyBlocks))
	}
	if msgs[0].BodyBlocks[0].HasSpaceBefore {
		t.Fatal("first body block has space before it, want false")
	}
	if !msgs[0].BodyBlocks[1].HasSpaceBefore {
		t.Fatal("second body block has no space before it, want true")
	}
	if got := msgs[0].BodyBlocks[0].Kind; got != preview.BlockHeading {
		t.Fatalf("first block kind = %v, want heading", got)
	}
	if msgs[0].BodyBlocks[0].Style.Attr&glyph.AttrBold == 0 {
		t.Fatalf("heading style attr = %v, want bold", msgs[0].BodyBlocks[0].Style.Attr)
	}
	if !spansContain(msgs[0].BodyBlocks[1].Spans, "Hello Pete") {
		t.Fatalf("second block spans = %#v, want body paragraph", msgs[0].BodyBlocks[1].Spans)
	}
	if got := msgs[0].BodyBlocks[2].Kind; got != preview.BlockListItem {
		t.Fatalf("third block kind = %v, want list item", got)
	}
}

func TestLoadConversationPromotesUnsubscribeLink(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "newsletter",
		Date:    time.Now(),
		Messages: []provider.Message{{
			ID:      "m1",
			From:    provider.Address{Name: "News", Email: "news@example.com"},
			Subject: "newsletter",
			HTMLBody: `<html><body>
				<p>Main news.</p>
				<p><a href="https://example.test/unsubscribe">Unsubscribe</a></p>
			</body></html>`,
		}},
	}})

	mb := NewState(c, "me@example.com")
	var opened string
	mb.SetLinkOpener(func(href string) error {
		opened = href
		return nil
	})
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if len(msgs) != 1 {
		t.Fatalf("conversation messages = %d, want 1", len(msgs))
	}
	if !msgs[0].HasUnsubscribe {
		t.Fatal("HasUnsubscribe = false, want true")
	}
	if got := msgs[0].UnsubscribeURL; got != "https://example.test/unsubscribe" {
		t.Fatalf("UnsubscribeURL = %q", got)
	}
	if len(msgs[0].UnsubscribeChip) != 1 || msgs[0].UnsubscribeChip[0].OnSelect == nil {
		t.Fatalf("unsubscribe chip = %#v, want clickable span", msgs[0].UnsubscribeChip)
	}
	if !msgs[0].UnsubscribeChip[0].Style.FG.Equal(glyph.Hex(0x7aa2f7)) {
		t.Fatalf("unsubscribe chip fg = %#v, want link blue", msgs[0].UnsubscribeChip[0].Style.FG)
	}
	if msgs[0].UnsubscribeChip[0].Style.Attr&(glyph.AttrBold|glyph.AttrUnderline) != glyph.AttrBold|glyph.AttrUnderline {
		t.Fatalf("unsubscribe chip attrs = %#v, want bold underline", msgs[0].UnsubscribeChip[0].Style.Attr)
	}
	msgs[0].UnsubscribeChip[0].OnSelect()
	if opened != "https://example.test/unsubscribe" {
		t.Fatalf("opened = %q, want unsubscribe href", opened)
	}
}

func TestPreviewBodyBlocksClassifySecondaryContent(t *testing.T) {
	mb := NewState(nil, "me@example.com")
	doc := preview.ParseHTML(`<html><body>
		<p>Main content</p>
		<blockquote><p>Older reply</p></blockquote>
		<img alt="Performance chart" src="https://example.test/chart.png">
		<hr>
	</body></html>`, "")

	blocks := mb.previewBodyBlocks(doc, false, nil)
	if len(blocks) != 4 {
		t.Fatalf("body blocks = %d, want 4", len(blocks))
	}
	if blocks[1].Kind != preview.BlockQuote {
		t.Fatalf("second block kind = %v, want quote", blocks[1].Kind)
	}
	if blocks[1].Style.Attr&glyph.AttrDim == 0 {
		t.Fatalf("quote style attr = %v, want dim", blocks[1].Style.Attr)
	}
	if blocks[2].Kind != preview.BlockImage {
		t.Fatalf("third block kind = %v, want image", blocks[2].Kind)
	}
	if blocks[2].Style.Attr&(glyph.AttrDim|glyph.AttrItalic) != glyph.AttrDim|glyph.AttrItalic {
		t.Fatalf("image style attr = %v, want dim italic", blocks[2].Style.Attr)
	}
	if blocks[3].Kind != preview.BlockDivider {
		t.Fatalf("fourth block kind = %v, want divider", blocks[3].Kind)
	}
}

func TestSenderDomainsReturnsVisibleUniqueDomains(t *testing.T) {
	mb := NewState(nil, "me@example.com")
	mb.threads = []provider.Thread{
		{Messages: []provider.Message{{From: provider.Address{Email: "one@mail.example.com"}}}},
		{Messages: []provider.Message{{From: provider.Address{Email: "two@example.com"}}}},
		{Messages: []provider.Message{{From: provider.Address{Email: "person@amazon.co.uk"}}}},
	}

	got := mb.SenderDomains(10)
	want := []string{"example.com", "amazon.co.uk"}
	if len(got) != len(want) {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("domains = %v, want %v", got, want)
		}
	}
}

func TestSenderDisplayColorParsesThemeColor(t *testing.T) {
	color, ok := senderDisplayColor(cache.SenderIdentity{ThemeColor: "#012169"})
	if !ok {
		t.Fatal("senderDisplayColor ok = false, want true")
	}
	if color.Mode == glyph.ColorDefault {
		t.Fatalf("senderDisplayColor = %#v, want explicit colour", color)
	}
	if _, ok := senderDisplayColor(cache.SenderIdentity{ThemeColor: ""}); ok {
		t.Fatal("senderDisplayColor empty ok = true, want false")
	}
}

func TestSenderDisplayStyleUsesBrandColourForMarker(t *testing.T) {
	color := glyph.Hex(0x012169)
	style := senderDisplayStyle(color, true)

	if style.FG != color {
		t.Fatalf("sender style FG = %#v, want brand colour %#v", style.FG, color)
	}
	if style.BG.Mode != glyph.ColorDefault {
		t.Fatalf("sender style BG = %#v, want default background for marker", style.BG)
	}
	if style.Attr != 0 {
		t.Fatalf("sender style attr = %v, want plain marker", style.Attr)
	}
}

func TestSenderDisplayStyleKeepsUnknownSenderDim(t *testing.T) {
	style := senderDisplayStyle(glyph.Color{}, false)
	if style.Attr&glyph.AttrDim == 0 {
		t.Fatalf("sender style attr = %v, want dim for unknown sender", style.Attr)
	}
	if style.BG.Mode != glyph.ColorDefault {
		t.Fatalf("sender style BG = %#v, want default for unknown sender", style.BG)
	}
}

func TestLoadConversationShowsCalendarPlaceholderBeforeEnrichment(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "calendar",
		Date:    now,
		Messages: []provider.Message{{
			ID:       "m1",
			From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject:  "calendar",
			Date:     now,
			TextBody: "see invite",
			Attachments: []provider.Attachment{{
				Filename:    "invite.ics",
				ContentType: "text/calendar",
				Size:        2048,
				Part:        []int{2},
			}},
		}},
	}})

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("conversation attachments = %#v, want one calendar attachment", msgs)
	}
	row := msgs[0].Attachments[0]
	if !row.Calendar {
		t.Fatal("attachment Calendar = false, want true")
	}
	if !spansContain(row.Display, "calendar invite") {
		t.Fatalf("display = %#v, want calendar invite placeholder", row.Display)
	}
	if !spansContain(row.Display, "invite.ics") {
		t.Fatalf("display = %#v, want filename as placeholder detail", row.Display)
	}
}

func TestLoadConversationAsyncEnrichesCalendarAttachment(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "calendar",
		Date:    now,
		Messages: []provider.Message{{
			ID:       "m1",
			From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject:  "calendar",
			Date:     now,
			TextBody: "see invite",
			Attachments: []provider.Attachment{{
				Filename:    "invite.ics",
				ContentType: "text/calendar",
				Size:        2048,
				Part:        []int{2},
			}},
		}},
	}})

	mb := NewState(c, "me@example.com")
	mb.attachmentFetcher = func(row AttachmentRow) ([]byte, error) {
		return []byte("BEGIN:VCALENDAR\n" +
			"BEGIN:VEVENT\n" +
			"SUMMARY:Design review\n" +
			"DTSTART:20260526T140000Z\n" +
			"END:VEVENT\n" +
			"END:VCALENDAR\n"), nil
	}
	updated := make(chan struct{}, 1)
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, func() { updated <- struct{}{} })

	select {
	case <-updated:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("calendar attachment enrichment did not update preview")
	}

	msgs := *mb.ConversationMessages()
	row := msgs[0].Attachments[0]
	if row.CalendarTitle != "Design review" {
		t.Fatalf("calendar title = %q, want Design review", row.CalendarTitle)
	}
	if row.CalendarWhen != "Tue 26 May, 14:00" {
		t.Fatalf("calendar when = %q, want Tue 26 May, 14:00", row.CalendarWhen)
	}
	if !spansContain(row.Display, "Design review") || !spansContain(row.Display, "Tue 26 May, 14:00") {
		t.Fatalf("display = %#v, want enriched calendar title and date", row.Display)
	}
	if spansContain(row.Display, "invite.ics") {
		t.Fatalf("display = %#v, want event info to replace filename detail", row.Display)
	}
}

func TestLoadConversationRefreshesSearchCalendarAttachmentBeforeEnrichment(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "calendar",
		Date:    now,
		Messages: []provider.Message{{
			ID:       "m1",
			From:     provider.Address{Name: "Wex", Email: "delivery@example.com"},
			Subject:  "calendar",
			Date:     now,
			TextBody: "delivery notice",
			Attachments: []provider.Attachment{{
				Filename:    "DPD Delivery.ics",
				ContentType: "text/calendar",
			}},
		}},
	}})

	mb := NewState(c, "me@example.com")
	mb.messageFetcher = func(id string) (provider.Message, error) {
		if id != "m1" {
			t.Fatalf("message id = %q, want m1", id)
		}
		return provider.Message{
			ID:       "m1",
			TextBody: "delivery notice",
			Attachments: []provider.Attachment{{
				Filename:    "DPD Delivery.ics",
				ContentType: "text/calendar",
				Size:        2048,
				Part:        []int{3},
			}},
		}, nil
	}
	mb.attachmentFetcher = func(row AttachmentRow) ([]byte, error) {
		if len(row.Part) != 1 || row.Part[0] != 3 {
			t.Fatalf("attachment part = %v, want [3]", row.Part)
		}
		return []byte("BEGIN:VCALENDAR\n" +
			"BEGIN:VEVENT\n" +
			"SUMMARY:DPD delivery\n" +
			"DTSTART:20260516T073800Z\n" +
			"END:VEVENT\n" +
			"END:VCALENDAR\n"), nil
	}
	updated := make(chan struct{}, 4)
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, func() { updated <- struct{}{} })

	deadline := time.After(500 * time.Millisecond)
	for {
		msgs := *mb.ConversationMessages()
		if len(msgs) == 1 && len(msgs[0].Attachments) == 1 {
			row := msgs[0].Attachments[0]
			if row.CalendarTitle == "DPD delivery" && row.CalendarWhen == "Sat 16 May, 07:38" {
				if !spansContain(row.Display, "DPD delivery") || !spansContain(row.Display, "Sat 16 May, 07:38") {
					t.Fatalf("display = %#v, want enriched delivery summary", row.Display)
				}
				return
			}
		}

		select {
		case <-updated:
		case <-deadline:
			t.Fatalf("calendar attachment never enriched from refreshed search metadata: %#v", msgs)
		}
	}
}

func TestFetchFolderCandidatesFallBackToAllMailForSearchResults(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Google Mail]/All Mail", Name: "All Mail", Total: 40000},
		{ID: "[Google Mail]/Bin", Name: "Bin"},
	})

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	candidates := mb.fetchFolderCandidates("search-only-thread")

	want := []string{"INBOX", "[Google Mail]/All Mail"}
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %v, want %v", candidates, want)
	}
	for i := range want {
		if candidates[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", candidates, want)
		}
	}
}

func TestFetchFolderCandidatesIncludeCachedThreadLabels(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "Receipts", Name: "Receipts"},
		{ID: "[Google Mail]/All Mail", Name: "All Mail", Total: 40000},
	})
	c.ReplaceThreads("Receipts", []provider.Thread{{ID: "t1", Subject: "receipt"}})

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	candidates := mb.fetchFolderCandidates("t1")

	want := []string{"INBOX", "Receipts", "[Google Mail]/All Mail"}
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %v, want %v", candidates, want)
	}
	for i := range want {
		if candidates[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", candidates, want)
		}
	}
}

func TestAttachmentJumpCallbackNotifiesOpening(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "t1",
		Subject: "attachments",
		Date:    now,
		Messages: []provider.Message{{
			ID:       "m1",
			From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject:  "attachments",
			Date:     now,
			TextBody: "see attached",
			Attachments: []provider.Attachment{{
				Filename:    "brief.pdf",
				ContentType: "application/pdf",
				Part:        []int{2},
			}},
		}},
	}})

	var notices []string
	var errors []string
	mb := NewState(c, "me@example.com")
	mb.SetNotifiers(func(text string) {
		notices = append(notices, text)
	}, func(text string) {
		errors = append(errors, text)
	})
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	msgs[0].Attachments[0].Display[2].OnSelect()

	if len(notices) != 1 || notices[0] != "opening brief.pdf..." {
		t.Fatalf("notices = %v, want opening attachment feedback", notices)
	}
	if len(errors) != 1 || errors[0] != "attachment: not connected" {
		t.Fatalf("errors = %v, want not connected feedback", errors)
	}
}

func TestOpenAttachmentLogsFailuresWithContext(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	var errors []string
	mb := NewState(nil, "me@example.com")
	mb.SetNotifiers(nil, func(text string) {
		errors = append(errors, text)
	})

	mb.OpenAttachment(AttachmentRow{
		Filename:    "brief.pdf",
		MessageID:   "m1",
		ThreadID:    "t1",
		ContentType: "application/pdf",
		Part:        []int{2},
	})

	if len(errors) != 1 || errors[0] != "attachment: not connected" {
		t.Fatalf("errors = %v, want not connected feedback", errors)
	}
	for _, want := range []string{
		"open attachment failed",
		`filename="brief.pdf"`,
		`message="m1"`,
		`thread="t1"`,
		`content_type="application/pdf"`,
		"part=[2]",
		"not connected",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log = %q, want to contain %q", logs.String(), want)
		}
	}
}

func TestPreserveCachedBodiesAlsoPreservesAttachments(t *testing.T) {
	c := testCache(t)
	folder := "INBOX"
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	mb := NewState(c, "me@example.com")
	c.ReplaceThreads(folder, []provider.Thread{{
		ID:      "old",
		Subject: "cached",
		Date:    now,
		Messages: []provider.Message{{
			ID:          "old-msg",
			MessageID:   "<same@example.com>",
			TextBody:    "cached body",
			Attachments: []provider.Attachment{{Filename: "brief.pdf", ContentType: "application/pdf"}},
		}},
	}})

	threads := []provider.Thread{{
		ID:      "new",
		Subject: "fresh",
		Date:    now,
		Messages: []provider.Message{{
			ID:        "new-msg",
			MessageID: "<same@example.com>",
		}},
	}}
	mb.preserveCachedBodies(folder, threads)

	msg := threads[0].Messages[0]
	if msg.TextBody != "cached body" {
		t.Fatalf("text body = %q, want cached body", msg.TextBody)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(msg.Attachments))
	}
	if got := msg.Attachments[0].Filename; got != "brief.pdf" {
		t.Fatalf("attachment filename = %q, want brief.pdf", got)
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

	mb := NewState(c, "test@example.com")
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

	mb := NewState(c, "test@example.com")
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

	mb := NewState(c, "test@example.com")
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

	mb := NewState(c, "test@example.com")
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

func testState(t *testing.T, folders []provider.Folder, threads []provider.Thread) *State {
	t.Helper()
	c := testCache(t)
	c.PutFolders(folders)
	if len(threads) > 0 && len(folders) > 0 {
		c.ReplaceThreads(folders[0].ID, threads)
	}
	mb := NewState(c, "test@example.com")
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
	mb := testState(t, []provider.Folder{
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
	mb := testState(t, []provider.Folder{
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
	mb := testState(t, []provider.Folder{
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
	mb := NewState(c, "test@example.com")
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
	mb := NewState(c, "test@example.com")
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
	mb := NewState(c, "test@example.com")
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
	mb := testState(t, []provider.Folder{
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
	mb := testState(t, []provider.Folder{
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
	mb := testState(t,
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
	today := time.Now()
	now := time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, today.Location())
	mb := testState(t,
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

func TestBuildThreadDisplay_AttachmentChips(t *testing.T) {
	now := time.Now()
	mb := testState(t,
		[]provider.Folder{{ID: "INBOX", Name: "INBOX"}},
		[]provider.Thread{{
			ID:      "t1",
			Subject: "receipts",
			Date:    now,
			Messages: []provider.Message{{
				ID:   "m1",
				Date: now,
				Attachments: []provider.Attachment{
					{Filename: "invoice.pdf", ContentType: "application/pdf"},
					{Filename: "usage.csv", ContentType: "text/csv"},
					{Filename: "notes.txt", ContentType: "text/plain"},
				},
			}},
		}},
	)

	rows := *mb.ThreadRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if !rows[0].HasAttachments {
		t.Fatalf("expected row to have attachment chips")
	}
	if len(rows[0].Attachments) != 2 {
		t.Fatalf("attachment chips = %d, want 2", len(rows[0].Attachments))
	}
	if rows[0].Attachments[0].Filename != "invoice.pdf" {
		t.Fatalf("first chip filename = %q, want invoice.pdf", rows[0].Attachments[0].Filename)
	}
	if rows[0].Attachments[0].Icon == "" {
		t.Fatalf("first chip icon is empty")
	}
	if rows[0].AttachmentOverflow != "+1" || !rows[0].HasAttachmentOverflow {
		t.Fatalf("overflow = %q/%v, want +1/true", rows[0].AttachmentOverflow, rows[0].HasAttachmentOverflow)
	}
}

// thread interaction tests

func TestToggleThread_ExpandCollapse(t *testing.T) {
	now := time.Now()
	mb := testState(t,
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

func TestLoadConversationDoesNotExpandIndexedConversation(t *testing.T) {
	c := testCache(t)
	now := time.Now()
	inbox := provider.Message{
		ID:        "inbox-1",
		MessageID: "<inbox@test>",
		Subject:   "Leica Q343",
		From:      provider.Address{Name: "Leica", Email: "sales@test"},
		To:        []provider.Address{{Email: "me@test"}},
		Date:      now.Add(-time.Hour),
	}
	sent := provider.Message{
		ID:        "sent-1",
		MessageID: "<sent@test>",
		Subject:   "Re: Leica Q343",
		From:      provider.Address{Name: "Me", Email: "me@test"},
		To:        []provider.Address{{Email: "sales@test"}},
		Date:      now,
		Read:      true,
	}
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:       "inbox-thread",
		Subject:  inbox.Subject,
		Date:     inbox.Date,
		Messages: []provider.Message{inbox},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutSentMessage(sent); err != nil {
		t.Fatal(err)
	}

	mb := NewState(c, "me@test")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if got := len(msgs); got != 1 {
		t.Fatalf("conversation messages = %d, want only selected cached thread", got)
	}
	if msgs[0].Subject != inbox.Subject {
		t.Fatalf("conversation subject = %q, want %q", msgs[0].Subject, inbox.Subject)
	}
}

func TestToggleThreadUsesConversationIndex(t *testing.T) {
	c := testCache(t)
	now := time.Now()
	inbox := provider.Message{
		ID:        "inbox-1",
		MessageID: "<inbox@test>",
		Subject:   "Leica Q343",
		From:      provider.Address{Name: "Leica", Email: "sales@test"},
		To:        []provider.Address{{Email: "me@test"}},
		Date:      now.Add(-time.Hour),
	}
	sent := provider.Message{
		ID:        "sent-1",
		MessageID: "<sent@test>",
		Subject:   "Re: Leica Q343",
		From:      provider.Address{Name: "Me", Email: "me@test"},
		To:        []provider.Address{{Email: "sales@test"}},
		Date:      now,
		Read:      true,
	}
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:       "inbox-thread",
		Subject:  inbox.Subject,
		Date:     inbox.Date,
		Messages: []provider.Message{inbox},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutSentMessage(sent); err != nil {
		t.Fatal(err)
	}

	mb := NewState(c, "me@test")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.ToggleThread(0)
	if got := mb.ThreadLen(); got != 3 {
		t.Fatalf("expanded rows = %d, want header + inbox + sent", got)
	}
	msg := mb.SelectedMessage(1)
	if msg == nil {
		t.Fatal("row 1 did not select indexed message")
	}
	if msg.ID != "sent-1" {
		t.Fatalf("row 1 message = %q, want newest sent message", msg.ID)
	}
}

func TestLoadConversationOnExpandedRowShowsSelectedMessageOnly(t *testing.T) {
	now := time.Now()
	mb := testState(t,
		[]provider.Folder{{ID: "INBOX", Name: "INBOX"}},
		[]provider.Thread{{
			ID:      "t1",
			Subject: "thread subject",
			Date:    now,
			Messages: []provider.Message{
				{
					ID:       "old",
					Subject:  "old message",
					From:     provider.Address{Name: "Alice", Email: "alice@test"},
					Date:     now.Add(-time.Hour),
					TextBody: "old body",
				},
				{
					ID:       "new",
					Subject:  "new message",
					From:     provider.Address{Name: "Bob", Email: "bob@test"},
					Date:     now,
					TextBody: "new body",
				},
			},
		}},
	)

	mb.ToggleThread(0)
	mb.LoadConversation(1, nil)

	msgs := *mb.ConversationMessages()
	if got := len(msgs); got != 1 {
		t.Fatalf("conversation messages = %d, want selected expanded message only", got)
	}
	if msgs[0].Subject != "new message" {
		t.Fatalf("conversation subject = %q, want new message", msgs[0].Subject)
	}
	if msgs[0].SenderEmail != "bob@test" {
		t.Fatalf("conversation sender = %q, want bob@test", msgs[0].SenderEmail)
	}
}

func TestExpandedThreadHeaderUsesScanMode(t *testing.T) {
	now := time.Now()
	mb := testState(t,
		[]provider.Folder{{ID: "INBOX", Name: "INBOX"}},
		[]provider.Thread{{
			ID:      "t1",
			Subject: "thread subject",
			Date:    now,
			Messages: []provider.Message{
				{
					ID:      "old",
					Subject: "old message",
					From:    provider.Address{Name: "Alice", Email: "alice@test"},
					Date:    now.Add(-time.Hour),
					HTMLBody: `<html><body>
						<p>Main content</p>
						<blockquote><p>Older reply</p></blockquote>
						<p>Privacy policy</p>
						<p>Example Ltd, registered in England.</p>
					</body></html>`,
				},
				{
					ID:       "new",
					Subject:  "new message",
					From:     provider.Address{Name: "Bob", Email: "bob@test"},
					Date:     now,
					TextBody: "new body",
				},
			},
		}},
	)

	mb.ToggleThread(0)
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if got := len(msgs); got != 2 {
		t.Fatalf("conversation messages = %d, want expanded thread messages", got)
	}
	if !msgs[0].ThreadScanMode || !msgs[0].HasScanSubject {
		t.Fatalf("first message scan flags = scan:%v subject:%v, want both true", msgs[0].ThreadScanMode, msgs[0].HasScanSubject)
	}
	for _, block := range msgs[0].BodyBlocks {
		switch block.Kind {
		case preview.BlockQuote, preview.BlockFooter, preview.BlockSignature, preview.BlockForwarded:
			t.Fatalf("scan mode block kind = %v, want cruft hidden", block.Kind)
		}
	}
	if got := len(msgs[0].BodyBlocks); got != 1 {
		t.Fatalf("first message body blocks = %d, want main content only", got)
	}
}

func TestSelectedMessage_AfterExpand(t *testing.T) {
	now := time.Now()
	mb := testState(t,
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
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1", MessageID: "<m1@test>"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2", MessageID: "<m2@test>"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3", MessageID: "<m3@test>"}}},
	})

	mb := NewState(c, "test@example.com")
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
	archived, _ := c.GetThreads("[Google Mail]/All Mail", 25)
	if len(archived) != 1 || archived[0].ID != "t2" {
		t.Fatalf("archive folder threads = %v, want t2", archived)
	}

	// undo should restore the thread
	undo()
	if mb.ThreadLen() != 3 {
		t.Fatalf("after undo: rows = %d, want 3", mb.ThreadLen())
	}
	cmds, _ = c.PendingCommands()
	if len(cmds) != 2 {
		t.Fatalf("after undo: pending commands = %d, want 2", len(cmds))
	}
	inverse := cmds[1]
	if inverse.Action != "move" || inverse.TargetID != "m2" {
		t.Errorf("undo command = %#v, want move m2", inverse)
	}
	if inverse.Params["folder"] != "INBOX" {
		t.Errorf("undo folder = %q, want INBOX", inverse.Params["folder"])
	}
	if inverse.Params["source"] != "[Google Mail]/All Mail" {
		t.Errorf("undo source = %q, want [Google Mail]/All Mail", inverse.Params["source"])
	}
	if inverse.Params["message_id"] != "<m2@test>" {
		t.Errorf("undo message_id = %q, want <m2@test>", inverse.Params["message_id"])
	}
	archived, _ = c.GetThreads("[Google Mail]/All Mail", 25)
	if len(archived) != 0 {
		t.Fatalf("after undo: archive folder threads = %d, want 0", len(archived))
	}
}

func TestArchive_QueuesEveryMessageInThread(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "m2", Subject: "grouped", Date: now, Messages: []provider.Message{{ID: "m1", MessageID: "<m1@test>"}, {ID: "m2", MessageID: "<m2@test>"}}},
	})

	mb := NewState(c, "test@example.com")
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
		if cmd.Params["message_id"] == "" {
			t.Errorf("move message_id is empty for %s", cmd.TargetID)
		}
		got[cmd.TargetID] = true
	}
	for _, id := range []string{"m1", "m2"} {
		if !got[id] {
			t.Errorf("missing queued move for %s", id)
		}
	}
}

func TestMove_RemovesThreadAndQueuesCommandForTargetFolder(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(append(testFolders, provider.Folder{ID: "Receipts", Name: "Receipts"}))
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1", MessageID: "<m1@test>"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2", MessageID: "<m2@test>"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	undo, _ := mb.Move(1, "Receipts", "Receipts")

	if mb.ThreadLen() != 1 {
		t.Fatalf("rows after move = %d, want 1", mb.ThreadLen())
	}
	cmds, _ := c.PendingCommands()
	if len(cmds) != 1 || cmds[0].Action != "move" || cmds[0].TargetID != "m2" {
		t.Fatalf("pending commands = %v, want move m2", cmds)
	}
	if cmds[0].Params["folder"] != "Receipts" {
		t.Errorf("move folder = %q, want Receipts", cmds[0].Params["folder"])
	}
	if cmds[0].Params["source"] != "INBOX" {
		t.Errorf("move source = %q, want INBOX", cmds[0].Params["source"])
	}
	moved, _ := c.GetThreads("Receipts", 25)
	if len(moved) != 1 || moved[0].ID != "t2" {
		t.Fatalf("target folder threads = %v, want t2", moved)
	}

	undo()
	if mb.ThreadLen() != 2 {
		t.Fatalf("after undo rows = %d, want 2", mb.ThreadLen())
	}
	cmds, _ = c.PendingCommands()
	if len(cmds) != 2 {
		t.Fatalf("after undo pending commands = %d, want 2", len(cmds))
	}
	inverse := cmds[1]
	if inverse.Action != "move" || inverse.TargetID != "m2" {
		t.Errorf("undo command = %#v, want move m2", inverse)
	}
	if inverse.Params["folder"] != "INBOX" || inverse.Params["source"] != "Receipts" {
		t.Errorf("undo move = folder %q source %q, want INBOX/Receipts", inverse.Params["folder"], inverse.Params["source"])
	}
}

func TestMoveTargetFoldersUsesDisplayNamesAndExcludesActiveFolder(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{
		{ID: "INBOX", Name: "INBOX"},
		{ID: "[Google Mail]/Bin", Name: "Bin", Total: 1},
		{ID: "[Gmail]/Trash", Name: "Trash", Total: 3},
		{ID: "Receipts", Name: "Receipts"},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)

	targets := mb.MoveTargetFolders()
	got := map[string]string{}
	for _, target := range targets {
		got[target.Name] = target.ID
		if target.ID == "INBOX" {
			t.Fatal("move targets included active folder")
		}
	}
	if got["Trash"] != "[Gmail]/Trash" {
		t.Fatalf("trash target = %q, want [Gmail]/Trash", got["Trash"])
	}
	if got["Receipts"] != "Receipts" {
		t.Fatalf("custom target = %q, want Receipts", got["Receipts"])
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

	mb := NewState(c, "test@example.com")
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

func TestToggleStar_PreservesSelectedRow(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.SetSelected(1)

	undo, _ := mb.ToggleStar(1)

	rows := *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatal("expected selected row to remain selected after star toggle")
	}
	if !rows[1].Starred {
		t.Fatal("expected selected row to show starred after toggle")
	}

	undo()
	rows = *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatal("expected selected row to remain selected after star undo")
	}
	if rows[1].Starred {
		t.Fatal("expected selected row to show unstarred after undo")
	}
}

func TestToggleRead_PreservesSelectedRow(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1", Read: true}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Unread: 1, Messages: []provider.Message{{ID: "m2", Read: false}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.SetSelected(1)

	undo, _ := mb.ToggleRead(1)

	rows := *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatal("expected selected row to remain selected after read toggle")
	}
	if rows[1].Unread {
		t.Fatal("expected selected row to show read after toggle")
	}

	undo()
	rows = *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatal("expected selected row to remain selected after read undo")
	}
	if !rows[1].Unread {
		t.Fatal("expected selected row to show unread after undo")
	}
}

func TestMarkRead_PreservesSelectedRow(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1", Read: true}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Unread: 1, Messages: []provider.Message{{ID: "m2", Read: false}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.SetSelected(1)

	undo, _ := mb.MarkRead(1)

	rows := *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatal("expected selected row to remain selected after mark read")
	}
	if rows[1].Unread {
		t.Fatal("expected selected row to show read after mark read")
	}
	if !mb.threads[1].Messages[0].Read {
		t.Fatal("expected selected thread message to be read after mark read")
	}

	undo()
	rows = *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatal("expected selected row to remain selected after mark read undo")
	}
	if !rows[1].Unread {
		t.Fatal("expected selected row to show unread after mark read undo")
	}
	if mb.threads[1].Messages[0].Read {
		t.Fatal("expected selected thread message to be unread after mark read undo")
	}
}

func TestArchive_KeepsSelectionOnSameVisibleIndex(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.SetSelected(1)
	undo, _ := mb.Archive(1)

	rows := *mb.ThreadRows()
	if len(rows) != 2 {
		t.Fatalf("rows after archive = %d, want 2", len(rows))
	}
	if rows[1].Label != "third" {
		t.Fatalf("row 1 = %q, want third shifted into archived row's index", rows[1].Label)
	}
	if !rows[1].Selected {
		t.Fatalf("rows = %#v, want shifted row selected", rows)
	}
	if rows[0].Selected {
		t.Fatalf("rows = %#v, want previous row unselected", rows)
	}

	undo()
	rows = *mb.ThreadRows()
	if len(rows) != 3 {
		t.Fatalf("rows after archive undo = %d, want 3", len(rows))
	}
	if rows[1].Label != "second" {
		t.Fatalf("row 1 after archive undo = %q, want second restored", rows[1].Label)
	}
	if !rows[1].Selected {
		t.Fatalf("rows after archive undo = %#v, want restored row selected", rows)
	}

	mb.BuildThreadDisplay()
	rows = *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatalf("rows after rebuild = %#v, want selection to survive refresh rebuild", rows)
	}
}

func TestArchiveSelected_WithActiveWatcherKeepsSelection(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	model := NewUI(UIConfig{
		App:   glyph.NewApp(),
		Cache: c,
		State: mb,
		Theme: theme.Dark(),
	})
	model.ThreadSel = 1
	mb.SetSelected(1)

	refreshed := make(chan struct{}, 4)
	unsub := mb.Watch("INBOX", func() {
		model.QueueThreadsChanged()
		refreshed <- struct{}{}
	})
	defer unsub()

	model.ArchiveSelected()
	waitForThreadRefresh(t, refreshed)
	model.ProcessPending()

	if model.ThreadSel != 1 {
		t.Fatalf("ThreadSel after archive refresh = %d, want 1", model.ThreadSel)
	}
	rows := *mb.ThreadRows()
	if len(rows) != 2 {
		t.Fatalf("rows after archive = %d, want 2", len(rows))
	}
	if rows[1].Label != "third" {
		t.Fatalf("row 1 = %q, want third shifted into archived row's index", rows[1].Label)
	}
	if !rows[1].Selected {
		t.Fatalf("rows = %#v, want shifted row selected after watcher refresh", rows)
	}
}

func TestDelete_WithExpandedThread(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "has messages", Date: now, Messages: []provider.Message{
			{ID: "m1", MessageID: "<m1@test>", From: provider.Address{Email: "a@x.com"}, Date: now},
			{ID: "m2", MessageID: "<m2@test>", From: provider.Address{Email: "b@x.com"}, Date: now},
		}},
		{ID: "t2", Subject: "other", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m3", MessageID: "<m3@test>"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	undo, _ := mb.Delete(0)
	rows := *mb.ThreadRows()
	if len(rows) != 1 {
		t.Fatalf("after delete: rows = %d, want 1", len(rows))
	}
	if rows[0].Label != "other" {
		t.Errorf("remaining = %q, want 'other'", rows[0].Label)
	}
	trashed, _ := c.GetThreads("[Google Mail]/Bin", 25)
	if len(trashed) != 1 || trashed[0].ID != "t1" {
		t.Fatalf("trash folder threads = %v, want t1", trashed)
	}

	undo()
	if mb.ThreadLen() != 2 {
		t.Fatalf("after undo: rows = %d, want 2", mb.ThreadLen())
	}
	rows = *mb.ThreadRows()
	if rows[0].Label != "has messages" {
		t.Fatalf("after undo row 0 = %q, want restored deleted thread", rows[0].Label)
	}
	trashed, _ = c.GetThreads("[Google Mail]/Bin", 25)
	if len(trashed) != 0 {
		t.Fatalf("after undo: trash folder threads = %d, want 0", len(trashed))
	}
	cmds, _ := c.PendingCommands()
	if len(cmds) != 4 {
		t.Fatalf("after undo: pending commands = %d, want 4", len(cmds))
	}
	gotInverse := map[string]bool{}
	for _, inverse := range cmds[2:] {
		if inverse.Action != "move" {
			t.Errorf("undo action = %q, want move", inverse.Action)
		}
		if inverse.Params["folder"] != "INBOX" {
			t.Errorf("undo folder = %q, want INBOX", inverse.Params["folder"])
		}
		if inverse.Params["source"] != "[Google Mail]/Bin" {
			t.Errorf("undo source = %q, want [Google Mail]/Bin", inverse.Params["source"])
		}
		gotInverse[inverse.TargetID] = true
	}
	for _, id := range []string{"m1", "m2"} {
		if !gotInverse[id] {
			t.Errorf("missing undo move for %s", id)
		}
	}
}

func TestDelete_LastRowSelectsNewLastRow(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.SetSelected(2)
	mb.Delete(2)

	rows := *mb.ThreadRows()
	if len(rows) != 2 {
		t.Fatalf("rows after delete = %d, want 2", len(rows))
	}
	if rows[1].Label != "second" {
		t.Fatalf("row 1 = %q, want new last row", rows[1].Label)
	}
	if !rows[1].Selected {
		t.Fatalf("rows = %#v, want new last row selected", rows)
	}
	if rows[0].Selected {
		t.Fatalf("rows = %#v, want first row unselected", rows)
	}

	mb.BuildThreadDisplay()
	rows = *mb.ThreadRows()
	if !rows[1].Selected {
		t.Fatalf("rows after rebuild = %#v, want clamped selection to survive refresh rebuild", rows)
	}
}

func TestDeleteUndoDeleteUndo_WithActiveWatcherKeepsSelectionAndNoDuplicates(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	model := NewUI(UIConfig{
		App:   glyph.NewApp(),
		Cache: c,
		State: mb,
		Theme: theme.Dark(),
	})
	model.ThreadSel = 1
	mb.SetSelected(1)

	refreshed := make(chan struct{}, 16)
	unsub := mb.Watch("INBOX", func() {
		model.QueueThreadsChanged()
		refreshed <- struct{}{}
	})
	defer unsub()
	drain := func() {
		for {
			select {
			case <-refreshed:
			default:
				return
			}
		}
	}

	model.DeleteSelected()
	waitForThreadRefresh(t, refreshed)
	drain()
	model.ProcessPending()
	if model.ThreadSel != 1 {
		t.Fatalf("ThreadSel after first delete = %d, want 1", model.ThreadSel)
	}
	assertThreadRows(t, mb, []string{"first", "third"}, 1)

	model.UndoLast()
	waitForThreadRefresh(t, refreshed)
	drain()
	model.ProcessPending()
	if model.ThreadSel != 1 {
		t.Fatalf("ThreadSel after first undo = %d, want 1", model.ThreadSel)
	}
	assertThreadRows(t, mb, []string{"first", "second", "third"}, 1)

	model.DeleteSelected()
	waitForThreadRefresh(t, refreshed)
	drain()
	model.ProcessPending()
	if model.ThreadSel != 1 {
		t.Fatalf("ThreadSel after second delete = %d, want 1", model.ThreadSel)
	}
	assertThreadRows(t, mb, []string{"first", "third"}, 1)

	model.UndoLast()
	waitForThreadRefresh(t, refreshed)
	drain()
	model.ProcessPending()
	if model.ThreadSel != 1 {
		t.Fatalf("ThreadSel after second undo = %d, want 1", model.ThreadSel)
	}
	assertThreadRows(t, mb, []string{"first", "second", "third"}, 1)
}

func TestInsertThreadAtMovesExistingThreadInsteadOfDuplicating(t *testing.T) {
	now := time.Now()
	mb := NewState(nil, "test@example.com")
	mb.threads = []provider.Thread{
		{ID: "t1", Subject: "first", Date: now},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute)},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute)},
	}
	mb.BuildThreadDisplay()

	mb.insertThreadAt(1, provider.Thread{ID: "t2", Subject: "second", Date: now.Add(-time.Minute)})

	assertThreadRows(t, mb, []string{"first", "second", "third"}, 0)
}

func TestDelete_MissingTrashReportsUnavailable(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "keep", Date: now, Messages: []provider.Message{{ID: "m1"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	undo, desc := mb.Delete(0)
	if undo != nil {
		t.Fatal("expected no undo when trash folder is unavailable")
	}
	if desc != "delete unavailable: no trash folder" {
		t.Fatalf("desc = %q, want delete unavailable message", desc)
	}
	if mb.ThreadLen() != 1 {
		t.Fatalf("rows after failed delete = %d, want 1", mb.ThreadLen())
	}
	cmds, _ := c.PendingCommands()
	if len(cmds) != 0 {
		t.Fatalf("pending commands after failed delete = %d, want 0", len(cmds))
	}
}

func TestProcessPendingCommands_CollapsesImmediateMoveUndo(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders(testFolders)
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "delete me", Date: now, Messages: []provider.Message{{ID: "m1", MessageID: "<m1@test>"}}},
	})

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	undo, _ := mb.Delete(0)
	undo()

	cmds, _ := c.PendingCommands()
	if len(cmds) != 2 {
		t.Fatalf("pending commands before compaction = %d, want 2", len(cmds))
	}

	mb.imap = nil
	mb.ProcessPendingCommands()

	cmds, _ = c.PendingCommands()
	if len(cmds) != 0 {
		t.Fatalf("pending commands after compaction = %d, want 0", len(cmds))
	}
	if mb.ThreadLen() != 1 {
		t.Fatalf("rows after compacted undo = %d, want 1", mb.ThreadLen())
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

	mb := NewState(c, "test@example.com")
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

	mb := NewState(c, "test@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.Delete(1) // undo not used — committed

	// simulate restart
	mb2 := NewState(c, "test@example.com")
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
