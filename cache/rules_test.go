package cache

import (
	"testing"
	"time"

	"github.com/kungfusheep/mail/provider"
)

func TestRulesRoundTripAndMatch(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	rule := Rule{
		ID:              "billing",
		Name:            "billing",
		Enabled:         true,
		FromContains:    "stripe",
		SubjectContains: "invoice",
		MoveTo:          "Receipts",
		MarkRead:        true,
		CreatedAt:       time.Date(2026, 5, 29, 13, 0, 0, 0, time.UTC),
	}
	if err := c.PutRule(rule); err != nil {
		t.Fatal(err)
	}

	rules, err := c.Rules()
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != "billing" || rules[0].MoveTo != "Receipts" {
		t.Fatalf("rules = %#v, want stored billing rule", rules)
	}

	thread := provider.Thread{
		ID:      "t1",
		Subject: "Your invoice",
		Messages: []provider.Message{{
			From:    provider.Address{Name: "Stripe", Email: "billing@stripe.com"},
			Subject: "Your invoice",
		}},
	}
	if !rules[0].Matches(thread) {
		t.Fatal("rule should match stripe invoice thread")
	}
	thread.Subject = "Security alert"
	thread.Messages[0].Subject = "Security alert"
	if rules[0].Matches(thread) {
		t.Fatal("rule matched a non-invoice subject")
	}
}
