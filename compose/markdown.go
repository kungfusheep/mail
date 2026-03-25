package compose

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

// wedMetadata is the JSON structure for metadata stored in HTML comments
type wedMetadata struct {
	Title  string        `json:"title,omitempty"`
	Theme  string        `json:"theme,omitempty"`
	Styles []StyleChoice `json:"styles,omitempty"`
}

// ParseMarkdown converts markdown text to a Document
func ParseMarkdown(content string) *Document {
	doc := &Document{
		Version: 1,
		Theme:   "default",
		Meta: Meta{
			Title: "Untitled",
		},
	}

	// extract wed metadata from HTML comment if present
	content = extractWedMetadata(content, doc)

	// extract YAML front matter if present
	var frontMatterBlock *Block
	content, frontMatterBlock = extractFrontMatter(content)

	lines := strings.Split(content, "\n")
	var blocks []Block
	var currentParagraph []string

	flushParagraph := func() {
		if len(currentParagraph) > 0 {
			text := strings.Join(currentParagraph, " ")
			text = strings.TrimSpace(text)
			if text != "" {
				blocks = append(blocks, Block{
					Type: BlockParagraph,
					Runs: parseInlineMarkdown(text),
				})
			}
			currentParagraph = nil
		}
	}

	// track if we've added content (to avoid leading empty blocks)
	hasContent := false
	// track consecutive empty lines
	emptyLineCount := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// empty line ends paragraph and adds visual spacing
		if trimmed == "" {
			flushParagraph()
			emptyLineCount++
			// add empty block for visual spacing (but not at start of doc)
			if hasContent && emptyLineCount == 1 {
				blocks = append(blocks, Block{
					Type: BlockParagraph,
					Runs: []Run{{Text: "", Style: StyleNone}},
				})
			}
			continue
		}
		emptyLineCount = 0
		hasContent = true

		// headings
		if strings.HasPrefix(trimmed, "# ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "# ")
			blocks = append(blocks, Block{
				Type: BlockH1,
				Runs: parseInlineMarkdown(text),
			})
			// first h1 becomes document title
			if doc.Meta.Title == "Untitled" {
				doc.Meta.Title = stripMarkdown(text)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "## ")
			blocks = append(blocks, Block{
				Type: BlockH2,
				Runs: parseInlineMarkdown(text),
			})
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "### ")
			blocks = append(blocks, Block{
				Type: BlockH3,
				Runs: parseInlineMarkdown(text),
			})
			continue
		}
		if strings.HasPrefix(trimmed, "#### ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "#### ")
			blocks = append(blocks, Block{
				Type: BlockH4,
				Runs: parseInlineMarkdown(text),
			})
			continue
		}
		if strings.HasPrefix(trimmed, "##### ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "##### ")
			blocks = append(blocks, Block{
				Type: BlockH5,
				Runs: parseInlineMarkdown(text),
			})
			continue
		}
		if strings.HasPrefix(trimmed, "###### ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "###### ")
			blocks = append(blocks, Block{
				Type: BlockH6,
				Runs: parseInlineMarkdown(text),
			})
			continue
		}

		// blockquote
		if strings.HasPrefix(trimmed, "> ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "> ")
			// collect multi-line quotes
			for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "> ") {
				i++
				text += " " + strings.TrimPrefix(strings.TrimSpace(lines[i]), "> ")
			}
			blocks = append(blocks, Block{
				Type: BlockQuote,
				Runs: parseInlineMarkdown(text),
			})
			continue
		}

		// dialogue block (@@ CHARACTER)
		if strings.HasPrefix(trimmed, "@@ ") {
			flushParagraph()
			character := strings.TrimPrefix(trimmed, "@@ ")
			// collect dialogue lines until blank line or next @@
			var dialogueLines []string
			for i+1 < len(lines) {
				nextLine := lines[i+1]
				nextTrimmed := strings.TrimSpace(nextLine)
				if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "@@ ") {
					break
				}
				i++
				dialogueLines = append(dialogueLines, nextTrimmed)
			}
			dialogueText := strings.Join(dialogueLines, " ")
			blocks = append(blocks, Block{
				Type:  BlockDialogue,
				Runs:  parseInlineMarkdown(dialogueText),
				Attrs: map[string]string{"character": character},
			})
			continue
		}

		// parenthetical (( stage direction )
		if strings.HasPrefix(trimmed, "(( ") {
			flushParagraph()
			text := strings.TrimPrefix(trimmed, "(( ")
			blocks = append(blocks, Block{
				Type: BlockParenthetical,
				Runs: parseInlineMarkdown(text),
			})
			continue
		}

		// scene heading (INT., EXT., I/E., INT./EXT.)
		upperTrimmed := strings.ToUpper(trimmed)
		if strings.HasPrefix(upperTrimmed, "INT. ") || strings.HasPrefix(upperTrimmed, "EXT. ") ||
			strings.HasPrefix(upperTrimmed, "I/E. ") || strings.HasPrefix(upperTrimmed, "INT./EXT. ") {
			flushParagraph()
			blocks = append(blocks, Block{
				Type: BlockSceneHeading,
				Runs: parseInlineMarkdown(trimmed),
			})
			continue
		}

		// unordered list item
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flushParagraph()
			text := trimmed[2:]
			blocks = append(blocks, Block{
				Type:  BlockListItem,
				Runs:  parseInlineMarkdown(text),
				Attrs: map[string]string{"marker": "bullet"},
			})
			continue
		}

		// ordered list item (1. 2. etc)
		if match := regexp.MustCompile(`^(\d+)\.\s+(.*)$`).FindStringSubmatch(trimmed); match != nil {
			flushParagraph()
			blocks = append(blocks, Block{
				Type:  BlockListItem,
				Runs:  parseInlineMarkdown(match[2]),
				Attrs: map[string]string{"marker": "number", "number": match[1]},
			})
			continue
		}

		// table (lines starting with |)
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			flushParagraph()
			var tableLines []string
			tableLines = append(tableLines, trimmed)
			// collect all consecutive table lines
			for i+1 < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(nextTrimmed, "|") && strings.HasSuffix(nextTrimmed, "|") {
					i++
					tableLines = append(tableLines, nextTrimmed)
				} else {
					break
				}
			}
			blocks = append(blocks, Block{
				Type: BlockTable,
				Runs: []Run{{Text: strings.Join(tableLines, "\n"), Style: StyleNone}},
			})
			continue
		}

		// code block (fenced)
		if strings.HasPrefix(trimmed, "```") {
			flushParagraph()
			lang := strings.TrimPrefix(trimmed, "```")
			var codeLines []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				codeLines = append(codeLines, lines[i])
				i++
			}
			attrs := map[string]string{}
			if lang != "" {
				attrs["lang"] = lang
			}
			// empty code block gets one empty line
			if len(codeLines) == 0 {
				codeLines = []string{""}
			}
			for _, cl := range codeLines {
				a := make(map[string]string)
				for k, v := range attrs {
					a[k] = v
				}
				blocks = append(blocks, Block{
					Type:  BlockCodeLine,
					Runs:  []Run{{Text: cl, Style: StyleCode}},
					Attrs: a,
				})
			}
			continue
		}

		// horizontal rule / divider
		if trimmed == "---" || trimmed == "***" || trimmed == "___" ||
			strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "***") || strings.HasPrefix(trimmed, "___") {
			// make sure it's actually a divider (3+ chars of same type)
			if len(trimmed) >= 3 && (allSameRune(trimmed, '-') || allSameRune(trimmed, '*') || allSameRune(trimmed, '_')) {
				flushParagraph()
				blocks = append(blocks, Block{
					Type: BlockDivider,
					Runs: []Run{{Text: "", Style: StyleNone}},
				})
				continue
			}
		}

		// regular text - accumulate into paragraph
		currentParagraph = append(currentParagraph, trimmed)
	}

	flushParagraph()

	// prepend front matter block if present (with spacing after)
	if frontMatterBlock != nil {
		spacer := Block{Type: BlockParagraph, Runs: []Run{{Text: "", Style: StyleNone}}}
		blocks = append([]Block{*frontMatterBlock, spacer}, blocks...)
	}

	if len(blocks) == 0 {
		blocks = []Block{{Type: BlockParagraph, Runs: []Run{{Text: ""}}}}
	}

	doc.Blocks = blocks
	return doc
}

// parseInlineMarkdown converts inline markdown (bold, italic, etc.) to runs
func parseInlineMarkdown(text string) []Run {
	if text == "" {
		return []Run{{Text: "", Style: StyleNone}}
	}

	var runs []Run

	// process text character by character, tracking style state
	type styleMarker struct {
		style    InlineStyle
		startIdx int
	}

	runes := []rune(text)
	var currentText strings.Builder
	var currentStyle InlineStyle
	i := 0

	flushRun := func() {
		if currentText.Len() > 0 {
			runs = append(runs, Run{Text: currentText.String(), Style: currentStyle})
			currentText.Reset()
		}
	}

	for i < len(runes) {
		// code (backtick) - highest priority
		if runes[i] == '`' {
			flushRun()
			// find closing backtick
			end := i + 1
			for end < len(runes) && runes[end] != '`' {
				end++
			}
			if end < len(runes) {
				runs = append(runs, Run{
					Text:  string(runes[i+1 : end]),
					Style: StyleCode,
				})
				i = end + 1
				continue
			}
		}

		// bold (**text** or __text__)
		if i+1 < len(runes) && ((runes[i] == '*' && runes[i+1] == '*') || (runes[i] == '_' && runes[i+1] == '_')) {
			marker := string(runes[i : i+2])
			end := strings.Index(string(runes[i+2:]), marker)
			if end >= 0 {
				flushRun()
				innerText := string(runes[i+2 : i+2+end])
				// parse inner text for nested styles
				innerRuns := parseInlineMarkdown(innerText)
				for _, r := range innerRuns {
					runs = append(runs, Run{
						Text:  r.Text,
						Style: r.Style.With(StyleBold),
					})
				}
				i = i + 2 + end + 2
				continue
			}
		}

		// strikethrough (~~text~~)
		if i+1 < len(runes) && runes[i] == '~' && runes[i+1] == '~' {
			end := strings.Index(string(runes[i+2:]), "~~")
			if end >= 0 {
				flushRun()
				innerText := string(runes[i+2 : i+2+end])
				innerRuns := parseInlineMarkdown(innerText)
				for _, r := range innerRuns {
					runs = append(runs, Run{
						Text:  r.Text,
						Style: r.Style.With(StyleStrikethrough),
					})
				}
				i = i + 2 + end + 2
				continue
			}
		}

		// italic (*text* or _text_) - check single marker after ruling out double
		if runes[i] == '*' || runes[i] == '_' {
			marker := runes[i]
			// make sure it's not a double marker (already handled above)
			if i+1 < len(runes) && runes[i+1] == marker {
				// this is a double marker that didn't match - output as literal
				currentText.WriteRune(runes[i])
				i++
				continue
			}
			// find closing single marker
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == marker {
					// make sure it's not part of a double marker
					if j+1 < len(runes) && runes[j+1] == marker {
						j++ // skip the double
						continue
					}
					end = j
					break
				}
			}
			if end > i+1 {
				flushRun()
				innerText := string(runes[i+1 : end])
				innerRuns := parseInlineMarkdown(innerText)
				for _, r := range innerRuns {
					runs = append(runs, Run{
						Text:  r.Text,
						Style: r.Style.With(StyleItalic),
					})
				}
				i = end + 1
				continue
			}
		}

		// regular character
		currentText.WriteRune(runes[i])
		i++
	}

	flushRun()

	if len(runs) == 0 {
		runs = []Run{{Text: text, Style: StyleNone}}
	}

	return runs
}

// parseInlineMarkdownRaw parses inline markdown but keeps markers in the text.
// used in raw mode so markers are visible AND styled. flat parsing (no nesting).
func parseInlineMarkdownRaw(text string) []Run {
	if text == "" {
		return []Run{{Text: "", Style: StyleNone}}
	}

	var runs []Run
	runes := []rune(text)
	var buf []rune
	i := 0

	flush := func() {
		if len(buf) > 0 {
			runs = append(runs, Run{Text: string(buf), Style: StyleNone})
			buf = buf[:0]
		}
	}

	for i < len(runes) {
		// backtick code
		if runes[i] == '`' {
			end := i + 1
			for end < len(runes) && runes[end] != '`' {
				end++
			}
			if end < len(runes) {
				flush()
				runs = append(runs, Run{Text: string(runes[i : end+1]), Style: StyleCode})
				i = end + 1
				continue
			}
		}

		// strikethrough ~~
		if i+1 < len(runes) && runes[i] == '~' && runes[i+1] == '~' {
			closing := strings.Index(string(runes[i+2:]), "~~")
			if closing >= 0 {
				flush()
				end := i + 2 + closing + 2
				runs = append(runs, Run{Text: string(runes[i:end]), Style: StyleStrikethrough})
				i = end
				continue
			}
		}

		// bold+italic ***
		if i+2 < len(runes) && runes[i] == '*' && runes[i+1] == '*' && runes[i+2] == '*' {
			closing := strings.Index(string(runes[i+3:]), "***")
			if closing >= 0 {
				flush()
				end := i + 3 + closing + 3
				runs = append(runs, Run{Text: string(runes[i:end]), Style: StyleBold | StyleItalic})
				i = end
				continue
			}
		}

		// bold **
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			closing := strings.Index(string(runes[i+2:]), "**")
			if closing >= 0 {
				flush()
				end := i + 2 + closing + 2
				runs = append(runs, Run{Text: string(runes[i:end]), Style: StyleBold})
				i = end
				continue
			}
		}

		// italic * (single, after ruling out **)
		if runes[i] == '*' && (i+1 >= len(runes) || runes[i+1] != '*') {
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '*' && (j+1 >= len(runes) || runes[j+1] != '*') {
					end = j
					break
				}
			}
			if end > i+1 {
				flush()
				runs = append(runs, Run{Text: string(runes[i : end+1]), Style: StyleItalic})
				i = end + 1
				continue
			}
		}

		buf = append(buf, runes[i])
		i++
	}

	flush()
	if len(runs) == 0 {
		runs = []Run{{Text: text, Style: StyleNone}}
	}
	return runs
}

// stripMarkdown removes markdown formatting from text (for plain text extraction)
func stripMarkdown(text string) string {
	// remove bold/italic markers
	text = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`\*(.+?)\*`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`_(.+?)_`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile("`(.+?)`").ReplaceAllString(text, "$1")
	return text
}

// IsMarkdownFile checks if a filename has a markdown extension
func IsMarkdownFile(filename string) bool {
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}

// extractFrontMatter extracts YAML front matter (---...---) from content
// Returns the remaining content and a front matter block (if any)
func extractFrontMatter(content string) (string, *Block) {
	// front matter must start at the very beginning with ---
	if !strings.HasPrefix(content, "---") {
		return content, nil
	}

	// find the closing ---
	rest := content[3:]
	// skip any newline after opening ---
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		// try \r\n---
		endIdx = strings.Index(rest, "\r\n---")
		if endIdx < 0 {
			return content, nil // no closing delimiter
		}
	}

	// extract the front matter content
	fmContent := strings.TrimSpace(rest[:endIdx])
	remaining := rest[endIdx+4:] // skip \n---

	// skip any newline after closing ---
	if strings.HasPrefix(remaining, "\n") {
		remaining = remaining[1:]
	} else if strings.HasPrefix(remaining, "\r\n") {
		remaining = remaining[2:]
	}

	// create the front matter block with the raw content
	block := &Block{
		Type:  BlockFrontMatter,
		Runs:  []Run{{Text: fmContent, Style: StyleNone.With(8)}},
		Attrs: map[string]string{"content": fmContent},
	}

	return strings.TrimSpace(remaining), block
}

// extractWedMetadata looks for <!-- wed:{...} --> comment and parses metadata
func extractWedMetadata(content string, doc *Document) string {
	// look for wed metadata comment anywhere in the file
	re := regexp.MustCompile(`<!--\s*wed:(.*?)-->`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		return content
	}

	// parse the JSON
	var meta wedMetadata
	if err := json.Unmarshal([]byte(match[1]), &meta); err == nil {
		if meta.Title != "" {
			doc.Meta.Title = meta.Title
		}
		if meta.Theme != "" {
			doc.Theme = meta.Theme
		}
		if len(meta.Styles) > 0 {
			doc.Meta.Styles = meta.Styles
		}
	}

	// remove the comment from content
	content = re.ReplaceAllString(content, "")
	return strings.TrimSpace(content)
}

// WriteMarkdown serializes a Document to markdown format
func WriteMarkdown(doc *Document, w io.Writer) error {
	var sb strings.Builder

	// write metadata comment at the start if we have non-default values
	meta := wedMetadata{
		Title:  doc.Meta.Title,
		Theme:  doc.Theme,
		Styles: doc.Meta.Styles,
	}
	// only write if there's something meaningful
	if meta.Title != "Untitled" || meta.Theme != "default" || len(meta.Styles) > 0 {
		if metaJSON, err := json.Marshal(meta); err == nil {
			sb.WriteString("<!-- wed:")
			sb.Write(metaJSON)
			sb.WriteString(" -->\n\n")
		}
	}

	for i := 0; i < len(doc.Blocks); i++ {
		block := doc.Blocks[i]
		// add blank line between blocks (except at start)
		if i > 0 && block.Type != BlockParagraph || (block.Type == BlockParagraph && block.Length() > 0) {
			// check if previous block was empty (visual spacer)
			if i > 0 && doc.Blocks[i-1].Length() == 0 {
				// already have spacing from empty block
			} else if i > 0 {
				sb.WriteString("\n")
			}
		}

		switch block.Type {
		case BlockH1:
			sb.WriteString("# ")
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockH2:
			sb.WriteString("## ")
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockH3:
			sb.WriteString("### ")
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockH4:
			sb.WriteString("#### ")
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockH5:
			sb.WriteString("##### ")
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockH6:
			sb.WriteString("###### ")
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockQuote:
			sb.WriteString("> ")
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockListItem:
			marker := block.Attrs["marker"]
			if marker == "number" {
				num := block.Attrs["number"]
				if num == "" {
					num = "1"
				}
				sb.WriteString(num)
				sb.WriteString(". ")
			} else {
				sb.WriteString("- ")
			}
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockCodeLine:
			// collect consecutive code lines into a single fenced block
			lang := block.Attrs["lang"]
			sb.WriteString("```")
			sb.WriteString(lang)
			sb.WriteString("\n")
			sb.WriteString(block.Text())
			for i+1 < len(doc.Blocks) && doc.Blocks[i+1].Type == BlockCodeLine {
				i++
				sb.WriteString("\n")
				sb.WriteString(doc.Blocks[i].Text())
			}
			sb.WriteString("\n```\n")
		case BlockDivider:
			sb.WriteString("---\n")
		case BlockTable:
			// tables are stored as-is with pipe syntax
			sb.WriteString(block.Text())
			sb.WriteString("\n")
		case BlockFrontMatter:
			sb.WriteString("---\n")
			sb.WriteString(block.Text())
			sb.WriteString("\n---\n")
		case BlockDialogue:
			character := block.Attrs["character"]
			sb.WriteString("@@ ")
			sb.WriteString(character)
			sb.WriteString("\n")
			dialogue := runsToMarkdown(block.Runs)
			if dialogue != "" {
				sb.WriteString(dialogue)
				sb.WriteString("\n")
			}
		case BlockParenthetical:
			text := runsToMarkdown(block.Runs)
			// write as (( text format
			sb.WriteString("(( ")
			sb.WriteString(text)
			sb.WriteString("\n")
		case BlockSceneHeading:
			// write scene heading as-is (uppercase handled by renderer)
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		case BlockParagraph:
			text := runsToMarkdown(block.Runs)
			if text == "" {
				// empty paragraph = blank line
				sb.WriteString("\n")
			} else {
				sb.WriteString(text)
				sb.WriteString("\n")
			}
		default:
			// fallback: just output the text
			sb.WriteString(runsToMarkdown(block.Runs))
			sb.WriteString("\n")
		}
	}

	_, err := w.Write([]byte(sb.String()))
	return err
}

// runsToMarkdown converts runs back to markdown inline syntax
func runsToMarkdown(runs []Run) string {
	var sb strings.Builder

	for _, r := range runs {
		text := r.Text
		if r.Style == StyleNone {
			sb.WriteString(text)
			continue
		}

		// wrap text with markdown syntax (order matters for nesting)
		if r.Style.Has(StyleCode) {
			text = "`" + text + "`"
		}
		if r.Style.Has(StyleStrikethrough) {
			text = "~~" + text + "~~"
		}
		if r.Style.Has(StyleBold) && r.Style.Has(StyleItalic) {
			text = "***" + text + "***"
		} else if r.Style.Has(StyleBold) {
			text = "**" + text + "**"
		} else if r.Style.Has(StyleItalic) {
			text = "*" + text + "*"
		}
		// underline has no standard markdown - skip or could use <u> but keeping it pure

		sb.WriteString(text)
	}

	return sb.String()
}

// allSameRune returns true if the string consists only of the given rune
func allSameRune(s string, r rune) bool {
	for _, c := range s {
		if c != r {
			return false
		}
	}
	return true
}
