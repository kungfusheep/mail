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
	lines := strings.Split(body, "\n")
	var result []string
	consecutiveQuoted := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// "On ... wrote:" — everything after is quoted
		if onWrotePattern.MatchString(trimmed) {
			break
		}

		// forwarded message marker
		if forwardPattern.MatchString(trimmed) {
			break
		}

		// signature markers
		if trimmed == "--" || trimmed == "-- " {
			break
		}

		// common mobile signatures
		lower := strings.ToLower(trimmed)
		if lower == "sent from my iphone" ||
			lower == "sent from my ipad" ||
			strings.HasPrefix(lower, "sent from my ") ||
			strings.HasPrefix(lower, "get outlook for") {
			break
		}

		// quote-prefixed lines
		if strings.HasPrefix(trimmed, ">") {
			consecutiveQuoted++
			// skip isolated single ">" lines too
			continue
		}

		// if we had quoted lines and now hit non-quoted, keep going
		// (there might be interleaved text)
		consecutiveQuoted = 0
		result = append(result, line)
	}

	// trim trailing empty lines
	for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
		result = result[:len(result)-1]
	}

	return strings.Join(result, "\n")
}
