package preview

import (
	"fmt"
	"os/exec"
	"strings"
)

// RenderHTML converts html to plain text using w3m.
// falls back to raw text body if w3m is unavailable.
// cols sets the wrap width (0 defaults to 72).
func RenderHTML(html, fallbackText string, cols ...int) string {
	if html == "" {
		return fallbackText
	}

	width := 72
	if len(cols) > 0 && cols[0] > 0 {
		width = cols[0]
	}

	cmd := exec.Command("w3m", "-dump", "-T", "text/html", "-cols", fmt.Sprintf("%d", width))
	cmd.Stdin = strings.NewReader(html)

	out, err := cmd.Output()
	if err != nil {
		if fallbackText != "" {
			return fallbackText
		}
		return html
	}
	return string(out)
}

// RenderToLines splits rendered text into lines for display.
func RenderToLines(html, fallbackText string) []string {
	text := RenderHTML(html, fallbackText)
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
