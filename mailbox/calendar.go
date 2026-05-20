package mailbox

import (
	"strings"
	"time"
)

type calendarSummary struct {
	Title string
	When  string
}

func parseCalendarSummary(data string) (calendarSummary, bool) {
	lines := unfoldCalendarLines(data)
	inEvent := false
	var summary calendarSummary
	for _, line := range lines {
		name, params, value, ok := splitCalendarLine(line)
		if !ok {
			continue
		}
		switch name {
		case "BEGIN":
			inEvent = strings.EqualFold(strings.TrimSpace(value), "VEVENT")
		case "END":
			if inEvent && strings.EqualFold(strings.TrimSpace(value), "VEVENT") {
				if summary.Title != "" || summary.When != "" {
					return summary, true
				}
				inEvent = false
			}
		case "SUMMARY":
			if inEvent && summary.Title == "" {
				summary.Title = unescapeCalendarText(value)
			}
		case "DTSTART":
			if inEvent && summary.When == "" {
				summary.When = formatCalendarDate(value, params)
			}
		}
	}
	return summary, summary.Title != "" || summary.When != ""
}

func unfoldCalendarLines(data string) []string {
	raw := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if len(lines) > 0 && (line[0] == ' ' || line[0] == '\t') {
			lines[len(lines)-1] += strings.TrimLeft(line, " \t")
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func splitCalendarLine(line string) (string, map[string]string, string, bool) {
	before, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", nil, "", false
	}
	parts := strings.Split(before, ";")
	name := strings.ToUpper(strings.TrimSpace(parts[0]))
	if name == "" {
		return "", nil, "", false
	}
	params := make(map[string]string)
	for _, part := range parts[1:] {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		params[strings.ToUpper(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(val), `"`)
	}
	return name, params, strings.TrimSpace(value), true
}

func unescapeCalendarText(text string) string {
	var b strings.Builder
	escaped := false
	for _, r := range text {
		if escaped {
			switch r {
			case 'n', 'N':
				b.WriteRune(' ')
			case ',', ';', '\\':
				b.WriteRune(r)
			default:
				b.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func formatCalendarDate(value string, params map[string]string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.EqualFold(params["VALUE"], "DATE") || len(value) == len("20060102") {
		if t, err := time.Parse("20060102", value); err == nil {
			return t.Format("Mon 2 Jan")
		}
	}
	layout := "20060102T150405"
	parseValue := strings.TrimSuffix(value, "Z")
	var (
		t   time.Time
		err error
	)
	if strings.HasSuffix(value, "Z") {
		t, err = time.Parse(layout, parseValue)
		if err == nil {
			t = t.UTC()
		}
	} else {
		t, err = time.ParseInLocation(layout, parseValue, time.Local)
	}
	if err != nil {
		return ""
	}
	return t.Format("Mon 2 Jan, 15:04")
}
