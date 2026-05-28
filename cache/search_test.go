package cache

import (
	"testing"
	"time"

	"github.com/kungfusheep/mail/provider"
)

func TestSearchSupportsFieldOperators(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := c.ReplaceThreads("INBOX", []provider.Thread{
		{
			ID:      "stripe",
			Subject: "Dispute update",
			Date:    now,
			Messages: []provider.Message{{
				ID:       "m1",
				From:     provider.Address{Name: "Stripe", Email: "support@stripe.com"},
				To:       []provider.Address{{Email: "me@example.com"}},
				Subject:  "Dispute update",
				TextBody: "new evidence is ready",
			}},
		},
		{
			ID:      "apple",
			Subject: "Receipt",
			Date:    now.Add(-time.Hour),
			Messages: []provider.Message{{
				ID:       "m2",
				From:     provider.Address{Name: "Apple", Email: "no-reply@apple.com"},
				To:       []provider.Address{{Email: "me@example.com"}},
				Subject:  "Receipt",
				TextBody: "stripe card charge",
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	results, err := c.Search("from:stripe subject:dispute", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "stripe" {
		t.Fatalf("Search field results = %#v, want stripe only", results)
	}

	results, err = c.Search("to:me@example.com stripe", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("Search mixed field/plain results = %d, want 2", len(results))
	}
}
