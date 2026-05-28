package mailbox

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/provider"
)

func TestCalendarLinkEventsRoyalMailCollection(t *testing.T) {
	msg := provider.Message{
		Subject: "Your Royal Mail Collection - Confirmation",
		From:    provider.Address{Name: "Royal Mail", Email: "no-reply@royalmail.com"},
	}
	body := strings.Join([]string{
		"Thank you for your collection request.",
		"Your collection is due:",
		"Friday 29 May 2026*",
		"",
		"between 10.55am and 12.55pm",
		"for the following item(s):",
	}, "\n")

	events := calendarLinkEvents(body, msg)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Summary != "Royal Mail collection" {
		t.Fatalf("summary = %q, want %q", event.Summary, "Royal Mail collection")
	}
	assertTime(t, event.Start, 2026, time.May, 29, 10, 55)
	assertTime(t, event.End, 2026, time.May, 29, 12, 55)
	if !containsString(event.Triggers, "Friday 29 May 2026*") {
		t.Fatalf("triggers = %#v, want date text", event.Triggers)
	}
	if !containsString(event.Triggers, "between 10.55am and 12.55pm") {
		t.Fatalf("triggers = %#v, want time range text", event.Triggers)
	}
}

func TestCalendarLinkEventsFallbackSummary(t *testing.T) {
	msg := provider.Message{Subject: "Dentist reminder"}
	body := "Friday 29 May 2026\nbetween 10:55am and 12:55pm"

	events := calendarLinkEvents(body, msg)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Summary != "Dentist reminder" {
		t.Fatalf("summary = %q, want subject fallback", events[0].Summary)
	}
}

func TestCalendarLinkSpansAddsSelectableDateWithoutTouchingRealLinks(t *testing.T) {
	mb := NewState(nil, "")
	event := calendarLinkEvent{
		Summary:  "Royal Mail collection",
		Start:    time.Date(2026, time.May, 29, 10, 55, 0, 0, time.Local),
		End:      time.Date(2026, time.May, 29, 12, 55, 0, 0, time.Local),
		Triggers: []string{"Friday 29 May 2026*"},
	}
	spans := []glyph.Span{
		{Text: "Visit ", Style: glyph.Style{}},
		{Text: "Royal Mail", Style: glyph.Style{}, OnSelect: func() {}},
		{Text: " on Friday 29 May 2026*", Style: glyph.Style{}},
	}

	out := mb.calendarLinkSpans(spans, []calendarLinkEvent{event})
	var foundCalendarLink bool
	for _, span := range out {
		if span.Text == "Friday 29 May 2026*" {
			foundCalendarLink = span.OnSelect != nil && span.Style.Attr&glyph.AttrUnderline != 0
		}
		if span.Text == "Royal Mail" && span.Style.Attr&glyph.AttrUnderline != 0 {
			t.Fatal("existing link span was restyled by calendar links")
		}
	}
	if !foundCalendarLink {
		t.Fatalf("spans = %#v, want selectable calendar date", out)
	}
}

func TestOpenCalendarEventWritesICSAndOpensIt(t *testing.T) {
	mb := NewState(nil, "")
	var opened string
	mb.SetAttachmentOpener(func(path string) error {
		opened = path
		return nil
	})
	event := calendarLinkEvent{
		Summary: "Royal Mail collection",
		Source:  "Your Royal Mail Collection - Confirmation",
		Start:   time.Date(2026, time.May, 29, 10, 55, 0, 0, time.Local),
		End:     time.Date(2026, time.May, 29, 12, 55, 0, 0, time.Local),
	}

	mb.OpenCalendarEvent(event)
	if opened == "" {
		t.Fatal("calendar opener was not called")
	}
	t.Cleanup(func() { _ = os.Remove(opened) })
	data, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	ics := string(data)
	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"SUMMARY:Royal Mail collection",
		"DESCRIPTION:Your Royal Mail Collection - Confirmation",
		"DTSTART:20260529T105500",
		"DTEND:20260529T125500",
	} {
		if !strings.Contains(ics, want) {
			t.Fatalf("ics missing %q:\n%s", want, ics)
		}
	}
}

func TestLoadConversationAddsCalendarLinksFromBodyDates(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "royal-mail",
		Subject: "Your Royal Mail Collection - Confirmation",
		Date:    time.Date(2026, time.May, 28, 7, 45, 13, 0, time.UTC),
		Messages: []provider.Message{{
			ID:      "royal-mail-1",
			Subject: "Your Royal Mail Collection - Confirmation",
			From:    provider.Address{Name: "Royal Mail", Email: "no-reply@royalmail.com"},
			Date:    time.Date(2026, time.May, 28, 7, 45, 13, 0, time.UTC),
			HTMLBody: `<html><body>
				<p>Thank you for your collection request.</p>
				<p>Your collection is due:</p>
				<p>Friday 29 May 2026*</p>
				<p>between 10.55am and 12.55pm</p>
			</body></html>`,
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	mb := NewState(c, "me@example.com")
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.LoadConversation(0, nil)

	msgs := *mb.ConversationMessages()
	if len(msgs) != 1 {
		t.Fatalf("conversation messages = %d, want 1", len(msgs))
	}
	if !spanSelectable(msgs[0].BodySpans, "Friday 29 May 2026*") {
		t.Fatalf("body spans = %#v, want selectable calendar date", msgs[0].BodySpans)
	}
	if !spanSelectable(msgs[0].BodySpans, "between 10.55am and 12.55pm") {
		t.Fatalf("body spans = %#v, want selectable calendar time range", msgs[0].BodySpans)
	}
}

func assertTime(t *testing.T, got time.Time, year int, month time.Month, day, hour, minute int) {
	t.Helper()
	if got.Year() != year || got.Month() != month || got.Day() != day || got.Hour() != hour || got.Minute() != minute {
		t.Fatalf("time = %v, want %04d-%02d-%02d %02d:%02d", got, year, month, day, hour, minute)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func spanSelectable(spans []glyph.Span, text string) bool {
	for _, span := range spans {
		if span.Text == text && span.OnSelect != nil {
			return true
		}
	}
	return false
}
