package preview

import "strings"

type SegmentKind int

const (
	SegmentMain SegmentKind = iota
	SegmentQuote
	SegmentSignature
	SegmentForwarded
	SegmentFooter
)

type Segment struct {
	Kind SegmentKind
	Text string
}

func SegmentText(body string) []Segment {
	lines := strings.Split(body, "\n")
	segments := make([]Segment, 0, 4)
	kind := SegmentMain

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		switch {
		case onWrotePattern.MatchString(trimmed):
			kind = SegmentQuote
		case forwardPattern.MatchString(trimmed):
			kind = SegmentForwarded
		case trimmed == "--" || trimmed == "-- " || isMobileSignature(lower):
			kind = SegmentSignature
		case kind == SegmentMain && isFooterLine(lower):
			kind = SegmentFooter
		case strings.HasPrefix(trimmed, ">"):
			appendSegment(&segments, SegmentQuote, line)
			continue
		}

		appendSegment(&segments, kind, line)
	}

	out := segments[:0]
	for _, segment := range segments {
		segment.Text = strings.Trim(segment.Text, "\n")
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}
		out = append(out, segment)
	}
	return out
}

func appendSegment(segments *[]Segment, kind SegmentKind, line string) {
	if len(*segments) == 0 || (*segments)[len(*segments)-1].Kind != kind {
		*segments = append(*segments, Segment{Kind: kind, Text: line})
		return
	}
	last := &(*segments)[len(*segments)-1]
	last.Text += "\n" + line
}

func isMobileSignature(lower string) bool {
	return lower == "sent from my iphone" ||
		lower == "sent from my ipad" ||
		strings.HasPrefix(lower, "sent from my ") ||
		strings.HasPrefix(lower, "get outlook for")
}

func isFooterLine(lower string) bool {
	if lower == "" {
		return false
	}
	needles := []string{
		"unsubscribe",
		"email preferences",
		"manage your subscription",
		"privacy policy",
		"terms & conditions",
		"terms and conditions",
		"registered office",
		"company number",
		"this mailbox is not monitored",
		"please do not reply",
		"confidentiality notice",
		"intended recipient",
	}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
