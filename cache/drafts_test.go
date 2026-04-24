package cache

import (
	"testing"
	"time"
)

func memCache(t *testing.T) *Cache {
	t.Helper()
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// PutDraft writes a new draft row when none exists for the thread id.
func TestPutDraft_NewRow(t *testing.T) {
	c := memCache(t)
	if err := c.PutDraft(Draft{ThreadID: "", Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	d, found, err := c.GetDraft("")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("draft not found")
	}
	if d.Body != "hello" {
		t.Errorf("body = %q, want %q", d.Body, "hello")
	}
	if d.RemoteUID != "" {
		t.Errorf("remote_uid = %q, want empty on fresh row", d.RemoteUID)
	}
}

// PutDraft upserts the existing row when ThreadID matches.
func TestPutDraft_OverwritesSameThreadID(t *testing.T) {
	c := memCache(t)
	c.PutDraft(Draft{ThreadID: "", Body: "first"})
	c.PutDraft(Draft{ThreadID: "", Body: "second"})

	// cache should hold one row with the latest content
	d, found, _ := c.GetDraft("")
	if !found {
		t.Fatal("draft not found")
	}
	if d.Body != "second" {
		t.Errorf("body = %q, want %q (first should have been overwritten)", d.Body, "second")
	}
}

// PutDraft preserves RemoteUID across edits — we rely on ON CONFLICT
// upsert to leave remote_uid untouched so a subsequent sync_draft command
// still knows which server UID to expunge.
func TestPutDraft_PreservesRemoteUID(t *testing.T) {
	c := memCache(t)
	c.PutDraft(Draft{ThreadID: "", Body: "A"})
	if err := c.UpdateDraftRemoteUID("", "100"); err != nil {
		t.Fatal(err)
	}

	// second write with new body; remote_uid should survive
	c.PutDraft(Draft{ThreadID: "", Body: "B"})

	d, _, _ := c.GetDraft("")
	if d.Body != "B" {
		t.Errorf("body = %q, want %q", d.Body, "B")
	}
	if d.RemoteUID != "100" {
		t.Errorf("remote_uid = %q, want %q (should be preserved across PutDraft)", d.RemoteUID, "100")
	}
}

// Every PutDraft queues a sync_draft command with a stable id so rapid
// re-saves coalesce to a single pending command rather than queueing many.
func TestPutDraft_QueuesSyncCommand(t *testing.T) {
	c := memCache(t)
	c.PutDraft(Draft{ThreadID: "", Body: "A"})

	cmds, err := c.PendingCommands()
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 {
		t.Fatalf("pending commands = %d, want 1", len(cmds))
	}
	if cmds[0].Action != "sync_draft" {
		t.Errorf("action = %q, want sync_draft", cmds[0].Action)
	}
	if cmds[0].TargetID != "" {
		t.Errorf("target_id = %q, want empty (new compose)", cmds[0].TargetID)
	}
	if cmds[0].ID != "sync_draft-" {
		t.Errorf("id = %q, want %q (stable id for coalescing)", cmds[0].ID, "sync_draft-")
	}
}

// Rapid re-saves for the same thread should leave exactly one pending
// sync_draft command, not a queue of duplicates.
func TestPutDraft_CoalescesSyncCommands(t *testing.T) {
	c := memCache(t)
	c.PutDraft(Draft{ThreadID: "", Body: "A"})
	c.PutDraft(Draft{ThreadID: "", Body: "B"})
	c.PutDraft(Draft{ThreadID: "", Body: "C"})

	cmds, _ := c.PendingCommands()
	syncCount := 0
	for _, cmd := range cmds {
		if cmd.Action == "sync_draft" {
			syncCount++
		}
	}
	if syncCount != 1 {
		t.Errorf("sync_draft commands = %d, want 1 (should coalesce on stable id)", syncCount)
	}
}

// DeleteDraft queues a delete_draft command when the row had a remote_uid,
// so the server-side copy gets expunged on next ProcessPendingCommands run.
func TestDeleteDraft_QueuesDeleteCommand(t *testing.T) {
	c := memCache(t)
	c.PutDraft(Draft{ThreadID: "", Body: "A"})
	c.UpdateDraftRemoteUID("", "100")

	if err := c.DeleteDraft(""); err != nil {
		t.Fatal(err)
	}

	cmds, _ := c.PendingCommands()
	foundDelete := false
	for _, cmd := range cmds {
		if cmd.Action == "delete_draft" && cmd.TargetID == "100" {
			foundDelete = true
			if cmd.ID != "delete_draft-100" {
				t.Errorf("delete_draft id = %q, want %q", cmd.ID, "delete_draft-100")
			}
		}
	}
	if !foundDelete {
		t.Error("no delete_draft-100 command queued")
	}

	// draft row should be gone
	_, found, _ := c.GetDraft("")
	if found {
		t.Error("draft row still present after DeleteDraft")
	}
}

// SeedDraft is the path used by ResumeFromMessage when the user clicks an
// existing server draft — it preserves any local row unchanged and only
// writes a new row if none exists yet (INSERT OR IGNORE).
func TestSeedDraft_DoesNotOverwriteLocalEdits(t *testing.T) {
	c := memCache(t)

	// user has local unsynced edits for this thread id
	c.PutDraft(Draft{ThreadID: "200", Body: "local edits"})

	// SeedDraft from a server draft for the same id
	c.SeedDraft(Draft{
		ThreadID:  "200",
		RemoteUID: "200",
		Body:      "server body",
	})

	d, _, _ := c.GetDraft("200")
	if d.Body != "local edits" {
		t.Errorf("body = %q, want %q (local edits should win over seed)", d.Body, "local edits")
	}
}

func TestSeedDraft_FillsEmptyCacheFromServer(t *testing.T) {
	c := memCache(t)

	// no local row yet; server draft exists at UID 300
	c.SeedDraft(Draft{
		ThreadID:  "300",
		RemoteUID: "300",
		Body:      "server body",
	})

	d, found, _ := c.GetDraft("300")
	if !found {
		t.Fatal("seeded draft not found")
	}
	if d.Body != "server body" {
		t.Errorf("body = %q, want %q", d.Body, "server body")
	}
	if d.RemoteUID != "300" {
		t.Errorf("remote_uid = %q, want %q", d.RemoteUID, "300")
	}
}

// TestBugReport_SharedThreadIDLosesFirstDraft reproduces the user-reported
// bug at the cache layer. Quoting the report:
//
//	"i've just created a draft... created another draft, different content
//	(timestamp) gone back to the drafts view and both of the drafts content
//	have been updated to the latest one - so i've lost the first one and
//	have two of the second... editing it then doesn't show me the edit
//	until i move to a different folder and come back - then they've both
//	been edited"
//
// Root cause in one sentence: main.go was passing ThreadID="" for every
// new compose session, so PutDraft's upsert collapsed both drafts into
// one cache row — keeping the second body, losing the first.
//
// This test locks in the "when both parameters are '', only one row
// survives" behaviour so any future regression shows up loudly.
func TestBugReport_SharedThreadIDLosesFirstDraft(t *testing.T) {
	c := memCache(t)

	// reproduce the old buggy call pattern: pass "" for both composes
	const buggyID = ""

	c.PutDraft(Draft{ThreadID: buggyID, Body: "draft 1 body"})
	c.UpdateDraftRemoteUID(buggyID, "100")

	c.PutDraft(Draft{ThreadID: buggyID, Body: "draft 2 body"})

	// bug signature: only one cache row; the first body is gone
	if n := countDrafts(t, c); n != 1 {
		t.Fatalf("rows = %d, want 1 (shared-slot collapsed both drafts)", n)
	}
	d, _, _ := c.GetDraft(buggyID)
	if d.Body != "draft 2 body" {
		t.Errorf("surviving body = %q, want 'draft 2 body' (first draft lost)", d.Body)
	}
	if d.Body == "draft 1 body" {
		t.Error("first draft survived somehow — the shared-slot bug is no longer present, update this test or the cache logic changed")
	}
}

// TestBugFix_UniqueThreadIDsPreserveDrafts proves the shape of the fix:
// when main.go gives each new compose a unique thread_id (e.g.
// "new-<unix-nano>"), both drafts get their own cache row AND their own
// remote_uid, and editing one never touches the other.
func TestBugFix_UniqueThreadIDsPreserveDrafts(t *testing.T) {
	c := memCache(t)

	// two new composes → two distinct thread_ids (simulates the fix)
	const tid1, tid2 = "new-t1", "new-t2"

	c.PutDraft(Draft{ThreadID: tid1, Body: "draft 1"})
	c.UpdateDraftRemoteUID(tid1, "100")
	c.PutDraft(Draft{ThreadID: tid2, Body: "draft 2"})
	c.UpdateDraftRemoteUID(tid2, "101")

	if n := countDrafts(t, c); n != 2 {
		t.Fatalf("rows = %d, want 2 (both drafts must survive)", n)
	}

	d1, _, _ := c.GetDraft(tid1)
	if d1.Body != "draft 1" || d1.RemoteUID != "100" {
		t.Errorf("draft 1: body=%q ruid=%q, want 'draft 1'/100", d1.Body, d1.RemoteUID)
	}
	d2, _, _ := c.GetDraft(tid2)
	if d2.Body != "draft 2" || d2.RemoteUID != "101" {
		t.Errorf("draft 2: body=%q ruid=%q, want 'draft 2'/101", d2.Body, d2.RemoteUID)
	}

	// editing one must not affect the other — the user's "both got edited"
	// symptom only happened because they shared the same cache row
	c.PutDraft(Draft{ThreadID: tid1, Body: "draft 1 edited"})
	d1, _, _ = c.GetDraft(tid1)
	d2, _, _ = c.GetDraft(tid2)
	if d1.Body != "draft 1 edited" {
		t.Errorf("draft 1 post-edit: %q, want 'draft 1 edited'", d1.Body)
	}
	if d2.Body != "draft 2" {
		t.Errorf("draft 2 post-edit-of-1: %q, want unchanged 'draft 2'", d2.Body)
	}
}

// Simulates the path taken when the user clicks an existing server draft
// in the Drafts folder view. Each clicked message gets its own cache row
// keyed by the server UID — they shouldn't interfere with each other.
func TestScenario_ClickDifferentServerDrafts(t *testing.T) {
	c := memCache(t)

	// user clicks draft at UID 500 — SeedDraft creates a row keyed by UID
	c.SeedDraft(Draft{ThreadID: "500", RemoteUID: "500", Body: "body-500"})

	// user clicks draft at UID 501 — separate row
	c.SeedDraft(Draft{ThreadID: "501", RemoteUID: "501", Body: "body-501"})

	// edit the 500 draft
	c.PutDraft(Draft{ThreadID: "500", Body: "edited-500"})

	// verify: only 500's cache row was affected
	d, _, _ := c.GetDraft("500")
	if d.Body != "edited-500" {
		t.Errorf("500 body = %q, want edited-500", d.Body)
	}
	d, _, _ = c.GetDraft("501")
	if d.Body != "body-501" {
		t.Errorf("501 body = %q, want body-501 (SHOULD NOT HAVE BEEN TOUCHED)", d.Body)
	}

	if n := countDrafts(t, c); n != 2 {
		t.Errorf("cache has %d rows, want 2 (one per clicked draft)", n)
	}
}

// ListDrafts returns all rows ordered by updated_at DESC so the Drafts
// folder view can project straight from it.
func TestListDrafts_OrderedByMostRecentlyUpdated(t *testing.T) {
	c := memCache(t)

	c.PutDraft(Draft{ThreadID: "draft-a", Body: "oldest"})
	// small wait so updated_at (second-resolution) differs between rows
	c.db.Exec("UPDATE drafts SET updated_at = updated_at - 10 WHERE thread_id = 'draft-a'")
	c.PutDraft(Draft{ThreadID: "draft-b", Body: "middle"})
	c.db.Exec("UPDATE drafts SET updated_at = updated_at - 5 WHERE thread_id = 'draft-b'")
	c.PutDraft(Draft{ThreadID: "draft-c", Body: "newest"})

	drafts, err := c.ListDrafts()
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 3 {
		t.Fatalf("got %d drafts, want 3", len(drafts))
	}
	if drafts[0].ThreadID != "draft-c" || drafts[2].ThreadID != "draft-a" {
		t.Errorf("order wrong: %v (want [draft-c, draft-b, draft-a])",
			[]string{drafts[0].ThreadID, drafts[1].ThreadID, drafts[2].ThreadID})
	}
}

// FindDraftByRemoteUID bridges the "server UID" namespace back to the
// stable local id — sync uses it to decide whether a server-side draft
// is already tracked locally.
func TestFindDraftByRemoteUID(t *testing.T) {
	c := memCache(t)

	_ = c.SeedDraft(Draft{ThreadID: "draft-xyz", Subject: "hi", Body: "body", RemoteUID: "42"})

	got, found, err := c.FindDraftByRemoteUID("42")
	if err != nil || !found {
		t.Fatalf("find failed: err=%v found=%v", err, found)
	}
	if got.ThreadID != "draft-xyz" {
		t.Errorf("thread_id = %q, want draft-xyz", got.ThreadID)
	}

	if _, found, _ := c.FindDraftByRemoteUID("99"); found {
		t.Error("found draft for unknown remote uid — reconciliation would wrongly skip adoption")
	}
	if _, found, _ := c.FindDraftByRemoteUID(""); found {
		t.Error("empty remote_uid matched rows — locally-only drafts would be wrongly treated as server-tracked")
	}
}

// DraftRemoteUIDs feeds the "what's tracked locally?" side of reconciliation.
// Empty remote_uid means never-synced, and MUST not leak into the set.
func TestDraftRemoteUIDs_ExcludesUnsynced(t *testing.T) {
	c := memCache(t)

	_ = c.PutDraft(Draft{ThreadID: "draft-unsynced", Body: "never-sent"}) // no remote_uid
	_ = c.SeedDraft(Draft{ThreadID: "draft-synced", Body: "server-known", RemoteUID: "42"})

	uids, err := c.DraftRemoteUIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(uids) != 1 || !uids["42"] {
		t.Errorf("uids = %v, want {42}", uids)
	}
}

// BackfillDraftContent is the reconcile-time path for filling in body /
// recipients that ListThreads didn't return. Must update without queueing
// a sync (the server already has this content) and must publish so the
// UI refreshes.
func TestBackfillDraftContent_UpdatesAndPublishes(t *testing.T) {
	c := memCache(t)
	c.SetDraftsLabel("drafts")
	ch, unsub := c.Subscribe("drafts")
	defer unsub()

	// seed an adopted-but-bodyless row, drain the seed publish
	_ = c.SeedDraft(Draft{ThreadID: "draft-x", Subject: "from server", Body: "", RemoteUID: "42"})
	<-ch

	serverDate := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	if err := c.BackfillDraftContent("draft-x", "to@example.com", "", "", "from server", "the real body", serverDate); err != nil {
		t.Fatal(err)
	}

	got, _, _ := c.GetDraft("draft-x")
	if got.Body != "the real body" {
		t.Errorf("body = %q, want \"the real body\"", got.Body)
	}
	if got.To != "to@example.com" {
		t.Errorf("to = %q, want \"to@example.com\"", got.To)
	}

	// must NOT queue a sync_draft — backfill is downstream, not an edit
	cmds, _ := c.PendingCommands()
	for _, cmd := range cmds {
		if cmd.Action == "sync_draft" && cmd.TargetID == "draft-x" {
			t.Error("backfill queued a sync_draft — will cause the client to PUSH the same content back, creating a loop")
		}
	}

	// must publish so subscribers re-render
	select {
	case <-ch:
	default:
		t.Error("backfill did not publish — preview pane stays empty after reconcile fills in bodies")
	}
}

func TestDeleteDraftByRemoteUID(t *testing.T) {
	c := memCache(t)
	_ = c.SeedDraft(Draft{ThreadID: "draft-x", Body: "x", RemoteUID: "42"})
	_ = c.SeedDraft(Draft{ThreadID: "draft-y", Body: "y", RemoteUID: "43"})

	if err := c.DeleteDraftByRemoteUID("42"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := c.GetDraft("draft-x"); found {
		t.Error("draft-x should have been deleted when its remote copy was pruned")
	}
	if _, found, _ := c.GetDraft("draft-y"); !found {
		t.Error("draft-y was wrongly deleted — DeleteDraftByRemoteUID should only touch the matched row")
	}
}

func countDrafts(t *testing.T, c *Cache) int {
	t.Helper()
	var n int
	err := c.db.QueryRow("SELECT COUNT(*) FROM drafts").Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
