package mailruntime

import (
	"testing"
	"time"

	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/provider"
)

func TestRuntimeOfflineSyncRefreshesCacheProjection(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}}); err != nil {
		t.Fatal(err)
	}

	mb := mailbox.NewState(db, "me@example.test")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()

	threadsChanged := 0
	rendered := 0
	status := ""
	rt := New(db, mb, Config{}, Callbacks{
		Status: func(text string) {
			status = text
		},
		ThreadsChanged: func() {
			mb.BuildThreadDisplay()
			threadsChanged++
		},
		Render: func() {
			rendered++
		},
	})

	if err := db.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "fixture-1",
		Subject: "fixture thread",
		Date:    time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC),
		Messages: []provider.Message{{
			ID:       "msg-1",
			Subject:  "fixture thread",
			TextBody: "body",
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	rt.SyncActiveFolder()

	if threadsChanged != 1 {
		t.Fatalf("threadsChanged = %d, want 1", threadsChanged)
	}
	if rendered != 1 {
		t.Fatalf("rendered = %d, want 1", rendered)
	}
	if status != "cache refreshed" {
		t.Fatalf("status = %q, want cache refreshed", status)
	}
	rows := *mb.ThreadRows()
	if len(rows) != 1 {
		t.Fatalf("thread rows = %d, want 1", len(rows))
	}
	if rows[0].Label != "fixture thread" {
		t.Fatalf("thread row label = %q, want fixture thread", rows[0].Label)
	}
}
