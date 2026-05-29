package mailbox

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/provider"
	"github.com/olebedev/when"
)

type calendarLinkEvent struct {
	Summary  string
	Source   string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Triggers []string
}

type calendarDateCandidate struct {
	Start int
	End   int
	Text  string
	Date  time.Time
}

var (
	calendarDateRE       = regexp.MustCompile(`(?i)\b(?:(?:monday|mon|tuesday|tues|tue|wednesday|wed|thursday|thurs|thur|thu|friday|fri|saturday|sat|sunday|sun)\s+)?\d{1,2}(?:st|nd|rd|th)?\s+(?:january|jan|february|feb|march|mar|april|apr|may|june|jun|july|jul|august|aug|september|sept|sep|october|oct|november|nov|december|dec)(?:\s+\d{4})?\*?`)
	calendarTimeRangeRE  = regexp.MustCompile(`(?i)\bbetween\s+(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)\s+and\s+(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)\b`)
	calendarSingleTimeRE = regexp.MustCompile(`(?i)\bat\s+(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)?\b`)
	calendarOrdinalRE    = regexp.MustCompile(`(?i)(\d{1,2})(st|nd|rd|th)`)
	calendarMonthWordRE  = regexp.MustCompile(`(?i)\b(?:january|jan\.?|february|feb\.?|march|mar\.?|april|apr\.?|may|june|jun\.?|july|jul\.?|august|aug\.?|september|sept?\.?|october|oct\.?|november|nov\.?|december|dec\.?)\b`)
	calendarDigitRE      = regexp.MustCompile(`\d`)
	calendarRangeJoinRE  = regexp.MustCompile(`(?i)^\s*(?:to|until|through|-|–|—)\s*$`)
)

func calendarLinkEvents(text string, msg provider.Message) []calendarLinkEvent {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	candidates := calendarDateCandidates(text, msg.Date)
	if len(candidates) == 0 {
		return nil
	}

	events := make([]calendarLinkEvent, 0, len(candidates))
	seen := make(map[string]bool)
	consumed := make(map[int]bool)
	for i, candidate := range candidates {
		if consumed[candidate.Start] {
			continue
		}

		if endDate, rangeTrigger, endStart, ok := calendarDateRangeEnd(text, candidate, candidates[i+1:]); ok {
			start := time.Date(candidate.Date.Year(), candidate.Date.Month(), candidate.Date.Day(), 0, 0, 0, 0, time.Local)
			end := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
			if addCalendarLinkEvent(&events, seen, calendarLinkEvent{
				Summary:  calendarLinkSummary(text, msg),
				Source:   strings.TrimSpace(msg.Subject),
				Start:    start,
				End:      end,
				AllDay:   true,
				Triggers: []string{rangeTrigger},
			}) {
				consumed[candidate.Start] = true
				consumed[endStart] = true
			}
			continue
		}

		windowEnd := min(len(text), candidate.End+220)
		nearby := text[candidate.Start:windowEnd]
		date := candidate.Date
		start, end, rangeText, ok := parseBodyCalendarTime(date, nearby)
		allDay := false
		if !ok {
			if !calendarDateOnlyContext(text, []int{candidate.Start, candidate.End}) {
				continue
			}
			start = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.Local)
			end = start.AddDate(0, 0, 1)
			rangeText = ""
			allDay = true
		}

		addCalendarLinkEvent(&events, seen, calendarLinkEvent{
			Summary:  calendarLinkSummary(text, msg),
			Source:   strings.TrimSpace(msg.Subject),
			Start:    start,
			End:      end,
			AllDay:   allDay,
			Triggers: calendarLinkTriggers(candidate.Text, rangeText),
		})
	}
	return events
}

func addCalendarLinkEvent(events *[]calendarLinkEvent, seen map[string]bool, event calendarLinkEvent) bool {
	key := event.Start.Format(time.RFC3339) + "|" + event.End.Format(time.RFC3339)
	if seen[key] {
		return false
	}
	seen[key] = true
	*events = append(*events, event)
	return true
}

func calendarDateCandidates(text string, reference time.Time) []calendarDateCandidate {
	candidates := make([]calendarDateCandidate, 0)
	for _, match := range calendarDateRE.FindAllStringIndex(text, -1) {
		dateText := text[match[0]:match[1]]
		date, ok := parseBodyCalendarDate(dateText, reference)
		if !ok {
			continue
		}
		candidates = append(candidates, calendarDateCandidate{
			Start: match[0],
			End:   match[1],
			Text:  dateText,
			Date:  date,
		})
	}
	for _, candidate := range whenCalendarDateCandidates(text, reference) {
		if calendarCandidateOverlaps(candidates, candidate) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Start < candidates[j].Start
	})
	return candidates
}

func whenCalendarDateCandidates(text string, reference time.Time) []calendarDateCandidate {
	base := reference
	if base.IsZero() {
		base = time.Now()
	}
	candidates := make([]calendarDateCandidate, 0)
	offset := 0
	for offset < len(text) {
		result, err := when.EN.Parse(text[offset:], base)
		if err != nil || result == nil {
			break
		}
		start := offset + result.Index
		end := start + len(result.Text)
		if calendarWhenDateText(result.Text) {
			candidates = append(candidates, calendarDateCandidate{
				Start: start,
				End:   end,
				Text:  strings.TrimSpace(result.Text),
				Date:  result.Time,
			})
		}
		if end <= offset {
			offset++
			continue
		}
		offset = end
	}
	return candidates
}

func calendarWhenDateText(text string) bool {
	return calendarDigitRE.MatchString(text) && calendarMonthWordRE.MatchString(text)
}

func calendarCandidateOverlaps(candidates []calendarDateCandidate, candidate calendarDateCandidate) bool {
	for _, existing := range candidates {
		if candidate.Start < existing.End && candidate.End > existing.Start {
			return true
		}
	}
	return false
}

func calendarDateRangeEnd(text string, start calendarDateCandidate, candidates []calendarDateCandidate) (time.Time, string, int, bool) {
	for _, candidate := range candidates {
		if candidate.Start-start.End > 40 {
			return time.Time{}, "", 0, false
		}
		between := strings.TrimSpace(text[start.End:candidate.Start])
		if !calendarRangeJoinRE.MatchString(between) {
			continue
		}
		endDate := time.Date(candidate.Date.Year(), candidate.Date.Month(), candidate.Date.Day(), 0, 0, 0, 0, time.Local)
		startDate := time.Date(start.Date.Year(), start.Date.Month(), start.Date.Day(), 0, 0, 0, 0, time.Local)
		if !endDate.Before(startDate) {
			return candidate.Date, strings.TrimSpace(text[start.Start:candidate.End]), candidate.Start, true
		}
	}
	return time.Time{}, "", 0, false
}

func calendarLinkTriggers(dateText, rangeText string) []string {
	if rangeText == "" {
		return []string{dateText}
	}
	return []string{dateText, rangeText}
}

func parseBodyCalendarDate(text string, reference time.Time) (time.Time, bool) {
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

	year := time.Now().In(time.Local).Year()
	if !reference.IsZero() {
		year = reference.In(time.Local).Year()
	}
	noYearLayouts := []string{"Monday 2 January", "Mon 2 January", "2 January", "Monday 2 Jan", "Mon 2 Jan", "2 Jan"}
	for _, layout := range noYearLayouts {
		t, err := time.ParseInLocation(layout, clean, time.Local)
		if err != nil {
			continue
		}
		t = time.Date(year, t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
		if !reference.IsZero() {
			refDay := time.Date(reference.In(time.Local).Year(), reference.In(time.Local).Month(), reference.In(time.Local).Day(), 0, 0, 0, 0, time.Local)
			if t.Before(refDay.AddDate(0, 0, -1)) {
				t = t.AddDate(1, 0, 0)
			}
		}
		return t, true
	}
	if len(fields) > 2 {
		withoutWeekday := strings.Join(fields[1:], " ")
		for _, layout := range []string{"2 January", "2 Jan"} {
			t, err := time.ParseInLocation(layout, withoutWeekday, time.Local)
			if err != nil {
				continue
			}
			t = time.Date(year, t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			if !reference.IsZero() {
				refDay := time.Date(reference.In(time.Local).Year(), reference.In(time.Local).Month(), reference.In(time.Local).Day(), 0, 0, 0, 0, time.Local)
				if t.Before(refDay.AddDate(0, 0, -1)) {
					t = t.AddDate(1, 0, 0)
				}
			}
			return t, true
		}
	}
	return time.Time{}, false
}

func parseBodyCalendarTime(date time.Time, text string) (time.Time, time.Time, string, bool) {
	if match := calendarTimeRangeRE.FindStringSubmatchIndex(text); match != nil {
		start, end, ok := parseBodyCalendarRange(date, text, match)
		return start, end, text[match[0]:match[1]], ok
	}
	if match := calendarSingleTimeRE.FindStringSubmatchIndex(text); match != nil {
		start, end, ok := parseBodyCalendarSingleTime(date, text, match)
		return start, end, text[match[0]:match[1]], ok
	}
	return time.Time{}, time.Time{}, "", false
}

func calendarDateOnlyContext(text string, match []int) bool {
	beforeStart := max(0, match[0]-90)
	afterEnd := min(len(text), match[1]+60)
	context := strings.ToLower(text[beforeStart:afterEnd])
	keywords := []string{
		"appointment",
		"booking",
		"deadline",
		"due",
		"expires",
		"expiry",
		"renew",
		"renewal",
		"valid until",
	}
	for _, keyword := range keywords {
		if strings.Contains(context, keyword) {
			return true
		}
	}
	return false
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

func parseBodyCalendarSingleTime(date time.Time, text string, match []int) (time.Time, time.Time, bool) {
	hour, minute, ok := parseBodyClock(text[match[2]:match[3]], submatchText(text, match[4], match[5]), submatchText(text, match[6], match[7]))
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, time.Local)
	return start, start.Add(time.Hour), true
}

func parseBodyClock(hourText, minuteText, meridiem string) (int, int, bool) {
	hour, err := strconv.Atoi(hourText)
	if err != nil {
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
	case "":
		if hour < 0 || hour > 23 {
			return 0, 0, false
		}
	case "am":
		if hour < 1 || hour > 12 {
			return 0, 0, false
		}
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 1 || hour > 12 {
			return 0, 0, false
		}
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
			if idx >= 0 && (idx < bestIdx || idx == bestIdx && len(trigger) > len(bestTrigger)) {
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
	if event.AllDay {
		b.WriteString("DTSTART;VALUE=DATE:" + event.Start.Format("20060102") + "\r\n")
		b.WriteString("DTEND;VALUE=DATE:" + event.End.Format("20060102") + "\r\n")
	} else {
		b.WriteString("DTSTART:" + event.Start.Format("20060102T150405") + "\r\n")
		b.WriteString("DTEND:" + event.End.Format("20060102T150405") + "\r\n")
	}
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
