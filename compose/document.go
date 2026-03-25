package compose

import (
	"unicode/utf8"
)

// InlineStyle represents styling that can be applied to text runs
type InlineStyle uint8

const (
	StyleNone          InlineStyle = 0
	StyleBold          InlineStyle = 1 << 0
	StyleItalic        InlineStyle = 1 << 1
	StyleUnderline     InlineStyle = 1 << 2
	StyleStrikethrough InlineStyle = 1 << 3
	StyleCode          InlineStyle = 1 << 4
)

func (s InlineStyle) Has(style InlineStyle) bool {
	return s&style != 0
}

func (s InlineStyle) With(style InlineStyle) InlineStyle {
	return s | style
}

func (s InlineStyle) Without(style InlineStyle) InlineStyle {
	return s &^ style
}

func (s InlineStyle) Toggle(style InlineStyle) InlineStyle {
	return s ^ style
}

// Run represents a contiguous span of text with the same style
type Run struct {
	Text  string
	Style InlineStyle
}

// BlockType identifies the semantic type of a block
type BlockType string

const (
	BlockParagraph     BlockType = "p"
	BlockH1            BlockType = "h1"
	BlockH2            BlockType = "h2"
	BlockH3            BlockType = "h3"
	BlockH4            BlockType = "h4"
	BlockH5            BlockType = "h5"
	BlockH6            BlockType = "h6"
	BlockCallout       BlockType = "callout"
	BlockList          BlockType = "list"
	BlockListItem      BlockType = "li"
	BlockCodeLine      BlockType = "codeline"
	BlockQuote         BlockType = "quote"
	BlockDivider       BlockType = "divider"
	BlockFrontMatter   BlockType = "frontmatter"
	BlockTable         BlockType = "table"
	BlockDialogue      BlockType = "dialogue"
	BlockParenthetical BlockType = "paren"
	BlockSceneHeading  BlockType = "scene"
)

// Block represents a structural element in the document
type Block struct {
	Type     BlockType
	Runs     []Run
	Children []Block           // for nested structures (lists, etc.)
	Attrs    map[string]string // type="note", ordered="true", etc.
}

// Text returns the plain text content of a block
func (b *Block) Text() string {
	var result string
	for _, r := range b.Runs {
		result += r.Text
	}
	return result
}

// Length returns the total character (rune) count of the block
func (b *Block) Length() int {
	total := 0
	for _, r := range b.Runs {
		total += utf8.RuneCountInString(r.Text)
	}
	return total
}

// Position represents a location in the document
type Position struct {
	Block  int // block index
	Offset int // character offset within block
}

// Annotation represents a highlight or comment on a range of text
type Annotation struct {
	From  Position
	To    Position
	Color string
	Note  string
	Layer string // for future: "review", "personal", etc.
}

// StyleChoice maps an element type to a template name
type StyleChoice struct {
	Element  string
	Template string
}

// Meta holds document metadata
type Meta struct {
	Title  string
	Styles []StyleChoice
}

// Document is the root container for a wed document
type Document struct {
	Version     int
	Theme       string
	Meta        Meta
	Blocks      []Block
	Annotations []Annotation
}

// NewDocument creates a new empty document with defaults
func NewDocument() *Document {
	return &Document{
		Version: 1,
		Theme:   "default",
		Meta: Meta{
			Title: "Untitled",
		},
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
		},
	}
}

// RunAt returns the run and offset within that run for a given block offset
func (b *Block) RunAt(offset int) (runIndex, runOffset int) {
	pos := 0
	for i, r := range b.Runs {
		runLen := utf8.RuneCountInString(r.Text)
		// offset within this run (exclusive of end boundary, except for last run)
		if offset < pos+runLen || (i == len(b.Runs)-1 && offset <= pos+runLen) {
			return i, offset - pos
		}
		pos += runLen
	}
	// past end - return last run
	if len(b.Runs) > 0 {
		lastRun := b.Runs[len(b.Runs)-1]
		return len(b.Runs) - 1, utf8.RuneCountInString(lastRun.Text)
	}
	return 0, 0
}

// SplitRunAt splits a run at the given offset, returning the new run slice
func (b *Block) SplitRunAt(offset int) {
	runIdx, runOff := b.RunAt(offset)
	if runIdx >= len(b.Runs) {
		return
	}
	run := b.Runs[runIdx]
	runes := []rune(run.Text)
	if runOff == 0 || runOff >= len(runes) {
		return // no split needed
	}
	// split into two runs
	left := Run{Text: string(runes[:runOff]), Style: run.Style}
	right := Run{Text: string(runes[runOff:]), Style: run.Style}
	newRuns := make([]Run, 0, len(b.Runs)+1)
	newRuns = append(newRuns, b.Runs[:runIdx]...)
	newRuns = append(newRuns, left, right)
	newRuns = append(newRuns, b.Runs[runIdx+1:]...)
	b.Runs = newRuns
}

// MergeAdjacentRuns combines adjacent runs with identical styles
func (b *Block) MergeAdjacentRuns() {
	if len(b.Runs) <= 1 {
		return
	}
	merged := make([]Run, 0, len(b.Runs))
	current := b.Runs[0]
	for i := 1; i < len(b.Runs); i++ {
		if b.Runs[i].Style == current.Style {
			current.Text += b.Runs[i].Text
		} else {
			if current.Text != "" {
				merged = append(merged, current)
			}
			current = b.Runs[i]
		}
	}
	if current.Text != "" {
		merged = append(merged, current)
	}
	if len(merged) == 0 {
		merged = []Run{{Text: "", Style: StyleNone}}
	}
	b.Runs = merged
}

// ApplyStyle applies a style to a range within the block (toggle semantics)
func (b *Block) ApplyStyle(start, end int, style InlineStyle) {
	if start >= end {
		return
	}
	// split at boundaries
	b.SplitRunAt(start)
	b.SplitRunAt(end)

	// toggle style on runs within range
	pos := 0
	for i := range b.Runs {
		runEnd := pos + len(b.Runs[i].Text)
		if pos >= start && runEnd <= end {
			b.Runs[i].Style = b.Runs[i].Style.Toggle(style)
		}
		pos = runEnd
	}

	b.MergeAdjacentRuns()
}

// ClearStyle removes all inline styles from a range
func (b *Block) ClearStyle(start, end int) {
	if start >= end {
		return
	}
	b.SplitRunAt(start)
	b.SplitRunAt(end)

	pos := 0
	for i := range b.Runs {
		runEnd := pos + len(b.Runs[i].Text)
		if pos >= start && runEnd <= end {
			b.Runs[i].Style = StyleNone
		}
		pos = runEnd
	}

	b.MergeAdjacentRuns()
}
