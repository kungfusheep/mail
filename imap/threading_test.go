package imap

import (
	"testing"
	"time"

	"github.com/kungfusheep/mail/provider"
)

func TestGroupByInReplyTo(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "hello", Date: now.Add(-2 * time.Hour)},
		{ID: "2", MessageID: "<b@test>", Subject: "Re: hello", InReplyTo: "<a@test>", Date: now.Add(-1 * time.Hour)},
		{ID: "3", MessageID: "<c@test>", Subject: "Re: hello", InReplyTo: "<b@test>", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	if len(threads[0].Messages) != 3 {
		t.Fatalf("thread has %d messages, want 3", len(threads[0].Messages))
	}
	if threads[0].Messages[0].ID != "1" || threads[0].Messages[2].ID != "3" {
		t.Errorf("messages not in chronological order")
	}
}

func TestGroupBySubjectFallback(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<orig@test>", Subject: "weekend plans", Date: now.Add(-1 * time.Hour)},
		{ID: "2", MessageID: "<reply@test>", Subject: "Re: weekend plans", InReplyTo: "<missing@sent>", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1 (subject fallback should group them)", len(threads))
	}
	if len(threads[0].Messages) != 2 {
		t.Fatalf("thread has %d messages, want 2", len(threads[0].Messages))
	}
}

func TestStandaloneMessages(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "banana bread recipe", Date: now},
		{ID: "2", MessageID: "<b@test>", Subject: "launch window confirmed", Date: now},
		{ID: "3", MessageID: "<c@test>", Subject: "dentist tuesday", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 3 {
		t.Fatalf("got %d threads, want 3 (no grouping)", len(threads))
	}
}

func TestFwdGroupsWithOriginal(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "lunch spots", Date: now.Add(-1 * time.Hour)},
		{ID: "2", MessageID: "<b@test>", Subject: "Fwd: lunch spots", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1 (Fwd: should group with original)", len(threads))
	}
}

func TestChainedReplies(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "road trip", Date: now.Add(-3 * time.Hour)},
		{ID: "2", MessageID: "<b@test>", Subject: "Re: road trip", InReplyTo: "<a@test>", Date: now.Add(-2 * time.Hour)},
		{ID: "3", MessageID: "<c@test>", Subject: "Re: road trip", InReplyTo: "<b@test>", Date: now.Add(-1 * time.Hour)},
		{ID: "4", MessageID: "<d@test>", Subject: "Re: road trip", InReplyTo: "<c@test>", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	if len(threads[0].Messages) != 4 {
		t.Fatalf("thread has %d messages, want 4", len(threads[0].Messages))
	}
}

func TestMixedThreadsAndStandalone(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "camping gear", Date: now.Add(-2 * time.Hour)},
		{ID: "2", MessageID: "<b@test>", Subject: "Re: camping gear", InReplyTo: "<a@test>", Date: now.Add(-1 * time.Hour)},
		{ID: "3", MessageID: "<c@test>", Subject: "vinyl arrived", Date: now},
		{ID: "4", MessageID: "<d@test>", Subject: "wifi password", Date: now.Add(-3 * time.Hour)},
		{ID: "5", MessageID: "<e@test>", Subject: "Re: wifi password", InReplyTo: "<d@test>", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 3 {
		t.Fatalf("got %d threads, want 3", len(threads))
	}

	var multiThreads int
	for _, th := range threads {
		if len(th.Messages) > 1 {
			multiThreads++
		}
	}
	if multiThreads != 2 {
		t.Errorf("got %d multi-message threads, want 2", multiThreads)
	}
}

func TestNormalizeSubject(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"Re: hello", "hello"},
		{"RE: hello", "hello"},
		{"re: hello", "hello"},
		{"Fwd: hello", "hello"},
		{"FWD: hello", "hello"},
		{"Fw: hello", "hello"},
		{"Re: Fwd: Re: hello", "hello"},
		{"Re: Re: Re: deep thread", "deep thread"},
	}
	for _, c := range cases {
		got := normalizeSubject(c.in)
		if got != c.want {
			t.Errorf("normalizeSubject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnreadCount(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "gig tickets", Date: now, Read: true},
		{ID: "2", MessageID: "<b@test>", Subject: "Re: gig tickets", InReplyTo: "<a@test>", Date: now, Read: false},
		{ID: "3", MessageID: "<c@test>", Subject: "Re: gig tickets", InReplyTo: "<b@test>", Date: now, Read: false},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	if threads[0].Unread != 2 {
		t.Errorf("unread = %d, want 2", threads[0].Unread)
	}
}

func TestParticipantDedup(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "bbq saturday", From: provider.Address{Email: "alice@test"}, Date: now},
		{ID: "2", MessageID: "<b@test>", Subject: "Re: bbq saturday", From: provider.Address{Email: "bob@test"}, InReplyTo: "<a@test>", Date: now},
		{ID: "3", MessageID: "<c@test>", Subject: "Re: bbq saturday", From: provider.Address{Email: "alice@test"}, InReplyTo: "<b@test>", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads[0].Participants) != 2 {
		t.Errorf("participants = %d, want 2 (alice deduped)", len(threads[0].Participants))
	}
}

// Gmail X-GM-THRID tests

func TestGmailThreadID_GroupsConversation(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "100", MessageID: "<x@test>", Subject: "coffee tomorrow", ThreadID: "1234567890", From: provider.Address{Email: "a@test"}, Date: now.Add(-2 * time.Hour)},
		{ID: "101", MessageID: "<y@test>", Subject: "Re: coffee tomorrow", ThreadID: "1234567890", From: provider.Address{Email: "b@test"}, Date: now.Add(-1 * time.Hour)},
		{ID: "102", MessageID: "<z@test>", Subject: "Re: coffee tomorrow", ThreadID: "1234567890", From: provider.Address{Email: "a@test"}, Date: now},
	}

	threads := groupByGmailThread(msgs)

	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	if len(threads[0].Messages) != 3 {
		t.Fatalf("thread has %d messages, want 3", len(threads[0].Messages))
	}
	if threads[0].Messages[0].ID != "100" {
		t.Errorf("first message should be the earliest")
	}
}

func TestGmailThreadID_SeparateConversations(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "thing one", ThreadID: "111", Date: now},
		{ID: "2", MessageID: "<b@test>", Subject: "Re: thing one", ThreadID: "111", Date: now},
		{ID: "3", MessageID: "<c@test>", Subject: "thing two", ThreadID: "222", Date: now},
		{ID: "4", MessageID: "<d@test>", Subject: "thing three", ThreadID: "333", Date: now},
	}

	threads := groupByGmailThread(msgs)

	if len(threads) != 3 {
		t.Fatalf("got %d threads, want 3", len(threads))
	}
}

func TestGmailThreadID_PreferredOverInReplyTo(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "1", MessageID: "<a@test>", Subject: "ping", ThreadID: "999", Date: now},
		{ID: "2", MessageID: "<b@test>", Subject: "Re: ping", ThreadID: "999", InReplyTo: "<a@test>", Date: now},
		{ID: "3", MessageID: "<c@test>", Subject: "pong", ThreadID: "888", Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}
}

func TestGmailThreadID_CrossFolder(t *testing.T) {
	now := time.Now()
	// only inbox messages fetched, but shared thread ID groups them
	msgs := []provider.Message{
		{ID: "426", MessageID: "<reply1@test>", Subject: "Re: catch up", ThreadID: "7700000",
			From: provider.Address{Name: "B", Email: "b@test"}, InReplyTo: "<missing@sent>", Date: now.Add(-1 * time.Hour)},
		{ID: "427", MessageID: "<reply2@test>", Subject: "Re: catch up", ThreadID: "7700000",
			From: provider.Address{Name: "A", Email: "a@test"}, InReplyTo: "<reply1@test>", Date: now},
		{ID: "428", MessageID: "<other@test>", Subject: "new keyboard", ThreadID: "8800000",
			From: provider.Address{Email: "shop@test"}, Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}

	var catchUp *provider.Thread
	for i := range threads {
		if len(threads[i].Messages) == 2 {
			catchUp = &threads[i]
		}
	}
	if catchUp == nil {
		t.Fatal("expected a 2-message thread")
	}
	if catchUp.Messages[0].From.Name != "B" {
		t.Errorf("first message from = %q, want B", catchUp.Messages[0].From.Name)
	}
}

func TestRealWorldInbox_MixOfThreadsAndStandalone(t *testing.T) {
	now := time.Now()
	msgs := []provider.Message{
		{ID: "250", MessageID: "<n1@test>", Subject: "parcel delivery", ThreadID: "t1", Date: now.Add(-8 * 24 * time.Hour)},
		{ID: "299", MessageID: "<n2@test>", Subject: "subscription renewal", ThreadID: "t2", Date: now.Add(-5 * 24 * time.Hour)},
		{ID: "310", MessageID: "<n3@test>", Subject: "Fwd: old document", ThreadID: "t3", InReplyTo: "<original@ext>", Date: now.Add(-4 * 24 * time.Hour)},
		{ID: "311", MessageID: "<n4@test>", Subject: "please review", ThreadID: "t4", Date: now.Add(-4 * 24 * time.Hour)},
		{ID: "348", MessageID: "<n5@test>", Subject: "account notice", ThreadID: "t5", Date: now.Add(-3 * 24 * time.Hour)},
		{ID: "414", MessageID: "<n6@test>", Subject: "reminder", ThreadID: "t6", Date: now.Add(-7 * time.Hour)},
		{ID: "426", MessageID: "<n7@test>", Subject: "Re: catch up", ThreadID: "t7", InReplyTo: "<sent@test>",
			From: provider.Address{Name: "B", Email: "b@test"}, Date: now.Add(-30 * time.Minute)},
		{ID: "427", MessageID: "<n8@test>", Subject: "Re: catch up", ThreadID: "t7", InReplyTo: "<n7@test>",
			From: provider.Address{Name: "A", Email: "a@test"}, Date: now},
	}

	threads := groupIntoThreads(msgs)

	if len(threads) != 7 {
		t.Fatalf("got %d threads, want 7", len(threads))
	}

	var conversation *provider.Thread
	for i := range threads {
		if len(threads[i].Messages) > 1 {
			conversation = &threads[i]
		}
	}
	if conversation == nil {
		t.Fatal("expected one multi-message thread")
	}
	if len(conversation.Messages) != 2 {
		t.Fatalf("conversation has %d messages, want 2", len(conversation.Messages))
	}
}
