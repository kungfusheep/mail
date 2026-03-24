package compose

import "unicode/utf8"

type InlineStyle uint8

const (
	StyleNone          InlineStyle = 0
	StyleBold          InlineStyle = 1 << 0
	StyleItalic        InlineStyle = 1 << 1
	StyleUnderline     InlineStyle = 1 << 2
	StyleStrikethrough InlineStyle = 1 << 3
	StyleCode          InlineStyle = 1 << 4
)

func (s InlineStyle) Has(style InlineStyle) bool    { return s&style != 0 }
func (s InlineStyle) With(style InlineStyle) InlineStyle    { return s | style }
func (s InlineStyle) Without(style InlineStyle) InlineStyle { return s &^ style }
func (s InlineStyle) Toggle(style InlineStyle) InlineStyle  { return s ^ style }

type Run struct {
	Text  string
	Style InlineStyle
}

type BlockType string

const (
	BlockParagraph BlockType = "p"
	BlockH1        BlockType = "h1"
	BlockH2        BlockType = "h2"
	BlockH3        BlockType = "h3"
	BlockQuote     BlockType = "quote"
	BlockListItem  BlockType = "li"
	BlockCodeLine  BlockType = "codeline"
)

type Block struct {
	Type  BlockType
	Runs  []Run
	Attrs map[string]string
}

func (b *Block) Text() string {
	var result string
	for _, r := range b.Runs {
		result += r.Text
	}
	return result
}

func (b *Block) Length() int {
	total := 0
	for _, r := range b.Runs {
		total += utf8.RuneCountInString(r.Text)
	}
	return total
}

func (b *Block) RunAt(offset int) (runIndex, runOffset int) {
	pos := 0
	for i, r := range b.Runs {
		runLen := utf8.RuneCountInString(r.Text)
		if offset < pos+runLen || (i == len(b.Runs)-1 && offset <= pos+runLen) {
			return i, offset - pos
		}
		pos += runLen
	}
	if len(b.Runs) > 0 {
		lastRun := b.Runs[len(b.Runs)-1]
		return len(b.Runs) - 1, utf8.RuneCountInString(lastRun.Text)
	}
	return 0, 0
}

func (b *Block) SplitRunAt(offset int) {
	runIdx, runOff := b.RunAt(offset)
	if runIdx >= len(b.Runs) {
		return
	}
	run := b.Runs[runIdx]
	runes := []rune(run.Text)
	if runOff == 0 || runOff >= len(runes) {
		return
	}
	left := Run{Text: string(runes[:runOff]), Style: run.Style}
	right := Run{Text: string(runes[runOff:]), Style: run.Style}
	newRuns := make([]Run, 0, len(b.Runs)+1)
	newRuns = append(newRuns, b.Runs[:runIdx]...)
	newRuns = append(newRuns, left, right)
	newRuns = append(newRuns, b.Runs[runIdx+1:]...)
	b.Runs = newRuns
}

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

func (b *Block) ApplyStyle(start, end int, style InlineStyle) {
	if start >= end {
		return
	}
	b.SplitRunAt(start)
	b.SplitRunAt(end)
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

type Pos struct {
	Block int
	Col   int
}

type Range struct {
	Start Pos
	End   Pos
}

type Document struct {
	Blocks []Block
}

func NewDocument() *Document {
	return &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
		},
	}
}
