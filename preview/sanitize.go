package preview

import (
	"strings"
	"unicode"
)

// Sanitize strips zero-width and invisible Unicode characters that cause
// terminal rendering issues (cursor drift, ghost characters). Call once
// on text converted from HTML before display.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isInvisible(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isInvisible(r rune) bool {
	// keep normal whitespace and printable characters
	if r <= 0x7F {
		return false
	}
	// zero-width and invisible formatting characters
	switch r {
	case '\u034F', // combining grapheme joiner
		'\u200B', // zero-width space
		'\u200C', // zero-width non-joiner
		'\u200D', // zero-width joiner
		'\u200E', // left-to-right mark
		'\u200F', // right-to-left mark
		'\u2060', // word joiner
		'\u2061', // function application
		'\u2062', // invisible times
		'\u2063', // invisible separator
		'\u2064', // invisible plus
		'\uFEFF', // byte order mark / zero-width no-break space
		'\uFFF9', // interlinear annotation anchor
		'\uFFFA', // interlinear annotation separator
		'\uFFFB': // interlinear annotation terminator
		return true
	}
	// unicode "format" category (Cf) covers the rest
	if unicode.Is(unicode.Cf, r) {
		return true
	}
	return false
}
