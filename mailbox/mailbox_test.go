package mailbox

import (
	"testing"
	"time"

	"github.com/kungfusheep/mail/cache"
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

func testMailbox(t *testing.T, folders []provider.Folder, threads []provider.Thread) *Mailbox {
	t.Helper()
	c := testCache(t)
	c.PutFolders(folders)
	if len(threads) > 0 && len(folders) > 0 {
		c.ReplaceThreads(folders[0].ID, threads)
	}
	mb := New(c)
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
	mb := New(c)
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
	mb := New(c)
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
	mb := New(c)
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
	msg := mb.SelectedMessage(2)
	if msg == nil {
		t.Fatal("expected message at row 2")
	}
	if msg.ID != "m2" {
		t.Errorf("message ID = %q, want m2", msg.ID)
	}
}

// action tests

func TestArchive_RemovesThreadAndQueuesCommand(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := New(c)
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
	if len(cmds) != 1 || cmds[0].Action != "archive" || cmds[0].TargetID != "t2" {
		t.Errorf("pending commands = %v, want archive t2", cmds)
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

func TestToggleRead_UpdatesDisplay(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "unread", Date: now, Unread: 2, Messages: []provider.Message{
			{ID: "m1", Read: false},
			{ID: "m2", Read: false},
		}},
	})

	mb := New(c)
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
}

func TestDelete_WithExpandedThread(t *testing.T) {
	now := time.Now()
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "has messages", Date: now, Messages: []provider.Message{
			{ID: "m1", From: provider.Address{Email: "a@x.com"}, Date: now},
			{ID: "m2", From: provider.Address{Email: "b@x.com"}, Date: now},
		}},
		{ID: "t2", Subject: "other", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := New(c)
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
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "first", Date: now, Messages: []provider.Message{{ID: "m1"}}},
		{ID: "t2", Subject: "second", Date: now.Add(-time.Minute), Messages: []provider.Message{{ID: "m2"}}},
		{ID: "t3", Subject: "third", Date: now.Add(-2 * time.Minute), Messages: []provider.Message{{ID: "m3"}}},
	})

	mb := New(c)
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
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "t1", Subject: "keep", Date: now},
		{ID: "t2", Subject: "delete me", Date: now.Add(-time.Hour)},
	})

	mb := New(c)
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.SelectFolder(0)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	mb.Delete(1) // undo not used — committed

	// simulate restart
	mb2 := New(c)
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
	c.PutThread("INBOX", provider.Thread{
		ID: "old", Subject: "stale thread", Date: time.Now().Add(-24 * time.Hour),
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
	c.PutThread("INBOX", provider.Thread{ID: "inbox1", Subject: "inbox", Date: time.Now()})
	c.PutThread("SENT", provider.Thread{ID: "sent1", Subject: "sent", Date: time.Now()})

	c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "inbox2", Subject: "new inbox", Date: time.Now()},
	})

	sent, _ := c.GetThreads("SENT", 25)
	if len(sent) != 1 || sent[0].ID != "sent1" {
		t.Errorf("SENT affected by INBOX replace: %v", sent)
	}
}
