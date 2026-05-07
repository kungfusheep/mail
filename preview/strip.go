package preview

import (
	"regexp"
	"strings"
)

var onWrotePattern = regexp.MustCompile(`(?i)^on .+ wrote:\s*$`)
var forwardPattern = regexp.MustCompile(`(?i)^-+ ?(forwarded message|original message) ?-+\s*$`)

// StripQuoted removes quoted reply text from an email body, returning
// only the new content the sender wrote. Handles:
//   - "On [date], [person] wrote:" markers
//   - "> " prefixed quote lines
//   - forwarded message markers
//   - common signature markers
func StripQuoted(body string) string {
	var result []string
	for _, segment := range SegmentText(body) {
		if segment.Kind != SegmentMain {
			break
		}
		result = append(result, strings.Split(segment.Text, "\n")...)
	}

	// trim trailing empty lines
	for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
		result = result[:len(result)-1]
	}

	return strings.Join(result, "\n")
}
