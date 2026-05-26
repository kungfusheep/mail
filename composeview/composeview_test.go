package composeview

import (
	"testing"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/provider"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/transition"
)

func TestSetupReplyRepairsBlankDraftHeaders(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.PutDraft(cache.Draft{
		ThreadID: "thread-1",
		Body:     "existing reply body",
	}); err != nil {
		t.Fatalf("PutDraft: %v", err)
	}

	controls := setupTestCompose(t, db)
	controls.SetupReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:        "message-1",
			MessageID: "<message-1@example.test>",
			From:      provider.Address{Name: "Leica Store Manchester", Email: "hello@leicastoremanchester.com"},
			Subject:   "Leica Q343",
			TextBody:  "original message",
		}},
	})

	got, ok, err := db.GetDraft("thread-1")
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	if !ok {
		t.Fatal("draft missing after reply setup")
	}
	if got.To != "Leica Store Manchester <hello@leicastoremanchester.com>" {
		t.Fatalf("draft To = %q", got.To)
	}
	if got.Subject != "Re: Leica Q343" {
		t.Fatalf("draft Subject = %q", got.Subject)
	}
	if got.Body != "existing reply body\n" {
		t.Fatalf("draft Body = %q", got.Body)
	}
}

func TestSetupReplyPreservesExistingDraftHeaders(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.PutDraft(cache.Draft{
		ThreadID: "thread-1",
		To:       "custom@example.test",
		Subject:  "custom subject",
		Body:     "existing reply body",
	}); err != nil {
		t.Fatalf("PutDraft: %v", err)
	}

	controls := setupTestCompose(t, db)
	controls.SetupReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:      "message-1",
			From:    provider.Address{Name: "Leica Store Manchester", Email: "hello@leicastoremanchester.com"},
			Subject: "Leica Q343",
		}},
	})

	got, ok, err := db.GetDraft("thread-1")
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	if !ok {
		t.Fatal("draft missing after reply setup")
	}
	if got.To != "custom@example.test" {
		t.Fatalf("draft To = %q", got.To)
	}
	if got.Subject != "custom subject" {
		t.Fatalf("draft Subject = %q", got.Subject)
	}
}

func TestReplySubject(t *testing.T) {
	if got := replySubject("Leica Q343"); got != "Re: Leica Q343" {
		t.Fatalf("replySubject = %q", got)
	}
	if got := replySubject("Re: Leica Q343"); got != "Re: Leica Q343" {
		t.Fatalf("replySubject duplicated prefix: %q", got)
	}
}

func setupTestCompose(t *testing.T, db *cache.Cache) mailbox.ComposeControls {
	t.Helper()
	palette := theme.Dark()
	ed := compose.NewEditor(compose.NewDocument(), "")
	ed.SetTheme(theme.ComposeTheme(palette))
	return Setup(
		NewApp(),
		ed,
		mailbox.NewState(db, "me@example.test"),
		nil,
		db,
		func(string) {},
		new(int),
		transition.New(time.Millisecond, palette.BG, Hex(0x3a3a3a)),
		palette,
	)
}
