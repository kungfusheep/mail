package compose

import (
	"fmt"
	"strings"
)

// Subject extracts the subject from the first heading block, or empty string
func (e *Editor) Subject() string {
	for _, block := range e.doc.Blocks {
		switch block.Type {
		case BlockH1, BlockH2, BlockH3:
			return block.Text()
		}
	}
	return ""
}

// ToHTML converts the document to HTML for email sending
func (e *Editor) ToHTML() string {
	var b strings.Builder
	b.WriteString("<html><body>")

	for _, block := range e.doc.Blocks {
		switch block.Type {
		case BlockH1:
			b.WriteString("<h1>")
			writeRunsHTML(&b, block.Runs)
			b.WriteString("</h1>")
		case BlockH2:
			b.WriteString("<h2>")
			writeRunsHTML(&b, block.Runs)
			b.WriteString("</h2>")
		case BlockH3:
			b.WriteString("<h3>")
			writeRunsHTML(&b, block.Runs)
			b.WriteString("</h3>")
		case BlockQuote:
			b.WriteString("<blockquote>")
			writeRunsHTML(&b, block.Runs)
			b.WriteString("</blockquote>")
		case BlockListItem:
			b.WriteString("<li>")
			writeRunsHTML(&b, block.Runs)
			b.WriteString("</li>")
		case BlockCodeLine:
			b.WriteString("<pre><code>")
			b.WriteString(escapeHTML(block.Text()))
			b.WriteString("</code></pre>")
		default:
			b.WriteString("<p>")
			writeRunsHTML(&b, block.Runs)
			b.WriteString("</p>")
		}
	}

	b.WriteString("</body></html>")
	return b.String()
}

// ToPlainText converts the document to plain text
func (e *Editor) ToPlainText() string {
	var b strings.Builder
	for i, block := range e.doc.Blocks {
		if i > 0 {
			b.WriteRune('\n')
		}
		switch block.Type {
		case BlockQuote:
			b.WriteString("> ")
		case BlockListItem:
			if block.Attrs != nil && block.Attrs["marker"] == "number" {
				num := block.Attrs["number"]
				if num == "" {
					num = "1"
				}
				b.WriteString(num + ". ")
			} else {
				b.WriteString("- ")
			}
		}
		b.WriteString(block.Text())
	}
	return b.String()
}

func writeRunsHTML(b *strings.Builder, runs []Run) {
	for _, r := range runs {
		text := escapeHTML(r.Text)
		if r.Style.Has(StyleBold) {
			text = "<strong>" + text + "</strong>"
		}
		if r.Style.Has(StyleItalic) {
			text = "<em>" + text + "</em>"
		}
		if r.Style.Has(StyleUnderline) {
			text = "<u>" + text + "</u>"
		}
		if r.Style.Has(StyleStrikethrough) {
			text = "<del>" + text + "</del>"
		}
		if r.Style.Has(StyleCode) {
			text = "<code>" + text + "</code>"
		}
		fmt.Fprint(b, text)
	}
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
