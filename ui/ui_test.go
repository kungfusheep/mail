package ui

import (
	"testing"
	"time"
)

func TestFeedPushesNewestAndCapsItems(t *testing.T) {
	now := time.Unix(0, 0)
	feed := NewFeed(func() time.Time { return now })

	for _, text := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		feed.Push(text)
	}

	items := *feed.Items()
	if len(items) != 6 {
		t.Fatalf("items = %d, want 6", len(items))
	}
	if items[0].Text != "two" || items[5].Text != "seven" {
		t.Fatalf("items = %#v, want fifo capped to two..seven", items)
	}
}

func TestFeedPreservesNotificationKind(t *testing.T) {
	feed := NewFeed(nil)

	feed.PushKind(NotificationError, "send failed")

	items := *feed.Items()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Kind != NotificationError {
		t.Fatalf("kind = %v, want error", items[0].Kind)
	}
}

func TestFeedFadesAndExpires(t *testing.T) {
	now := time.Unix(0, 0)
	feed := NewFeed(func() time.Time { return now })

	feed.Push("syncing")
	now = now.Add(2500 * time.Millisecond)
	feed.Update()

	items := *feed.Items()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Opacity <= 0 || items[0].Opacity >= 1 {
		t.Fatalf("opacity = %v, want fading value", items[0].Opacity)
	}

	now = now.Add(400 * time.Millisecond)
	feed.Update()

	if got := len(*feed.Items()); got != 0 {
		t.Fatalf("items after ttl = %d, want 0", got)
	}
}

func TestFeedIgnoresEmptyNotifications(t *testing.T) {
	feed := NewFeed(nil)
	feed.Push("")

	if got := len(*feed.Items()); got != 0 {
		t.Fatalf("items = %d, want 0", got)
	}
}
