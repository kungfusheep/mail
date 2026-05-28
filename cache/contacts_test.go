package cache

import (
	"testing"
	"time"

	"github.com/kungfusheep/mail/provider"
)

func TestSearchContactsRanksByRecentMail(t *testing.T) {
	c := memCache(t)

	oldDate := time.Unix(100, 0)
	recentDate := time.Unix(300, 0)
	if err := c.PutContacts([]provider.Address{
		{Name: "Old Contact", Email: "old@example.test"},
		{Name: "Recent Contact", Email: "recent@example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutMessage(provider.Message{
		ID:        "old-message",
		ThreadID:  "old-thread",
		From:      provider.Address{Name: "Old Contact", Email: "old@example.test"},
		Date:      oldDate,
		MessageID: "<old@example.test>",
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutMessage(provider.Message{
		ID:        "recent-message",
		ThreadID:  "recent-thread",
		From:      provider.Address{Name: "Recent Contact", Email: "recent@example.test"},
		Date:      recentDate,
		MessageID: "<recent@example.test>",
	}); err != nil {
		t.Fatal(err)
	}

	results, err := c.SearchContacts("example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("contacts = %d, want at least 2", len(results))
	}
	if results[0].Email != "recent@example.test" {
		t.Fatalf("first contact = %q, want recent@example.test", results[0].Email)
	}
}

func TestRebuildContactIndexIncludesThreadsSentAndDrafts(t *testing.T) {
	c := memCache(t)

	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "thread-1",
		Subject: "hello",
		Date:    time.Unix(200, 0),
		Messages: []provider.Message{{
			ID:        "message-1",
			MessageID: "<message-1@example.test>",
			From:      provider.Address{Name: "Thread Person", Email: "thread@example.test"},
			Date:      time.Unix(200, 0),
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutSentMessage(provider.Message{
		ID:        "sent-1",
		MessageID: "<sent-1@example.test>",
		From:      provider.Address{Name: "Me", Email: "me@example.test"},
		To:        []provider.Address{{Name: "Sent Person", Email: "sent@example.test"}},
		Date:      time.Unix(300, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.SeedDraft(Draft{
		ThreadID:  "draft-1",
		To:        "Draft Person <draft@example.test>",
		Subject:   "draft",
		RemoteUID: "42",
		UpdatedAt: time.Unix(400, 0),
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.RebuildContactIndex(); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"thread@example.test", "sent@example.test", "draft@example.test"} {
		results, err := c.SearchContacts(query)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 {
			t.Fatalf("SearchContacts(%q) = %d results, want 1", query, len(results))
		}
		if results[0].Email != query {
			t.Fatalf("SearchContacts(%q) = %q, want same email", query, results[0].Email)
		}
	}
}

func TestPutContactsDoesNotClobberMailRecency(t *testing.T) {
	c := memCache(t)

	if err := c.PutMessage(provider.Message{
		ID:        "message-1",
		ThreadID:  "thread-1",
		MessageID: "<message-1@example.test>",
		From:      provider.Address{Name: "Mail Name", Email: "recent@example.test"},
		Date:      time.Unix(500, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutMessage(provider.Message{
		ID:        "message-2",
		ThreadID:  "thread-2",
		MessageID: "<message-2@example.test>",
		From:      provider.Address{Name: "Other Name", Email: "other@example.test"},
		Date:      time.Unix(300, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutContacts([]provider.Address{{Name: "Contacts Name", Email: "recent@example.test"}}); err != nil {
		t.Fatal(err)
	}

	results, err := c.SearchContacts("example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("contacts = %d, want at least 2", len(results))
	}
	if results[0].Email != "recent@example.test" {
		t.Fatalf("first contact = %q, want recent@example.test", results[0].Email)
	}
	if results[0].Name != "Contacts Name" {
		t.Fatalf("first contact name = %q, want Contacts Name", results[0].Name)
	}
}
