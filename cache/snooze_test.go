package cache

import (
	"testing"
	"time"
)

func TestSnoozesRoundTripAndDueQuery(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	if err := c.PutSnooze(Snooze{
		ThreadID:       "thread-1",
		OriginalFolder: "INBOX",
		SnoozedFolder:  "[Mailbox]/Snoozed",
		WakeAt:         now.Add(time.Hour),
		CreatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}

	if due, err := c.DueSnoozes(now); err != nil {
		t.Fatal(err)
	} else if len(due) != 0 {
		t.Fatalf("due snoozes before wake = %d, want 0", len(due))
	}

	due, err := c.DueSnoozes(now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due snoozes = %d, want 1", len(due))
	}
	if due[0].ThreadID != "thread-1" || due[0].OriginalFolder != "INBOX" || due[0].SnoozedFolder != "[Mailbox]/Snoozed" {
		t.Fatalf("snooze = %#v, want stored folders and thread", due[0])
	}

	if err := c.DeleteSnooze("thread-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := c.Snooze("thread-1"); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("snooze row still present after delete")
	}
}
