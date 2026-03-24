package compose

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kungfusheep/glyph"
	"github.com/mattn/go-runewidth"
)

const contentWidth = 72 // email standard line width

type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
)

func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "NORMAL"
	case ModeInsert:
		return "INSERT"
	case ModeVisual:
		return "VISUAL"
	default:
		return "UNKNOWN"
	}
}

type VisualMode int

const (
	VisualNone VisualMode = iota
	VisualChar
	VisualLine
)

type blockLineMapping struct {
	screenLine int
	lineCount  int
}

type EditorSnapshot struct {
	Blocks []Block
	Cursor Pos
}

type Editor struct {
	doc  *Document
	mode Mode

	cursor      Pos
	visualMode  VisualMode
	visualStart Pos

	topLine      int
	screenWidth  int
	screenHeight int

	layer      *glyph.Layer
	blockLines []blockLineMapping

	cachedLines [][]glyph.Span
	cacheWidth  int
	cacheValid  bool

	undoStack []EditorSnapshot
	redoStack []EditorSnapshot

	yankText string

	marks map[rune]Pos

	dirty bool

	// callback for when content changes (to update html export etc.)
	OnChange func()
}

func NewEditor() *Editor {
	return &Editor{
		doc:   NewDocument(),
		mode:  ModeNormal,
		layer: glyph.NewLayer(),
		marks: make(map[rune]Pos),
	}
}

func (e *Editor) Layer() *glyph.Layer { return e.layer }
func (e *Editor) Mode() Mode          { return e.mode }
func (e *Editor) Cursor() Pos         { return e.cursor }
func (e *Editor) Doc() *Document      { return e.doc }
func (e *Editor) IsDirty() bool       { return e.dirty }

func (e *Editor) SetSize(w, h int) {
	e.screenWidth = w
	e.screenHeight = h
	e.InvalidateCache()
}

func (e *Editor) CurrentBlock() *Block {
	if e.cursor.Block >= 0 && e.cursor.Block < len(e.doc.Blocks) {
		return &e.doc.Blocks[e.cursor.Block]
	}
	return nil
}

// cursor management

func (e *Editor) SetCursor(p Pos) {
	e.setCursorQuiet(p)
	e.ensureCursorVisible()
	e.UpdateDisplay()
}

func (e *Editor) setCursorQuiet(p Pos) {
	if p.Block < 0 {
		p.Block = 0
	}
	if p.Block >= len(e.doc.Blocks) {
		p.Block = len(e.doc.Blocks) - 1
	}
	b := &e.doc.Blocks[p.Block]
	maxCol := b.Length()
	if e.mode == ModeNormal && maxCol > 0 {
		maxCol--
	}
	if p.Col < 0 {
		p.Col = 0
	}
	if p.Col > maxCol {
		p.Col = maxCol
	}
	e.cursor = p
}

func (e *Editor) ensureCursorVisible() {
	screenLine := e.cursorScreenLine()
	if screenLine < e.topLine {
		e.topLine = screenLine
	}
	if screenLine >= e.topLine+e.screenHeight {
		e.topLine = screenLine - e.screenHeight + 1
	}
}

func (e *Editor) cursorScreenLine() int {
	if e.cursor.Block >= len(e.blockLines) {
		return 0
	}
	mapping := e.blockLines[e.cursor.Block]
	wrapPoints := e.wrapPointsForBlock(&e.doc.Blocks[e.cursor.Block])
	visualLine := e.wrappedLineForCol(wrapPoints, e.cursor.Col)
	return mapping.screenLine + visualLine
}

func (e *Editor) wrapPointsForBlock(b *Block) []int {
	runes := []rune(b.Text())
	return e.calculateWrapPoints(runes)
}

func (e *Editor) wrappedLineForCol(wrapPoints []int, col int) int {
	for i, wp := range wrapPoints {
		if col < wp {
			return i
		}
	}
	return len(wrapPoints)
}

func (e *Editor) calculateWrapPoints(runes []rune) []int {
	if len(runes) == 0 {
		return nil
	}
	width := contentWidth
	var points []int
	lineStart := 0
	lineWidth := 0

	for i, r := range runes {
		w := runewidth.RuneWidth(r)
		if lineWidth+w > width {
			// find last space to break at
			breakAt := i
			for j := i - 1; j > lineStart; j-- {
				if runes[j] == ' ' {
					breakAt = j + 1
					break
				}
			}
			points = append(points, breakAt)
			lineStart = breakAt
			lineWidth = 0
			for k := breakAt; k <= i; k++ {
				lineWidth += runewidth.RuneWidth(runes[k])
			}
		} else {
			lineWidth += w
		}
	}
	return points
}

// movement

func (e *Editor) Left(n int) Pos {
	p := e.cursor
	p.Col -= n
	if p.Col < 0 {
		if p.Block > 0 {
			p.Block--
			p.Col = e.doc.Blocks[p.Block].Length()
		} else {
			p.Col = 0
		}
	}
	return p
}

func (e *Editor) Right(n int) Pos {
	p := e.cursor
	b := e.CurrentBlock()
	if b == nil {
		return p
	}
	p.Col += n
	if p.Col > b.Length() {
		if p.Block < len(e.doc.Blocks)-1 {
			p.Block++
			p.Col = 0
		} else {
			p.Col = b.Length()
		}
	}
	return p
}

func (e *Editor) Up(n int) Pos {
	p := e.cursor
	for range n {
		if p.Block > 0 {
			p.Block--
		}
	}
	b := &e.doc.Blocks[p.Block]
	if p.Col > b.Length() {
		p.Col = b.Length()
	}
	return p
}

func (e *Editor) Down(n int) Pos {
	p := e.cursor
	for range n {
		if p.Block < len(e.doc.Blocks)-1 {
			p.Block++
		}
	}
	b := &e.doc.Blocks[p.Block]
	if p.Col > b.Length() {
		p.Col = b.Length()
	}
	return p
}

func (e *Editor) LineStart() Pos { return Pos{Block: e.cursor.Block, Col: 0} }

func (e *Editor) LineEnd() Pos {
	b := e.CurrentBlock()
	if b == nil {
		return e.cursor
	}
	col := b.Length()
	if e.mode == ModeNormal && col > 0 {
		col--
	}
	return Pos{Block: e.cursor.Block, Col: col}
}

func (e *Editor) FirstNonBlank() Pos {
	b := e.CurrentBlock()
	if b == nil {
		return e.cursor
	}
	runes := []rune(b.Text())
	for i, r := range runes {
		if !unicode.IsSpace(r) {
			return Pos{Block: e.cursor.Block, Col: i}
		}
	}
	return Pos{Block: e.cursor.Block, Col: 0}
}

func (e *Editor) DocStart() Pos { return Pos{Block: 0, Col: 0} }

func (e *Editor) DocEnd() Pos {
	lastBlock := len(e.doc.Blocks) - 1
	if lastBlock < 0 {
		return Pos{}
	}
	return Pos{Block: lastBlock, Col: 0}
}

func (e *Editor) WordForward() Pos {
	p := e.cursor
	b := e.CurrentBlock()
	if b == nil {
		return p
	}
	runes := []rune(b.Text())
	col := p.Col

	// skip current word chars
	for col < len(runes) && !unicode.IsSpace(runes[col]) {
		col++
	}
	// skip whitespace
	for col < len(runes) && unicode.IsSpace(runes[col]) {
		col++
	}
	if col >= len(runes) && p.Block < len(e.doc.Blocks)-1 {
		return Pos{Block: p.Block + 1, Col: 0}
	}
	return Pos{Block: p.Block, Col: col}
}

func (e *Editor) WordBackward() Pos {
	p := e.cursor
	b := e.CurrentBlock()
	if b == nil {
		return p
	}
	runes := []rune(b.Text())
	col := p.Col

	if col > 0 {
		col--
	}
	// skip whitespace
	for col > 0 && unicode.IsSpace(runes[col]) {
		col--
	}
	// skip word chars
	for col > 0 && !unicode.IsSpace(runes[col-1]) {
		col--
	}
	if col == 0 && p.Col == 0 && p.Block > 0 {
		prevLen := e.doc.Blocks[p.Block-1].Length()
		return Pos{Block: p.Block - 1, Col: prevLen}
	}
	return Pos{Block: p.Block, Col: col}
}

func (e *Editor) WordEnd() Pos {
	p := e.cursor
	b := e.CurrentBlock()
	if b == nil {
		return p
	}
	runes := []rune(b.Text())
	col := p.Col

	if col < len(runes)-1 {
		col++
	}
	// skip whitespace
	for col < len(runes) && unicode.IsSpace(runes[col]) {
		col++
	}
	// to end of word
	for col < len(runes)-1 && !unicode.IsSpace(runes[col+1]) {
		col++
	}
	return Pos{Block: p.Block, Col: col}
}

// scroll

func (e *Editor) ScrollHalfPageDown() {
	half := e.screenHeight / 2
	e.topLine += half
	maxTop := len(e.cachedLines) - e.screenHeight
	if maxTop < 0 {
		maxTop = 0
	}
	if e.topLine > maxTop {
		e.topLine = maxTop
	}
	e.SetCursor(e.Down(half))
}

func (e *Editor) ScrollHalfPageUp() {
	half := e.screenHeight / 2
	e.topLine -= half
	if e.topLine < 0 {
		e.topLine = 0
	}
	e.SetCursor(e.Up(half))
}

// mode transitions

func (e *Editor) EnterNormal() {
	if e.mode == ModeInsert {
		if e.cursor.Col > 0 {
			e.cursor.Col--
		}
	}
	e.mode = ModeNormal
	e.visualMode = VisualNone
}

func (e *Editor) EnterInsert() {
	e.saveUndo()
	e.mode = ModeInsert
}

func (e *Editor) EnterInsertAfter() {
	e.saveUndo()
	e.mode = ModeInsert
	b := e.CurrentBlock()
	if b != nil && e.cursor.Col < b.Length() {
		e.cursor.Col++
	}
}

func (e *Editor) EnterInsertLineEnd() {
	e.saveUndo()
	e.mode = ModeInsert
	b := e.CurrentBlock()
	if b != nil {
		e.cursor.Col = b.Length()
	}
}

func (e *Editor) EnterInsertLineStart() {
	e.saveUndo()
	e.mode = ModeInsert
	e.cursor.Col = 0
}

func (e *Editor) EnterVisual() {
	e.mode = ModeVisual
	e.visualMode = VisualChar
	e.visualStart = e.cursor
}

func (e *Editor) EnterVisualLine() {
	e.mode = ModeVisual
	e.visualMode = VisualLine
	e.visualStart = e.cursor
}

func (e *Editor) ExitVisual() {
	e.mode = ModeNormal
	e.visualMode = VisualNone
}

func (e *Editor) VisualRange() Range {
	s, c := e.visualStart, e.cursor
	if e.visualMode == VisualLine {
		if s.Block > c.Block {
			s, c = c, s
		}
		return Range{
			Start: Pos{Block: s.Block, Col: 0},
			End:   Pos{Block: c.Block, Col: e.doc.Blocks[c.Block].Length()},
		}
	}
	if s.Block > c.Block || (s.Block == c.Block && s.Col > c.Col) {
		s, c = c, s
	}
	return Range{Start: s, End: c}
}

// editing operations

func (e *Editor) InsertChar(ch rune) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	runes := []rune(b.Text())
	col := e.cursor.Col
	if col > len(runes) {
		col = len(runes)
	}
	newText := string(runes[:col]) + string(ch) + string(runes[col:])

	if len(b.Runs) == 1 {
		b.Runs[0].Text = newText
	} else {
		runIdx, runOff := b.RunAt(col)
		if runIdx < len(b.Runs) {
			run := &b.Runs[runIdx]
			runRunes := []rune(run.Text)
			run.Text = string(runRunes[:runOff]) + string(ch) + string(runRunes[runOff:])
		}
	}
	e.cursor.Col++

	// markdown shorthand for block types
	if ch == ' ' && b.Type == BlockParagraph {
		e.tryMarkdownUpgrade()
	}

	e.ensureCursorVisible()
	e.InvalidateCache()
	e.changed()
}

func (e *Editor) InsertText(text string) {
	if text == "" {
		return
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		for _, ch := range line {
			e.InsertChar(ch)
		}
		if i < len(lines)-1 {
			e.NewLine()
		}
	}
}

func (e *Editor) Backspace() {
	b := e.CurrentBlock()
	if e.cursor.Col == 0 {
		if e.cursor.Block > 0 {
			prevBlock := &e.doc.Blocks[e.cursor.Block-1]
			prevLen := prevBlock.Length()
			prevBlock.Runs = append(prevBlock.Runs, b.Runs...)
			prevBlock.MergeAdjacentRuns()
			e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block], e.doc.Blocks[e.cursor.Block+1:]...)
			e.cursor.Block--
			e.cursor.Col = prevLen
			e.ensureCursorVisible()
			e.InvalidateCache()
			e.changed()
		}
		return
	}
	if b == nil {
		return
	}
	runIdx, runOff := b.RunAt(e.cursor.Col - 1)
	if runIdx < len(b.Runs) {
		run := &b.Runs[runIdx]
		runRunes := []rune(run.Text)
		if runOff < len(runRunes) {
			run.Text = string(runRunes[:runOff]) + string(runRunes[runOff+1:])
		}
	}
	e.cursor.Col--
	b.MergeAdjacentRuns()
	e.ensureCursorVisible()
	e.InvalidateCache()
	e.changed()
}

func (e *Editor) DeleteChar() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	if e.cursor.Col >= b.Length() {
		if e.cursor.Block < len(e.doc.Blocks)-1 {
			e.saveUndo()
			nextBlock := &e.doc.Blocks[e.cursor.Block+1]
			b.Runs = append(b.Runs, nextBlock.Runs...)
			b.MergeAdjacentRuns()
			e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1], e.doc.Blocks[e.cursor.Block+2:]...)
		}
		return
	}
	e.saveUndo()
	runIdx, runOff := b.RunAt(e.cursor.Col)
	if runIdx < len(b.Runs) {
		run := &b.Runs[runIdx]
		runRunes := []rune(run.Text)
		if runOff < len(runRunes) {
			run.Text = string(runRunes[:runOff]) + string(runRunes[runOff+1:])
		}
	}
	b.MergeAdjacentRuns()
	e.setCursorQuiet(e.cursor)
	e.changed()
}

func (e *Editor) NewLine() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// list item continuation
	if b.Type == BlockListItem {
		if b.Length() == 0 {
			b.Type = BlockParagraph
			b.Attrs = nil
			e.InvalidateCache()
			e.changed()
			return
		}
		newBlock := Block{Type: BlockListItem, Runs: []Run{{Text: ""}}}
		if b.Attrs != nil {
			newBlock.Attrs = copyAttrs(b.Attrs)
			if b.Attrs["marker"] == "number" {
				if num := b.Attrs["number"]; num != "" {
					var n int
					fmt.Sscanf(num, "%d", &n)
					newBlock.Attrs["number"] = fmt.Sprintf("%d", n+1)
				}
			}
		}
		// split at cursor
		if e.cursor.Col < b.Length() {
			b.SplitRunAt(e.cursor.Col)
			runIdx, _ := b.RunAt(e.cursor.Col)
			newBlock.Runs = make([]Run, len(b.Runs)-runIdx)
			copy(newBlock.Runs, b.Runs[runIdx:])
			b.Runs = b.Runs[:runIdx]
			if len(b.Runs) == 0 {
				b.Runs = []Run{{Text: ""}}
			}
		}
		e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
			append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
		e.cursor.Block++
		e.cursor.Col = 0
		e.ensureCursorVisible()
		e.InvalidateCache()
		e.changed()
		return
	}

	// split current block at cursor
	var newRuns []Run
	if e.cursor.Col < b.Length() {
		b.SplitRunAt(e.cursor.Col)
		runIdx, _ := b.RunAt(e.cursor.Col)
		newRuns = make([]Run, len(b.Runs)-runIdx)
		copy(newRuns, b.Runs[runIdx:])
		b.Runs = b.Runs[:runIdx]
		if len(b.Runs) == 0 {
			b.Runs = []Run{{Text: ""}}
		}
	}
	if len(newRuns) == 0 {
		newRuns = []Run{{Text: ""}}
	}

	newBlock := Block{Type: BlockParagraph, Runs: newRuns}
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
		append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
	e.cursor.Block++
	e.cursor.Col = 0
	e.ensureCursorVisible()
	e.InvalidateCache()
	e.changed()
}

func (e *Editor) OpenBelow() {
	e.saveUndo()
	newBlock := Block{Type: BlockParagraph, Runs: []Run{{Text: ""}}}
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
		append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
	e.cursor.Block++
	e.cursor.Col = 0
	e.mode = ModeInsert
	e.ensureCursorVisible()
	e.InvalidateCache()
}

func (e *Editor) OpenAbove() {
	e.saveUndo()
	newBlock := Block{Type: BlockParagraph, Runs: []Run{{Text: ""}}}
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block],
		append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block:]...)...)
	e.cursor.Col = 0
	e.mode = ModeInsert
	e.ensureCursorVisible()
	e.InvalidateCache()
}

func (e *Editor) JoinLines() {
	if e.cursor.Block >= len(e.doc.Blocks)-1 {
		return
	}
	e.saveUndo()
	b := e.CurrentBlock()
	next := &e.doc.Blocks[e.cursor.Block+1]
	joinCol := b.Length()
	if joinCol > 0 && next.Length() > 0 {
		b.Runs = append(b.Runs, Run{Text: " "})
	}
	b.Runs = append(b.Runs, next.Runs...)
	b.MergeAdjacentRuns()
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1], e.doc.Blocks[e.cursor.Block+2:]...)
	e.cursor.Col = joinCol
	e.InvalidateCache()
	e.changed()
}

func (e *Editor) DeleteLine() {
	if len(e.doc.Blocks) <= 1 {
		e.doc.Blocks[0] = Block{Type: BlockParagraph, Runs: []Run{{Text: ""}}}
		e.cursor.Col = 0
		e.InvalidateCache()
		e.changed()
		return
	}
	e.saveUndo()
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block], e.doc.Blocks[e.cursor.Block+1:]...)
	if e.cursor.Block >= len(e.doc.Blocks) {
		e.cursor.Block = len(e.doc.Blocks) - 1
	}
	e.cursor.Col = 0
	e.ensureCursorVisible()
	e.InvalidateCache()
	e.changed()
}

// range operations

func (e *Editor) Delete(r Range) string {
	e.saveUndo()
	s, end := r.Start, r.End
	if s.Block > end.Block || (s.Block == end.Block && s.Col > end.Col) {
		s, end = end, s
	}

	if s.Block == end.Block {
		b := &e.doc.Blocks[s.Block]
		runes := []rune(b.Text())
		deleted := string(runes[s.Col:end.Col])
		b.Runs = []Run{{Text: string(runes[:s.Col]) + string(runes[end.Col:])}}
		e.cursor = s
		e.setCursorQuiet(e.cursor)
		e.InvalidateCache()
		e.changed()
		return deleted
	}

	// multi-block delete
	var deleted strings.Builder
	startBlock := &e.doc.Blocks[s.Block]
	endBlock := &e.doc.Blocks[end.Block]

	startRunes := []rune(startBlock.Text())
	endRunes := []rune(endBlock.Text())

	deleted.WriteString(string(startRunes[s.Col:]))
	for i := s.Block + 1; i < end.Block; i++ {
		deleted.WriteRune('\n')
		deleted.WriteString(e.doc.Blocks[i].Text())
	}
	deleted.WriteRune('\n')
	deleted.WriteString(string(endRunes[:end.Col]))

	// merge start and end
	remaining := string(startRunes[:s.Col]) + string(endRunes[end.Col:])
	startBlock.Runs = []Run{{Text: remaining}}

	// remove blocks between
	e.doc.Blocks = append(e.doc.Blocks[:s.Block+1], e.doc.Blocks[end.Block+1:]...)
	e.cursor = s
	e.setCursorQuiet(e.cursor)
	e.InvalidateCache()
	e.changed()
	return deleted.String()
}

func (e *Editor) Change(r Range) {
	e.Delete(r)
	e.mode = ModeInsert
}

func (e *Editor) Yank(r Range) {
	s, end := r.Start, r.End
	if s.Block > end.Block || (s.Block == end.Block && s.Col > end.Col) {
		s, end = end, s
	}
	var text strings.Builder
	for i := s.Block; i <= end.Block; i++ {
		b := &e.doc.Blocks[i]
		runes := []rune(b.Text())
		start := 0
		endCol := len(runes)
		if i == s.Block {
			start = s.Col
		}
		if i == end.Block {
			endCol = end.Col
		}
		if i > s.Block {
			text.WriteRune('\n')
		}
		text.WriteString(string(runes[start:endCol]))
	}
	e.yankText = text.String()
}

func (e *Editor) Put() {
	if e.yankText == "" {
		return
	}
	e.saveUndo()
	if strings.Contains(e.yankText, "\n") {
		// paste below current line
		e.cursor.Col = 0
		if e.cursor.Block < len(e.doc.Blocks)-1 {
			e.cursor.Block++
		}
	} else {
		e.cursor.Col++
	}
	e.InsertText(e.yankText)
}

func (e *Editor) PutBefore() {
	if e.yankText == "" {
		return
	}
	e.saveUndo()
	e.InsertText(e.yankText)
}

func (e *Editor) ApplyStyleRange(r Range, style InlineStyle) {
	e.saveUndo()
	s, end := r.Start, r.End
	if s.Block > end.Block || (s.Block == end.Block && s.Col > end.Col) {
		s, end = end, s
	}
	for i := s.Block; i <= end.Block; i++ {
		b := &e.doc.Blocks[i]
		start := 0
		endCol := b.Length()
		if i == s.Block {
			start = s.Col
		}
		if i == end.Block {
			endCol = end.Col
		}
		b.ApplyStyle(start, endCol, style)
	}
	e.InvalidateCache()
	e.changed()
}

func (e *Editor) ClearStyleRange(r Range) {
	e.saveUndo()
	s, end := r.Start, r.End
	if s.Block > end.Block || (s.Block == end.Block && s.Col > end.Col) {
		s, end = end, s
	}
	for i := s.Block; i <= end.Block; i++ {
		b := &e.doc.Blocks[i]
		start := 0
		endCol := b.Length()
		if i == s.Block {
			start = s.Col
		}
		if i == end.Block {
			endCol = end.Col
		}
		b.ClearStyle(start, endCol)
	}
	e.InvalidateCache()
	e.changed()
}

func (e *Editor) SetBlockType(bt BlockType) {
	e.saveUndo()
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	b.Type = bt
	e.InvalidateCache()
	e.changed()
}

// text objects

func (e *Editor) InnerWord() Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}
	runes := []rune(b.Text())
	col := e.cursor.Col
	if col >= len(runes) {
		return Range{Start: e.cursor, End: e.cursor}
	}
	start, end := col, col
	if unicode.IsSpace(runes[col]) {
		for start > 0 && unicode.IsSpace(runes[start-1]) {
			start--
		}
		for end < len(runes)-1 && unicode.IsSpace(runes[end+1]) {
			end++
		}
	} else {
		for start > 0 && !unicode.IsSpace(runes[start-1]) {
			start--
		}
		for end < len(runes)-1 && !unicode.IsSpace(runes[end+1]) {
			end++
		}
	}
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start},
		End:   Pos{Block: e.cursor.Block, Col: end + 1},
	}
}

func (e *Editor) AWord() Range {
	r := e.InnerWord()
	b := e.CurrentBlock()
	if b == nil {
		return r
	}
	runes := []rune(b.Text())
	// extend to include trailing whitespace
	for r.End.Col < len(runes) && unicode.IsSpace(runes[r.End.Col]) {
		r.End.Col++
	}
	return r
}

func (e *Editor) InnerParagraph() Range {
	start, end := e.cursor.Block, e.cursor.Block
	for start > 0 && e.doc.Blocks[start-1].Length() > 0 {
		start--
	}
	for end < len(e.doc.Blocks)-1 && e.doc.Blocks[end+1].Length() > 0 {
		end++
	}
	return Range{
		Start: Pos{Block: start, Col: 0},
		End:   Pos{Block: end, Col: e.doc.Blocks[end].Length()},
	}
}

func (e *Editor) AParagraph() Range {
	r := e.InnerParagraph()
	// include blank line after
	if r.End.Block < len(e.doc.Blocks)-1 {
		r.End.Block++
		r.End.Col = e.doc.Blocks[r.End.Block].Length()
	}
	return r
}

func (e *Editor) ToDocEnd() Range {
	return Range{Start: e.cursor, End: e.DocEnd()}
}

func (e *Editor) ToDocStart() Range {
	return Range{Start: e.DocStart(), End: e.cursor}
}

func (e *Editor) WholeDoc() Range {
	return Range{Start: e.DocStart(), End: Pos{
		Block: len(e.doc.Blocks) - 1,
		Col:   e.doc.Blocks[len(e.doc.Blocks)-1].Length(),
	}}
}

func (e *Editor) ToLineEnd() Range {
	return Range{Start: e.cursor, End: e.LineEnd()}
}

func (e *Editor) ToLineStart() Range {
	return Range{Start: e.LineStart(), End: e.cursor}
}

// bracket matching

func (e *Editor) InnerQuote(quote rune) Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}
	runes := []rune(b.Text())
	col := e.cursor.Col
	start, end := -1, -1
	for i := col; i >= 0; i-- {
		if runes[i] == quote {
			start = i
			break
		}
	}
	for i := col; i < len(runes); i++ {
		if runes[i] == quote && i != start {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		return Range{Start: e.cursor, End: e.cursor}
	}
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start + 1},
		End:   Pos{Block: e.cursor.Block, Col: end},
	}
}

func (e *Editor) AroundQuote(quote rune) Range {
	r := e.InnerQuote(quote)
	if r.Start.Col > 0 {
		r.Start.Col--
	}
	b := e.CurrentBlock()
	if b != nil && r.End.Col < b.Length() {
		r.End.Col++
	}
	return r
}

func (e *Editor) InnerBracket(open, close rune) Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}
	runes := []rune(b.Text())
	col := e.cursor.Col
	depth := 0
	start := -1
	for i := col; i >= 0; i-- {
		if runes[i] == close && i != col {
			depth++
		}
		if runes[i] == open {
			if depth == 0 {
				start = i
				break
			}
			depth--
		}
	}
	if start < 0 {
		return Range{Start: e.cursor, End: e.cursor}
	}
	depth = 0
	end := -1
	for i := start + 1; i < len(runes); i++ {
		if runes[i] == open {
			depth++
		}
		if runes[i] == close {
			if depth == 0 {
				end = i
				break
			}
			depth--
		}
	}
	if end < 0 {
		return Range{Start: e.cursor, End: e.cursor}
	}
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start + 1},
		End:   Pos{Block: e.cursor.Block, Col: end},
	}
}

func (e *Editor) AroundBracket(open, close rune) Range {
	r := e.InnerBracket(open, close)
	if r.Start.Col > 0 {
		r.Start.Col--
	}
	b := e.CurrentBlock()
	if b != nil && r.End.Col < b.Length() {
		r.End.Col++
	}
	return r
}

func (e *Editor) InnerParen() Range  { return e.InnerBracket('(', ')') }
func (e *Editor) AroundParen() Range { return e.AroundBracket('(', ')') }
func (e *Editor) InnerSquare() Range  { return e.InnerBracket('[', ']') }
func (e *Editor) AroundSquare() Range { return e.AroundBracket('[', ']') }
func (e *Editor) InnerCurly() Range  { return e.InnerBracket('{', '}') }
func (e *Editor) AroundCurly() Range { return e.AroundBracket('{', '}') }
func (e *Editor) InnerAngle() Range  { return e.InnerBracket('<', '>') }
func (e *Editor) AroundAngle() Range { return e.AroundBracket('<', '>') }

// markdown upgrade

func (e *Editor) tryMarkdownUpgrade() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	text := b.Text()
	switch {
	case strings.HasPrefix(text, "# "):
		e.upgradeBlock(BlockH1, "# ")
	case strings.HasPrefix(text, "## "):
		e.upgradeBlock(BlockH2, "## ")
	case strings.HasPrefix(text, "### "):
		e.upgradeBlock(BlockH3, "### ")
	case strings.HasPrefix(text, "> "):
		e.upgradeBlock(BlockQuote, "> ")
	case strings.HasPrefix(text, "- "):
		b.Type = BlockListItem
		b.Attrs = map[string]string{"marker": "bullet"}
		b.Runs = []Run{{Text: strings.TrimPrefix(text, "- ")}}
		e.cursor.Col -= 2
		e.InvalidateCache()
	case len(text) >= 4 && text[:3] == "1. ":
		b.Type = BlockListItem
		b.Attrs = map[string]string{"marker": "number", "number": "1"}
		b.Runs = []Run{{Text: text[3:]}}
		e.cursor.Col -= 3
		e.InvalidateCache()
	}
}

func (e *Editor) upgradeBlock(bt BlockType, prefix string) {
	b := e.CurrentBlock()
	text := b.Text()
	b.Type = bt
	b.Runs = []Run{{Text: strings.TrimPrefix(text, prefix)}}
	e.cursor.Col -= utf8.RuneCountInString(prefix)
	e.InvalidateCache()
}

// undo/redo

func (e *Editor) saveUndo() {
	blocks := make([]Block, len(e.doc.Blocks))
	for i, b := range e.doc.Blocks {
		blocks[i] = Block{
			Type:  b.Type,
			Runs:  append([]Run{}, b.Runs...),
			Attrs: copyAttrs(b.Attrs),
		}
	}
	e.undoStack = append(e.undoStack, EditorSnapshot{
		Blocks: blocks,
		Cursor: e.cursor,
	})
	e.redoStack = nil
	e.dirty = true
}

func (e *Editor) Undo() {
	if len(e.undoStack) == 0 {
		return
	}
	blocks := make([]Block, len(e.doc.Blocks))
	for i, b := range e.doc.Blocks {
		blocks[i] = Block{
			Type:  b.Type,
			Runs:  append([]Run{}, b.Runs...),
			Attrs: copyAttrs(b.Attrs),
		}
	}
	e.redoStack = append(e.redoStack, EditorSnapshot{Blocks: blocks, Cursor: e.cursor})
	snap := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.doc.Blocks = snap.Blocks
	e.cursor = snap.Cursor
	e.setCursorQuiet(e.cursor)
	e.InvalidateCache()
	e.changed()
}

func (e *Editor) Redo() {
	if len(e.redoStack) == 0 {
		return
	}
	blocks := make([]Block, len(e.doc.Blocks))
	for i, b := range e.doc.Blocks {
		blocks[i] = Block{
			Type:  b.Type,
			Runs:  append([]Run{}, b.Runs...),
			Attrs: copyAttrs(b.Attrs),
		}
	}
	e.undoStack = append(e.undoStack, EditorSnapshot{Blocks: blocks, Cursor: e.cursor})
	snap := e.redoStack[len(e.redoStack)-1]
	e.redoStack = e.redoStack[:len(e.redoStack)-1]
	e.doc.Blocks = snap.Blocks
	e.cursor = snap.Cursor
	e.setCursorQuiet(e.cursor)
	e.InvalidateCache()
	e.changed()
}

// marks

func (e *Editor) SetMark(reg rune) { e.marks[reg] = e.cursor }

func (e *Editor) GotoMark(reg rune) bool {
	if p, ok := e.marks[reg]; ok {
		e.SetCursor(p)
		return true
	}
	return false
}

// rendering

func (e *Editor) InvalidateCache() { e.cacheValid = false }

func (e *Editor) UpdateDisplay() {
	if e.screenWidth <= 0 || e.screenHeight <= 0 {
		return
	}
	if e.layer == nil {
		e.layer = glyph.NewLayer()
	}

	margin := (e.screenWidth - contentWidth) / 2
	if margin < 0 {
		margin = 0
	}

	baseStyle := glyph.Style{}

	if !e.cacheValid || e.cacheWidth != e.screenWidth {
		e.rebuildRenderCache()
	}

	e.layer.EnsureSize(e.screenWidth, e.screenHeight)

	for i := 0; i < e.screenHeight; i++ {
		docLine := e.topLine + i
		if docLine >= 0 && docLine < len(e.cachedLines) {
			line := e.cachedLines[docLine]
			if e.mode == ModeVisual {
				line = e.applyVisibleLineSelection(docLine, line)
			}
			e.layer.SetLineAt(i, margin, line, baseStyle)
		} else {
			e.layer.SetLineAt(i, 0, nil, baseStyle)
		}
	}
}

func (e *Editor) rebuildRenderCache() {
	e.blockLines = make([]blockLineMapping, len(e.doc.Blocks))
	screenLine := 0
	var allLines [][]glyph.Span

	for i, block := range e.doc.Blocks {
		blockSpans := e.renderBlock(&block)

		var wrappedLines [][]glyph.Span
		skipWrap := block.Type == BlockCodeLine
		for _, line := range blockSpans {
			if skipWrap {
				wrappedLines = append(wrappedLines, line)
			} else {
				wrapped := wrapSpans(line, contentWidth)
				wrappedLines = append(wrappedLines, wrapped...)
			}
		}

		e.blockLines[i] = blockLineMapping{
			screenLine: screenLine,
			lineCount:  len(wrappedLines),
		}

		allLines = append(allLines, wrappedLines...)
		screenLine += len(wrappedLines)
	}

	e.cachedLines = allLines
	e.cacheWidth = e.screenWidth
	e.cacheValid = true
}

func (e *Editor) renderBlock(block *Block) [][]glyph.Span {
	spans := e.runsToSpans(block.Runs)

	switch block.Type {
	case BlockH1:
		for i := range spans {
			spans[i].Style = spans[i].Style.Bold()
			spans[i].Style.FG = glyph.Cyan
		}
		return [][]glyph.Span{spans}
	case BlockH2:
		for i := range spans {
			spans[i].Style = spans[i].Style.Bold()
			spans[i].Style.FG = glyph.Blue
		}
		return [][]glyph.Span{spans}
	case BlockH3:
		for i := range spans {
			spans[i].Style = spans[i].Style.Bold()
		}
		return [][]glyph.Span{spans}
	case BlockQuote:
		marker := glyph.Span{Text: "│ ", Style: glyph.Style{FG: glyph.BrightBlack}}
		for i := range spans {
			spans[i].Style.FG = glyph.BrightBlack
		}
		return [][]glyph.Span{append([]glyph.Span{marker}, spans...)}
	case BlockListItem:
		bullet := "• "
		if block.Attrs != nil && block.Attrs["marker"] == "number" {
			num := block.Attrs["number"]
			if num == "" {
				num = "1"
			}
			bullet = num + ". "
		}
		markerSpan := glyph.Span{Text: bullet, Style: glyph.Style{FG: glyph.Cyan}}
		return [][]glyph.Span{append([]glyph.Span{markerSpan}, spans...)}
	case BlockCodeLine:
		for i := range spans {
			spans[i].Style.FG = glyph.Green
		}
		return [][]glyph.Span{spans}
	default:
		return [][]glyph.Span{spans}
	}
}

func (e *Editor) runsToSpans(runs []Run) []glyph.Span {
	spans := make([]glyph.Span, 0, len(runs))
	for _, r := range runs {
		style := glyph.Style{}
		if r.Style.Has(StyleBold) {
			style = style.Bold()
		}
		if r.Style.Has(StyleItalic) {
			style = style.Italic()
		}
		if r.Style.Has(StyleUnderline) {
			style = style.Underline()
		}
		if r.Style.Has(StyleStrikethrough) {
			style = style.Strikethrough()
		}
		if r.Style.Has(StyleCode) {
			style.FG = glyph.Green
		}
		spans = append(spans, glyph.Span{Text: r.Text, Style: style})
	}
	return spans
}

func (e *Editor) applyVisibleLineSelection(docLine int, line []glyph.Span) []glyph.Span {
	blockIdx := -1
	lineInBlock := 0
	for i, mapping := range e.blockLines {
		if docLine >= mapping.screenLine && docLine < mapping.screenLine+mapping.lineCount {
			blockIdx = i
			lineInBlock = docLine - mapping.screenLine
			break
		}
	}
	if blockIdx < 0 {
		return line
	}

	r := e.VisualRange()
	if blockIdx < r.Start.Block || blockIdx > r.End.Block {
		return line
	}

	// highlight the whole line for simplicity
	_ = lineInBlock
	highlighted := make([]glyph.Span, len(line))
	for i, span := range line {
		s := span
		s.Style = s.Style.Inverse()
		highlighted[i] = s
	}
	return highlighted
}

// CursorScreenPos returns the screen x,y for the cursor
func (e *Editor) CursorScreenPos() (int, int) {
	if e.cursor.Block >= len(e.blockLines) {
		return 0, 0
	}
	margin := (e.screenWidth - contentWidth) / 2
	if margin < 0 {
		margin = 0
	}

	mapping := e.blockLines[e.cursor.Block]
	b := &e.doc.Blocks[e.cursor.Block]
	wrapPoints := e.wrapPointsForBlock(b)
	visualLine := e.wrappedLineForCol(wrapPoints, e.cursor.Col)

	// col within the wrapped line
	lineStart := 0
	if visualLine > 0 && visualLine-1 < len(wrapPoints) {
		lineStart = wrapPoints[visualLine-1]
	}

	// account for block prefix on first line
	prefixLen := 0
	if visualLine == 0 {
		prefixLen = e.blockPrefixLength(b)
	}

	x := margin + (e.cursor.Col - lineStart) + prefixLen
	y := mapping.screenLine + visualLine - e.topLine
	return x, y
}

func (e *Editor) blockPrefixLength(b *Block) int {
	switch b.Type {
	case BlockQuote:
		return 2 // "│ "
	case BlockListItem:
		if b.Attrs != nil && b.Attrs["marker"] == "number" {
			num := b.Attrs["number"]
			if num == "" {
				num = "1"
			}
			return len(num) + 2 // "1. "
		}
		return 2 // "• "
	default:
		return 0
	}
}

func (e *Editor) changed() {
	e.dirty = true
	if e.OnChange != nil {
		e.OnChange()
	}
}

func copyAttrs(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// wrapSpans wraps a line of spans to fit within maxWidth
func wrapSpans(spans []glyph.Span, maxWidth int) [][]glyph.Span {
	if maxWidth <= 0 {
		return [][]glyph.Span{spans}
	}

	// calculate total width
	totalWidth := 0
	for _, s := range spans {
		totalWidth += runewidth.StringWidth(s.Text)
	}
	if totalWidth <= maxWidth {
		return [][]glyph.Span{spans}
	}

	// concatenate all text with style tracking
	var all []styledRune
	for _, s := range spans {
		for _, r := range s.Text {
			all = append(all, styledRune{r: r, style: s.Style})
		}
	}

	var lines [][]glyph.Span
	lineStart := 0
	lineWidth := 0

	for i, sr := range all {
		w := runewidth.RuneWidth(sr.r)
		if lineWidth+w > maxWidth {
			// find word break
			breakAt := i
			for j := i - 1; j > lineStart; j-- {
				if all[j].r == ' ' {
					breakAt = j + 1
					break
				}
			}
			lines = append(lines, styledRunesToSpans(all[lineStart:breakAt]))
			lineStart = breakAt
			lineWidth = 0
			for k := breakAt; k <= i; k++ {
				lineWidth += runewidth.RuneWidth(all[k].r)
			}
		} else {
			lineWidth += w
		}
	}
	if lineStart < len(all) {
		lines = append(lines, styledRunesToSpans(all[lineStart:]))
	}
	if len(lines) == 0 {
		lines = [][]glyph.Span{spans}
	}
	return lines
}

type styledRune struct {
	r     rune
	style glyph.Style
}

func styledRunesToSpans(runes []styledRune) []glyph.Span {
	if len(runes) == 0 {
		return nil
	}
	var spans []glyph.Span
	current := glyph.Span{Text: string(runes[0].r), Style: runes[0].style}
	for _, sr := range runes[1:] {
		if sr.style == current.Style {
			current.Text += string(sr.r)
		} else {
			spans = append(spans, current)
			current = glyph.Span{Text: string(sr.r), Style: sr.style}
		}
	}
	spans = append(spans, current)
	return spans
}
