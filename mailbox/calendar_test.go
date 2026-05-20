package mailbox

import "testing"

func TestParseCalendarSummary(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\n" +
		"BEGIN:VEVENT\r\n" +
		"SUMMARY:Project sync\\, phase 2\r\n" +
		"DTSTART:20260526T140000Z\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	summary, ok := parseCalendarSummary(data)
	if !ok {
		t.Fatal("parseCalendarSummary ok = false, want true")
	}
	if summary.Title != "Project sync, phase 2" {
		t.Fatalf("title = %q, want Project sync, phase 2", summary.Title)
	}
	if summary.When != "Tue 26 May, 14:00" {
		t.Fatalf("when = %q, want Tue 26 May, 14:00", summary.When)
	}
}

func TestParseCalendarSummaryUnfoldsLinesAndAllDayDates(t *testing.T) {
	data := "BEGIN:VCALENDAR\n" +
		"BEGIN:VEVENT\n" +
		"SUMMARY:Annual planning with a very long \n" +
		"  agenda\n" +
		"DTSTART;VALUE=DATE:20260527\n" +
		"END:VEVENT\n" +
		"END:VCALENDAR\n"

	summary, ok := parseCalendarSummary(data)
	if !ok {
		t.Fatal("parseCalendarSummary ok = false, want true")
	}
	if summary.Title != "Annual planning with a very long agenda" {
		t.Fatalf("title = %q, want folded summary", summary.Title)
	}
	if summary.When != "Wed 27 May" {
		t.Fatalf("when = %q, want Wed 27 May", summary.When)
	}
}
