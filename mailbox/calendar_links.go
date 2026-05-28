package mailbox

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/provider"
)

type calendarLinkEvent struct {
	Summary  string
	Source   string
	Start    time.Time
	End      time.Time
	Triggers []string
}

var (
	calendarDateRE    = regexp.MustCompile(`(?i)\b(?:(?:mon|monday|tue|tues|tuesday|wed|wednesday|thu|thur|thurs|thursday|fri|friday|sat|saturday|sun|sunday)\s+)?\d{1,2}(?:st|nd|rd|th)?\s+(?:jan|january|feb|february|mar|march|apr|april|may|jun|june|jul|july|aug|august|sep|sept|september|oct|october|nov|november|dec|december)\s+\d{4}\*?`)
	calendarTimeRE    = regexp.MustCompile(`(?i)\bbetween\s+(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)\s+and\s+(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)\b`)
	calendarOrdinalRE = regexp.MustCompile(`(?i)(\d{1,2})(st|nd|rd|th)`)
)

func calendarLinkEvents(text string, msg provider.Message) []calendarLinkEvent {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	dateMatches := calendarDateRE.FindAllStringIndex(text, -1)
	if len(dateMatches) == 0 {
		return nil
	}

	events := make([]calendarLinkEvent, 0, len(dateMatches))
	seen := make(map[string]bool)
	for _, dateMatch := range dateMatches {
		dateText := text[dateMatch[0]:dateMatch[1]]
		date, ok := parseBodyCalendarDate(dateText)
		if !ok {
			continue
		}

		windowEnd := min(len(text), dateMatch[1]+220)
		nearby := text[dateMatch[1]:windowEnd]
		timeMatch := calendarTimeRE.FindStringSubmatchIndex(nearby)
		if timeMatch == nil {
			continue
		}

		rangeText := nearby[timeMatch[0]:timeMatch[1]]
		start, end, ok := parseBodyCalendarRange(date, nearby, timeMatch)
		if !ok {
			continue
		}

		key := start.Format(time.RFC3339) + "|" + end.Format(time.RFC3339)
		if seen[key] {
			continue
		}
		seen[key] = true

		events = append(events, calendarLinkEvent{
			Summary:  calendarLinkSummary(text, msg),
			Source:   strings.TrimSpace(msg.Subject),
			Start:    start,
			End:      end,
			Triggers: []string{dateText, rangeText},
		})
	}
	return events
}

func parseBodyCalendarDate(text string) (time.Time, bool) {
	clean := strings.TrimSuffix(strings.TrimSpace(text), "*")
	clean = calendarOrdinalRE.ReplaceAllString(clean, "$1")
	fields := strings.Fields(clean)
	layouts := []string{"Monday 2 January 2006", "Mon 2 January 2006", "2 January 2006", "Monday 2 Jan 2006", "Mon 2 Jan 2006", "2 Jan 2006"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, clean, time.Local); err == nil {
			return t, true
		}
	}
	if len(fields) > 3 {
		withoutWeekday := strings.Join(fields[1:], " ")
		for _, layout := range []string{"2 January 2006", "2 Jan 2006"} {
			if t, err := time.ParseInLocation(layout, withoutWeekday, time.Local); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func parseBodyCalendarRange(date time.Time, text string, match []int) (time.Time, time.Time, bool) {
	startHour, startMinute, ok := parseBodyClock(text[match[2]:match[3]], submatchText(text, match[4], match[5]), text[match[6]:match[7]])
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	endHour, endMinute, ok := parseBodyClock(text[match[8]:match[9]], submatchText(text, match[10], match[11]), text[match[12]:match[13]])
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), startHour, startMinute, 0, 0, time.Local)
	end := time.Date(date.Year(), date.Month(), date.Day(), endHour, endMinute, 0, 0, time.Local)
	if !end.After(start) {
		end = end.Add(24 * time.Hour)
	}
	return start, end, true
}

func parseBodyClock(hourText, minuteText, meridiem string) (int, int, bool) {
	hour, err := strconv.Atoi(hourText)
	if err != nil || hour < 1 || hour > 12 {
		return 0, 0, false
	}
	minute := 0
	if minuteText != "" {
		minute, err = strconv.Atoi(minuteText)
		if err != nil || minute < 0 || minute > 59 {
			return 0, 0, false
		}
	}
	switch strings.ToLower(meridiem) {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour != 12 {
			hour += 12
		}
	default:
		return 0, 0, false
	}
	return hour, minute, true
}

func submatchText(text string, start, end int) string {
	if start < 0 || end < 0 {
		return ""
	}
	return text[start:end]
}

func calendarLinkSummary(text string, msg provider.Message) string {
	body := strings.ToLower(text)
	from := strings.ToLower(msg.From.String())
	switch {
	case strings.Contains(from, "royalmail") || strings.Contains(from, "royal mail"):
		if strings.Contains(body, "collection") {
			return "Royal Mail collection"
		}
		return "Royal Mail"
	case strings.Contains(body, "collection"):
		return "Collection"
	case strings.Contains(body, "delivery"):
		return "Delivery"
	}
	if subject := strings.TrimSpace(msg.Subject); subject != "" {
		return subject
	}
	return "Email event"
}

func (m *State) calendarLinkSpans(spans []glyph.Span, events []calendarLinkEvent) []glyph.Span {
	if len(events) == 0 || len(spans) == 0 {
		return spans
	}
	out := make([]glyph.Span, 0, len(spans)+len(events))
	for _, span := range spans {
		if span.OnSelect != nil || span.Text == "" {
			out = append(out, span)
			continue
		}
		out = appendCalendarLinkSpan(out, span, events, m.OpenCalendarEvent)
	}
	return out
}

func appendCalendarLinkSpan(out []glyph.Span, span glyph.Span, events []calendarLinkEvent, open func(calendarLinkEvent)) []glyph.Span {
	remaining := span.Text
	for remaining != "" {
		idx, trigger, event, ok := nextCalendarTrigger(remaining, events)
		if !ok {
			out = append(out, glyph.Span{Text: remaining, Style: span.Style})
			break
		}
		if idx > 0 {
			out = append(out, glyph.Span{Text: remaining[:idx], Style: span.Style})
		}
		linked := span
		linked.Text = remaining[idx : idx+len(trigger)]
		linked.Style.FG = glyph.Hex(0x7aa2f7)
		linked.Style.Attr |= glyph.AttrUnderline | glyph.AttrBold
		captured := event
		linked.OnSelect = func() {
			open(captured)
		}
		out = append(out, linked)
		remaining = remaining[idx+len(trigger):]
	}
	return out
}

func nextCalendarTrigger(text string, events []calendarLinkEvent) (int, string, calendarLinkEvent, bool) {
	bestIdx := len(text)
	var bestTrigger string
	var bestEvent calendarLinkEvent
	found := false
	for _, event := range events {
		for _, trigger := range event.Triggers {
			if trigger == "" {
				continue
			}
			idx := strings.Index(text, trigger)
			if idx >= 0 && idx < bestIdx {
				bestIdx = idx
				bestTrigger = trigger
				bestEvent = event
				found = true
			}
		}
	}
	return bestIdx, bestTrigger, bestEvent, found
}

func (m *State) OpenCalendarEvent(event calendarLinkEvent) {
	summary := event.Summary
	if summary == "" {
		summary = "Email event"
	}
	m.notifyInfo(fmt.Sprintf("opening %s...", summary))

	path, err := writeCalendarEventTemp(event)
	if err != nil {
		log.Printf("calendar link temp write failed: summary=%q start=%v end=%v: %v", summary, event.Start, event.End, err)
		m.notifyError(fmt.Sprintf("calendar: %v", err))
		return
	}
	open := m.attachmentOpener
	if open == nil {
		open = OpenSystemFile
	}
	if err := open(path); err != nil {
		log.Printf("calendar link system open failed: summary=%q path=%q start=%v end=%v: %v", summary, path, event.Start, event.End, err)
		m.notifyError(fmt.Sprintf("open calendar: %v", err))
	}
}

func writeCalendarEventTemp(event calendarLinkEvent) (string, error) {
	dir := filepath.Join(os.TempDir(), "mail-calendar")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	name := safeAttachmentFilename(event.Summary)
	if !strings.HasSuffix(strings.ToLower(name), ".ics") {
		name += ".ics"
	}
	f, err := os.CreateTemp(dir, "mail-*-"+name)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(renderCalendarEventICS(event)); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func renderCalendarEventICS(event calendarLinkEvent) string {
	now := time.Now().UTC().Format("20060102T150405Z")
	uid := calendarEventUID(event)
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//kungfusheep mail//EN\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString("UID:" + uid + "\r\n")
	b.WriteString("DTSTAMP:" + now + "\r\n")
	b.WriteString("DTSTART:" + event.Start.Format("20060102T150405") + "\r\n")
	b.WriteString("DTEND:" + event.End.Format("20060102T150405") + "\r\n")
	b.WriteString("SUMMARY:" + escapeCalendarEventText(event.Summary) + "\r\n")
	if event.Source != "" && event.Source != event.Summary {
		b.WriteString("DESCRIPTION:" + escapeCalendarEventText(event.Source) + "\r\n")
	}
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func calendarEventUID(event calendarLinkEvent) string {
	h := sha1.Sum([]byte(event.Summary + "|" + event.Start.Format(time.RFC3339) + "|" + event.End.Format(time.RFC3339) + "|" + event.Source))
	return hex.EncodeToString(h[:]) + "@mail.local"
}

func escapeCalendarEventText(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", "", ",", `\,`, ";", `\;`)
	return replacer.Replace(strings.TrimSpace(text))
}
