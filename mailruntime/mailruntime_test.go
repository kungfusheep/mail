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

func TestRuntimeDesktopNotificationsOnlyForNewInboxThreads(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}}); err != nil {
		t.Fatal(err)
	}
	existing := provider.Thread{
		ID:      "old",
		Subject: "already cached",
		Date:    time.Date(2026, 5, 29, 8, 0, 0, 0, time.UTC),
		Messages: []provider.Message{{
			ID:      "old-msg",
			Subject: "already cached",
			From:    provider.Address{Name: "Old Sender", Email: "old@example.test"},
		}},
	}
	if err := db.ReplaceThreads("INBOX", []provider.Thread{existing}); err != nil {
		t.Fatal(err)
	}

	mb := mailbox.NewState(db, "me@example.test")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)

	type notice struct {
		title string
		body  string
	}
	notices := make(chan notice, 2)
	rt := New(db, mb, Config{}, Callbacks{
		DesktopNotifyMail: func(title, body string) {
			notices <- notice{title: title, body: body}
		},
	})
	rt.Start()
	t.Cleanup(rt.Close)

	select {
	case got := <-notices:
		t.Fatalf("unexpected startup notification: %#v", got)
	case <-time.After(30 * time.Millisecond):
	}

	newThread := provider.Thread{
		ID:      "new",
		Subject: "fresh invoice",
		Date:    time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC),
		Messages: []provider.Message{{
			ID:      "new-msg",
			Subject: "fresh invoice",
			From:    provider.Address{Name: "Acme Billing", Email: "billing@example.test"},
		}},
	}
	if err := db.ReplaceThreads("INBOX", []provider.Thread{newThread, existing}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-notices:
		if got.title != "Acme Billing" || got.body != "fresh invoice" {
			t.Fatalf("notification = %#v, want Acme Billing/fresh invoice", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no desktop notification for new inbox thread")
	}

	select {
	case got := <-notices:
		t.Fatalf("unexpected duplicate notification: %#v", got)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestInboxNotificationTextFallsBackCleanly(t *testing.T) {
	title, body := inboxNotificationText(provider.Thread{})
	if title != "new email" || body != "(no subject)" {
		t.Fatalf("fallback notification = %q/%q, want new email/(no subject)", title, body)
	}
}

func TestSenderIdentityFreshRequiresColourSamplingForColourlessRows(t *testing.T) {
	now := time.Now()
	maxAge := 14 * 24 * time.Hour

	if senderIdentityFresh(cache.SenderIdentity{
		ThemeColor: "#123456",
		UpdatedAt:  now.Add(-time.Hour),
	}, maxAge) != true {
		t.Fatal("fresh theme colour row should not refresh")
	}

	if senderIdentityFresh(cache.SenderIdentity{
		UpdatedAt:      now.Add(-time.Hour),
		ColorCheckedAt: now.Add(-time.Hour),
	}, maxAge) != true {
		t.Fatal("fresh colourless row with recent colour check should not refresh")
	}

	if senderIdentityFresh(cache.SenderIdentity{
		UpdatedAt: now.Add(-time.Hour),
	}, maxAge) != false {
		t.Fatal("fresh colourless row without colour check should refresh once")
	}
}
