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

func TestCalendarLinkEventsFrancescoReminder(t *testing.T) {
	msg := provider.Message{
		Subject: "Your Reminder!",
		From:    provider.Address{Name: "Francesco Hair Salon", Email: "francescohairsalonnorreply@salonemail.com"},
		Date:    time.Date(2026, time.May, 29, 8, 11, 49, 0, time.Local),
	}
	body := strings.Join([]string{
		"This is a reminder of your Appointment at",
		"Francesco Hair Salon!",
		"Your Appointment is on:",
		"Friday 5 June at 10:00.",
	}, "\n")

	events := calendarLinkEvents(body, msg)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Summary != "Your Reminder!" {
		t.Fatalf("summary = %q, want subject fallback", event.Summary)
	}
	assertTime(t, event.Start, 2026, time.June, 5, 10, 0)
	assertTime(t, event.End, 2026, time.June, 5, 11, 0)
	if !containsString(event.Triggers, "Friday 5 June") {
		t.Fatalf("triggers = %#v, want inferred-year date text", event.Triggers)
	}
	if !containsString(event.Triggers, "at 10:00") {
		t.Fatalf("triggers = %#v, want single time text", event.Triggers)
	}
}

func TestCalendarLinkEventsMonthFirstDateFromWhen(t *testing.T) {
	msg := provider.Message{
		Subject: "Your Reminder!",
		From:    provider.Address{Name: "Francesco Hair Salon", Email: "francescohairsalonnorreply@salonemail.com"},
		Date:    time.Date(2026, time.May, 29, 8, 11, 49, 0, time.Local),
	}
	body := "This is a reminder of your Appointment on June 5th at 10:00."

	events := calendarLinkEvents(body, msg)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	assertTime(t, event.Start, 2026, time.June, 5, 10, 0)
	assertTime(t, event.End, 2026, time.June, 5, 11, 0)
	if !containsString(event.Triggers, "June 5th at 10:00") {
		t.Fatalf("triggers = %#v, want when date text", event.Triggers)
	}
}

func TestCalendarLinkEventsAdmiralRenewalDateOnly(t *testing.T) {
	msg := provider.Message{
		Subject: "Your Admiral Insurance policy is due to renew.",
		From:    provider.Address{Name: "Admiral.com", Email: "noreply@support.admiral.com"},
		Date:    time.Date(2026, time.May, 28, 21, 24, 42, 0, time.Local),
	}
	body := "Pete, it's almost time for your renewal!\nYour MultiCar policy will automatically renew on 12th June 2026."

	events := calendarLinkEvents(body, msg)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if !event.AllDay {
		t.Fatal("event AllDay = false, want true")
	}
	assertTime(t, event.Start, 2026, time.June, 12, 0, 0)
	assertTime(t, event.End, 2026, time.June, 13, 0, 0)
	if !containsString(event.Triggers, "12th June 2026") {
		t.Fatalf("triggers = %#v, want renewal date text", event.Triggers)
	}
}

func TestCalendarLinkEventsAdmiralRenewalRange(t *testing.T) {
	msg := provider.Message{
		Subject: "Your Admiral Insurance policy is due to renew.",
		From:    provider.Address{Name: "Admiral.com", Email: "noreply@support.admiral.com"},
		Date:    time.Date(2026, time.May, 28, 21, 24, 42, 0, time.Local),
	}
	body := strings.Join([]string{
		"Renewal Date",
		"Includes",
		"12th June 2026",
		"Car",
		"Katy Griffiths",
		"12th June 2026 to 12th June 2027",
	}, "\n")

	events := calendarLinkEvents(body, msg)
	if len(events) != 2 {
		t.Fatalf("events = %d, want renewal date plus range", len(events))
	}
	event := events[1]
	if !event.AllDay {
		t.Fatal("range event AllDay = false, want true")
	}
	assertTime(t, event.Start, 2026, time.June, 12, 0, 0)
	assertTime(t, event.End, 2027, time.June, 13, 0, 0)
	if !containsString(event.Triggers, "12th June 2026 to 12th June 2027") {
		t.Fatalf("triggers = %#v, want whole range", event.Triggers)
	}
}

func TestCalendarLinkSpansLinksWholeDateRange(t *testing.T) {
	mb := NewState(nil, "")
	event := calendarLinkEvent{
		Summary:  "Policy cover",
		Start:    time.Date(2026, time.June, 12, 0, 0, 0, 0, time.Local),
		End:      time.Date(2027, time.June, 13, 0, 0, 0, 0, time.Local),
		AllDay:   true,
		Triggers: []string{"12th June 2026 to 12th June 2027"},
	}
	spans := []glyph.Span{{
		Text:  "Cover runs 12th June 2026 to 12th June 2027 for this car.",
		Style: glyph.Style{},
	}}

	out := mb.calendarLinkSpans(spans, []calendarLinkEvent{event})
	if !spanSelectable(out, "12th June 2026 to 12th June 2027") {
		t.Fatalf("spans = %#v, want whole range selectable", out)
	}
	for _, span := range out {
		if span.Text == "12th June 2026" || span.Text == "12th June 2027" {
			t.Fatalf("spans = %#v, range was split into endpoint links", out)
		}
	}
}

func TestCalendarLinkEventsIgnoresArticlePublishedDateOnly(t *testing.T) {
	msg := provider.Message{
		Subject: "Google Alert - SO_REUSEPORT",
		From:    provider.Address{Name: "Google Alerts", Email: "googlealerts-noreply@google.com"},
		Date:    time.Date(2026, time.May, 29, 2, 16, 30, 0, time.Local),
	}
	body := "CVE-2026-46015 is a Linux kernel TCP bug published by NVD on May 27, 2026, after kernel.org reported a missing listener wakeup."

	if events := calendarLinkEvents(body, msg); len(events) != 0 {
		t.Fatalf("events = %#v, want no date-only article event", events)
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

func TestOpenCalendarEventWritesAllDayICS(t *testing.T) {
	event := calendarLinkEvent{
		Summary: "Admiral renewal",
		Source:  "Your Admiral Insurance policy is due to renew.",
		Start:   time.Date(2026, time.June, 12, 0, 0, 0, 0, time.Local),
		End:     time.Date(2026, time.June, 13, 0, 0, 0, 0, time.Local),
		AllDay:  true,
	}
	ics := renderCalendarEventICS(event)
	for _, want := range []string{
		"SUMMARY:Admiral renewal",
		"DTSTART;VALUE=DATE:20260612",
		"DTEND;VALUE=DATE:20260613",
	} {
		if !strings.Contains(ics, want) {
			t.Fatalf("ics missing %q:\n%s", want, ics)
		}
	}
	if strings.Contains(ics, "DTSTART:20260612T") {
		t.Fatalf("ics uses timed DTSTART for all-day event:\n%s", ics)
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

func TestLoadConversationAddsCalendarLinksFromFrancescoHTML(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "francesco-reminder",
		Subject: "Your Reminder!",
		Date:    time.Date(2026, time.May, 29, 8, 11, 49, 0, time.Local),
		Messages: []provider.Message{{
			ID:      "francesco-reminder-1",
			Subject: "Your Reminder!",
			From:    provider.Address{Name: "Francesco Hair Salon", Email: "francescohairsalonnorreply@salonemail.com"},
			Date:    time.Date(2026, time.May, 29, 8, 11, 49, 0, time.Local),
			HTMLBody: `<html><body>
				<p>This is a reminder of your Appointment at<br><strong>Francesco Hair Salon!</strong></p>
				<p>Your Appointment is on:<br><br><strong>Friday 5 June at 10:00.</strong></p>
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
	if !spanSelectable(msgs[0].BodySpans, "Friday 5 June") {
		t.Fatalf("body spans = %#v, want selectable inferred-year Francesco date", msgs[0].BodySpans)
	}
	if !spanSelectable(msgs[0].BodySpans, "at 10:00") {
		t.Fatalf("body spans = %#v, want selectable single appointment time", msgs[0].BodySpans)
	}
}

func TestLoadConversationAddsCalendarLinksFromAdmiralHTML(t *testing.T) {
	c := testCache(t)
	c.PutFolders([]provider.Folder{{ID: "INBOX", Name: "INBOX"}})
	if err := c.ReplaceThreads("INBOX", []provider.Thread{{
		ID:      "admiral-renewal",
		Subject: "Your Admiral Insurance policy is due to renew.",
		Date:    time.Date(2026, time.May, 28, 21, 24, 42, 0, time.Local),
		Messages: []provider.Message{{
			ID:      "admiral-renewal-1",
			Subject: "Your Admiral Insurance policy is due to renew.",
			From:    provider.Address{Name: "Admiral.com", Email: "noreply@support.admiral.com"},
			Date:    time.Date(2026, time.May, 28, 21, 24, 42, 0, time.Local),
			HTMLBody: `<html><body>
				<h2>Pete, it's almost time for your renewal!</h2>
				<p>Your MultiCar policy will automatically renew on 12th June 2026.</p>
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
	if !spanSelectable(msgs[0].BodySpans, "12th June 2026") {
		t.Fatalf("body spans = %#v, want selectable Admiral renewal date", msgs[0].BodySpans)
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
