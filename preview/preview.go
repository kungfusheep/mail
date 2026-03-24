package preview

import (
	"os/exec"
	"strings"
)

// RenderHTML converts html to plain text using w3m.
// falls back to raw text body if w3m is unavailable.
func RenderHTML(html, fallbackText string) string {
	if html == "" {
		return fallbackText
	}

	cmd := exec.Command("w3m", "-dump", "-T", "text/html", "-cols", "80")
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
