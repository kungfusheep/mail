package cache

import (
	"testing"
	"time"

	"github.com/kungfusheep/mail/provider"
)

func TestConversationIndexFollowsReplyHeaders(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	now := time.Now()
	original := provider.Message{
		ID:        "m1",
		MessageID: "<m1@test>",
		Subject:   "project update",
		From:      provider.Address{Name: "Alice", Email: "alice@test"},
		Date:      now.Add(-time.Hour),
		Read:      true,
	}
	reply := provider.Message{
		ID:        "m2",
		MessageID: "<m2@test>",
		InReplyTo: "<m1@test>",
		Subject:   "Re: project update",
		From:      provider.Address{Name: "Bob", Email: "bob@test"},
		Date:      now,
	}

	if err := c.ReplaceThreads("Archive", []provider.Thread{{
		ID:       "archive-thread",
		Subject:  original.Subject,
		Date:     original.Date,
		Messages: []provider.Message{original},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:       "reply-thread",
		Subject:  reply.Subject,
		Date:     reply.Date,
		Messages: []provider.Message{reply},
	}}); err != nil {
		t.Fatal(err)
	}

	messages, err := c.GetConversationMessages(provider.Thread{
		ID:       "reply-thread",
		Subject:  reply.Subject,
		Date:     reply.Date,
		Messages: []provider.Message{reply},
	}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(messages); got != 2 {
		t.Fatalf("messages = %d, want original + reply", got)
	}
	if messages[0].ID != "m1" || messages[1].ID != "m2" {
		t.Fatalf("message order = %q, %q; want m1, m2", messages[0].ID, messages[1].ID)
	}
}

func TestConversationIndexIncludesRecentSentSubjectMatch(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

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

	messages, err := c.GetConversationMessages(provider.Thread{
		ID:       "inbox-thread",
		Subject:  inbox.Subject,
		Date:     inbox.Date,
		Messages: []provider.Message{inbox},
	}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(messages); got != 2 {
		t.Fatalf("messages = %d, want inbox + sent", got)
	}
	if messages[1].ID != "sent-1" {
		t.Fatalf("latest message = %q, want sent-1", messages[1].ID)
	}
}

func TestConversationIndexSubjectFallbackRequiresSharedParticipant(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	now := time.Now()
	original := provider.Message{
		ID:        "original",
		MessageID: "<original@test>",
		Subject:   "Re: Leica Q343",
		From:      provider.Address{Name: "Leica Store Manchester", Email: "hello@leicastoremanchester.com"},
		To:        []provider.Address{{Email: "me@test"}},
		Date:      now.Add(-90 * time.Minute),
	}
	reply := provider.Message{
		ID:        "reply",
		MessageID: "<reply@test>",
		Subject:   "Re: Leica Q343",
		From:      provider.Address{Email: "me@test"},
		To:        []provider.Address{{Name: "Leica Store Manchester", Email: "hello@leicastoremanchester.com"}},
		Date:      now,
	}
	unrelated := provider.Message{
		ID:        "unrelated",
		MessageID: "<unrelated@test>",
		Subject:   "Re: Leica Q343",
		From:      provider.Address{Email: "someone-else@test"},
		To:        []provider.Address{{Email: "me@test"}},
		Date:      now.Add(-30 * time.Minute),
	}

	if err := c.ReplaceThreads("INBOX", []provider.Thread{
		{ID: "original-thread", Subject: original.Subject, Date: original.Date, Messages: []provider.Message{original}},
		{ID: "unrelated-thread", Subject: unrelated.Subject, Date: unrelated.Date, Messages: []provider.Message{unrelated}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplaceThreads("Sent", []provider.Thread{{
		ID:       "reply-thread",
		Subject:  reply.Subject,
		Date:     reply.Date,
		Messages: []provider.Message{reply},
	}}); err != nil {
		t.Fatal(err)
	}

	messages, err := c.GetConversationMessages(provider.Thread{
		ID:       "reply-thread",
		Subject:  reply.Subject,
		Date:     reply.Date,
		Messages: []provider.Message{reply},
	}, 50)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(messages); got != 2 {
		t.Fatalf("messages = %d, want original + reply", got)
	}
	if messages[0].ID != "original" || messages[1].ID != "reply" {
		t.Fatalf("messages = %q, %q; want original, reply", messages[0].ID, messages[1].ID)
	}
}
