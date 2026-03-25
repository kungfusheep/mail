package compose

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kungfusheep/glyph"

	"github.com/mattn/go-runewidth"
)

const (
	contentWidth = 64 // fixed content width (iA Writer style)
)

// =============================================================================
// Foundation Types (aligned with minivim)
// =============================================================================

// Pos represents a position in the document
type Pos struct {
	Block int // block index
	Col   int // column (character offset within block)
}

// Range represents a selection in the document
type Range struct {
	Start Pos
	End   Pos
}

// VisualMode represents the type of visual selection
type VisualMode int

const (
	VisualNone  VisualMode = iota // Not in visual mode
	VisualChar                    // Character-wise (v)
	VisualLine                    // Line-wise (V)
	VisualBlock                   // Block-wise (Ctrl-V)
)

// Mode represents the editor mode
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

// =============================================================================
// Editor
// =============================================================================

// Editor is the main editor state
type Editor struct {
	doc       *Document
	filename  string
	theme     Theme
	mode      Mode
	app       *glyph.App
	templates *TemplateRegistry

	// cursor position
	cursor Pos

	// visual mode
	visualMode  VisualMode
	visualStart Pos

	// viewport
	topLine      int // first visible screen line (for scrolling)
	screenWidth  int
	screenHeight int

	// rendering
	layer      *glyph.Layer
	blockLines []blockLineMapping

	// render cache - avoid rebuilding spans every frame
	cachedLines        [][]glyph.Span
	cacheWidth         int
	cacheSidebarHidden bool // true when cache was built without sidebar
	cacheValid         bool

	// undo/redo
	undoStack []EditorSnapshot
	redoStack []EditorSnapshot

	// registers
	yankStyle InlineStyle // style yank register
	yankText  string      // text yank register

	// f/F/t/T state
	lastFindChar rune
	lastFindDir  int  // 1 = forward, -1 = backward
	lastFindTill bool // true if was t/T, false if f/F

	// marks
	marks map[rune]Pos

	// omnibox
	omnibox *Omnibox

	// dirty flag
	dirty bool

	// search state
	searchPattern   string
	searchDirection int // 1 = forward, -1 = backward
	lastSearchPos   Pos // for n/N navigation

	// jump list for Ctrl-o/Ctrl-i
	jumpList    []Pos
	jumpListIdx int

	// repeat (.) state
	lastAction     func()
	lastActionName string

	// typewriter mode - cursor stays vertically centered
	typewriterMode bool

	// focus mode - dims content outside focus scope
	focusMode  bool
	focusScope FocusScope // line, sentence, paragraph

	// zen mode - combines typewriter + focus
	zenMode bool

	// raw mode - show markdown syntax characters alongside styling
	rawMode bool

	// dialogue editing mode - true when editing character name, false for dialogue text
	dialogueCharMode bool

	// dialogue character autocomplete
	characterHistory       []string // unique character names in order of first use
	dialogueSuggestionMode bool     // true when showing a suggested character name
	suggestionIndex        int      // current index in characterHistory when cycling
	dialogueAwaitingInput  bool     // true after confirming character, prevents immediate yield

	// debug logging
	debugDialogue bool

	// spellcheck
	spellChecker     *SpellChecker     // async checker for background checking
	syncSpellChecker *SyncSpellChecker // sync checker for z= suggestions (separate process)
	misspelledWord   map[string]bool   // cache: word -> is misspelled
	spellPending     map[string]bool   // words queued but not yet checked
	userApproved     map[string]bool   // words user added to dictionary (never mark misspelled)
	spellMu          sync.RWMutex      // protects misspelledWord, spellPending, userApproved
	onSpellUpdate    func()            // callback when spell results arrive

	// callback when content changes
	OnChange func()

	// user config (persisted)
	config *Config

	// file browser
	browserVisible  bool
	browserEntries  []BrowserEntry
	browserSelected int
	browserList     *glyph.SelectionList

	// project index
	projectIndex *ProjectIndex
}

// debugLog writes dialogue state to stderr when debugging is enabled
func (e *Editor) debugLog(event string) {
	if !e.debugDialogue {
		return
	}
	b := e.CurrentBlock()
	blockType := "nil"
	char := ""
	contentLen := 0
	if b != nil {
		blockType = string(b.Type)
		if b.Attrs != nil {
			char = b.Attrs["character"]
		}
		contentLen = b.Length()
	}
	fmt.Fprintf(os.Stderr, "[DLG] %s | block=%d col=%d | type=%s char=%q len=%d | charMode=%v suggMode=%v awaiting=%v | history=%v\n",
		event,
		e.cursor.Block, e.cursor.Col,
		blockType, char, contentLen,
		e.dialogueCharMode, e.dialogueSuggestionMode, e.dialogueAwaitingInput,
		e.characterHistory,
	)
}

// FocusScope defines what content stays in focus
type FocusScope int

const (
	FocusScopeLine FocusScope = iota
	FocusScopeSentence
	FocusScopeParagraph
)

func (s FocusScope) String() string {
	switch s {
	case FocusScopeLine:
		return "line"
	case FocusScopeSentence:
		return "sentence"
	case FocusScopeParagraph:
		return "paragraph"
	default:
		return "line"
	}
}

func (s FocusScope) Next() FocusScope {
	switch s {
	case FocusScopeLine:
		return FocusScopeSentence
	case FocusScopeSentence:
		return FocusScopeParagraph
	default:
		return FocusScopeLine
	}
}

// EditorSnapshot captures state for undo
type EditorSnapshot struct {
	Blocks []Block
	Cursor Pos
}

// NewEditor creates a new editor instance
func NewEditor(doc *Document, filename string) *Editor {
	cfg := LoadConfig()
	// try to init spell checkers (non-fatal if aspell not available)
	var sc *SpellChecker
	var syncSc *SyncSpellChecker
	if IsAspellAvailable() {
		sc, _ = NewSpellChecker("en_GB")
		syncSc, _ = NewSyncSpellChecker("en_GB")
	}

	ed := &Editor{
		doc:              doc,
		filename:         filename,
		theme:            DefaultTheme(),
		mode:             ModeNormal,
		layer:            glyph.NewLayer(),
		templates:        NewTemplateRegistry(),
		marks:            make(map[rune]Pos),
		omnibox:          NewOmnibox(),
		config:           cfg,
		focusScope:       FocusScopeFromString(cfg.FocusScope),
		debugDialogue:    os.Getenv("WED_DEBUG_DIALOGUE") != "",
		spellChecker:     sc,
		syncSpellChecker: syncSc,
		misspelledWord:   make(map[string]bool),
		spellPending:     make(map[string]bool),
		userApproved:     make(map[string]bool),
	}
	// build character history from existing dialogue blocks
	ed.rebuildCharacterHistory()
	return ed
}


// =============================================================================
// Exported Accessors
// =============================================================================

// SetApp sets the glyph app for the editor
func (e *Editor) SetApp(app *glyph.App) {
	e.app = app
}

// ResetDocument replaces the editor's document and resets all editing state
func (e *Editor) ResetDocument(doc *Document) {
	e.doc = doc
	e.cursor = Pos{}
	e.mode = ModeNormal
	e.visualStart = Pos{}
	e.undoStack = nil
	e.redoStack = nil
	e.yankText = ""
	e.yankStyle = StyleNone
	e.marks = make(map[rune]Pos)
	e.topLine = 0
	e.dirty = false
	e.searchPattern = ""
	e.jumpList = nil
	e.jumpListIdx = 0
	e.dialogueCharMode = false
	e.characterHistory = nil
	e.InvalidateCache()
	e.rebuildCharacterHistory()
}

// SetSize sets the screen dimensions and invalidates the cache
func (e *Editor) SetSize(w, h int) {
	e.screenWidth = w
	e.screenHeight = h
	e.InvalidateCache()
}

// ScreenWidth returns the current screen width
func (e *Editor) ScreenWidth() int {
	return e.screenWidth
}

// ScreenHeight returns the current screen height
func (e *Editor) ScreenHeight() int {
	return e.screenHeight
}

// Mode returns the current editor mode
func (e *Editor) Mode() Mode {
	return e.mode
}

// Doc returns the editor's document
func (e *Editor) Doc() *Document {
	return e.doc
}

// Layer returns the editor's render layer
func (e *Editor) Layer() *glyph.Layer {
	return e.layer
}

// Dirty returns whether the document has unsaved changes
func (e *Editor) Dirty() bool {
	return e.dirty
}

// =============================================================================
// Cursor Management (core primitives)
// =============================================================================

// Cursor returns the current cursor position
func (e *Editor) Cursor() Pos {
	return e.cursor
}

// BlockCount returns the number of blocks in the document
func (e *Editor) BlockCount() int {
	return len(e.doc.Blocks)
}

// SetCursor moves cursor with clamping, viewport scroll, and display update
func (e *Editor) SetCursor(p Pos) {
	e.SetCursorQuiet(p)
	e.ensureCursorVisible()
	e.UpdateDisplay()
	e.updateCursor()
}

// SetCursorQuiet moves cursor with clamping but no display update (for batch ops)
func (e *Editor) SetCursorQuiet(p Pos) {
	oldBlock := e.cursor.Block

	// Clamp block to valid range
	if p.Block < 0 {
		p.Block = 0
	}
	if p.Block >= len(e.doc.Blocks) {
		p.Block = len(e.doc.Blocks) - 1
	}
	if p.Block < 0 {
		p.Block = 0
	}

	// Clamp column to line length
	if p.Block < len(e.doc.Blocks) {
		block := &e.doc.Blocks[p.Block]
		lineLen := block.Length()

		// for dialogue blocks in character mode, use character name length
		if block.Type == BlockDialogue && e.dialogueCharMode {
			if block.Attrs != nil {
				lineLen = utf8.RuneCountInString(block.Attrs["character"])
			} else {
				lineLen = 0
			}
		}

		if lineLen == 0 {
			p.Col = 0
		} else if e.mode == ModeInsert {
			// In insert mode, can be at end of line
			if p.Col < 0 {
				p.Col = 0
			}
			if p.Col > lineLen {
				p.Col = lineLen
			}
		} else {
			// In normal/visual mode, allow cursor at end of line (like virtualedit=onemore)
			if p.Col < 0 {
				p.Col = 0
			}
			if p.Col > lineLen {
				p.Col = lineLen
			}
		}
	}

	e.cursor = p

	// sync dialogue mode when moving to a different block
	if p.Block != oldBlock {
		e.SyncDialogueMode()
	}
}

// moveCursor moves cursor with clamping and scroll
func (e *Editor) moveCursor(p Pos) {
	e.SetCursorQuiet(p)
	e.ensureCursorVisible()
}

// updateCursor updates the terminal cursor position and appearance
func (e *Editor) updateCursor() {
	if e.app == nil {
		return
	}

	// hide cursor when browser sidebar is focused
	if e.browserVisible {
		e.app.HideCursor()
		return
	}

	x, y := e.CursorScreenPos()
	e.app.SetCursor(x, y)

	e.app.SetCursorStyle(glyph.CursorBlock)
	e.app.ShowCursor()
	switch e.mode {
	case ModeInsert:
		e.app.SetCursorColor(e.theme.Cursor.Insert)
	case ModeVisual:
		e.app.SetCursorColor(e.theme.Cursor.Visual)
	default:
		e.app.SetCursorColor(e.theme.Cursor.Normal)
	}
}

// Refresh updates display and cursor - call after any content/state change
func (e *Editor) Refresh() {
	e.refresh()
}

func (e *Editor) refresh() {
	t0 := time.Now()
	e.UpdateDisplay()
	t1 := time.Now()
	e.updateCursor()
	Debug("refresh: UpdateDisplay=%v updateCursor=%v total=%v cacheValid=%v",
		t1.Sub(t0), time.Since(t1), time.Since(t0), e.cacheValid)
}

// ensureCursorVisible scrolls viewport if cursor is outside visible area
func (e *Editor) ensureCursorVisible() {
	if e.screenHeight <= 0 || len(e.doc.Blocks) == 0 {
		return
	}

	// calculate which screen line the cursor is on
	cursorScreenLine := e.cursorScreenLine()

	if e.typewriterMode {
		// typewriter mode: cursor always at vertical center,
		// document starts partway down screen like a real typewriter
		e.topLine = cursorScreenLine - e.screenHeight/2
		return
	}

	// normal mode: scroll margin (keep cursor this many lines from edge)
	scrollMargin := 3
	if scrollMargin > e.screenHeight/4 {
		scrollMargin = e.screenHeight / 4
	}

	// scroll up if cursor is above visible area
	if cursorScreenLine < e.topLine+scrollMargin {
		e.topLine = cursorScreenLine - scrollMargin
		if e.topLine < 0 {
			e.topLine = 0
		}
	}

	// scroll down if cursor is below visible area
	if cursorScreenLine >= e.topLine+e.screenHeight-scrollMargin {
		e.topLine = cursorScreenLine - e.screenHeight + scrollMargin + 1
	}
}

// cursorScreenLine returns the absolute screen line the cursor is on
// always calculates from scratch using text wrapping for consistent results
func (e *Editor) cursorScreenLine() int {
	// use cached blockLines if available (populated by UpdateDisplay)
	if e.cursor.Block >= 0 && e.cursor.Block < len(e.blockLines) {
		mapping := e.blockLines[e.cursor.Block]
		block := &e.doc.Blocks[e.cursor.Block]
		wrapPoints := e.wrapPointsForBlock(block)
		wrappedLine := e.wrappedLineForCol(wrapPoints, e.cursor.Col)
		return mapping.screenLine + wrappedLine
	}

	// fallback: calculate from scratch (before first UpdateDisplay)
	screenLine := 0
	for i := 0; i < len(e.doc.Blocks); i++ {
		block := &e.doc.Blocks[i]
		wrapPoints := e.wrapPointsForBlock(block)
		blockLineCount := len(wrapPoints) + 1

		if i == e.cursor.Block {
			wrappedLine := e.wrappedLineForCol(wrapPoints, e.cursor.Col)
			return screenLine + wrappedLine
		}

		screenLine += blockLineCount
	}

	return screenLine
}

// =============================================================================
// Buffer Access
// =============================================================================

// CurrentBlock returns the block at the cursor
func (e *Editor) CurrentBlock() *Block {
	if e.cursor.Block < 0 || e.cursor.Block >= len(e.doc.Blocks) {
		return nil
	}
	return &e.doc.Blocks[e.cursor.Block]
}

// CurrentLine returns the plain text of the current block
func (e *Editor) CurrentLine() string {
	b := e.CurrentBlock()
	if b == nil {
		return ""
	}
	return b.Text()
}

// OutlineEntry represents a heading for the outline view
type OutlineEntry struct {
	BlockIdx int
	Level    int
	Text     string
}

// GetOutline returns all headings in the document for outline navigation
func (e *Editor) GetOutline() []OutlineEntry {
	var entries []OutlineEntry
	for i, block := range e.doc.Blocks {
		var level int
		switch block.Type {
		case BlockH1:
			level = 1
		case BlockH2:
			level = 2
		case BlockH3:
			level = 3
		case BlockH4:
			level = 4
		case BlockH5:
			level = 5
		case BlockH6:
			level = 6
		default:
			continue
		}
		entries = append(entries, OutlineEntry{
			BlockIdx: i,
			Level:    level,
			Text:     block.Text(),
		})
	}
	return entries
}

// GotoBlock moves the cursor to the specified block
func (e *Editor) GotoBlock(blockIdx int) {
	if blockIdx < 0 || blockIdx >= len(e.doc.Blocks) {
		return
	}
	e.SetCursor(Pos{Block: blockIdx, Col: 0})
}

// WordCount returns the total word count in the document
func (e *Editor) WordCount() int {
	count := 0
	for _, block := range e.doc.Blocks {
		text := block.Text()
		words := strings.Fields(text)
		count += len(words)
	}
	return count
}

// =============================================================================
// Block Navigation - generic matcher-based navigation
// =============================================================================

// NextBlockMatching jumps to the next block matching the given predicate
func (e *Editor) NextBlockMatching(match BlockMatcher) Pos {
	for i := e.cursor.Block + 1; i < len(e.doc.Blocks); i++ {
		if match(&e.doc.Blocks[i]) {
			e.moveCursor(Pos{Block: i, Col: 0})
			return e.cursor
		}
	}
	return e.cursor
}

// PrevBlockMatching jumps to the previous block matching the given predicate
func (e *Editor) PrevBlockMatching(match BlockMatcher) Pos {
	for i := e.cursor.Block - 1; i >= 0; i-- {
		if match(&e.doc.Blocks[i]) {
			e.moveCursor(Pos{Block: i, Col: 0})
			return e.cursor
		}
	}
	return e.cursor
}

// AllBlocksMatching returns all blocks matching the given predicate
func (e *Editor) AllBlocksMatching(match BlockMatcher) []MapEntry {
	var entries []MapEntry
	for i, b := range e.doc.Blocks {
		if match(&b) {
			text := b.Text()
			if len(text) > 60 {
				text = text[:60] + "..."
			}
			entries = append(entries, MapEntry{
				BlockIdx: i,
				Col:      0,
				Text:     text,
			})
		}
	}
	return entries
}

// =============================================================================
// Text Navigation - generic matcher-based navigation within text
// =============================================================================

// NextTextMatching jumps to the next text matching the given pattern
func (e *Editor) NextTextMatching(match TextMatcher) Pos {
	// first check rest of current block
	if b := e.CurrentBlock(); b != nil {
		text := b.Text()
		if start, _, found := match(text, e.cursor.Col+1); found {
			e.moveCursor(Pos{Block: e.cursor.Block, Col: start})
			return e.cursor
		}
	}

	// then check subsequent blocks
	for i := e.cursor.Block + 1; i < len(e.doc.Blocks); i++ {
		text := e.doc.Blocks[i].Text()
		if start, _, found := match(text, 0); found {
			e.moveCursor(Pos{Block: i, Col: start})
			return e.cursor
		}
	}
	return e.cursor
}

// PrevTextMatching jumps to the previous text matching the given pattern
func (e *Editor) PrevTextMatching(match TextMatcher) Pos {
	// search backward in current block first
	if b := e.CurrentBlock(); b != nil {
		text := b.Text()
		// search from start up to cursor, find the last match before cursor
		lastStart := -1
		col := 0
		for col < e.cursor.Col {
			start, end, found := match(text, col)
			if !found || start >= e.cursor.Col {
				break
			}
			lastStart = start
			col = end // continue searching after this match
		}
		if lastStart >= 0 {
			e.moveCursor(Pos{Block: e.cursor.Block, Col: lastStart})
			return e.cursor
		}
	}

	// then check previous blocks (find last match in each block)
	for i := e.cursor.Block - 1; i >= 0; i-- {
		text := e.doc.Blocks[i].Text()
		lastStart := -1
		col := 0
		for {
			start, end, found := match(text, col)
			if !found {
				break
			}
			lastStart = start
			col = end
		}
		if lastStart >= 0 {
			e.moveCursor(Pos{Block: i, Col: lastStart})
			return e.cursor
		}
	}
	return e.cursor
}

// AllTextMatching returns all text matches in the document
func (e *Editor) AllTextMatching(match TextMatcher) []MapEntry {
	var entries []MapEntry
	for i, b := range e.doc.Blocks {
		text := b.Text()
		col := 0
		for {
			start, end, found := match(text, col)
			if !found {
				break
			}
			// get context around match
			contextStart := start - 20
			if contextStart < 0 {
				contextStart = 0
			}
			contextEnd := end + 20
			if contextEnd > len(text) {
				contextEnd = len(text)
			}
			preview := text[contextStart:contextEnd]
			if contextStart > 0 {
				preview = "..." + preview
			}
			if contextEnd < len(text) {
				preview = preview + "..."
			}

			entries = append(entries, MapEntry{
				BlockIdx: i,
				Col:      start,
				Text:     preview,
			})
			col = end
		}
	}
	return entries
}

// =============================================================================
// Block Manipulation
// =============================================================================

// PromoteHeading increases heading level (H2 -> H1, etc.) or converts paragraph to H6
func (e *Editor) PromoteHeading() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	e.saveUndo()
	switch b.Type {
	case BlockH2:
		b.Type = BlockH1
	case BlockH3:
		b.Type = BlockH2
	case BlockH4:
		b.Type = BlockH3
	case BlockH5:
		b.Type = BlockH4
	case BlockH6:
		b.Type = BlockH5
	case BlockParagraph:
		b.Type = BlockH6 // paragraph -> H6 (lowest heading)
	}
	e.InvalidateCache()
}

// DemoteHeading decreases heading level (H1 -> H2, etc.) or converts H6 to paragraph
func (e *Editor) DemoteHeading() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	e.saveUndo()
	switch b.Type {
	case BlockH1:
		b.Type = BlockH2
	case BlockH2:
		b.Type = BlockH3
	case BlockH3:
		b.Type = BlockH4
	case BlockH4:
		b.Type = BlockH5
	case BlockH5:
		b.Type = BlockH6
	case BlockH6:
		b.Type = BlockParagraph // H6 -> paragraph
	}
	e.InvalidateCache()
}

// MoveBlockUp swaps the current block with the one above
func (e *Editor) MoveBlockUp() {
	if e.cursor.Block <= 0 {
		return
	}
	e.saveUndo()
	i := e.cursor.Block
	e.doc.Blocks[i], e.doc.Blocks[i-1] = e.doc.Blocks[i-1], e.doc.Blocks[i]
	e.cursor.Block--
	e.ensureCursorVisible()
	e.InvalidateCache()
}

// MoveBlockDown swaps the current block with the one below
func (e *Editor) MoveBlockDown() {
	if e.cursor.Block >= len(e.doc.Blocks)-1 {
		return
	}
	e.saveUndo()
	i := e.cursor.Block
	e.doc.Blocks[i], e.doc.Blocks[i+1] = e.doc.Blocks[i+1], e.doc.Blocks[i]
	e.cursor.Block++
	e.ensureCursorVisible()
	e.InvalidateCache()
}

// =============================================================================
// Movement Actions - return Pos, use moveCursor (no display update)
// Display updates are handled by middleware
// =============================================================================

// Left moves cursor left by n characters (h)
func (e *Editor) Left(n int) Pos {
	e.moveCursor(Pos{Block: e.cursor.Block, Col: e.cursor.Col - n})
	return e.cursor
}

// Right moves cursor right by n characters (l)
func (e *Editor) Right(n int) Pos {
	e.moveCursor(Pos{Block: e.cursor.Block, Col: e.cursor.Col + n})
	return e.cursor
}

// Up moves cursor up by n visual lines (k) - moves by screen lines, not blocks
func (e *Editor) Up(n int) Pos {
	for i := 0; i < n; i++ {
		e.visualLineUp()
	}
	return e.cursor
}

// Down moves cursor down by n visual lines (j) - moves by screen lines, not blocks
func (e *Editor) Down(n int) Pos {
	for i := 0; i < n; i++ {
		e.visualLineDown()
	}
	return e.cursor
}

// blockWrapWidth returns the effective wrap width for a block type
// uses blockPrefixLength to determine offset, so any template with a prefix
// (dialogue, lists, quotes, etc.) will get the correct wrap width
func (e *Editor) blockWrapWidth(b *Block) int {
	prefixLen := e.blockPrefixLength(b)
	width := contentWidth - prefixLen
	if width < 20 {
		width = 20
	}
	return width
}

// wrapPointsForBlock returns wrap points in document coordinates for a block
func (e *Editor) wrapPointsForBlock(b *Block) []int {
	wrapWidth := e.blockWrapWidth(b)
	return e.calculateWrapPointsForWidth([]rune(b.Text()), wrapWidth)
}

// visualLineUp moves up one screen line within wrapped text
func (e *Editor) visualLineUp() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	textLen := len([]rune(b.Text()))
	wrapPoints := e.wrapPointsForBlock(b)
	currentWrappedLine := e.wrappedLineForCol(wrapPoints, e.cursor.Col)

	if currentWrappedLine > 0 {
		// move to previous wrapped line within same block
		prevLineStart := 0
		if currentWrappedLine > 1 {
			prevLineStart = wrapPoints[currentWrappedLine-2]
		}
		currentLineStart := 0
		if currentWrappedLine > 0 {
			currentLineStart = wrapPoints[currentWrappedLine-1]
		}
		offsetInLine := e.cursor.Col - currentLineStart
		newCol := prevLineStart + offsetInLine
		// clamp to line bounds
		lineEnd := currentLineStart - 1
		if lineEnd < prevLineStart {
			lineEnd = prevLineStart
		}
		if newCol > lineEnd {
			newCol = lineEnd
		}
		e.moveCursor(Pos{Block: e.cursor.Block, Col: newCol})
	} else if e.cursor.Block > 0 {
		// move to last wrapped line of previous block
		e.cursor.Block--
		prevBlock := e.CurrentBlock()
		if prevBlock != nil {
			prevLen := len([]rune(prevBlock.Text()))
			prevWrapPoints := e.wrapPointsForBlock(prevBlock)
			if len(prevWrapPoints) > 0 {
				lastLineStart := prevWrapPoints[len(prevWrapPoints)-1]
				offsetInLine := e.cursor.Col
				newCol := lastLineStart + offsetInLine
				if newCol >= prevLen {
					newCol = prevLen - 1
					if newCol < lastLineStart {
						newCol = lastLineStart
					}
				}
				e.moveCursor(Pos{Block: e.cursor.Block, Col: newCol})
			} else {
				// single line block
				e.moveCursor(Pos{Block: e.cursor.Block, Col: e.cursor.Col})
			}
		}
	}
	_ = textLen // avoid unused variable warning
}

// visualLineDown moves down one screen line within wrapped text
func (e *Editor) visualLineDown() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	textLen := len([]rune(b.Text()))
	wrapPoints := e.wrapPointsForBlock(b)
	currentWrappedLine := e.wrappedLineForCol(wrapPoints, e.cursor.Col)
	totalWrappedLines := len(wrapPoints) + 1

	if currentWrappedLine < totalWrappedLines-1 {
		// move to next wrapped line within same block
		currentLineStart := 0
		if currentWrappedLine > 0 {
			currentLineStart = wrapPoints[currentWrappedLine-1]
		}
		nextLineStart := wrapPoints[currentWrappedLine]
		offsetInLine := e.cursor.Col - currentLineStart
		newCol := nextLineStart + offsetInLine
		// clamp to line bounds
		lineEnd := textLen
		if currentWrappedLine+1 < len(wrapPoints) {
			lineEnd = wrapPoints[currentWrappedLine+1] - 1
		}
		if newCol >= lineEnd {
			newCol = lineEnd - 1
			if newCol < nextLineStart {
				newCol = nextLineStart
			}
		}
		e.moveCursor(Pos{Block: e.cursor.Block, Col: newCol})
	} else if e.cursor.Block < len(e.doc.Blocks)-1 {
		// move to first wrapped line of next block
		currentLineStart := 0
		if currentWrappedLine > 0 {
			currentLineStart = wrapPoints[currentWrappedLine-1]
		}
		offsetInLine := e.cursor.Col - currentLineStart
		e.cursor.Block++
		e.moveCursor(Pos{Block: e.cursor.Block, Col: offsetInLine})
	}
}

// calculateWrapPoints returns the column positions where line wraps occur
// handles both hard newlines and soft width-based wrapping
func (e *Editor) calculateWrapPoints(text string) []int {
	if len(text) == 0 {
		return nil
	}

	var wrapPoints []int
	lineStart := 0

	for lineStart < len(text) {
		// first, check for hard newline within this segment
		newlinePos := -1
		for i := lineStart; i < len(text) && i < lineStart+contentWidth; i++ {
			if text[i] == '\n' {
				newlinePos = i
				break
			}
		}

		if newlinePos >= 0 {
			// hard newline found - wrap after it
			wrapPoints = append(wrapPoints, newlinePos+1)
			lineStart = newlinePos + 1
			continue
		}

		// no newline in this segment - check for width-based wrap
		lineEnd := lineStart + contentWidth
		if lineEnd >= len(text) {
			break // rest fits on one line
		}

		// check if there's a newline just past the width
		for i := lineStart; i < len(text); i++ {
			if text[i] == '\n' {
				newlinePos = i
				break
			}
		}

		if newlinePos >= 0 && newlinePos <= lineEnd {
			// newline is within reach
			wrapPoints = append(wrapPoints, newlinePos+1)
			lineStart = newlinePos + 1
			continue
		}

		// soft wrap at word boundary
		wrapAt := lineEnd
		for i := lineEnd - 1; i >= lineStart; i-- {
			if text[i] == ' ' {
				wrapAt = i + 1
				break
			}
		}
		wrapPoints = append(wrapPoints, wrapAt)
		lineStart = wrapAt

		// skip leading spaces (but not newlines)
		for lineStart < len(text) && text[lineStart] == ' ' {
			lineStart++
			if len(wrapPoints) > 0 {
				wrapPoints[len(wrapPoints)-1] = lineStart
			}
		}
	}

	return wrapPoints
}

// calculateWrapPointsRunes returns wrap points using rune indices (for cursor movement)
func (e *Editor) calculateWrapPointsRunes(runes []rune) []int {
	if len(runes) == 0 {
		return nil
	}

	var wrapPoints []int
	lineStart := 0

	for lineStart < len(runes) {
		// check for hard newline within this segment
		newlinePos := -1
		for i := lineStart; i < len(runes) && i < lineStart+contentWidth; i++ {
			if runes[i] == '\n' {
				newlinePos = i
				break
			}
		}

		if newlinePos >= 0 {
			wrapPoints = append(wrapPoints, newlinePos+1)
			lineStart = newlinePos + 1
			continue
		}

		// check for width-based wrap
		lineEnd := lineStart + contentWidth
		if lineEnd >= len(runes) {
			break
		}

		// check if there's a newline just past the width
		for i := lineStart; i < len(runes); i++ {
			if runes[i] == '\n' {
				newlinePos = i
				break
			}
		}

		if newlinePos >= 0 && newlinePos <= lineEnd {
			wrapPoints = append(wrapPoints, newlinePos+1)
			lineStart = newlinePos + 1
			continue
		}

		// soft wrap at word boundary
		wrapAt := lineEnd
		for i := lineEnd - 1; i >= lineStart; i-- {
			if runes[i] == ' ' {
				wrapAt = i + 1
				break
			}
		}
		wrapPoints = append(wrapPoints, wrapAt)
		lineStart = wrapAt

		// skip leading spaces
		for lineStart < len(runes) && runes[lineStart] == ' ' {
			lineStart++
			if len(wrapPoints) > 0 {
				wrapPoints[len(wrapPoints)-1] = lineStart
			}
		}
	}

	return wrapPoints
}

// calculateWrapPointsForWidth returns wrap points for a custom width (for dialogue etc)
// uses display width to handle CJK double-width characters correctly
func (e *Editor) calculateWrapPointsForWidth(runes []rune, width int) []int {
	if len(runes) == 0 || width <= 0 {
		return nil
	}

	var wrapPoints []int
	lineStart := 0

	for lineStart < len(runes) {
		// scan forward tracking display width
		displayWidth := 0
		lineEnd := lineStart
		lastSpace := -1

		for i := lineStart; i < len(runes); i++ {
			r := runes[i]

			// hard newline - wrap here
			if r == '\n' {
				wrapPoints = append(wrapPoints, i+1)
				lineStart = i + 1
				lineEnd = -1 // signal we handled it
				break
			}

			rw := runewidth.RuneWidth(r)
			if displayWidth+rw > width && i > lineStart {
				// would exceed width - need to wrap
				lineEnd = i
				break
			}

			displayWidth += rw
			if r == ' ' {
				lastSpace = i
			}
			lineEnd = i + 1
		}

		if lineEnd == -1 {
			// handled by newline
			continue
		}

		if lineEnd >= len(runes) {
			// rest fits on one line
			break
		}

		// soft wrap at word boundary if possible
		wrapAt := lineEnd
		if lastSpace > lineStart {
			wrapAt = lastSpace + 1
		}
		wrapPoints = append(wrapPoints, wrapAt)
		lineStart = wrapAt

		// skip leading spaces
		for lineStart < len(runes) && runes[lineStart] == ' ' {
			lineStart++
			if len(wrapPoints) > 0 {
				wrapPoints[len(wrapPoints)-1] = lineStart
			}
		}
	}

	return wrapPoints
}

// wrappedLineForCol returns which wrapped line (0-indexed) a column is on
func (e *Editor) wrappedLineForCol(wrapPoints []int, col int) int {
	for i, wp := range wrapPoints {
		if col < wp {
			return i
		}
	}
	return len(wrapPoints)
}

// BlockUp moves cursor up by n blocks (gk equivalent)
func (e *Editor) BlockUp(n int) Pos {
	e.moveCursor(Pos{Block: e.cursor.Block - n, Col: e.cursor.Col})
	return e.cursor
}

// BlockDown moves cursor down by n blocks (gj equivalent)
func (e *Editor) BlockDown(n int) Pos {
	e.moveCursor(Pos{Block: e.cursor.Block + n, Col: e.cursor.Col})
	return e.cursor
}

// ScrollHalfPageDown scrolls down half a page (Ctrl-d)
func (e *Editor) ScrollHalfPageDown() {
	halfPage := e.screenHeight / 2
	if halfPage < 1 {
		halfPage = 1
	}
	for i := 0; i < halfPage; i++ {
		e.visualLineDown()
	}
}

// ScrollHalfPageUp scrolls up half a page (Ctrl-u)
func (e *Editor) ScrollHalfPageUp() {
	halfPage := e.screenHeight / 2
	if halfPage < 1 {
		halfPage = 1
	}
	for i := 0; i < halfPage; i++ {
		e.visualLineUp()
	}
}

// ScrollPageDown scrolls down a full page (Ctrl-f)
func (e *Editor) ScrollPageDown() {
	page := e.screenHeight - 2 // keep a couple lines of context
	if page < 1 {
		page = 1
	}
	for i := 0; i < page; i++ {
		e.visualLineDown()
	}
}

// ScrollPageUp scrolls up a full page (Ctrl-b)
func (e *Editor) ScrollPageUp() {
	page := e.screenHeight - 2 // keep a couple lines of context
	if page < 1 {
		page = 1
	}
	for i := 0; i < page; i++ {
		e.visualLineUp()
	}
}

// ScrollCenter centers the cursor line on screen (zz)
func (e *Editor) ScrollCenter() {
	cursorLine := e.cursorScreenLine()
	e.topLine = cursorLine - e.screenHeight/2
	if !e.typewriterMode && e.topLine < 0 {
		e.topLine = 0
	}
}

// ScrollTop moves cursor line to top of screen (zt)
func (e *Editor) ScrollTop() {
	cursorLine := e.cursorScreenLine()
	e.topLine = cursorLine
	if e.topLine < 0 {
		e.topLine = 0
	}
}

// ScrollBottom moves cursor line to bottom of screen (zb)
func (e *Editor) ScrollBottom() {
	cursorLine := e.cursorScreenLine()
	e.topLine = cursorLine - e.screenHeight + 1
	if e.topLine < 0 {
		e.topLine = 0
	}
}

// LineStart moves to first column (0)
func (e *Editor) LineStart() Pos {
	e.moveCursor(Pos{Block: e.cursor.Block, Col: 0})
	return e.cursor
}

// LineEnd moves to last column ($)
func (e *Editor) LineEnd() Pos {
	b := e.CurrentBlock()
	if b == nil {
		return e.cursor
	}
	col := b.Length()
	if e.mode != ModeInsert && col > 0 {
		col--
	}
	e.moveCursor(Pos{Block: e.cursor.Block, Col: col})
	return e.cursor
}

// FirstNonBlank moves to first non-whitespace character (^)
func (e *Editor) FirstNonBlank() Pos {
	text := e.CurrentLine()
	col := 0
	for col < len(text) && (text[col] == ' ' || text[col] == '\t') {
		col++
	}
	e.moveCursor(Pos{Block: e.cursor.Block, Col: col})
	return e.cursor
}

// DocStart moves to the first block (gg)
func (e *Editor) DocStart() Pos {
	e.addToJumpList()
	e.moveCursor(Pos{Block: 0, Col: 0})
	return e.cursor
}

// DocEnd moves to the last block (G)
func (e *Editor) DocEnd() Pos {
	e.addToJumpList()
	e.moveCursor(Pos{Block: len(e.doc.Blocks) - 1, Col: 0})
	return e.cursor
}

// ToDocEnd returns a linewise range from current block to end of document (for dG, cG, yG)
func (e *Editor) ToDocEnd() Range {
	lastBlock := len(e.doc.Blocks) - 1
	lastBlockLen := e.doc.Blocks[lastBlock].Length()
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: 0},
		End:   Pos{Block: lastBlock, Col: lastBlockLen},
	}
}

// ToDocStart returns a linewise range from start of document to current block (for dgg, cgg, ygg)
func (e *Editor) ToDocStart() Range {
	curBlockLen := e.doc.Blocks[e.cursor.Block].Length()
	return Range{
		Start: Pos{Block: 0, Col: 0},
		End:   Pos{Block: e.cursor.Block, Col: curBlockLen},
	}
}

// WholeDoc returns a range covering the entire document (for dgG, cgG, ygG)
func (e *Editor) WholeDoc() Range {
	lastBlock := len(e.doc.Blocks) - 1
	lastBlockLen := e.doc.Blocks[lastBlock].Length()
	return Range{
		Start: Pos{Block: 0, Col: 0},
		End:   Pos{Block: lastBlock, Col: lastBlockLen},
	}
}

// ToLineEnd returns range from cursor to end of current line (for d$, c$, y$)
func (e *Editor) ToLineEnd() Range {
	lineLen := e.doc.Blocks[e.cursor.Block].Length()
	return Range{
		Start: e.cursor,
		End:   Pos{Block: e.cursor.Block, Col: lineLen},
	}
}

// ToLineStart returns range from start of line to cursor (for d0, c0, y0)
func (e *Editor) ToLineStart() Range {
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: 0},
		End:   e.cursor,
	}
}

// NextWordStart moves to the start of the next word (w)
func (e *Editor) NextWordStart(n int) Pos {
	for i := 0; i < n; i++ {
		e.wordForward()
	}
	return e.cursor
}

// PrevWordStart moves to the start of the previous word (b)
func (e *Editor) PrevWordStart(n int) Pos {
	for i := 0; i < n; i++ {
		e.wordBackward()
	}
	return e.cursor
}

// NextWordEnd moves to the end of the current/next word (e)
func (e *Editor) NextWordEnd(n int) Pos {
	for i := 0; i < n; i++ {
		e.wordEnd()
	}
	return e.cursor
}

// wordForward moves to start of next word (internal helper)
func (e *Editor) wordForward() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	runes := []rune(b.Text())

	// skip current word
	for e.cursor.Col < len(runes) && !isWhitespace(runes[e.cursor.Col]) {
		e.cursor.Col++
	}
	// skip whitespace
	for e.cursor.Col < len(runes) && isWhitespace(runes[e.cursor.Col]) {
		e.cursor.Col++
	}

	// if at end of block, move to next block
	if e.cursor.Col >= len(runes) && e.cursor.Block < len(e.doc.Blocks)-1 {
		e.cursor.Block++
		e.cursor.Col = 0
		b = e.CurrentBlock()
		if b != nil {
			runes = []rune(b.Text())
			for e.cursor.Col < len(runes) && isWhitespace(runes[e.cursor.Col]) {
				e.cursor.Col++
			}
		}
	}
	e.SetCursorQuiet(e.cursor) // clamp
}

// wordBackward moves to start of previous word (internal helper)
func (e *Editor) wordBackward() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	runes := []rune(b.Text())

	// if at start of block, move to previous block
	if e.cursor.Col == 0 && e.cursor.Block > 0 {
		e.cursor.Block--
		b = e.CurrentBlock()
		if b != nil {
			e.cursor.Col = b.Length()
			runes = []rune(b.Text())
		}
	}

	// skip whitespace backwards
	for e.cursor.Col > 0 && isWhitespace(runes[e.cursor.Col-1]) {
		e.cursor.Col--
	}
	// skip word backwards
	for e.cursor.Col > 0 && !isWhitespace(runes[e.cursor.Col-1]) {
		e.cursor.Col--
	}
	e.SetCursorQuiet(e.cursor) // clamp
}

// wordEnd moves to end of current/next word (internal helper)
func (e *Editor) wordEnd() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	runes := []rune(b.Text())

	e.cursor.Col++ // move at least one

	// if past end of block, move to next block
	if e.cursor.Col >= len(runes) && e.cursor.Block < len(e.doc.Blocks)-1 {
		e.cursor.Block++
		e.cursor.Col = 0
		b = e.CurrentBlock()
		if b != nil {
			runes = []rune(b.Text())
		}
	}

	// skip whitespace
	for e.cursor.Col < len(runes) && isWhitespace(runes[e.cursor.Col]) {
		e.cursor.Col++
	}
	// skip to end of word
	for e.cursor.Col < len(runes)-1 && !isWhitespace(runes[e.cursor.Col+1]) {
		e.cursor.Col++
	}

	e.SetCursorQuiet(e.cursor) // clamp
}

func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func isSentenceEnd(r rune) bool {
	return r == '.' || r == '!' || r == '?'
}

func isClauseDelimiter(r rune) bool {
	return r == ',' || r == ';' || r == ':' || r == '\u2014' || r == '\u2013'
}

// =============================================================================
// Raw Mode Helpers
// =============================================================================

// rawInlinePrefix returns the opening markdown marker for an inline style
func rawInlinePrefix(s InlineStyle) string {
	if s == StyleNone {
		return ""
	}
	if s.Has(StyleCode) {
		return "`"
	}
	var p string
	if s.Has(StyleStrikethrough) {
		p += "~~"
	}
	if s.Has(StyleBold) && s.Has(StyleItalic) {
		p += "***"
	} else if s.Has(StyleBold) {
		p += "**"
	} else if s.Has(StyleItalic) {
		p += "*"
	}
	return p
}

// rawInlineSuffix returns the closing markdown marker for an inline style
func rawInlineSuffix(s InlineStyle) string {
	if s == StyleNone {
		return ""
	}
	if s.Has(StyleCode) {
		return "`"
	}
	var p string
	if s.Has(StyleBold) && s.Has(StyleItalic) {
		p += "***"
	} else if s.Has(StyleBold) {
		p += "**"
	} else if s.Has(StyleItalic) {
		p += "*"
	}
	if s.Has(StyleStrikethrough) {
		p += "~~"
	}
	return p
}

// deriveBlockTypeFromText determines block type and attrs from the raw text content.
// used in raw mode where the text IS the source of truth.
func deriveBlockTypeFromText(text string) (BlockType, map[string]string) {
	// code fence (must contain newline — opening and closing fence)
	if strings.HasPrefix(text, "```") {
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			lines := strings.Split(text, "\n")
			if len(lines) >= 2 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
				lang := strings.TrimPrefix(lines[0], "```")
				attrs := map[string]string{}
				if lang != "" {
					attrs["lang"] = lang
				}
				return BlockCodeLine, attrs
			}
		}
	}

	// front matter (---\n...\n---)
	if strings.HasPrefix(text, "---\n") && strings.HasSuffix(text, "\n---") {
		return BlockFrontMatter, nil
	}

	// divider (exactly ---, ***, or ___)
	if text == "---" || text == "***" || text == "___" {
		return BlockDivider, nil
	}

	// headings (longest prefix first)
	if strings.HasPrefix(text, "###### ") {
		return BlockH6, nil
	}
	if strings.HasPrefix(text, "##### ") {
		return BlockH5, nil
	}
	if strings.HasPrefix(text, "#### ") {
		return BlockH4, nil
	}
	if strings.HasPrefix(text, "### ") {
		return BlockH3, nil
	}
	if strings.HasPrefix(text, "## ") {
		return BlockH2, nil
	}
	if strings.HasPrefix(text, "# ") {
		return BlockH1, nil
	}

	// quote
	if strings.HasPrefix(text, "> ") {
		return BlockQuote, nil
	}

	// bullet list
	if strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "* ") {
		return BlockListItem, nil
	}

	// numbered list (digits followed by ". ")
	for j, r := range text {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && j > 0 && j+1 < len(text) && text[j+1] == ' ' {
			return BlockListItem, map[string]string{
				"marker": "number",
				"number": text[:j],
			}
		}
		break
	}

	// dialogue (@@ character )
	if strings.HasPrefix(text, "@@ ") {
		rest := text[3:]
		if idx := strings.IndexByte(rest, ' '); idx >= 0 {
			return BlockDialogue, map[string]string{"character": rest[:idx]}
		}
	}

	return BlockParagraph, nil
}

// rawBlockPrefix returns the markdown prefix for a block type (e.g. "# " for h1)
func rawBlockPrefix(b *Block) string {
	if b == nil {
		return ""
	}
	switch b.Type {
	case BlockH1:
		return "# "
	case BlockH2:
		return "## "
	case BlockH3:
		return "### "
	case BlockH4:
		return "#### "
	case BlockH5:
		return "##### "
	case BlockH6:
		return "###### "
	case BlockQuote:
		return "> "
	case BlockListItem:
		if b.Attrs != nil && b.Attrs["marker"] == "number" {
			num := b.Attrs["number"]
			if num == "" {
				num = "1"
			}
			return num + ". "
		}
		return "- "
	case BlockCallout:
		return "> "
	case BlockDialogue:
		character := ""
		if b.Attrs != nil {
			character = b.Attrs["character"]
		}
		return "@@ " + character + " "
	default:
		return ""
	}
}

// =============================================================================
// Find Character Motions (f/F/t/T)
// =============================================================================

// FindChar finds the next occurrence of ch on the line (f)
func (e *Editor) FindChar(ch rune) {
	e.lastFindChar = ch
	e.lastFindDir = 1
	e.lastFindTill = false
	e.doFindChar(true, false, ch)
}

// FindCharBack finds the previous occurrence of ch on the line (F)
func (e *Editor) FindCharBack(ch rune) {
	e.lastFindChar = ch
	e.lastFindDir = -1
	e.lastFindTill = false
	e.doFindChar(false, false, ch)
}

// TillChar moves to just before the next occurrence of ch (t)
func (e *Editor) TillChar(ch rune) {
	e.lastFindChar = ch
	e.lastFindDir = 1
	e.lastFindTill = true
	e.doFindChar(true, true, ch)
}

// TillCharBack moves to just after the previous occurrence of ch (T)
func (e *Editor) TillCharBack(ch rune) {
	e.lastFindChar = ch
	e.lastFindDir = -1
	e.lastFindTill = true
	e.doFindChar(false, true, ch)
}

// RepeatFind repeats the last f/F/t/T motion (;)
func (e *Editor) RepeatFind() {
	if e.lastFindChar != 0 {
		e.doFindChar(e.lastFindDir == 1, e.lastFindTill, e.lastFindChar)
	}
}

// RepeatFindReverse repeats the last f/F/t/T motion in reverse (,)
func (e *Editor) RepeatFindReverse() {
	if e.lastFindChar != 0 {
		e.doFindChar(e.lastFindDir != 1, e.lastFindTill, e.lastFindChar)
	}
}

func (e *Editor) doFindChar(forward, till bool, ch rune) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	runes := []rune(b.Text())

	if forward {
		// search forward from cursor+1
		for i := e.cursor.Col + 1; i < len(runes); i++ {
			if runes[i] == ch {
				newCol := i
				if till {
					newCol = i - 1
				}
				e.moveCursor(Pos{Block: e.cursor.Block, Col: newCol})
				return
			}
		}
	} else {
		// search backward from cursor-1
		for i := e.cursor.Col - 1; i >= 0; i-- {
			if runes[i] == ch {
				newCol := i
				if till {
					newCol = i + 1
				}
				e.moveCursor(Pos{Block: e.cursor.Block, Col: newCol})
				return
			}
		}
	}
}

// =============================================================================
// Marks
// =============================================================================

// SetMark sets a mark at the current position (m{a-z})
func (e *Editor) SetMark(reg rune) {
	e.marks[reg] = e.cursor
}

// GotoMark jumps to exact mark position (`{a-z})
func (e *Editor) GotoMark(reg rune) bool {
	if mark, ok := e.marks[reg]; ok {
		e.addToJumpList()
		e.moveCursor(mark)
		return true
	}
	return false
}

// GotoMarkLine jumps to mark line, first non-blank ('{a-z})
func (e *Editor) GotoMarkLine(reg rune) bool {
	if mark, ok := e.marks[reg]; ok {
		e.addToJumpList()
		e.cursor.Block = mark.Block
		e.FirstNonBlank()
		return true
	}
	return false
}

// =============================================================================
// Search (/, ?, n, N)
// =============================================================================

// Search sets the search pattern and direction, then finds first match
func (e *Editor) Search(pattern string, forward bool) bool {
	e.searchPattern = pattern
	if forward {
		e.searchDirection = 1
	} else {
		e.searchDirection = -1
	}
	e.lastSearchPos = e.cursor
	return e.SearchNext()
}

// SearchNext finds the next occurrence of the search pattern (n)
func (e *Editor) SearchNext() bool {
	if e.searchPattern == "" {
		return false
	}
	return e.doSearch(e.searchDirection == 1)
}

// SearchPrev finds the previous occurrence of the search pattern (N)
func (e *Editor) SearchPrev() bool {
	if e.searchPattern == "" {
		return false
	}
	return e.doSearch(e.searchDirection != 1)
}

func (e *Editor) doSearch(forward bool) bool {
	pattern := strings.ToLower(e.searchPattern)
	startBlock := e.cursor.Block
	startCol := e.cursor.Col + 1 // start searching from after cursor (rune position)

	if forward {
		// search forward through blocks
		for blockIdx := startBlock; blockIdx < len(e.doc.Blocks); blockIdx++ {
			text := strings.ToLower(e.doc.Blocks[blockIdx].Text())
			runes := []rune(text)
			searchStartRune := 0
			if blockIdx == startBlock {
				searchStartRune = startCol
			}
			if searchStartRune < len(runes) {
				// convert rune position to byte position for slicing
				searchStartByte := len(string(runes[:searchStartRune]))
				idx := strings.Index(text[searchStartByte:], pattern)
				if idx >= 0 {
					// convert byte position back to rune position
					matchRunePos := utf8.RuneCountInString(text[:searchStartByte+idx])
					e.addToJumpList()
					e.moveCursor(Pos{Block: blockIdx, Col: matchRunePos})
					return true
				}
			}
		}
		// wrap around to beginning
		for blockIdx := 0; blockIdx <= startBlock; blockIdx++ {
			text := strings.ToLower(e.doc.Blocks[blockIdx].Text())
			runes := []rune(text)
			maxColRune := len(runes)
			if blockIdx == startBlock {
				maxColRune = e.cursor.Col
			}
			// convert rune position to byte position
			maxColByte := len(string(runes[:maxColRune]))
			idx := strings.Index(text[:maxColByte], pattern)
			if idx >= 0 {
				// convert byte position back to rune position
				matchRunePos := utf8.RuneCountInString(text[:idx])
				e.addToJumpList()
				e.moveCursor(Pos{Block: blockIdx, Col: matchRunePos})
				return true
			}
		}
	} else {
		// search backward through blocks
		for blockIdx := startBlock; blockIdx >= 0; blockIdx-- {
			text := strings.ToLower(e.doc.Blocks[blockIdx].Text())
			runes := []rune(text)
			searchEndRune := len(runes)
			if blockIdx == startBlock {
				searchEndRune = e.cursor.Col
			}
			if searchEndRune > 0 {
				// convert rune position to byte position
				searchEndByte := len(string(runes[:searchEndRune]))
				idx := strings.LastIndex(text[:searchEndByte], pattern)
				if idx >= 0 {
					// convert byte position back to rune position
					matchRunePos := utf8.RuneCountInString(text[:idx])
					e.addToJumpList()
					e.moveCursor(Pos{Block: blockIdx, Col: matchRunePos})
					return true
				}
			}
		}
		// wrap around to end
		for blockIdx := len(e.doc.Blocks) - 1; blockIdx >= startBlock; blockIdx-- {
			text := strings.ToLower(e.doc.Blocks[blockIdx].Text())
			runes := []rune(text)
			minColRune := 0
			if blockIdx == startBlock {
				minColRune = e.cursor.Col + 1
			}
			if minColRune < len(runes) {
				// convert rune position to byte position
				minColByte := len(string(runes[:minColRune]))
				idx := strings.LastIndex(text[minColByte:], pattern)
				if idx >= 0 {
					// convert byte position back to rune position
					matchRunePos := utf8.RuneCountInString(text[:minColByte+idx])
					e.addToJumpList()
					e.moveCursor(Pos{Block: blockIdx, Col: matchRunePos})
					return true
				}
			}
		}
	}
	return false
}

// =============================================================================
// Jump List (Ctrl-o/Ctrl-i)
// =============================================================================

// addToJumpList adds current position to jump list before a jump
func (e *Editor) addToJumpList() {
	// don't add duplicates of current position
	if len(e.jumpList) > 0 && e.jumpListIdx > 0 {
		last := e.jumpList[e.jumpListIdx-1]
		if last.Block == e.cursor.Block && last.Col == e.cursor.Col {
			return
		}
	}

	// truncate forward history if we're not at the end
	if e.jumpListIdx < len(e.jumpList) {
		e.jumpList = e.jumpList[:e.jumpListIdx]
	}

	e.jumpList = append(e.jumpList, e.cursor)
	e.jumpListIdx = len(e.jumpList)

	// limit size
	if len(e.jumpList) > 100 {
		e.jumpList = e.jumpList[len(e.jumpList)-100:]
		e.jumpListIdx = len(e.jumpList)
	}
}

// JumpBack jumps to previous position in jump list (Ctrl-o)
func (e *Editor) JumpBack() bool {
	if e.jumpListIdx <= 0 || len(e.jumpList) == 0 {
		return false
	}

	// if at end of list, save current position first
	if e.jumpListIdx == len(e.jumpList) {
		e.jumpList = append(e.jumpList, e.cursor)
	}

	e.jumpListIdx--
	pos := e.jumpList[e.jumpListIdx]
	e.moveCursor(pos)
	return true
}

// JumpForward jumps to next position in jump list (Ctrl-i)
func (e *Editor) JumpForward() bool {
	if e.jumpListIdx >= len(e.jumpList)-1 {
		return false
	}

	e.jumpListIdx++
	pos := e.jumpList[e.jumpListIdx]
	e.moveCursor(pos)
	return true
}

// =============================================================================
// Screen Position Jumps (H/M/L)
// =============================================================================

// GotoScreenTop moves cursor to top of visible screen (H)
func (e *Editor) GotoScreenTop() Pos {
	// find which block is at topLine
	screenLine := 0
	for i := 0; i < len(e.doc.Blocks); i++ {
		block := &e.doc.Blocks[i]
		wrapPoints := e.wrapPointsForBlock(block)
		blockLineCount := len(wrapPoints) + 1

		if screenLine+blockLineCount > e.topLine {
			// cursor should be on this block
			lineInBlock := e.topLine - screenLine
			col := 0
			if lineInBlock > 0 && len(wrapPoints) > 0 && lineInBlock-1 < len(wrapPoints) {
				col = wrapPoints[lineInBlock-1]
			}
			e.moveCursor(Pos{Block: i, Col: col})
			e.FirstNonBlank()
			return e.cursor
		}
		screenLine += blockLineCount
	}
	e.moveCursor(Pos{Block: 0, Col: 0})
	return e.cursor
}

// GotoScreenMiddle moves cursor to middle of visible screen (M)
func (e *Editor) GotoScreenMiddle() Pos {
	middleLine := e.topLine + e.screenHeight/2
	return e.gotoScreenLine(middleLine)
}

// GotoScreenBottom moves cursor to bottom of visible screen (L)
func (e *Editor) GotoScreenBottom() Pos {
	bottomLine := e.topLine + e.screenHeight - 1
	return e.gotoScreenLine(bottomLine)
}

func (e *Editor) gotoScreenLine(targetLine int) Pos {
	screenLine := 0
	for i := 0; i < len(e.doc.Blocks); i++ {
		block := &e.doc.Blocks[i]
		wrapPoints := e.wrapPointsForBlock(block)
		blockLineCount := len(wrapPoints) + 1

		if screenLine+blockLineCount > targetLine {
			lineInBlock := targetLine - screenLine
			col := 0
			if lineInBlock > 0 && len(wrapPoints) > 0 && lineInBlock-1 < len(wrapPoints) {
				col = wrapPoints[lineInBlock-1]
			}
			e.moveCursor(Pos{Block: i, Col: col})
			e.FirstNonBlank()
			return e.cursor
		}
		screenLine += blockLineCount
	}
	// past end - go to last block
	e.moveCursor(Pos{Block: len(e.doc.Blocks) - 1, Col: 0})
	e.FirstNonBlank()
	return e.cursor
}

// =============================================================================
// Line Scroll (Ctrl-e/Ctrl-y)
// =============================================================================

// ScrollLineDown scrolls view down one line, keeping cursor (Ctrl-e)
func (e *Editor) ScrollLineDown() {
	e.topLine++
	// ensure cursor stays visible
	cursorLine := e.cursorScreenLine()
	if cursorLine < e.topLine {
		e.visualLineDown()
	}
}

// ScrollLineUp scrolls view up one line, keeping cursor (Ctrl-y)
func (e *Editor) ScrollLineUp() {
	if e.topLine > 0 {
		e.topLine--
	}
	// ensure cursor stays visible
	cursorLine := e.cursorScreenLine()
	if cursorLine >= e.topLine+e.screenHeight {
		e.visualLineUp()
	}
}

// =============================================================================
// Case Toggle (~)
// =============================================================================

// ToggleCase toggles the case of the character under cursor (~)
func (e *Editor) ToggleCase() {
	b := e.CurrentBlock()
	if b == nil || e.cursor.Col >= b.Length() {
		return
	}
	e.saveUndo()

	text := b.Text()
	runes := []rune(text)
	ch := runes[e.cursor.Col]

	if ch >= 'a' && ch <= 'z' {
		ch = ch - 'a' + 'A'
	} else if ch >= 'A' && ch <= 'Z' {
		ch = ch - 'A' + 'a'
	}
	runes[e.cursor.Col] = ch

	// update block
	if len(b.Runs) == 1 {
		b.Runs[0].Text = string(runes)
	} else {
		runIdx, runOff := b.RunAt(e.cursor.Col)
		if runIdx < len(b.Runs) {
			run := &b.Runs[runIdx]
			runRunes := []rune(run.Text)
			if runOff < len(runRunes) {
				runRunes[runOff] = ch
				run.Text = string(runRunes)
			}
		}
	}

	// move cursor right
	e.Right(1)
}

// ToggleCaseRange toggles case for all characters in range (visual mode ~)
func (e *Editor) ToggleCaseRange(r Range) {
	e.saveUndo()
	e.transformCaseRange(r, func(ch rune) rune {
		if ch >= 'a' && ch <= 'z' {
			return ch - 'a' + 'A'
		} else if ch >= 'A' && ch <= 'Z' {
			return ch - 'A' + 'a'
		}
		return ch
	})
}

// UppercaseRange converts all characters in range to uppercase (visual mode U)
func (e *Editor) UppercaseRange(r Range) {
	e.saveUndo()
	e.transformCaseRange(r, func(ch rune) rune {
		if ch >= 'a' && ch <= 'z' {
			return ch - 'a' + 'A'
		}
		return ch
	})
}

// LowercaseRange converts all characters in range to lowercase (visual mode u)
func (e *Editor) LowercaseRange(r Range) {
	e.saveUndo()
	e.transformCaseRange(r, func(ch rune) rune {
		if ch >= 'A' && ch <= 'Z' {
			return ch - 'A' + 'a'
		}
		return ch
	})
}

// transformCaseRange applies a transform function to each character in range
func (e *Editor) transformCaseRange(r Range, transform func(rune) rune) {
	for blockIdx := r.Start.Block; blockIdx <= r.End.Block && blockIdx < len(e.doc.Blocks); blockIdx++ {
		b := &e.doc.Blocks[blockIdx]
		text := b.Text()
		runes := []rune(text)

		startCol := 0
		endCol := len(runes)

		if blockIdx == r.Start.Block {
			startCol = r.Start.Col
		}
		if blockIdx == r.End.Block {
			endCol = r.End.Col
			if endCol > len(runes) {
				endCol = len(runes)
			}
		}

		// transform each character
		for i := startCol; i < endCol; i++ {
			runes[i] = transform(runes[i])
		}

		// update the block
		if len(b.Runs) == 1 {
			b.Runs[0].Text = string(runes)
		} else {
			b.Runs = []Run{{Text: string(runes), Style: StyleNone}}
		}
	}
}

// =============================================================================
// Line Navigation (-/Enter)
// =============================================================================

// PrevLineFirstNonBlank moves to first non-blank of previous visual line (-)
func (e *Editor) PrevLineFirstNonBlank() Pos {
	e.visualLineUp()
	e.FirstNonBlank()
	return e.cursor
}

// NextLineFirstNonBlank moves to first non-blank of next visual line (Enter)
func (e *Editor) NextLineFirstNonBlank() Pos {
	e.visualLineDown()
	e.FirstNonBlank()
	return e.cursor
}

// =============================================================================
// Insert Mode Extras
// =============================================================================

// DeleteWordBack deletes word before cursor in insert mode (Ctrl-w)
func (e *Editor) DeleteWordBack() {
	b := e.CurrentBlock()
	if b == nil || e.cursor.Col == 0 {
		return
	}

	runes := []rune(b.Text())
	col := e.cursor.Col

	// skip trailing whitespace
	for col > 0 && isWhitespace(runes[col-1]) {
		col--
	}
	// skip word characters
	for col > 0 && !isWhitespace(runes[col-1]) {
		col--
	}

	// delete from col to cursor
	if col < e.cursor.Col {
		newText := string(runes[:col]) + string(runes[e.cursor.Col:])
		if len(b.Runs) == 1 {
			b.Runs[0].Text = newText
		} else {
			b.Runs = []Run{{Text: newText, Style: StyleNone}}
		}
		e.cursor.Col = col
	}
}

// DeleteToLineStart deletes from cursor to start of line (Ctrl-u)
func (e *Editor) DeleteToLineStart() {
	b := e.CurrentBlock()
	if b == nil || e.cursor.Col == 0 {
		return
	}

	runes := []rune(b.Text())
	newText := string(runes[e.cursor.Col:])
	if len(b.Runs) == 1 {
		b.Runs[0].Text = newText
	} else {
		b.Runs = []Run{{Text: newText, Style: StyleNone}}
	}
	e.cursor.Col = 0
}

// =============================================================================
// Increment/Decrement Numbers (Ctrl-a/Ctrl-x)
// =============================================================================

// IncrementNumber increments number under cursor (Ctrl-a)
func (e *Editor) IncrementNumber(delta int) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	text := b.Text()
	runes := []rune(text)

	// find number under or after cursor
	numStart := -1
	numEnd := -1

	// first check if cursor is on a digit
	if e.cursor.Col < len(runes) && runes[e.cursor.Col] >= '0' && runes[e.cursor.Col] <= '9' {
		numStart = e.cursor.Col
		numEnd = e.cursor.Col + 1
	} else {
		// look forward for a number
		for i := e.cursor.Col; i < len(runes); i++ {
			if runes[i] >= '0' && runes[i] <= '9' {
				numStart = i
				numEnd = i + 1
				break
			}
		}
	}

	if numStart < 0 {
		return
	}

	// expand to full number
	for numStart > 0 && runes[numStart-1] >= '0' && runes[numStart-1] <= '9' {
		numStart--
	}
	for numEnd < len(runes) && runes[numEnd] >= '0' && runes[numEnd] <= '9' {
		numEnd++
	}

	// check for negative sign
	isNegative := false
	if numStart > 0 && runes[numStart-1] == '-' {
		isNegative = true
		numStart--
	}

	numStr := string(runes[numStart:numEnd])
	var num int
	_, err := fmt.Sscanf(numStr, "%d", &num)
	if err != nil {
		return
	}

	e.saveUndo()
	num += delta
	newNumStr := fmt.Sprintf("%d", num)

	// handle leading zeros preservation (simplified)
	if !isNegative && len(numStr) > 1 && numStr[0] == '0' && num >= 0 && num < 10 {
		newNumStr = fmt.Sprintf("%0*d", len(numStr), num)
	}

	newText := string(runes[:numStart]) + newNumStr + string(runes[numEnd:])
	if len(b.Runs) == 1 {
		b.Runs[0].Text = newText
	} else {
		b.Runs = []Run{{Text: newText, Style: StyleNone}}
	}

	// position cursor at end of number
	e.cursor.Col = numStart + len(newNumStr) - 1
	e.SetCursorQuiet(e.cursor)
}

// =============================================================================
// Repeat Last Change (.)
// =============================================================================

// SetLastAction records an action for repeat
func (e *Editor) SetLastAction(name string, action func()) {
	e.lastActionName = name
	e.lastAction = action
}

// RepeatLastAction repeats the last recorded action (.)
func (e *Editor) RepeatLastAction() {
	if e.lastAction != nil {
		e.lastAction()
	}
}

// =============================================================================
// Sentence Motions
// =============================================================================

// NextSentence moves to the start of the next sentence ())
func (e *Editor) NextSentence() Pos {
	b := e.CurrentBlock()
	if b == nil {
		return e.cursor
	}
	runes := []rune(b.Text())

	// search forward for sentence end followed by whitespace
	for i := e.cursor.Col; i < len(runes); i++ {
		if isSentenceEnd(runes[i]) {
			// skip to after punctuation and any trailing space
			for j := i + 1; j < len(runes); j++ {
				if !isWhitespace(runes[j]) {
					e.moveCursor(Pos{Block: e.cursor.Block, Col: j})
					return e.cursor
				}
			}
			// reached end of block - go to next block
			if e.cursor.Block < len(e.doc.Blocks)-1 {
				e.cursor.Block++
				e.cursor.Col = 0
				// skip leading whitespace
				nextBlock := e.CurrentBlock()
				if nextBlock != nil {
					nextRunes := []rune(nextBlock.Text())
					for e.cursor.Col < len(nextRunes) && isWhitespace(nextRunes[e.cursor.Col]) {
						e.cursor.Col++
					}
				}
				e.SetCursorQuiet(e.cursor)
				return e.cursor
			}
		}
	}

	// no sentence end found - try next block
	if e.cursor.Block < len(e.doc.Blocks)-1 {
		e.cursor.Block++
		e.cursor.Col = 0
		e.SetCursorQuiet(e.cursor)
	}
	return e.cursor
}

// PrevSentence moves to the start of the previous sentence (()
func (e *Editor) PrevSentence() Pos {
	b := e.CurrentBlock()
	if b == nil {
		return e.cursor
	}
	runes := []rune(b.Text())

	// search backward for sentence end
	startCol := e.cursor.Col - 1
	if startCol < 0 {
		// at start of block - go to previous
		if e.cursor.Block > 0 {
			e.cursor.Block--
			b = e.CurrentBlock()
			runes = []rune(b.Text())
			startCol = len(runes) - 1
		} else {
			return e.cursor
		}
	}

	// skip whitespace we're currently in
	for startCol >= 0 && isWhitespace(runes[startCol]) {
		startCol--
	}

	// search for sentence start
	for i := startCol; i >= 0; i-- {
		if isSentenceEnd(runes[i]) {
			// found end of previous sentence - sentence starts after it
			for j := i + 1; j < len(runes); j++ {
				if !isWhitespace(runes[j]) {
					e.moveCursor(Pos{Block: e.cursor.Block, Col: j})
					return e.cursor
				}
			}
		}
	}

	// no sentence end found - go to start of block
	e.moveCursor(Pos{Block: e.cursor.Block, Col: 0})
	return e.cursor
}

// =============================================================================
// Text Objects - return Range (don't move cursor)
// =============================================================================

// InnerWord returns the bounds of the word under cursor (iw)
func (e *Editor) InnerWord() Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}
	runes := []rune(b.Text())
	if len(runes) == 0 {
		return Range{Start: e.cursor, End: e.cursor}
	}

	start := e.cursor.Col
	end := e.cursor.Col

	// expand backwards
	for start > 0 && isSpellWordChar(runes[start-1]) {
		start--
	}
	// expand forwards
	for end < len(runes) && isWordChar(runes[end]) {
		end++
	}

	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start},
		End:   Pos{Block: e.cursor.Block, Col: end},
	}
}

// AWord returns the bounds of the word under cursor including trailing space (aw)
func (e *Editor) AWord() Range {
	r := e.InnerWord()
	b := e.CurrentBlock()
	if b == nil {
		return r
	}
	runes := []rune(b.Text())

	// include trailing whitespace
	end := r.End.Col
	for end < len(runes) && isWhitespace(runes[end]) {
		end++
	}
	r.End.Col = end

	return r
}

// InnerSentence returns the bounds of the current sentence (is)
func (e *Editor) InnerSentence() Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}
	text := b.Text()
	if len(text) == 0 {
		return Range{Start: e.cursor, End: e.cursor}
	}

	// work with runes to handle UTF-8 correctly
	runes := []rune(text)

	// find sentence start
	start := e.cursor.Col
	if start > len(runes) {
		start = len(runes)
	}
	for start > 0 {
		if isSentenceEnd(runes[start-1]) && (start >= len(runes) || runes[start] == ' ') {
			break
		}
		start--
	}
	// skip leading whitespace
	for start < len(runes) && isWhitespace(runes[start]) {
		start++
	}

	// find sentence end
	end := e.cursor.Col
	if end > len(runes) {
		end = len(runes)
	}
	for end < len(runes) {
		if isSentenceEnd(runes[end]) {
			end++ // include the punctuation
			break
		}
		end++
	}

	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start},
		End:   Pos{Block: e.cursor.Block, Col: end},
	}
}

// ASentence returns the bounds of the current sentence including trailing space (as)
func (e *Editor) ASentence() Range {
	r := e.InnerSentence()
	b := e.CurrentBlock()
	if b == nil {
		return r
	}
	runes := []rune(b.Text())

	// include trailing whitespace
	end := r.End.Col
	for end < len(runes) && isWhitespace(runes[end]) {
		end++
	}
	r.End.Col = end

	return r
}

// =============================================================================
// Clause Text Objects (i,/a,) - text between clause-level punctuation
// =============================================================================

// InnerClause returns the bounds of the current clause (i,)
// clauses are delimited by commas, semicolons, colons, em/en-dashes,
// sentence-ending punctuation, and block boundaries
func (e *Editor) InnerClause() Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}
	text := b.Text()
	if len(text) == 0 {
		return Range{Start: e.cursor, End: e.cursor}
	}

	runes := []rune(text)

	start := e.cursor.Col
	if start > len(runes) {
		start = len(runes)
	}
	end := start

	// scan backward to find clause boundary
	for start > 0 {
		r := runes[start-1]
		if isClauseDelimiter(r) || isSentenceEnd(r) {
			break
		}
		start--
	}
	// skip leading whitespace
	for start < len(runes) && isWhitespace(runes[start]) {
		start++
	}

	// scan forward to find clause boundary
	for end < len(runes) {
		r := runes[end]
		if isClauseDelimiter(r) || isSentenceEnd(r) {
			break
		}
		end++
	}

	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start},
		End:   Pos{Block: e.cursor.Block, Col: end},
	}
}

// AClause returns the clause including a trailing delimiter + whitespace (a,)
// if no trailing clause delimiter exists, includes leading delimiter + whitespace instead
func (e *Editor) AClause() Range {
	r := e.InnerClause()
	b := e.CurrentBlock()
	if b == nil {
		return r
	}
	runes := []rune(b.Text())

	// try trailing: include clause delimiter + whitespace after
	if r.End.Col < len(runes) && isClauseDelimiter(runes[r.End.Col]) {
		r.End.Col++
		for r.End.Col < len(runes) && isWhitespace(runes[r.End.Col]) {
			r.End.Col++
		}
		return r
	}

	// no trailing clause delimiter — grab leading delimiter + whitespace
	if r.Start.Col > 0 {
		lead := r.Start.Col
		// walk back past whitespace before the inner clause
		for lead > 0 && isWhitespace(runes[lead-1]) {
			lead--
		}
		// include the clause delimiter itself
		if lead > 0 && isClauseDelimiter(runes[lead-1]) {
			r.Start.Col = lead - 1
		}
	}

	return r
}

// =============================================================================
// Section Text Objects (iS/aS) - structural document sections
// =============================================================================

// headingLevel returns the heading level (1-6) or 0 if not a heading
func headingLevel(bt BlockType) int {
	switch bt {
	case BlockH1:
		return 1
	case BlockH2:
		return 2
	case BlockH3:
		return 3
	case BlockH4:
		return 4
	case BlockH5:
		return 5
	case BlockH6:
		return 6
	default:
		return 0
	}
}

// findSectionBounds finds the start and end of the section containing the cursor
// returns: sectionStart (the heading block), sectionEnd (exclusive), headingLevel
// if cursor is not under any heading, sectionStart = 0
func (e *Editor) findSectionBounds() (sectionStart, sectionEnd, level int) {
	curBlock := e.cursor.Block

	// find the heading that owns this position (search backward)
	level = 0
	sectionStart = 0
	for i := curBlock; i >= 0; i-- {
		l := headingLevel(e.doc.Blocks[i].Type)
		if l > 0 {
			sectionStart = i
			level = l
			break
		}
	}

	// if no heading found, section is from start of document
	if level == 0 {
		sectionStart = 0
		level = 0 // treat as "level 0" - everything until first heading
	}

	// find section end: next heading at same or higher (lower number) level
	sectionEnd = len(e.doc.Blocks)
	for i := sectionStart + 1; i < len(e.doc.Blocks); i++ {
		l := headingLevel(e.doc.Blocks[i].Type)
		if l > 0 && (level == 0 || l <= level) {
			sectionEnd = i
			break
		}
	}

	return sectionStart, sectionEnd, level
}

// InnerSection returns the section content based on cursor position (iS)
// - cursor on heading: content after heading until section end (no heading)
// - cursor in body: immediate content until first sub-heading or section end
func (e *Editor) InnerSection() Range {
	curBlock := e.cursor.Block
	sectionStart, sectionEnd, level := e.findSectionBounds()

	// check if cursor is on a heading
	curLevel := headingLevel(e.doc.Blocks[curBlock].Type)

	if curLevel > 0 {
		// cursor is on a heading - select content after this heading
		contentStart := curBlock + 1
		if contentStart >= len(e.doc.Blocks) {
			// heading at end of doc, empty selection
			return Range{
				Start: Pos{Block: curBlock, Col: 0},
				End:   Pos{Block: curBlock, Col: 0},
			}
		}

		// find end: next heading at same or higher level
		contentEnd := len(e.doc.Blocks)
		for i := contentStart; i < len(e.doc.Blocks); i++ {
			l := headingLevel(e.doc.Blocks[i].Type)
			if l > 0 && l <= curLevel {
				contentEnd = i
				break
			}
		}

		if contentStart >= contentEnd {
			return Range{
				Start: Pos{Block: curBlock, Col: 0},
				End:   Pos{Block: curBlock, Col: 0},
			}
		}

		return Range{
			Start: Pos{Block: contentStart, Col: 0},
			End:   Pos{Block: contentEnd - 1, Col: e.doc.Blocks[contentEnd-1].Length()},
		}
	}

	// cursor in body - select immediate content until first sub-heading
	contentStart := curBlock

	// find end: first heading or section end
	contentEnd := sectionEnd
	for i := curBlock; i < sectionEnd; i++ {
		l := headingLevel(e.doc.Blocks[i].Type)
		if l > 0 {
			contentEnd = i
			break
		}
	}

	// if we're past sectionStart, also look backward to find content boundary
	// (include all paragraphs in this immediate group)
	for i := curBlock - 1; i > sectionStart; i-- {
		l := headingLevel(e.doc.Blocks[i].Type)
		if l > 0 {
			contentStart = i + 1
			break
		}
	}
	if contentStart <= sectionStart && level > 0 {
		contentStart = sectionStart + 1 // skip the section heading
	}

	if contentStart >= contentEnd {
		// empty content
		return Range{
			Start: Pos{Block: curBlock, Col: 0},
			End:   Pos{Block: curBlock, Col: e.doc.Blocks[curBlock].Length()},
		}
	}

	return Range{
		Start: Pos{Block: contentStart, Col: 0},
		End:   Pos{Block: contentEnd - 1, Col: e.doc.Blocks[contentEnd-1].Length()},
	}
}

// ASection returns the entire section containing the cursor (aS)
// always includes heading + all content + subsections until next same-or-higher level heading
func (e *Editor) ASection() Range {
	curBlock := e.cursor.Block
	sectionStart, sectionEnd, level := e.findSectionBounds()

	// check if cursor is on a heading - use that heading's level for end detection
	curLevel := headingLevel(e.doc.Blocks[curBlock].Type)
	if curLevel > 0 {
		// cursor on heading - use this heading as section root
		sectionStart = curBlock
		level = curLevel

		// find end: next heading at same or higher level
		sectionEnd = len(e.doc.Blocks)
		for i := curBlock + 1; i < len(e.doc.Blocks); i++ {
			l := headingLevel(e.doc.Blocks[i].Type)
			if l > 0 && l <= level {
				sectionEnd = i
				break
			}
		}
	}

	// handle edge case: cursor before any heading
	if level == 0 {
		// select from start to first heading (or end of doc)
		sectionStart = 0
	}

	if sectionEnd <= sectionStart {
		sectionEnd = sectionStart + 1
	}

	return Range{
		Start: Pos{Block: sectionStart, Col: 0},
		End:   Pos{Block: sectionEnd - 1, Col: e.doc.Blocks[sectionEnd-1].Length()},
	}
}

// =============================================================================
// Section Navigation (]S/[S) - same-level section jumping
// =============================================================================

// NextSameLevel jumps to the next heading at the same level as the current section
func (e *Editor) NextSameLevel() Pos {
	_, _, level := e.findSectionBounds()

	// if we're on a heading, use that level instead
	curLevel := headingLevel(e.doc.Blocks[e.cursor.Block].Type)
	if curLevel > 0 {
		level = curLevel
	}

	// if no heading context (level 0), jump to first heading
	if level == 0 {
		for i := e.cursor.Block + 1; i < len(e.doc.Blocks); i++ {
			if headingLevel(e.doc.Blocks[i].Type) > 0 {
				e.moveCursor(Pos{Block: i, Col: 0})
				return e.cursor
			}
		}
		return e.cursor
	}

	// find next heading at same level
	for i := e.cursor.Block + 1; i < len(e.doc.Blocks); i++ {
		l := headingLevel(e.doc.Blocks[i].Type)
		if l == level {
			e.moveCursor(Pos{Block: i, Col: 0})
			return e.cursor
		}
	}
	return e.cursor
}

// PrevSameLevel jumps to the previous heading at the same level as the current section
func (e *Editor) PrevSameLevel() Pos {
	sectionStart, _, level := e.findSectionBounds()

	// if we're on a heading, use that level instead
	curLevel := headingLevel(e.doc.Blocks[e.cursor.Block].Type)
	if curLevel > 0 {
		level = curLevel
		sectionStart = e.cursor.Block
	}

	// if no heading context (level 0), jump to any heading
	if level == 0 {
		for i := e.cursor.Block - 1; i >= 0; i-- {
			if headingLevel(e.doc.Blocks[i].Type) > 0 {
				e.moveCursor(Pos{Block: i, Col: 0})
				return e.cursor
			}
		}
		return e.cursor
	}

	// search backward from before the current section's heading
	for i := sectionStart - 1; i >= 0; i-- {
		l := headingLevel(e.doc.Blocks[i].Type)
		if l == level {
			e.moveCursor(Pos{Block: i, Col: 0})
			return e.cursor
		}
	}
	return e.cursor
}

// SwapSentenceNext swaps the current sentence with the next one (gs))
func (e *Editor) SwapSentenceNext() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	runes := []rune(b.Text())
	if len(runes) == 0 {
		return
	}

	// get current sentence
	curr := e.InnerSentence()
	if curr.Start.Col >= curr.End.Col {
		return
	}

	// find next sentence start (skip whitespace after current)
	nextStart := curr.End.Col
	for nextStart < len(runes) && isWhitespace(runes[nextStart]) {
		nextStart++
	}
	if nextStart >= len(runes) {
		return // no next sentence
	}

	// find next sentence end
	nextEnd := nextStart
	for nextEnd < len(runes) {
		if isSentenceEnd(runes[nextEnd]) {
			nextEnd++ // include punctuation
			break
		}
		nextEnd++
	}

	// extract the sentences with their trailing spaces
	currText := string(runes[curr.Start.Col:curr.End.Col])
	spaceBetween := string(runes[curr.End.Col:nextStart])
	nextText := string(runes[nextStart:nextEnd])

	// build the swapped text
	newText := string(runes[:curr.Start.Col]) + nextText + spaceBetween + currText + string(runes[nextEnd:])

	// apply the change
	e.saveUndo()
	b.Runs = []Run{{Text: newText, Style: StyleNone}}

	// position cursor at start of moved sentence (now in next position)
	e.cursor.Col = curr.Start.Col + len([]rune(nextText)) + len([]rune(spaceBetween))
}

// SwapSentencePrev swaps the current sentence with the previous one (gs()
func (e *Editor) SwapSentencePrev() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	runes := []rune(b.Text())
	if len(runes) == 0 {
		return
	}

	// get current sentence
	curr := e.InnerSentence()
	if curr.Start.Col >= curr.End.Col {
		return
	}
	if curr.Start.Col == 0 {
		return // already at start, no previous sentence
	}

	// find previous sentence end (skip whitespace before current)
	prevEnd := curr.Start.Col
	for prevEnd > 0 && isWhitespace(runes[prevEnd-1]) {
		prevEnd--
	}
	if prevEnd == 0 {
		return // no previous sentence
	}

	// find previous sentence start
	prevStart := prevEnd
	for prevStart > 0 {
		if isSentenceEnd(runes[prevStart-1]) && prevStart < prevEnd {
			break
		}
		prevStart--
	}
	// skip leading whitespace of prev sentence
	for prevStart < prevEnd && isWhitespace(runes[prevStart]) {
		prevStart++
	}

	// extract the sentences
	prevText := string(runes[prevStart:prevEnd])
	spaceBetween := string(runes[prevEnd:curr.Start.Col])
	currText := string(runes[curr.Start.Col:curr.End.Col])

	// build the swapped text
	newText := string(runes[:prevStart]) + currText + spaceBetween + prevText + string(runes[curr.End.Col:])

	// apply the change
	e.saveUndo()
	b.Runs = []Run{{Text: newText, Style: StyleNone}}

	// position cursor at start of moved sentence (now in prev position)
	e.cursor.Col = prevStart
}

// InnerBlock returns the entire current block (ib - wed-specific)
func (e *Editor) InnerBlock() Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: 0},
		End:   Pos{Block: e.cursor.Block, Col: b.Length()},
	}
}

// InnerParagraph returns all consecutive non-empty blocks as a single paragraph (ip - vim compatible)
func (e *Editor) InnerParagraph() Range {
	if len(e.doc.Blocks) == 0 {
		return Range{Start: e.cursor, End: e.cursor}
	}

	curBlock := e.cursor.Block
	if curBlock < 0 || curBlock >= len(e.doc.Blocks) {
		return Range{Start: e.cursor, End: e.cursor}
	}

	// find start: go backward to first non-empty block after an empty one (or start of doc)
	startBlock := curBlock
	for startBlock > 0 {
		prevBlock := &e.doc.Blocks[startBlock-1]
		if prevBlock.Length() == 0 || prevBlock.Text() == "" {
			break
		}
		startBlock--
	}

	// find end: go forward to last non-empty block before an empty one (or end of doc)
	endBlock := curBlock
	for endBlock+1 < len(e.doc.Blocks) {
		nextBlock := &e.doc.Blocks[endBlock+1]
		if nextBlock.Length() == 0 || nextBlock.Text() == "" {
			break
		}
		endBlock++
	}

	return Range{
		Start: Pos{Block: startBlock, Col: 0},
		End:   Pos{Block: endBlock, Col: e.doc.Blocks[endBlock].Length()},
	}
}

// AParagraph returns the paragraph plus trailing empty blocks (ap - vim compatible)
func (e *Editor) AParagraph() Range {
	r := e.InnerParagraph()

	// include trailing empty blocks (like vim's ap includes trailing blank lines)
	endBlock := r.End.Block
	for endBlock+1 < len(e.doc.Blocks) {
		nextBlock := &e.doc.Blocks[endBlock+1]
		if nextBlock.Length() == 0 || nextBlock.Text() == "" {
			endBlock++
		} else {
			break
		}
	}

	if endBlock > r.End.Block {
		r.End.Block = endBlock
		r.End.Col = e.doc.Blocks[endBlock].Length()
	}

	return r
}

// =============================================================================
// Quote and Bracket Text Objects
// =============================================================================

// findMatchingPair searches for matching delimiters around cursor
// For quotes (same open/close), finds nearest pair containing cursor
// For brackets (different open/close), handles nesting
func (e *Editor) findMatchingPair(open, close rune) (start, end int, found bool) {
	b := e.CurrentBlock()
	if b == nil {
		return 0, 0, false
	}

	text := b.Text()
	runes := []rune(text)
	col := e.cursor.Col

	if open == close {
		// same delimiter (quotes) - vim behavior:
		// 1. if inside a pair, use that pair
		// 2. otherwise, search forward for next pair on the line

		// find all positions of the delimiter
		var positions []int
		for i, r := range runes {
			if r == open {
				positions = append(positions, i)
			}
		}

		// first: find pair that contains cursor (inside or on delimiter)
		for i := 0; i+1 < len(positions); i += 2 {
			pairStart := positions[i]
			pairEnd := positions[i+1]
			if col >= pairStart && col <= pairEnd {
				return pairStart, pairEnd, true
			}
		}

		// second: search forward for next pair after cursor (vim behavior)
		for i := 0; i+1 < len(positions); i += 2 {
			pairStart := positions[i]
			pairEnd := positions[i+1]
			if pairStart > col {
				return pairStart, pairEnd, true
			}
		}
	} else {
		// different delimiters (brackets) - vim behavior:
		// 1. if inside brackets, use innermost containing pair
		// 2. otherwise, search forward for next pair on the line

		// first: try to find containing pair (search backward for open)
		depth := 0
		startPos := -1

		for i := col; i >= 0; i-- {
			if runes[i] == close {
				depth++
			} else if runes[i] == open {
				if depth == 0 {
					startPos = i
					break
				}
				depth--
			}
		}

		if startPos >= 0 {
			// found opening, search forward for matching close
			depth = 0
			for i := startPos; i < len(runes); i++ {
				if runes[i] == open {
					depth++
				} else if runes[i] == close {
					depth--
					if depth == 0 {
						return startPos, i, true
					}
				}
			}
		}

		// second: search forward for next pair after cursor (vim behavior)
		for i := col; i < len(runes); i++ {
			if runes[i] == open {
				// found opening bracket, find its matching close
				depth := 1
				for j := i + 1; j < len(runes); j++ {
					if runes[j] == open {
						depth++
					} else if runes[j] == close {
						depth--
						if depth == 0 {
							return i, j, true
						}
					}
				}
			}
		}
	}

	return 0, 0, false
}

// findMatchingPairInCodeGroup searches for bracket pairs across consecutive
// BlockCodeLine blocks. concatenates the group text with \n, runs the matching,
// then maps the flat offset back to block+col.
func (e *Editor) findMatchingPairInCodeGroup(open, close rune) (startPos, endPos Pos, found bool) {
	// find group boundaries
	groupStart := e.cursor.Block
	for groupStart > 0 && e.doc.Blocks[groupStart-1].Type == BlockCodeLine {
		groupStart--
	}
	groupEnd := e.cursor.Block
	for groupEnd < len(e.doc.Blocks)-1 && e.doc.Blocks[groupEnd+1].Type == BlockCodeLine {
		groupEnd++
	}

	// build concatenated text and offset map (block index → start offset in flat text)
	blockOffsets := make([]int, groupEnd-groupStart+1)
	var sb strings.Builder
	for i := groupStart; i <= groupEnd; i++ {
		idx := i - groupStart
		blockOffsets[idx] = sb.Len()
		text := e.doc.Blocks[i].Text()
		sb.WriteString(text)
		if i < groupEnd {
			sb.WriteRune('\n')
		}
	}

	flatText := sb.String()
	runes := []rune(flatText)

	// compute cursor's flat offset
	cursorFlat := 0
	for i := groupStart; i < e.cursor.Block; i++ {
		cursorFlat += len([]rune(e.doc.Blocks[i].Text())) + 1 // +1 for \n
	}
	cursorFlat += e.cursor.Col

	// search backward for opening bracket
	depth := 0
	startFlat := -1
	for i := cursorFlat; i >= 0; i-- {
		if runes[i] == close {
			depth++
		} else if runes[i] == open {
			if depth == 0 {
				startFlat = i
				break
			}
			depth--
		}
	}
	if startFlat < 0 {
		return Pos{}, Pos{}, false
	}

	// search forward for closing bracket
	depth = 0
	endFlat := -1
	for i := startFlat; i < len(runes); i++ {
		if runes[i] == open {
			depth++
		} else if runes[i] == close {
			depth--
			if depth == 0 {
				endFlat = i
				break
			}
		}
	}
	if endFlat < 0 {
		return Pos{}, Pos{}, false
	}

	// map flat offsets back to block+col
	flatToPos := func(flat int) Pos {
		runeOffset := 0
		for i := groupStart; i <= groupEnd; i++ {
			blockLen := len([]rune(e.doc.Blocks[i].Text()))
			if runeOffset+blockLen > flat || i == groupEnd {
				return Pos{Block: i, Col: flat - runeOffset}
			}
			runeOffset += blockLen + 1 // +1 for \n
		}
		return Pos{Block: groupEnd, Col: 0}
	}

	return flatToPos(startFlat), flatToPos(endFlat), true
}

// InnerQuote returns content between quotes, not including quotes (i", i', i`)
func (e *Editor) InnerQuote(quote rune) Range {
	start, end, found := e.findMatchingPair(quote, quote)
	if !found {
		return Range{Start: e.cursor, End: e.cursor}
	}
	// inner = between quotes (exclusive)
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start + 1},
		End:   Pos{Block: e.cursor.Block, Col: end},
	}
}

// AroundQuote returns content including quotes (a", a', a`)
func (e *Editor) AroundQuote(quote rune) Range {
	start, end, found := e.findMatchingPair(quote, quote)
	if !found {
		return Range{Start: e.cursor, End: e.cursor}
	}
	// around = including quotes
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: start},
		End:   Pos{Block: e.cursor.Block, Col: end + 1},
	}
}

// InnerBracket returns content between brackets, not including brackets
func (e *Editor) InnerBracket(open, close rune) Range {
	start, end, found := e.findMatchingPair(open, close)
	if found {
		return Range{
			Start: Pos{Block: e.cursor.Block, Col: start + 1},
			End:   Pos{Block: e.cursor.Block, Col: end},
		}
	}
	// try cross-block matching within code line groups
	if b := e.CurrentBlock(); b != nil && b.Type == BlockCodeLine {
		sPos, ePos, ok := e.findMatchingPairInCodeGroup(open, close)
		if ok {
			// inner: advance past opening bracket, stop before closing
			sPos.Col++
			return Range{Start: sPos, End: ePos}
		}
	}
	return Range{Start: e.cursor, End: e.cursor}
}

// AroundBracket returns content including brackets
func (e *Editor) AroundBracket(open, close rune) Range {
	start, end, found := e.findMatchingPair(open, close)
	if found {
		return Range{
			Start: Pos{Block: e.cursor.Block, Col: start},
			End:   Pos{Block: e.cursor.Block, Col: end + 1},
		}
	}
	// try cross-block matching within code line groups
	if b := e.CurrentBlock(); b != nil && b.Type == BlockCodeLine {
		sPos, ePos, ok := e.findMatchingPairInCodeGroup(open, close)
		if ok {
			ePos.Col++
			return Range{Start: sPos, End: ePos}
		}
	}
	return Range{Start: e.cursor, End: e.cursor}
}

// Convenience methods for common brackets

func (e *Editor) InnerParen() Range  { return e.InnerBracket('(', ')') }
func (e *Editor) AroundParen() Range { return e.AroundBracket('(', ')') }

func (e *Editor) InnerSquare() Range  { return e.InnerBracket('[', ']') }
func (e *Editor) AroundSquare() Range { return e.AroundBracket('[', ']') }

func (e *Editor) InnerCurly() Range  { return e.InnerBracket('{', '}') }
func (e *Editor) AroundCurly() Range { return e.AroundBracket('{', '}') }

func (e *Editor) InnerAngle() Range  { return e.InnerBracket('<', '>') }
func (e *Editor) AroundAngle() Range { return e.AroundBracket('<', '>') }

// =============================================================================
// Tag Text Object (it/at for HTML/XML tags)
// =============================================================================

// findTag searches for matching HTML/XML tags around cursor
func (e *Editor) findTag() (outerStart, innerStart, innerEnd, outerEnd int, found bool) {
	b := e.CurrentBlock()
	if b == nil {
		return 0, 0, 0, 0, false
	}

	text := b.Text()
	runes := []rune(text)
	col := e.cursor.Col

	// search backward for opening tag <tagname>
	openTagEnd := -1
	openTagStart := -1

	for i := col; i >= 0; i-- {
		if runes[i] == '>' && openTagEnd < 0 {
			// check it's not a closing tag or self-closing
			if i > 0 && runes[i-1] != '/' {
				// look back for <
				for j := i - 1; j >= 0; j-- {
					if runes[j] == '<' {
						// check it's not a closing tag
						if j+1 < len(runes) && runes[j+1] != '/' {
							openTagStart = j
							openTagEnd = i
							break
						}
					}
				}
				if openTagStart >= 0 {
					break
				}
			}
		}
	}

	if openTagStart < 0 {
		return 0, 0, 0, 0, false
	}

	// extract tag name
	tagNameStart := openTagStart + 1
	tagNameEnd := tagNameStart
	for tagNameEnd < openTagEnd && runes[tagNameEnd] != ' ' && runes[tagNameEnd] != '>' {
		tagNameEnd++
	}
	tagName := string(runes[tagNameStart:tagNameEnd])

	// search forward for closing tag </tagname>
	closeTag := "</" + tagName + ">"
	closeTagRunes := []rune(closeTag)

	depth := 1
	i := openTagEnd + 1
	for i < len(runes) {
		// check for opening tag (same name)
		if i+len(tagName)+2 < len(runes) && runes[i] == '<' && runes[i+1] != '/' {
			// check if it's the same tag
			match := true
			for j := 0; j < len(tagName) && i+1+j < len(runes); j++ {
				if runes[i+1+j] != rune(tagName[j]) {
					match = false
					break
				}
			}
			if match && (i+1+len(tagName) >= len(runes) || runes[i+1+len(tagName)] == ' ' || runes[i+1+len(tagName)] == '>') {
				depth++
			}
		}

		// check for closing tag
		if i+len(closeTagRunes) <= len(runes) {
			match := true
			for j := 0; j < len(closeTagRunes); j++ {
				if runes[i+j] != closeTagRunes[j] {
					match = false
					break
				}
			}
			if match {
				depth--
				if depth == 0 {
					return openTagStart, openTagEnd + 1, i, i + len(closeTagRunes), true
				}
			}
		}
		i++
	}

	return 0, 0, 0, 0, false
}

// InnerTag returns content between tags, not including tags (it)
func (e *Editor) InnerTag() Range {
	_, innerStart, innerEnd, _, found := e.findTag()
	if !found {
		return Range{Start: e.cursor, End: e.cursor}
	}
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: innerStart},
		End:   Pos{Block: e.cursor.Block, Col: innerEnd},
	}
}

// AroundTag returns content including tags (at)
func (e *Editor) AroundTag() Range {
	outerStart, _, _, outerEnd, found := e.findTag()
	if !found {
		return Range{Start: e.cursor, End: e.cursor}
	}
	return Range{
		Start: Pos{Block: e.cursor.Block, Col: outerStart},
		End:   Pos{Block: e.cursor.Block, Col: outerEnd},
	}
}

// =============================================================================
// Visual Mode
// =============================================================================

// EnterVisual enters character-wise visual mode (v)
func (e *Editor) EnterVisual() {
	e.mode = ModeVisual
	e.visualMode = VisualChar
	e.visualStart = e.cursor
}

// EnterVisualLine enters line-wise visual mode (V)
func (e *Editor) EnterVisualLine() {
	e.mode = ModeVisual
	e.visualMode = VisualLine
	e.visualStart = e.cursor
}

// EnterVisualBlock enters block-wise visual mode (Ctrl-V)
func (e *Editor) EnterVisualBlock() {
	e.mode = ModeVisual
	e.visualMode = VisualBlock
	e.visualStart = e.cursor
}

// ExitVisual returns to normal mode
func (e *Editor) ExitVisual() {
	e.mode = ModeNormal
	e.visualMode = VisualNone
}

// SwapVisualEnds swaps cursor to other end of selection (o, O in visual mode)
func (e *Editor) SwapVisualEnds() {
	e.cursor, e.visualStart = e.visualStart, e.cursor
}

// ExpandVisualToRange expands visual selection to include the given range
func (e *Editor) ExpandVisualToRange(r Range) {
	// expand start to include range start if before current start
	if r.Start.Block < e.visualStart.Block ||
		(r.Start.Block == e.visualStart.Block && r.Start.Col < e.visualStart.Col) {
		e.visualStart = r.Start
	}
	// expand cursor to include range end
	if r.End.Block > e.cursor.Block ||
		(r.End.Block == e.cursor.Block && r.End.Col > e.cursor.Col) {
		e.cursor = Pos{Block: r.End.Block, Col: r.End.Col - 1}
		if e.cursor.Col < 0 {
			e.cursor.Col = 0
		}
	}
	e.SetCursorQuiet(e.cursor)
}

// SelectVisualRange sets visual selection to exactly the given range
// Used for text objects where we want to select the whole object, not expand
func (e *Editor) SelectVisualRange(r Range) {
	e.visualStart = r.Start
	endCol := r.End.Col - 1
	if endCol < 0 {
		endCol = 0
	}
	e.cursor = Pos{Block: r.End.Block, Col: endCol}
	e.SetCursorQuiet(e.cursor)
}

// CurrentVisualMode returns the current visual mode
func (e *Editor) CurrentVisualMode() VisualMode {
	return e.visualMode
}

// SetVisualMode changes the visual mode type (for switching between v, V, Ctrl-V)
func (e *Editor) SetVisualMode(mode VisualMode) {
	if mode == VisualNone {
		e.ExitVisual()
		return
	}
	e.mode = ModeVisual
	e.visualMode = mode
}

// VisualRange returns the normalized selection bounds (start always <= end)
func (e *Editor) VisualRange() Range {
	start := e.visualStart
	end := e.cursor

	// normalize so start <= end
	if start.Block > end.Block || (start.Block == end.Block && start.Col > end.Col) {
		start, end = end, start
	}

	// For visual char mode, include the character under cursor
	if e.visualMode == VisualChar {
		end.Col++
	}

	// For visual line mode, select whole blocks
	if e.visualMode == VisualLine {
		start.Col = 0
		if end.Block < len(e.doc.Blocks) {
			end.Col = e.doc.Blocks[end.Block].Length()
		}
	}

	return Range{Start: start, End: end}
}

// VisualSelectionInBlock returns the selected range within a specific block
// Returns (start, end, hasSelection)
func (e *Editor) VisualSelectionInBlock(blockIdx int) (int, int, bool) {
	if e.mode != ModeVisual {
		return 0, 0, false
	}

	r := e.VisualRange()

	if blockIdx < r.Start.Block || blockIdx > r.End.Block {
		return 0, 0, false
	}

	b := &e.doc.Blocks[blockIdx]
	blockLen := b.Length()

	start := 0
	end := blockLen

	if blockIdx == r.Start.Block {
		start = r.Start.Col
	}
	if blockIdx == r.End.Block {
		end = r.End.Col
		if end > blockLen {
			end = blockLen
		}
	}

	return start, end, true
}

// =============================================================================
// Mode Management
// =============================================================================

// EnterNormal enters normal mode
func (e *Editor) EnterNormal() {
	wasInsert := e.mode == ModeInsert
	e.mode = ModeNormal
	e.visualMode = VisualNone
	// In normal mode, cursor can't be past last char
	e.SetCursorQuiet(e.cursor)
	// invalidate cache if leaving insert mode with focus enabled
	// (focus dimming only applies in insert mode, so we need to re-render)
	if wasInsert && e.focusMode {
		e.InvalidateCache()
	}
}

// EnterInsert enters insert mode at current position (i)
func (e *Editor) EnterInsert() {
	e.saveUndo() // save state before insert session
	e.mode = ModeInsert
	// invalidate cache if focus mode enabled (dimming needs to be applied)
	if e.focusMode {
		e.InvalidateCache()
	}
}

// EnterInsertAfter enters insert mode after current character (a)
func (e *Editor) EnterInsertAfter() {
	e.saveUndo() // save state before insert session
	e.mode = ModeInsert
	b := e.CurrentBlock()
	if b != nil && e.cursor.Col < b.Length() {
		e.cursor.Col++
	}
	if e.focusMode {
		e.InvalidateCache()
	}
}

// EnterInsertLineEnd enters insert mode at end of line (A)
func (e *Editor) EnterInsertLineEnd() {
	e.saveUndo() // save state before insert session
	e.mode = ModeInsert
	b := e.CurrentBlock()
	if b != nil {
		e.cursor.Col = b.Length()
	}
	if e.focusMode {
		e.InvalidateCache()
	}
}

// EnterInsertLineStart enters insert mode at start of line (I)
func (e *Editor) EnterInsertLineStart() {
	e.saveUndo() // save state before insert session
	e.mode = ModeInsert
	e.FirstNonBlank()
	if e.focusMode {
		e.InvalidateCache()
	}
}

// OpenBelow opens a new line below and enters insert mode (o)
func (e *Editor) OpenBelow() {
	b := e.CurrentBlock()
	// if current dialogue block is empty, just enter insert mode here
	if b != nil && b.Type == BlockDialogue && b.Length() == 0 {
		e.mode = ModeInsert
		e.dialogueCharMode = false
		e.cursor.Col = 0
		if e.focusMode {
			e.InvalidateCache()
		}
		return
	}
	e.saveUndo()
	e.insertBlockBelow()
	e.mode = ModeInsert
	if e.focusMode {
		e.InvalidateCache()
	}
}

// OpenAbove opens a new line above and enters insert mode (O)
func (e *Editor) OpenAbove() {
	b := e.CurrentBlock()
	// if current dialogue block is empty, just enter insert mode here
	if b != nil && b.Type == BlockDialogue && b.Length() == 0 {
		e.mode = ModeInsert
		e.dialogueCharMode = false
		e.cursor.Col = 0
		if e.focusMode {
			e.InvalidateCache()
		}
		return
	}
	e.saveUndo()
	e.insertBlockAbove()
	e.mode = ModeInsert
	if e.focusMode {
		e.InvalidateCache()
	}
}

// =============================================================================
// Operators - work on Range
// =============================================================================

// Delete deletes text in range and returns deleted text
func (e *Editor) Delete(r Range) string {
	e.saveUndo()

	if r.Start.Block == r.End.Block {
		// single block deletion
		b := &e.doc.Blocks[r.Start.Block]
		runes := []rune(b.Text())

		start := r.Start.Col
		end := r.End.Col
		if end > len(runes) {
			end = len(runes)
		}
		if start > len(runes) {
			start = len(runes)
		}

		deleted := string(runes[start:end])
		newText := string(runes[:start]) + string(runes[end:])

		// preserve runs structure (simplified: just update text)
		b.Runs = []Run{{Text: newText, Style: StyleNone}}

		e.moveCursor(r.Start)
		e.InvalidateCache()
		return deleted
	}

	// multi-block deletion
	firstBlock := &e.doc.Blocks[r.Start.Block]
	lastBlock := &e.doc.Blocks[r.End.Block]

	firstRunes := []rune(firstBlock.Text())
	firstText := string(firstRunes[:r.Start.Col])
	lastRunes := []rune(lastBlock.Text())
	var lastText string
	if r.End.Col < len(lastRunes) {
		lastText = string(lastRunes[r.End.Col:])
	} else {
		lastText = ""
	}

	// if deleting from start of block and nothing remains, remove blocks entirely
	if firstText == "" && lastText == "" {
		// remove all blocks in range
		e.doc.Blocks = append(e.doc.Blocks[:r.Start.Block], e.doc.Blocks[r.End.Block+1:]...)
		// ensure at least one block remains
		if len(e.doc.Blocks) == 0 {
			e.doc.Blocks = []Block{{Type: BlockParagraph, Runs: []Run{{Text: ""}}}}
		}
		// position cursor
		if r.Start.Block >= len(e.doc.Blocks) {
			r.Start.Block = len(e.doc.Blocks) - 1
		}
		e.moveCursor(Pos{Block: r.Start.Block, Col: 0})
		e.InvalidateCache()
		return ""
	}

	// merge into first block
	firstBlock.Runs = []Run{{Text: firstText + lastText, Style: StyleNone}}

	// remove blocks in between
	e.doc.Blocks = append(e.doc.Blocks[:r.Start.Block+1], e.doc.Blocks[r.End.Block+1:]...)

	e.moveCursor(r.Start)
	e.InvalidateCache()
	return "" // TODO: collect deleted text
}

// Change deletes text in range and enters insert mode
func (e *Editor) Change(r Range) {
	e.Delete(r)
	e.mode = ModeInsert
}

// Yank copies text in range to register
func (e *Editor) Yank(r Range) {
	if r.Start.Block == r.End.Block {
		b := &e.doc.Blocks[r.Start.Block]
		text := b.Text()
		start := r.Start.Col
		end := r.End.Col
		if end > len(text) {
			end = len(text)
		}
		e.yankText = text[start:end]
	}
	// TODO: multi-block yank
}

// Put inserts yanked text after cursor (p)
func (e *Editor) Put() {
	if e.yankText == "" {
		return
	}
	e.saveUndo()

	b := e.CurrentBlock()
	if b == nil {
		return
	}

	runes := []rune(b.Text())
	col := e.cursor.Col + 1 // insert after cursor
	if col > len(runes) {
		col = len(runes)
	}

	newText := string(runes[:col]) + e.yankText + string(runes[col:])

	// update the block (simplified: single run)
	if len(b.Runs) == 1 {
		b.Runs[0].Text = newText
	} else {
		b.Runs = []Run{{Text: newText, Style: StyleNone}}
	}

	// move cursor to end of pasted text
	yankRunes := []rune(e.yankText)
	e.cursor.Col = col + len(yankRunes) - 1
	e.SetCursorQuiet(e.cursor)
	e.InvalidateCache()
}

// PutBefore inserts yanked text before cursor (P)
func (e *Editor) PutBefore() {
	if e.yankText == "" {
		return
	}
	e.saveUndo()

	b := e.CurrentBlock()
	if b == nil {
		return
	}

	runes := []rune(b.Text())
	col := e.cursor.Col
	if col > len(runes) {
		col = len(runes)
	}

	newText := string(runes[:col]) + e.yankText + string(runes[col:])

	if len(b.Runs) == 1 {
		b.Runs[0].Text = newText
	} else {
		b.Runs = []Run{{Text: newText, Style: StyleNone}}
	}

	// move cursor to end of pasted text
	yankRunes := []rune(e.yankText)
	e.cursor.Col = col + len(yankRunes) - 1
	e.SetCursorQuiet(e.cursor)
	e.InvalidateCache()
}

// ApplyStyle applies a style to range (wed-specific operator)
func (e *Editor) ApplyStyle(r Range, style InlineStyle) {
	if e.rawMode {
		e.applyStyleRaw(r, style)
		return
	}
	e.saveUndo()

	for blockIdx := r.Start.Block; blockIdx <= r.End.Block && blockIdx < len(e.doc.Blocks); blockIdx++ {
		b := &e.doc.Blocks[blockIdx]
		blockLen := b.Length()

		start := 0
		end := blockLen

		if blockIdx == r.Start.Block {
			start = r.Start.Col
		}
		if blockIdx == r.End.Block {
			end = r.End.Col
			if end > blockLen {
				end = blockLen
			}
		}

		if start < end {
			b.ApplyStyle(start, end, style)
		}
	}
	e.InvalidateCache()
}

// applyStyleRaw inserts or removes markdown markers in the text (raw mode).
// in raw mode, text is source of truth so styling = editing the syntax.
func (e *Editor) applyStyleRaw(r Range, style InlineStyle) {
	prefix := rawInlinePrefix(style)
	suffix := rawInlineSuffix(style)
	if prefix == "" && suffix == "" {
		return
	}
	e.saveUndo()

	prefixRunes := []rune(prefix)
	suffixRunes := []rune(suffix)
	prefixLen := len(prefixRunes)
	suffixLen := len(suffixRunes)

	for blockIdx := r.Start.Block; blockIdx <= r.End.Block && blockIdx < len(e.doc.Blocks); blockIdx++ {
		b := &e.doc.Blocks[blockIdx]
		runes := []rune(b.Text())
		blockLen := len(runes)

		start := 0
		end := blockLen
		if blockIdx == r.Start.Block {
			start = r.Start.Col
		}
		if blockIdx == r.End.Block {
			end = r.End.Col
			if end > blockLen {
				end = blockLen
			}
		}
		if start >= end {
			continue
		}

		// toggle: check if already wrapped with these markers
		hasBefore := start >= prefixLen && string(runes[start-prefixLen:start]) == prefix
		hasAfter := end+suffixLen <= len(runes) && string(runes[end:end+suffixLen]) == suffix
		if hasBefore && hasAfter {
			// remove markers
			newText := string(runes[:start-prefixLen]) + string(runes[start:end]) + string(runes[end+suffixLen:])
			b.Runs = []Run{{Text: newText, Style: StyleNone}}
			if blockIdx == e.cursor.Block {
				e.cursor.Col -= prefixLen
				if e.cursor.Col < 0 {
					e.cursor.Col = 0
				}
			}
		} else {
			// insert markers
			newText := string(runes[:start]) + prefix + string(runes[start:end]) + suffix + string(runes[end:])
			b.Runs = []Run{{Text: newText, Style: StyleNone}}
			if blockIdx == e.cursor.Block {
				e.cursor.Col += prefixLen
			}
		}
	}

	e.SetCursorQuiet(e.cursor)
	e.InvalidateCache()
}

// ClearStyle removes all styles from range (wed-specific operator)
func (e *Editor) ClearStyle(r Range) {
	if e.rawMode {
		e.clearStyleRaw(r)
		return
	}
	e.saveUndo()

	for blockIdx := r.Start.Block; blockIdx <= r.End.Block && blockIdx < len(e.doc.Blocks); blockIdx++ {
		b := &e.doc.Blocks[blockIdx]
		blockLen := b.Length()

		start := 0
		end := blockLen

		if blockIdx == r.Start.Block {
			start = r.Start.Col
		}
		if blockIdx == r.End.Block {
			end = r.End.Col
			if end > blockLen {
				end = blockLen
			}
		}

		if start < end {
			b.ClearStyle(start, end)
		}
	}
	e.InvalidateCache()
}

// clearStyleRaw strips inline markers surrounding the selected range (raw mode).
func (e *Editor) clearStyleRaw(r Range) {
	e.saveUndo()

	for blockIdx := r.Start.Block; blockIdx <= r.End.Block && blockIdx < len(e.doc.Blocks); blockIdx++ {
		b := &e.doc.Blocks[blockIdx]
		runes := []rune(b.Text())
		blockLen := len(runes)

		start := 0
		end := blockLen
		if blockIdx == r.Start.Block {
			start = r.Start.Col
		}
		if blockIdx == r.End.Block {
			end = r.End.Col
			if end > blockLen {
				end = blockLen
			}
		}
		if start >= end {
			continue
		}

		// expand outward to consume any surrounding markers
		markers := []string{"***", "**", "*", "~~", "`"}
		for _, m := range markers {
			mr := []rune(m)
			ml := len(mr)
			if start >= ml && end+ml <= len(runes) &&
				string(runes[start-ml:start]) == m &&
				string(runes[end:end+ml]) == m {
				start -= ml
				end += ml
			}
		}

		// strip any remaining markers from the expanded selection
		selected := string(runes[start:end])
		clean := stripMarkdown(selected)

		newText := string(runes[:start]) + clean + string(runes[end:])
		b.Runs = []Run{{Text: newText, Style: StyleNone}}
	}

	e.SetCursorQuiet(e.cursor)
	e.InvalidateCache()
}

// =============================================================================
// Block Type Operations (wed-specific)
// =============================================================================

// SetBlockType changes the current block's type
func (e *Editor) SetBlockType(blockType BlockType) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	e.saveUndo()
	b.Type = blockType
}

// =============================================================================
// Insert Mode Operations
// =============================================================================

// InsertChar inserts a character at cursor position
func (e *Editor) InsertChar(ch rune) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	// no saveUndo here - entire insert session is one undo unit

	// handle dialogue character mode - insert into character name
	if b.Type == BlockDialogue && e.dialogueCharMode {
		e.debugLog(fmt.Sprintf("InsertChar:charMode ch=%q", ch))
		// backslash always cycles through characters (if any exist)
		if ch == '\\' {
			e.rebuildCharacterHistory()
			if len(e.characterHistory) > 0 {
				// if not already in suggestion mode, enter it
				if !e.dialogueSuggestionMode {
					e.dialogueSuggestionMode = true
					// start at beginning of history
					e.suggestionIndex = 0
					name := e.characterHistory[0]
					if b.Attrs == nil {
						b.Attrs = make(map[string]string)
					}
					b.Attrs["character"] = name
					e.cursor.Col = utf8.RuneCountInString(name)
					e.InvalidateCache()
				} else {
					e.cycleCharacterSuggestion()
				}
				return
			}
			// no history - fall through to insert literal backslash
		}

		// in suggestion mode, any other character clears the suggestion and starts fresh
		if e.dialogueSuggestionMode {
			b.Attrs["character"] = string(ch)
			e.cursor.Col = 1
			e.dialogueSuggestionMode = false
			e.ensureCursorVisible()
			e.InvalidateCache()
			return
		}

		// normal character insertion
		charName := b.Attrs["character"]
		runes := []rune(charName)
		col := e.cursor.Col
		if col > len(runes) {
			col = len(runes)
		}
		newName := string(runes[:col]) + string(ch) + string(runes[col:])
		b.Attrs["character"] = newName
		e.cursor.Col++
		e.ensureCursorVisible()
		e.InvalidateCache()
		return
	}

	// typing clears the awaiting-input state (allows future yields)
	e.dialogueAwaitingInput = false

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

	// check for markdown syntax upgrade after space is typed
	if ch == ' ' && (b.Type == BlockParagraph || b.Type == BlockDialogue) {
		e.tryMarkdownUpgrade()
	}

	e.ensureCursorVisible()
	e.InvalidateCache()
}

// InsertText inserts a string at cursor position, handling newlines efficiently
func (e *Editor) InsertText(text string) {
	if text == "" {
		return
	}

	// clean the pasted text: strip escape sequences and invisible characters
	text = cleanPasteText(text)

	// normalize line endings and split
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// work with runes to handle UTF-8 correctly
	currentRunes := []rune(b.Text())
	col := e.cursor.Col
	if col > len(currentRunes) {
		col = len(currentRunes)
	}

	// insert first line at cursor position
	firstLineRunes := []rune(lines[0])
	if len(firstLineRunes) > 0 {
		newRunes := make([]rune, 0, len(currentRunes)+len(firstLineRunes))
		newRunes = append(newRunes, currentRunes[:col]...)
		newRunes = append(newRunes, firstLineRunes...)
		newRunes = append(newRunes, currentRunes[col:]...)
		b.Runs = []Run{{Text: string(newRunes)}}
		col += len(firstLineRunes)
		e.cursor.Col = col
	}

	// handle remaining lines by creating new blocks
	if len(lines) > 1 {
		// split current block at cursor if there's text after
		currentRunes = []rune(b.Text()) // re-read after modification
		var remainder string
		if col < len(currentRunes) {
			remainder = string(currentRunes[col:])
			b.Runs = []Run{{Text: string(currentRunes[:col])}}
		}

		// insert new blocks for lines 1 to n-1
		insertIdx := e.cursor.Block + 1
		newBlocks := make([]Block, 0, len(lines)-1)
		// new blocks inherit type from current block (e.g. code lines stay code lines)
		newBlockType := BlockParagraph
		var newBlockAttrs map[string]string
		if b.Type == BlockCodeLine {
			newBlockType = BlockCodeLine
			if b.Attrs != nil {
				newBlockAttrs = make(map[string]string)
				for k, v := range b.Attrs {
					newBlockAttrs[k] = v
				}
			}
		}
		for i := 1; i < len(lines); i++ {
			lineText := lines[i]
			// last line gets the remainder appended
			if i == len(lines)-1 && remainder != "" {
				lineText += remainder
			}
			nb := Block{
				Type: newBlockType,
				Runs: []Run{{Text: lineText}},
			}
			if newBlockAttrs != nil {
				nb.Attrs = make(map[string]string)
				for k, v := range newBlockAttrs {
					nb.Attrs[k] = v
				}
			}
			newBlocks = append(newBlocks, nb)
		}

		// insert all new blocks at once
		e.doc.Blocks = append(e.doc.Blocks[:insertIdx],
			append(newBlocks, e.doc.Blocks[insertIdx:]...)...)

		// move cursor to end of last inserted line (before remainder)
		e.cursor.Block = insertIdx + len(newBlocks) - 1
		e.cursor.Col = len([]rune(lines[len(lines)-1]))
	}

	e.ensureCursorVisible()
	e.InvalidateCache()

	// dump resulting blocks for debugging
	if debugPaste {
		fmt.Fprintf(os.Stderr, "\n=== BLOCKS AFTER INSERT (%d total) ===\n", len(e.doc.Blocks))
		for i := 0; i < len(e.doc.Blocks); i++ {
			text := e.doc.Blocks[i].Text()
			// show first 100 chars of each block
			if len(text) > 100 {
				text = text[:100] + "..."
			}
			fmt.Fprintf(os.Stderr, "  [%d] %q\n", i, text)
		}
		fmt.Fprintf(os.Stderr, "=== END BLOCKS ===\n")
	}
}

// debugPaste controls whether to dump paste content to stderr for debugging
var debugPaste = os.Getenv("WED_DEBUG_PASTE") != ""

// debugRender controls whether to dump render state to stderr for debugging
var debugRender = os.Getenv("WED_DEBUG_RENDER") != ""

// cleanPasteText removes ANSI escape sequences and invisible characters from pasted text
func cleanPasteText(s string) string {
	if debugPaste {
		fmt.Fprintf(os.Stderr, "\n=== PASTE DEBUG (%d bytes) ===\n", len(s))
		for i, b := range []byte(s) {
			if b >= 32 && b < 127 {
				fmt.Fprintf(os.Stderr, "%c", b)
			} else if b == '\n' {
				fmt.Fprintf(os.Stderr, "\\n\n")
			} else if b == '\r' {
				fmt.Fprintf(os.Stderr, "\\r")
			} else if b == '\t' {
				fmt.Fprintf(os.Stderr, "\\t")
			} else {
				fmt.Fprintf(os.Stderr, "[%02x]", b)
			}
			if i > 0 && i%80 == 0 && b != '\n' {
				fmt.Fprintf(os.Stderr, "\n")
			}
		}
		fmt.Fprintf(os.Stderr, "\n=== END PASTE ===\n")
	}
	var result strings.Builder
	result.Grow(len(s))

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// skip ANSI escape sequences
		if r == 0x1b && i+1 < len(runes) {
			next := runes[i+1]
			// CSI sequence: ESC [
			if next == '[' {
				j := i + 2
				for j < len(runes) && !((runes[j] >= 'A' && runes[j] <= 'Z') || (runes[j] >= 'a' && runes[j] <= 'z') || runes[j] == '~') {
					j++
				}
				if j < len(runes) {
					j++ // skip terminator
				}
				i = j - 1
				continue
			}
			// OSC sequence: ESC ]
			if next == ']' {
				j := i + 2
				for j < len(runes) {
					if runes[j] == 0x07 || (runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\') {
						if runes[j] == 0x1b {
							j++
						}
						j++
						break
					}
					j++
				}
				i = j - 1
				continue
			}
			// other ESC sequences: skip ESC + one char
			i++
			continue
		}

		// keep newlines
		if r == '\n' {
			result.WriteRune(r)
			continue
		}
		// handle carriage return: convert CR to LF but skip if part of CRLF
		if r == '\r' {
			// if followed by \n, skip the \r (the \n will be handled next iteration)
			if i+1 < len(runes) && runes[i+1] == '\n' {
				continue
			}
			// standalone CR (old Mac format) - convert to LF
			result.WriteRune('\n')
			continue
		}
		// convert TABs to spaces - tabs break terminal cursor tracking
		// because they jump to the next tab stop rather than advancing by 1
		if r == '\t' {
			result.WriteString("    ") // 4 spaces per tab
			continue
		}

		// skip control characters (except the above)
		if r < 32 {
			continue
		}

		// skip zero-width and invisible Unicode characters
		if r == 0x200B || // zero-width space
			r == 0x200C || // zero-width non-joiner
			r == 0x200D || // zero-width joiner
			r == 0xFEFF || // BOM / zero-width no-break space
			r == 0x00AD || // soft hyphen
			(r >= 0x2060 && r <= 0x206F) || // various invisible formatters
			(r >= 0xFFF0 && r <= 0xFFFF) { // specials
			continue
		}

		result.WriteRune(r)
	}
	return result.String()
}

// tryMarkdownUpgrade checks if the current line starts with markdown syntax
// and upgrades it to a first-class block type
func (e *Editor) tryMarkdownUpgrade() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	text := b.Text()

	// parenthetical can be triggered from dialogue blocks (for screenplay flow)
	if b.Type == BlockDialogue && strings.HasPrefix(text, "(( ") {
		e.upgradeDialogueToParenthetical("(( ")
		return
	}

	// other upgrades only from paragraph
	if b.Type != BlockParagraph {
		return
	}

	// check for list patterns (must be at start of line)
	switch {
	case strings.HasPrefix(text, "- "):
		// bullet list with dash
		e.upgradeToList("- ", "bullet")
	case strings.HasPrefix(text, "* "):
		// bullet list with asterisk
		e.upgradeToList("* ", "bullet")
	case strings.HasPrefix(text, "+ "):
		// bullet list with plus
		e.upgradeToList("+ ", "bullet")
	case len(text) >= 3 && text[0] >= '1' && text[0] <= '9' && text[1] == '.' && text[2] == ' ':
		// numbered list (e.g., "1. ")
		e.upgradeToList(text[:3], "number")
	case strings.HasPrefix(text, "# "):
		e.upgradeToHeading("# ", BlockH1)
	case strings.HasPrefix(text, "## "):
		e.upgradeToHeading("## ", BlockH2)
	case strings.HasPrefix(text, "### "):
		e.upgradeToHeading("### ", BlockH3)
	case strings.HasPrefix(text, "#### "):
		e.upgradeToHeading("#### ", BlockH4)
	case strings.HasPrefix(text, "##### "):
		e.upgradeToHeading("##### ", BlockH5)
	case strings.HasPrefix(text, "###### "):
		e.upgradeToHeading("###### ", BlockH6)
	case strings.HasPrefix(text, "> "):
		e.upgradeToQuote("> ")
	case strings.HasPrefix(text, "@@ "):
		e.upgradeToDialogue("@@ ")
	// note: (( is only allowed from dialogue blocks, not paragraphs (handled above)
	// scene headings
	case strings.HasPrefix(strings.ToUpper(text), "INT. "):
		e.upgradeToSceneHeading()
	case strings.HasPrefix(strings.ToUpper(text), "EXT. "):
		e.upgradeToSceneHeading()
	case strings.HasPrefix(strings.ToUpper(text), "I/E. "):
		e.upgradeToSceneHeading()
	case strings.HasPrefix(strings.ToUpper(text), "INT./EXT. "):
		e.upgradeToSceneHeading()
	}
}

// upgradeToList converts the current block to a list item, removing the marker
func (e *Editor) upgradeToList(marker string, listType string) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// remove the marker from the text
	text := b.Text()
	newText := strings.TrimPrefix(text, marker)

	// update the block
	b.Type = BlockListItem
	b.Runs = []Run{{Text: newText, Style: StyleNone}}
	if b.Attrs == nil {
		b.Attrs = make(map[string]string)
	}
	b.Attrs["marker"] = listType

	// adjust cursor position
	markerLen := utf8.RuneCountInString(marker)
	e.cursor.Col -= markerLen
	if e.cursor.Col < 0 {
		e.cursor.Col = 0
	}
}

// upgradeToHeading converts the current block to a heading, removing the marker
func (e *Editor) upgradeToHeading(marker string, headingType BlockType) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// remove the marker from the text
	text := b.Text()
	newText := strings.TrimPrefix(text, marker)

	// update the block
	b.Type = headingType
	b.Runs = []Run{{Text: newText, Style: StyleNone}}

	// adjust cursor position
	markerLen := utf8.RuneCountInString(marker)
	e.cursor.Col -= markerLen
	if e.cursor.Col < 0 {
		e.cursor.Col = 0
	}
}

// upgradeToQuote converts the current block to a quote, removing the marker
func (e *Editor) upgradeToQuote(marker string) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// remove the marker from the text
	text := b.Text()
	newText := strings.TrimPrefix(text, marker)

	// update the block
	b.Type = BlockQuote
	b.Runs = []Run{{Text: newText, Style: StyleNone}}

	// adjust cursor position
	markerLen := utf8.RuneCountInString(marker)
	e.cursor.Col -= markerLen
	if e.cursor.Col < 0 {
		e.cursor.Col = 0
	}
}

// upgradeToDialogue converts the current block to a dialogue block
func (e *Editor) upgradeToDialogue(marker string) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// get character name (text after marker)
	text := b.Text()
	character := strings.TrimPrefix(text, marker)

	// update the block
	b.Type = BlockDialogue
	b.Runs = []Run{{Text: "", Style: StyleNone}} // empty dialogue initially
	if b.Attrs == nil {
		b.Attrs = make(map[string]string)
	}
	b.Attrs["character"] = character

	// enter character editing mode
	e.dialogueCharMode = true

	// cursor is now at end of character name
	e.cursor.Col = utf8.RuneCountInString(character)

	e.InvalidateCache()
}

// upgradeToParenthetical converts the current block to a parenthetical (stage direction)
func (e *Editor) upgradeToParenthetical(marker string) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// get the parenthetical text (after marker, strip leading ()
	text := b.Text()
	parenText := strings.TrimPrefix(text, marker)

	// update the block
	b.Type = BlockParenthetical
	b.Runs = []Run{{Text: parenText, Style: StyleNone}}

	// cursor at end of text
	e.cursor.Col = utf8.RuneCountInString(parenText)

	e.InvalidateCache()
}

// upgradeDialogueToParenthetical converts dialogue block to parenthetical (within screenplay flow)
func (e *Editor) upgradeDialogueToParenthetical(marker string) {
	b := e.CurrentBlock()
	if b == nil || b.Type != BlockDialogue {
		return
	}

	// get the parenthetical text (after marker)
	text := b.Text()
	parenText := strings.TrimPrefix(text, marker)

	// convert the block
	b.Type = BlockParenthetical
	b.Runs = []Run{{Text: parenText, Style: StyleNone}}
	b.Attrs = nil // parentheticals don't have character attrs

	// exit dialogue mode
	e.dialogueCharMode = false
	e.dialogueSuggestionMode = false

	// cursor at end of text
	e.cursor.Col = utf8.RuneCountInString(parenText)

	e.InvalidateCache()
}

// upgradeToSceneHeading converts the current paragraph to a scene heading
func (e *Editor) upgradeToSceneHeading() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	// keep the text as-is (will be uppercased during render)
	text := b.Text()

	// update the block
	b.Type = BlockSceneHeading
	b.Runs = []Run{{Text: text, Style: StyleNone}}

	// cursor stays at current position
	e.InvalidateCache()
}

// ToggleDialogueMode switches between editing character name and dialogue text
func (e *Editor) ToggleDialogueMode() {
	b := e.CurrentBlock()
	if b == nil || b.Type != BlockDialogue {
		return
	}

	if e.dialogueCharMode {
		// switch to dialogue mode - cursor goes to start of dialogue
		e.trimCharacterName()
		e.dialogueCharMode = false
		e.cursor.Col = 0
	} else {
		// switch to character mode - cursor goes to end of character name
		e.dialogueCharMode = true
		character := ""
		if b.Attrs != nil {
			character = b.Attrs["character"]
		}
		e.cursor.Col = utf8.RuneCountInString(character)
	}
	e.InvalidateCache()
}

// SyncDialogueMode ensures dialogueCharMode is correct for current block
func (e *Editor) SyncDialogueMode() {
	b := e.CurrentBlock()
	if b == nil || b.Type != BlockDialogue {
		e.dialogueCharMode = false
		e.dialogueSuggestionMode = false
		return
	}
	// when navigating to a dialogue block, default to dialogue mode (not character mode)
	// unless the character name is empty
	if b.Attrs == nil || b.Attrs["character"] == "" {
		e.dialogueCharMode = true
	} else {
		e.dialogueCharMode = false // default to dialogue mode when character exists
	}
	e.dialogueSuggestionMode = false
	e.debugLog("SyncDialogueMode")
}

// ExitDialogueIfEmpty converts an empty dialogue block to paragraph (double-Esc to exit dialogue)
func (e *Editor) ExitDialogueIfEmpty() {
	b := e.CurrentBlock()
	if b == nil || b.Type != BlockDialogue {
		return
	}
	// only exit if dialogue content is empty
	if b.Length() == 0 {
		b.Type = BlockParagraph
		b.Attrs = nil
		e.dialogueCharMode = false
		e.dialogueSuggestionMode = false
		e.dialogueAwaitingInput = false
		e.cursor.Col = 0
		e.debugLog("ExitDialogueIfEmpty:converted")
		e.InvalidateCache()
	}
}

// trimCharacterName trims whitespace from the current block's character name
func (e *Editor) trimCharacterName() {
	b := e.CurrentBlock()
	if b == nil || b.Type != BlockDialogue || b.Attrs == nil {
		return
	}
	if name, ok := b.Attrs["character"]; ok {
		b.Attrs["character"] = strings.TrimSpace(name)
	}
}

// trackCharacterName adds a character name to history if not already present
func (e *Editor) trackCharacterName(name string) {
	if name == "" {
		return
	}
	// check if already in history
	for _, existing := range e.characterHistory {
		if existing == name {
			return
		}
	}
	e.characterHistory = append(e.characterHistory, name)
}

// rebuildCharacterHistory scans document for all character names
func (e *Editor) rebuildCharacterHistory() {
	// track last appearance (block index) for each character
	lastAppearance := make(map[string]int)
	for i, block := range e.doc.Blocks {
		if block.Type == BlockDialogue && block.Attrs != nil && block.Attrs["character"] != "" {
			lastAppearance[block.Attrs["character"]] = i
		}
	}

	// build list sorted by recency (most recent first)
	e.characterHistory = nil
	for name := range lastAppearance {
		e.characterHistory = append(e.characterHistory, name)
	}
	sort.Slice(e.characterHistory, func(i, j int) bool {
		return lastAppearance[e.characterHistory[i]] > lastAppearance[e.characterHistory[j]]
	})
}

// getBlockSpeaker returns the speaker/character for a block index
// for dialogue blocks, it's the character attr
// for parentheticals, it looks back to find the preceding dialogue's character
func (e *Editor) getBlockSpeaker(blockIdx int) string {
	if blockIdx < 0 || blockIdx >= len(e.doc.Blocks) {
		return ""
	}
	block := &e.doc.Blocks[blockIdx]

	if block.Type == BlockDialogue {
		if block.Attrs != nil {
			return block.Attrs["character"]
		}
		return ""
	}

	if block.Type == BlockParenthetical {
		// look back to find the preceding dialogue
		for i := blockIdx - 1; i >= 0; i-- {
			if e.doc.Blocks[i].Type == BlockDialogue {
				if e.doc.Blocks[i].Attrs != nil {
					return e.doc.Blocks[i].Attrs["character"]
				}
				return ""
			}
		}
	}

	return ""
}

// getSuggestedCharacter returns the character to pre-fill (the previous different speaker)
func (e *Editor) getSuggestedCharacter() string {
	// rebuild history fresh to catch any edits (fixed typos, etc.)
	e.rebuildCharacterHistory()

	if len(e.characterHistory) < 2 {
		return ""
	}

	// find who just spoke and who spoke before them (the actual previous speaker)
	currentChar := ""
	for i := e.cursor.Block; i >= 0; i-- {
		b := &e.doc.Blocks[i]
		if b.Type == BlockDialogue && b.Attrs != nil && b.Attrs["character"] != "" {
			if currentChar == "" {
				currentChar = b.Attrs["character"]
			} else if b.Attrs["character"] != currentChar {
				// found a different speaker - this is the previous speaker
				return b.Attrs["character"]
			}
		}
	}

	// fallback: return first character in history that isn't current
	for _, name := range e.characterHistory {
		if name != currentChar {
			return name
		}
	}
	return ""
}

// cycleCharacterSuggestion moves to next/prev character in history
func (e *Editor) cycleCharacterSuggestion() {
	if len(e.characterHistory) == 0 {
		return
	}
	e.suggestionIndex = (e.suggestionIndex + 1) % len(e.characterHistory)
	name := e.characterHistory[e.suggestionIndex]

	b := e.CurrentBlock()
	if b != nil && b.Type == BlockDialogue {
		if b.Attrs == nil {
			b.Attrs = make(map[string]string)
		}
		b.Attrs["character"] = name
		e.cursor.Col = utf8.RuneCountInString(name)
		e.InvalidateCache()
	}
}

// Backspace deletes character before cursor
func (e *Editor) Backspace() {
	b := e.CurrentBlock()

	// handle dialogue character mode - delete from character name
	if b != nil && b.Type == BlockDialogue && e.dialogueCharMode {
		// in suggestion mode, backspace clears the entire suggestion
		if e.dialogueSuggestionMode {
			b.Attrs["character"] = ""
			e.cursor.Col = 0
			e.dialogueSuggestionMode = false
			e.ensureCursorVisible()
			e.InvalidateCache()
			return
		}

		charName := b.Attrs["character"]
		if e.cursor.Col > 0 {
			runes := []rune(charName)
			col := e.cursor.Col
			if col > len(runes) {
				col = len(runes)
			}
			if col > 0 {
				newName := string(runes[:col-1]) + string(runes[col:])
				b.Attrs["character"] = newName
				e.cursor.Col--
				e.ensureCursorVisible()
				e.InvalidateCache()
			}
		}
		return
	}

	if e.cursor.Col == 0 {
		// don't merge across block type boundaries (code line into paragraph, etc.)
		if e.cursor.Block > 0 && b != nil && b.Type == BlockCodeLine {
			prev := &e.doc.Blocks[e.cursor.Block-1]
			if prev.Type != BlockCodeLine {
				return
			}
		}
		// merge with previous block
		if e.cursor.Block > 0 {
			// no saveUndo here - entire insert session is one undo unit
			prevBlock := &e.doc.Blocks[e.cursor.Block-1]
			currBlock := e.CurrentBlock()
			prevLen := prevBlock.Length()

			prevBlock.Runs = append(prevBlock.Runs, currBlock.Runs...)
			prevBlock.MergeAdjacentRuns()

			e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block], e.doc.Blocks[e.cursor.Block+1:]...)
			e.cursor.Block--
			e.cursor.Col = prevLen
			e.ensureCursorVisible()
			e.InvalidateCache()
		}
		return
	}

	if b == nil {
		return
	}
	// no saveUndo here - entire insert session is one undo unit

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
}

// DeleteChar deletes character at cursor (x)
func (e *Editor) DeleteChar() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}

	if e.cursor.Col >= b.Length() {
		// merge with next block
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
	e.SetCursorQuiet(e.cursor) // clamp if needed
}

// NewLine creates a new block, splitting current block if needed
func (e *Editor) NewLine() {
	// no saveUndo here - entire insert session is one undo unit
	b := e.CurrentBlock()

	// handle list item continuation
	if b != nil && b.Type == BlockListItem {
		// if list item is empty, convert to paragraph (end the list)
		if b.Length() == 0 {
			b.Type = BlockParagraph
			b.Attrs = nil
			e.InvalidateCache()
			return
		}

		// continue the list with a new item
		var newRuns []Run
		if e.cursor.Col < b.Length() {
			b.SplitRunAt(e.cursor.Col)
			runIdx, _ := b.RunAt(e.cursor.Col)

			newRuns = make([]Run, len(b.Runs)-runIdx)
			for i, r := range b.Runs[runIdx:] {
				newRuns[i] = Run{Text: r.Text, Style: r.Style}
			}
			b.Runs = b.Runs[:runIdx]

			if len(b.Runs) == 0 {
				b.Runs = []Run{{Text: ""}}
			}
		}

		if len(newRuns) == 0 {
			newRuns = []Run{{Text: ""}}
		}

		// create new list item with same marker type
		newBlock := Block{
			Type: BlockListItem,
			Runs: newRuns,
		}
		if b.Attrs != nil {
			newBlock.Attrs = make(map[string]string)
			for k, v := range b.Attrs {
				newBlock.Attrs[k] = v
			}
			// increment number for numbered lists
			if b.Attrs["marker"] == "number" {
				if num, ok := b.Attrs["number"]; ok {
					if n, err := fmt.Sscanf(num, "%d", new(int)); err == nil && n > 0 {
						var parsed int
						fmt.Sscanf(num, "%d", &parsed)
						newBlock.Attrs["number"] = fmt.Sprintf("%d", parsed+1)
					}
				}
			}
		}

		e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
			append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
		e.cursor.Block++
		e.cursor.Col = 0
		e.ensureCursorVisible()
		e.InvalidateCache()
		return
	}

	// handle dialogue block
	if b != nil && b.Type == BlockDialogue {
		e.debugLog("NewLine:enter")
		if e.dialogueCharMode {
			// character mode → switch to dialogue mode
			e.trimCharacterName()
			// track the character name for autocomplete
			if b.Attrs != nil && b.Attrs["character"] != "" {
				e.trackCharacterName(b.Attrs["character"])
			}
			e.dialogueCharMode = false
			e.dialogueSuggestionMode = false
			e.dialogueAwaitingInput = true // prevent immediate yield, wait for typing
			e.cursor.Col = 0
			e.debugLog("NewLine:charMode→dialogueMode")
			e.InvalidateCache()
			return
		}

		// dialogue mode
		character := b.Attrs["character"]
		dialogueEmpty := b.Length() == 0

		if dialogueEmpty && character == "" {
			// empty dialogue + empty character → exit to paragraph
			b.Type = BlockParagraph
			b.Attrs = nil
			e.dialogueAwaitingInput = false
			e.debugLog("NewLine:exitToParagraph")
			e.InvalidateCache()
			return
		}

		if dialogueEmpty && e.dialogueAwaitingInput {
			// just confirmed character, waiting for input - don't yield yet
			e.dialogueAwaitingInput = false
			e.debugLog("NewLine:gracePeriod")
			return
		}

		if dialogueEmpty {
			// empty dialogue but has character → new character (yield to next speaker)
			// track the current character before moving on
			e.trackCharacterName(character)

			// get suggested character (the "other" speaker)
			suggested := e.getSuggestedCharacter()

			var newRuns []Run
			newRuns = []Run{{Text: ""}}

			newBlock := Block{
				Type:  BlockDialogue,
				Runs:  newRuns,
				Attrs: map[string]string{"character": suggested},
			}

			e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
				append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
			e.cursor.Block++
			e.cursor.Col = utf8.RuneCountInString(suggested)
			e.dialogueCharMode = true // enter character mode for new speaker

			// enter suggestion mode if we have a suggestion
			if suggested != "" {
				e.dialogueSuggestionMode = true
				// set suggestion index to match the suggested character
				for i, name := range e.characterHistory {
					if name == suggested {
						e.suggestionIndex = i
						break
					}
				}
			}

			e.debugLog("NewLine:yield→" + suggested)
			e.ensureCursorVisible()
			e.InvalidateCache()
			return
		}

		e.debugLog("NewLine:continuation")
		// dialogue has content → continue same character with new paragraph
		var newRuns []Run
		if e.cursor.Col < b.Length() {
			b.SplitRunAt(e.cursor.Col)
			runIdx, _ := b.RunAt(e.cursor.Col)

			newRuns = make([]Run, len(b.Runs)-runIdx)
			for i, r := range b.Runs[runIdx:] {
				newRuns[i] = Run{Text: r.Text, Style: r.Style}
			}
			b.Runs = b.Runs[:runIdx]

			if len(b.Runs) == 0 {
				b.Runs = []Run{{Text: ""}}
			}
		}

		if len(newRuns) == 0 {
			newRuns = []Run{{Text: ""}}
		}

		newBlock := Block{
			Type:  BlockDialogue,
			Runs:  newRuns,
			Attrs: map[string]string{"character": character}, // same character
		}

		e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
			append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
		e.cursor.Block++
		e.cursor.Col = 0
		e.dialogueCharMode = false // stay in dialogue mode for continuation
		e.ensureCursorVisible()
		e.InvalidateCache()
		return
	}

	// handle parenthetical block - continue dialogue for same character
	if b != nil && b.Type == BlockParenthetical {
		// find the previous dialogue block to get the character
		character := ""
		for i := e.cursor.Block - 1; i >= 0; i-- {
			if e.doc.Blocks[i].Type == BlockDialogue {
				if e.doc.Blocks[i].Attrs != nil {
					character = e.doc.Blocks[i].Attrs["character"]
				}
				break
			}
		}

		// create new dialogue block for same character
		newBlock := Block{
			Type:  BlockDialogue,
			Runs:  []Run{{Text: ""}},
			Attrs: map[string]string{"character": character},
		}

		e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
			append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
		e.cursor.Block++
		e.cursor.Col = 0
		e.dialogueCharMode = false // in dialogue mode, ready to type
		e.SyncDialogueMode()
		e.ensureCursorVisible()
		e.InvalidateCache()
		return
	}

	// default behavior for other block types
	var newRuns []Run
	if b != nil && e.cursor.Col < b.Length() {
		b.SplitRunAt(e.cursor.Col)
		runIdx, _ := b.RunAt(e.cursor.Col)

		newRuns = make([]Run, len(b.Runs)-runIdx)
		for i, r := range b.Runs[runIdx:] {
			newRuns[i] = Run{Text: r.Text, Style: r.Style}
		}
		b.Runs = b.Runs[:runIdx]

		if len(b.Runs) == 0 {
			b.Runs = []Run{{Text: ""}}
		}
	}

	if len(newRuns) == 0 {
		newRuns = []Run{{Text: ""}}
	}

	newBlock := Block{
		Type: BlockParagraph,
		Runs: newRuns,
	}

	// code lines: new block inherits type, lang, and indentation
	if b != nil && b.Type == BlockCodeLine {
		newBlock.Type = BlockCodeLine
		if b.Attrs != nil {
			newBlock.Attrs = make(map[string]string)
			for k, v := range b.Attrs {
				newBlock.Attrs[k] = v
			}
		}
		indent := leadingWhitespace(b.Text())
		if indent != "" {
			newText := indent + newBlock.Text()
			newBlock.Runs = []Run{{Text: newText}}
		}
	}

	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
		append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
	e.cursor.Block++
	e.cursor.Col = len([]rune(leadingWhitespace(e.CurrentBlock().Text())))
	e.ensureCursorVisible()
	e.InvalidateCache()
}

// YieldToNextSpeaker immediately yields to the next speaker and enters dialogue mode
// Skips straight to typing the next character's dialogue (Ctrl+N shortcut)
func (e *Editor) YieldToNextSpeaker() {
	b := e.CurrentBlock()
	if b == nil || b.Type != BlockDialogue {
		// not in dialogue, fall back to regular newline
		e.NewLine()
		return
	}

	// track current character before yielding
	if e.dialogueCharMode {
		if b.Attrs != nil && b.Attrs["character"] != "" {
			e.trackCharacterName(b.Attrs["character"])
		}
	} else {
		character := ""
		if b.Attrs != nil {
			character = b.Attrs["character"]
		}
		if character != "" {
			e.trackCharacterName(character)
		}
	}

	// get the suggested next speaker
	suggested := e.getSuggestedCharacter()
	e.debugLog("Yield:suggested=" + suggested)

	// if no suggestion, stay in character mode so user can type a name
	if suggested == "" {
		newBlock := Block{
			Type:  BlockDialogue,
			Runs:  []Run{{Text: ""}},
			Attrs: map[string]string{"character": ""},
		}
		e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
			append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
		e.cursor.Block++
		e.cursor.Col = 0
		e.dialogueCharMode = true
		e.dialogueSuggestionMode = false
		e.dialogueAwaitingInput = false
		e.debugLog("Yield:noSuggestion→charMode")
		e.ensureCursorVisible()
		e.InvalidateCache()
		return
	}

	// track the suggested character (we're confirming it)
	e.trackCharacterName(suggested)

	// create new dialogue block for next speaker, ready to type dialogue
	newBlock := Block{
		Type:  BlockDialogue,
		Runs:  []Run{{Text: ""}},
		Attrs: map[string]string{"character": suggested},
	}

	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
		append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
	e.cursor.Block++
	e.cursor.Col = 0                 // cursor at start of dialogue
	e.dialogueCharMode = false       // dialogue mode, ready to type
	e.dialogueSuggestionMode = false // character is confirmed
	e.dialogueAwaitingInput = false

	e.debugLog("Yield:done→dialogueMode")
	e.ensureCursorVisible()
	e.InvalidateCache()
}

// ReplaceChar replaces the character at cursor position (r)
func (e *Editor) ReplaceChar(ch rune) {
	b := e.CurrentBlock()
	if b == nil || e.cursor.Col >= b.Length() {
		return
	}
	e.saveUndo()

	runIdx, runOff := b.RunAt(e.cursor.Col)
	if runIdx < len(b.Runs) {
		run := &b.Runs[runIdx]
		runRunes := []rune(run.Text)
		if runOff < len(runRunes) {
			run.Text = string(runRunes[:runOff]) + string(ch) + string(runRunes[runOff+1:])
		}
	}
}

// insertBlockAbove inserts a new empty block above current
// continues list items if inside a list
func (e *Editor) insertBlockAbove() {
	b := e.CurrentBlock()
	newBlock := Block{
		Type: BlockParagraph,
		Runs: []Run{{Text: ""}},
	}

	// continue list if we're in a list item
	if b != nil && b.Type == BlockListItem {
		newBlock.Type = BlockListItem
		if b.Attrs != nil {
			newBlock.Attrs = make(map[string]string)
			for k, v := range b.Attrs {
				newBlock.Attrs[k] = v
			}
		}
	}

	// continue dialogue for same character
	if b != nil && b.Type == BlockDialogue {
		newBlock.Type = BlockDialogue
		newBlock.Attrs = make(map[string]string)
		if b.Attrs != nil {
			newBlock.Attrs["character"] = b.Attrs["character"]
		}
		e.dialogueCharMode = false // dialogue mode, ready to type
		e.dialogueSuggestionMode = false
		e.dialogueAwaitingInput = false
	}

	// continue code line with same lang and indentation from previous line
	if b != nil && b.Type == BlockCodeLine {
		newBlock.Type = BlockCodeLine
		if b.Attrs != nil {
			newBlock.Attrs = make(map[string]string)
			for k, v := range b.Attrs {
				newBlock.Attrs[k] = v
			}
		}
		// use previous code line's indent if available, else current
		prevIdx := e.cursor.Block - 1
		indentSource := b.Text()
		if prevIdx >= 0 && e.doc.Blocks[prevIdx].Type == BlockCodeLine {
			indentSource = e.doc.Blocks[prevIdx].Text()
		}
		indent := leadingWhitespace(indentSource)
		if indent != "" {
			newBlock.Runs = []Run{{Text: indent}}
		}
	}

	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block],
		append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block:]...)...)
	e.cursor.Col = len([]rune(leadingWhitespace(e.doc.Blocks[e.cursor.Block].Text())))
}

// insertBlockBelow inserts a new empty block below current
// continues list items and dialogue if inside those block types
func (e *Editor) insertBlockBelow() {
	b := e.CurrentBlock()
	newBlock := Block{
		Type: BlockParagraph,
		Runs: []Run{{Text: ""}},
	}

	// continue list if we're in a list item
	if b != nil && b.Type == BlockListItem {
		newBlock.Type = BlockListItem
		if b.Attrs != nil {
			newBlock.Attrs = make(map[string]string)
			for k, v := range b.Attrs {
				newBlock.Attrs[k] = v
			}
			// increment number for numbered lists
			if b.Attrs["marker"] == "number" {
				if num, ok := b.Attrs["number"]; ok {
					var parsed int
					if _, err := fmt.Sscanf(num, "%d", &parsed); err == nil {
						newBlock.Attrs["number"] = fmt.Sprintf("%d", parsed+1)
					}
				}
			}
		}
	}

	// continue dialogue for same character
	if b != nil && b.Type == BlockDialogue {
		newBlock.Type = BlockDialogue
		newBlock.Attrs = make(map[string]string)
		if b.Attrs != nil {
			newBlock.Attrs["character"] = b.Attrs["character"]
		}
		e.dialogueCharMode = false // dialogue mode, ready to type
		e.dialogueSuggestionMode = false
		e.dialogueAwaitingInput = false
	}

	// continue code line with same lang and indentation from next line
	if b != nil && b.Type == BlockCodeLine {
		newBlock.Type = BlockCodeLine
		if b.Attrs != nil {
			newBlock.Attrs = make(map[string]string)
			for k, v := range b.Attrs {
				newBlock.Attrs[k] = v
			}
		}
		// use next code line's indent if available, else current
		nextIdx := e.cursor.Block + 1
		indentSource := b.Text()
		if nextIdx < len(e.doc.Blocks) && e.doc.Blocks[nextIdx].Type == BlockCodeLine {
			indentSource = e.doc.Blocks[nextIdx].Text()
		}
		indent := leadingWhitespace(indentSource)
		if indent != "" {
			newBlock.Runs = []Run{{Text: indent}}
		}
	}

	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
		append([]Block{newBlock}, e.doc.Blocks[e.cursor.Block+1:]...)...)
	e.cursor.Block++
	e.cursor.Col = len([]rune(leadingWhitespace(e.doc.Blocks[e.cursor.Block].Text())))
}

// InsertDivider inserts a divider block below current and moves cursor past it
func (e *Editor) InsertDivider() {
	e.saveUndo()
	divider := Block{
		Type: BlockDivider,
		Runs: []Run{{Text: ""}},
	}
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1],
		append([]Block{divider}, e.doc.Blocks[e.cursor.Block+1:]...)...)
	e.cursor.Block++
	e.cursor.Col = 0
	e.ensureCursorVisible()
}

// JoinLines joins the current block with the next block (J)
func (e *Editor) JoinLines() {
	if e.cursor.Block >= len(e.doc.Blocks)-1 {
		return // nothing to join with
	}
	e.saveUndo()

	currBlock := &e.doc.Blocks[e.cursor.Block]
	nextBlock := &e.doc.Blocks[e.cursor.Block+1]

	// remember where to put cursor (at join point)
	joinCol := currBlock.Length()

	// add space if current block doesn't end with one and next doesn't start with one
	currText := currBlock.Text()
	nextText := nextBlock.Text()
	needsSpace := len(currText) > 0 && currText[len(currText)-1] != ' ' &&
		len(nextText) > 0 && nextText[0] != ' '

	if needsSpace {
		currBlock.Runs = append(currBlock.Runs, Run{Text: " ", Style: StyleNone})
		joinCol++
	}

	// append next block's runs
	currBlock.Runs = append(currBlock.Runs, nextBlock.Runs...)
	currBlock.MergeAdjacentRuns()

	// remove the next block
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block+1], e.doc.Blocks[e.cursor.Block+2:]...)

	// position cursor at join point
	e.cursor.Col = joinCol
	e.SetCursorQuiet(e.cursor)
}

// DeleteLine deletes the current block (dd)
func (e *Editor) DeleteLine() {
	if len(e.doc.Blocks) <= 1 {
		e.saveUndo()
		e.doc.Blocks[0] = Block{Type: BlockParagraph, Runs: []Run{{Text: ""}}}
		e.cursor.Col = 0
		return
	}
	e.saveUndo()
	e.doc.Blocks = append(e.doc.Blocks[:e.cursor.Block], e.doc.Blocks[e.cursor.Block+1:]...)
	e.SetCursorQuiet(e.cursor)
}

// =============================================================================
// Undo/Redo
// =============================================================================

func (e *Editor) saveUndo() {
	blocks := make([]Block, len(e.doc.Blocks))
	for i, b := range e.doc.Blocks {
		blocks[i] = Block{
			Type:     b.Type,
			Runs:     append([]Run{}, b.Runs...),
			Children: append([]Block{}, b.Children...),
			Attrs:    copyAttrs(b.Attrs),
		}
	}
	e.undoStack = append(e.undoStack, EditorSnapshot{
		Blocks: blocks,
		Cursor: e.cursor,
	})
	e.redoStack = nil
	e.dirty = true
	e.InvalidateCache()
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

// Undo reverts to previous state
func (e *Editor) Undo() {
	if len(e.undoStack) == 0 {
		return
	}

	// save current to redo
	blocks := make([]Block, len(e.doc.Blocks))
	for i, b := range e.doc.Blocks {
		blocks[i] = Block{
			Type:     b.Type,
			Runs:     append([]Run{}, b.Runs...),
			Children: append([]Block{}, b.Children...),
			Attrs:    copyAttrs(b.Attrs),
		}
	}
	e.redoStack = append(e.redoStack, EditorSnapshot{
		Blocks: blocks,
		Cursor: e.cursor,
	})

	// restore from undo
	snap := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.doc.Blocks = snap.Blocks
	e.cursor = snap.Cursor
	e.SetCursorQuiet(e.cursor)
	e.InvalidateCache()
}

// Redo reapplies undone changes
func (e *Editor) Redo() {
	if len(e.redoStack) == 0 {
		return
	}

	// save current to undo
	blocks := make([]Block, len(e.doc.Blocks))
	for i, b := range e.doc.Blocks {
		blocks[i] = Block{
			Type:     b.Type,
			Runs:     append([]Run{}, b.Runs...),
			Children: append([]Block{}, b.Children...),
			Attrs:    copyAttrs(b.Attrs),
		}
	}
	e.undoStack = append(e.undoStack, EditorSnapshot{
		Blocks: blocks,
		Cursor: e.cursor,
	})

	// restore from redo
	snap := e.redoStack[len(e.redoStack)-1]
	e.redoStack = e.redoStack[:len(e.redoStack)-1]
	e.doc.Blocks = snap.Blocks
	e.cursor = snap.Cursor
	e.SetCursorQuiet(e.cursor)
	e.InvalidateCache()
}

// =============================================================================
// Style Yank/Paste
// =============================================================================

// YankStyle yanks the style at cursor position
func (e *Editor) YankStyle() {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	runIdx, _ := b.RunAt(e.cursor.Col)
	if runIdx < len(b.Runs) {
		e.yankStyle = b.Runs[runIdx].Style
	}
}

// PasteStyle applies the yanked style to range
func (e *Editor) PasteStyle(r Range) {
	e.saveUndo()

	for blockIdx := r.Start.Block; blockIdx <= r.End.Block && blockIdx < len(e.doc.Blocks); blockIdx++ {
		b := &e.doc.Blocks[blockIdx]

		start := 0
		end := b.Length()

		if blockIdx == r.Start.Block {
			start = r.Start.Col
		}
		if blockIdx == r.End.Block {
			end = r.End.Col
		}

		b.SplitRunAt(start)
		b.SplitRunAt(end)

		pos := 0
		for i := range b.Runs {
			runEnd := pos + len(b.Runs[i].Text)
			if pos >= start && runEnd <= end {
				b.Runs[i].Style = e.yankStyle
			}
			pos = runEnd
		}
		b.MergeAdjacentRuns()
	}
}

// =============================================================================
// File Operations
// =============================================================================

// ToggleTheme cycles through available themes
func (e *Editor) ToggleTheme() {
	switch e.theme.Name {
	case "default":
		e.theme = DarkTheme()
	case "dark":
		e.theme = MonographTheme()
		e.ApplyBundle("monograph")
	case "monograph":
		e.theme = DefaultTheme()
		e.ApplyBundle("minimal") // reset to clean defaults
	default:
		e.theme = DefaultTheme()
	}
	e.InvalidateCache()
}

// =============================================================================
// Typewriter & Focus Modes
// =============================================================================

// ToggleTypewriterMode toggles typewriter mode on/off
func (e *Editor) ToggleTypewriterMode() {
	e.typewriterMode = !e.typewriterMode
	if e.typewriterMode {
		e.ensureCursorVisible()
	} else {
		// reset topLine so content scrolls back to natural position
		cursorLine := e.cursorScreenLine()
		e.topLine = cursorLine - 3
		if e.topLine < 0 {
			e.topLine = 0
		}
		e.ensureCursorVisible()
	}
}

// TypewriterModeEnabled returns whether typewriter mode is active
func (e *Editor) TypewriterModeEnabled() bool {
	return e.typewriterMode
}

// ToggleFocusMode toggles focus mode on/off
func (e *Editor) ToggleFocusMode() {
	e.focusMode = !e.focusMode
	e.InvalidateCache()
}

// FocusModeEnabled returns whether focus mode is active
func (e *Editor) FocusModeEnabled() bool {
	return e.focusMode
}

// focusModeActive returns whether focus dimming should be applied
// only active when focus mode is on AND in insert mode (for performance)
func (e *Editor) focusModeActive() bool {
	return e.focusMode && e.mode == ModeInsert
}

// CycleFocusScope cycles through focus scopes (line -> sentence -> paragraph)
func (e *Editor) CycleFocusScope() {
	e.focusScope = e.focusScope.Next()
	// persist to config
	e.config.FocusScope = e.focusScope.String()
	e.config.Save()
}

// FocusScope returns the current focus scope
func (e *Editor) GetFocusScope() FocusScope {
	return e.focusScope
}

// SetFocusScope sets the focus scope
func (e *Editor) SetFocusScope(scope FocusScope) {
	e.focusScope = scope
	// persist to config
	e.config.FocusScope = e.focusScope.String()
	e.config.Save()
}

// ToggleZenMode toggles zen mode (typewriter + focus combined)
func (e *Editor) ToggleZenMode() {
	e.zenMode = !e.zenMode
	if e.zenMode {
		// enable both modes
		e.typewriterMode = true
		e.focusMode = true
		e.ensureCursorVisible() // snap to center
	} else {
		// disable both modes and reset scroll position
		e.typewriterMode = false
		e.focusMode = false
		cursorLine := e.cursorScreenLine()
		e.topLine = cursorLine - 3
		if e.topLine < 0 {
			e.topLine = 0
		}
		e.ensureCursorVisible()
	}
	e.InvalidateCache()
}

// ZenModeEnabled returns whether zen mode is active
func (e *Editor) ZenModeEnabled() bool {
	return e.zenMode
}

// ToggleRawMode toggles raw markdown display mode, transforming the document text
func (e *Editor) ToggleRawMode() {
	if e.rawMode {
		e.compressDocFromRawMode()
		e.rawMode = false
	} else {
		e.rawMode = true
		e.expandDocForRawMode()
	}
	e.InvalidateCache()
}

// RawModeEnabled returns whether raw mode is active
func (e *Editor) RawModeEnabled() bool {
	return e.rawMode
}

// expandDocForRawMode converts each block to its full markdown text representation
// as a single unstyled run. the renderer derives all styling from the text.
func (e *Editor) expandDocForRawMode() {
	cursorBlock := e.cursor.Block
	cursorCol := e.cursor.Col

	for i := range e.doc.Blocks {
		b := &e.doc.Blocks[i]

		// compute cursor offset before we flatten (only for cursor block)
		var inlineOffset int
		if i == cursorBlock {
			inlineOffset = rawCursorExpansionOffset(b.Runs, cursorCol)
		}

		prefix := rawBlockPrefix(b)
		var text string

		switch b.Type {
		case BlockDivider:
			text = "---"
		case BlockCodeLine:
			// handled in a separate pass — fence blocks are inserted around code groups
			text = b.Text()
		case BlockFrontMatter:
			text = "---\n" + b.Text() + "\n---"
		default:
			text = prefix + runsToMarkdown(b.Runs)
		}

		b.Runs = []Run{{Text: text, Style: StyleNone}}

		if i == cursorBlock {
			cursorCol += inlineOffset + len([]rune(prefix))
		}
	}

	e.cursor.Col = cursorCol

	// insert fence blocks around code line groups
	for i := 0; i < len(e.doc.Blocks); i++ {
		if e.doc.Blocks[i].Type != BlockCodeLine {
			continue
		}
		// found start of a code group
		lang := ""
		if e.doc.Blocks[i].Attrs != nil {
			lang = e.doc.Blocks[i].Attrs["lang"]
		}
		openFence := Block{Type: BlockParagraph, Runs: []Run{{Text: "```" + lang, Style: StyleNone}}}
		e.doc.Blocks = append(e.doc.Blocks[:i], append([]Block{openFence}, e.doc.Blocks[i:]...)...)
		if cursorBlock >= i {
			cursorBlock++
		}
		i++ // skip the fence we just inserted

		// find end of code group
		for i < len(e.doc.Blocks) && e.doc.Blocks[i].Type == BlockCodeLine {
			e.doc.Blocks[i].Type = BlockParagraph // convert to paragraph for raw mode
			i++
		}

		closeFence := Block{Type: BlockParagraph, Runs: []Run{{Text: "```", Style: StyleNone}}}
		e.doc.Blocks = append(e.doc.Blocks[:i], append([]Block{closeFence}, e.doc.Blocks[i:]...)...)
		if cursorBlock >= i {
			cursorBlock++
		}
		i++ // skip the closing fence
	}

	e.cursor.Block = cursorBlock
}

// compressDocFromRawMode parses each block's text to derive its type and
// restore structured runs with style flags. text is the source of truth.
func (e *Editor) compressDocFromRawMode() {
	cursorBlock := e.cursor.Block
	cursorCol := e.cursor.Col

	// first pass: detect code fences and convert blocks between them to BlockCodeLine,
	// removing the fence blocks themselves
	for i := 0; i < len(e.doc.Blocks); i++ {
		text := e.doc.Blocks[i].Text()
		if !strings.HasPrefix(text, "```") || text == "```" {
			continue
		}
		// opening fence found — extract lang
		lang := strings.TrimPrefix(text, "```")
		attrs := map[string]string{}
		if lang != "" {
			attrs["lang"] = lang
		}

		// find closing fence
		closeIdx := -1
		for j := i + 1; j < len(e.doc.Blocks); j++ {
			if strings.TrimSpace(e.doc.Blocks[j].Text()) == "```" {
				closeIdx = j
				break
			}
		}
		if closeIdx < 0 {
			continue // no closing fence, treat as regular text
		}

		// convert blocks between fences to BlockCodeLine
		for j := i + 1; j < closeIdx; j++ {
			e.doc.Blocks[j].Type = BlockCodeLine
			e.doc.Blocks[j].Attrs = make(map[string]string)
			for k, v := range attrs {
				e.doc.Blocks[j].Attrs[k] = v
			}
		}

		// if no content lines between fences, insert an empty code line
		if closeIdx == i+1 {
			empty := Block{Type: BlockCodeLine, Runs: []Run{{Text: "", Style: StyleNone}}, Attrs: make(map[string]string)}
			for k, v := range attrs {
				empty.Attrs[k] = v
			}
			e.doc.Blocks = append(e.doc.Blocks[:i+1], append([]Block{empty}, e.doc.Blocks[i+1:]...)...)
			closeIdx++ // closing fence shifted
			if cursorBlock > i {
				cursorBlock++
			}
		}

		// remove opening fence block
		e.doc.Blocks = append(e.doc.Blocks[:i], e.doc.Blocks[i+1:]...)
		closeIdx--
		if cursorBlock > i {
			cursorBlock--
		} else if cursorBlock == i {
			// cursor was on opening fence — move to first code line
			cursorCol = 0
		}

		// remove closing fence block
		e.doc.Blocks = append(e.doc.Blocks[:closeIdx], e.doc.Blocks[closeIdx+1:]...)
		if cursorBlock > closeIdx {
			cursorBlock--
		} else if cursorBlock == closeIdx {
			// cursor was on closing fence — move to last code line
			cursorBlock = closeIdx - 1
			if cursorBlock < 0 {
				cursorBlock = 0
			}
			cursorCol = 0
		}

		// continue scanning from where the code group ends
		i = closeIdx - 1
	}

	e.cursor.Block = cursorBlock

	for i := range e.doc.Blocks {
		b := &e.doc.Blocks[i]

		// code lines already handled by fence pass above
		if b.Type == BlockCodeLine {
			b.Runs = []Run{{Text: b.Text(), Style: StyleNone}}
			continue
		}

		text := b.Text()

		// derive block type from what the text actually says
		derivedType, derivedAttrs := deriveBlockTypeFromText(text)
		b.Type = derivedType
		b.Attrs = derivedAttrs

		switch derivedType {
		case BlockDivider:
			b.Runs = nil
			continue
		case BlockFrontMatter:
			inner := strings.TrimPrefix(text, "---\n")
			if idx := strings.LastIndex(inner, "\n---"); idx >= 0 {
				inner = inner[:idx]
			}
			b.Runs = []Run{{Text: inner, Style: StyleNone}}
			continue
		}

		// strip derived block prefix to get content
		prefix := rawBlockPrefix(b)
		prefixLen := len([]rune(prefix))
		content := text
		if prefix != "" && strings.HasPrefix(text, prefix) {
			content = text[len(prefix):]
		}

		// adjust cursor for prefix removal
		if i == cursorBlock {
			cursorCol -= prefixLen
			if cursorCol < 0 {
				cursorCol = 0
			}
		}

		// re-parse inline markdown to restore styled runs
		newRuns := parseInlineMarkdown(content)
		if len(newRuns) == 0 {
			newRuns = []Run{{Text: content, Style: StyleNone}}
		}

		// adjust cursor for inline marker removal
		if i == cursorBlock {
			cursorCol -= rawCursorExpansionOffset(newRuns, cursorCol)
			if cursorCol < 0 {
				cursorCol = 0
			}
			blockLen := 0
			for _, r := range newRuns {
				blockLen += len([]rune(r.Text))
			}
			if cursorCol > blockLen {
				cursorCol = blockLen
			}
		}

		b.Runs = newRuns
	}

	e.cursor.Col = cursorCol
}

// rawCursorExpansionOffset returns how many inline marker characters would be
// added before the given document column when expanding for raw mode
func rawCursorExpansionOffset(runs []Run, col int) int {
	extra := 0
	pos := 0
	for _, r := range runs {
		prefix := rawInlinePrefix(r.Style)
		suffix := rawInlineSuffix(r.Style)
		runLen := len([]rune(r.Text))

		if col <= pos {
			break
		}

		extra += len([]rune(prefix))

		if col <= pos+runLen {
			break
		}

		extra += len([]rune(suffix))
		pos += runLen
	}
	return extra
}

// FocusRange returns the range of content that should be in focus
// based on the current cursor position and focus scope
func (e *Editor) FocusRange() Range {
	switch e.focusScope {
	case FocusScopeLine:
		return e.focusRangeLine()
	case FocusScopeSentence:
		return e.focusRangeSentence()
	case FocusScopeParagraph:
		return e.focusRangeParagraph()
	default:
		return e.focusRangeLine()
	}
}

// focusRangeLine returns the current visual line as focus range
func (e *Editor) focusRangeLine() Range {
	b := e.CurrentBlock()
	if b == nil {
		return Range{Start: e.cursor, End: e.cursor}
	}

	runes := []rune(b.Text())
	wrapPoints := e.wrapPointsForBlock(b)

	// find which visual line the cursor is on
	lineStart := 0
	lineEnd := len(runes)

	for i, wp := range wrapPoints {
		if e.cursor.Col < wp {
			if i > 0 {
				lineStart = wrapPoints[i-1]
			}
			lineEnd = wp
			break
		}
		lineStart = wp
	}

	return Range{
		Start: Pos{Block: e.cursor.Block, Col: lineStart},
		End:   Pos{Block: e.cursor.Block, Col: lineEnd},
	}
}

// focusRangeSentence returns the current sentence as focus range
func (e *Editor) focusRangeSentence() Range {
	return e.InnerSentence()
}

// focusRangeParagraph returns the current paragraph/block as focus range
func (e *Editor) focusRangeParagraph() Range {
	return e.InnerParagraph()
}

// IsBlockInFocus returns whether a block (or part of it) is within focus range
func (e *Editor) IsBlockInFocus(blockIdx int) bool {
	if !e.focusModeActive() {
		return true
	}
	fr := e.FocusRange()
	return blockIdx >= fr.Start.Block && blockIdx <= fr.End.Block
}

// GetFocusRangeInBlock returns the focused character range within a specific block
// Returns (start, end, hasFocus) where start/end are character offsets
func (e *Editor) GetFocusRangeInBlock(blockIdx int) (int, int, bool) {
	if !e.focusModeActive() {
		return 0, 0, false
	}

	fr := e.FocusRange()

	// block entirely outside focus
	if blockIdx < fr.Start.Block || blockIdx > fr.End.Block {
		return 0, 0, false
	}

	b := &e.doc.Blocks[blockIdx]
	blockLen := b.Length()

	start := 0
	end := blockLen

	if blockIdx == fr.Start.Block {
		start = fr.Start.Col
	}
	if blockIdx == fr.End.Block {
		end = fr.End.Col
	}

	return start, end, true
}

// =============================================================================
// Rendering
// =============================================================================

// dimStyle returns a style with dimmed colors but preserved attributes (bold, italic, etc.)
func (e *Editor) dimStyle(original glyph.Style) glyph.Style {
	return glyph.Style{
		FG:   e.theme.Dimmed.FG,
		BG:   e.theme.Dimmed.BG,
		Attr: original.Attr, // preserve bold, italic, underline, etc.
	}
}

// applyFocusDimming dims spans that are outside the focus range
// focusStart and focusEnd are character positions within this block
// if the block is entirely outside focus, all spans are dimmed
func (e *Editor) applyFocusDimming(lines [][]glyph.Span, blockIdx int) [][]glyph.Span {
	if !e.focusModeActive() {
		return lines
	}

	focusStart, focusEnd, hasFocus := e.GetFocusRangeInBlock(blockIdx)

	if !hasFocus {
		// entire block is unfocused - dim everything but preserve formatting
		for i := range lines {
			for j := range lines[i] {
				lines[i][j].Style = e.dimStyle(lines[i][j].Style)
			}
		}
		return lines
	}

	// apply partial dimming within the block
	// track character offset as we iterate through lines
	charOffset := 0
	prefixLen := e.blockPrefixLength(&e.doc.Blocks[blockIdx])

	for lineIdx := range lines {
		newSpans := make([]glyph.Span, 0, len(lines[lineIdx]))

		for _, span := range lines[lineIdx] {
			spanRunes := []rune(span.Text)
			spanLen := len(spanRunes)

			// adjust for prefix on first line (prefix chars are not in document)
			effectiveOffset := charOffset
			if lineIdx == 0 && charOffset < prefixLen {
				// this span is part of the prefix decoration
				if charOffset+spanLen <= prefixLen {
					// entire span is prefix - check if block start is in focus
					if focusStart == 0 {
						newSpans = append(newSpans, span)
					} else {
						newSpans = append(newSpans, glyph.Span{Text: span.Text, Style: e.dimStyle(span.Style)})
					}
					charOffset += spanLen
					continue
				}
				// partial prefix
				effectiveOffset = 0
			} else if lineIdx == 0 {
				effectiveOffset = charOffset - prefixLen
			}

			// determine if span is in focus, partially in focus, or unfocused
			spanStart := effectiveOffset
			spanEnd := effectiveOffset + spanLen

			if spanEnd <= focusStart || spanStart >= focusEnd {
				// entirely unfocused - dim but preserve formatting
				newSpans = append(newSpans, glyph.Span{Text: span.Text, Style: e.dimStyle(span.Style)})
			} else if spanStart >= focusStart && spanEnd <= focusEnd {
				// entirely focused
				newSpans = append(newSpans, span)
			} else {
				// partially focused - split into up to 3 contiguous segments
				// (before focus, in focus, after focus) to preserve word boundaries

				// calculate split points relative to span
				beforeEnd := 0
				if focusStart > spanStart {
					beforeEnd = focusStart - spanStart
				}

				afterStart := spanLen
				if focusEnd < spanEnd {
					afterStart = focusEnd - spanStart
				}

				// before focus (dimmed)
				if beforeEnd > 0 {
					newSpans = append(newSpans, glyph.Span{
						Text:  string(spanRunes[:beforeEnd]),
						Style: e.dimStyle(span.Style),
					})
				}

				// in focus (original)
				if afterStart > beforeEnd {
					newSpans = append(newSpans, glyph.Span{
						Text:  string(spanRunes[beforeEnd:afterStart]),
						Style: span.Style,
					})
				}

				// after focus (dimmed)
				if afterStart < spanLen {
					newSpans = append(newSpans, glyph.Span{
						Text:  string(spanRunes[afterStart:]),
						Style: e.dimStyle(span.Style),
					})
				}
			}

			charOffset += spanLen
		}

		lines[lineIdx] = newSpans
	}

	return lines
}

func (e *Editor) runsToSpans(runs []Run) []glyph.Span {
	spans := make([]glyph.Span, 0, len(runs))
	for _, r := range runs {
		spans = append(spans, glyph.Span{
			Text:  r.Text,
			Style: e.theme.StyleForRun(r.Style),
		})
	}
	return spans
}

// runsToSpansWithBase converts runs to spans using a base style (for headings, quotes, etc.)
func (e *Editor) runsToSpansWithBase(runs []Run, baseStyle glyph.Style) []glyph.Span {
	spans := make([]glyph.Span, 0, len(runs))
	for _, r := range runs {
		style := baseStyle
		// apply inline styles on top of base
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
			style.FG = e.theme.Code.FG
			style.BG = e.theme.Code.BG
		}
		spans = append(spans, glyph.Span{
			Text:  r.Text,
			Style: style,
		})
	}
	return spans
}

// addSpellCheckUnderlines takes spans and returns new spans with misspelled words underlined
func (e *Editor) addSpellCheckUnderlines(spans []glyph.Span) []glyph.Span {
	if e.spellChecker == nil {
		return spans
	}

	var result []glyph.Span
	for _, span := range spans {
		// split span by words and add underline to misspelled ones
		result = append(result, e.splitSpanForSpelling(span)...)
	}
	return result
}

// splitSpanForSpelling splits a span only at misspelled word boundaries.
// when no words are misspelled (common case), returns the original span with zero extra allocs.
func (e *Editor) splitSpanForSpelling(span glyph.Span) []glyph.Span {
	text := span.Text
	if len(text) == 0 {
		return []glyph.Span{span}
	}

	// single pass: check words, only allocate result when misspelled words exist
	var result []glyph.Span
	lastSplit := 0
	i := 0
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !isSpellWordChar(r) {
			i += size
			continue
		}
		// found start of word
		wordStart := i
		for i < len(text) {
			r, size = utf8.DecodeRuneInString(text[i:])
			if !isSpellWordChar(r) {
				break
			}
			i += size
		}
		word := text[wordStart:i]
		if e.IsMisspelled(word) {
			// lazy init: add everything before this word as one span
			if wordStart > lastSplit {
				result = append(result, glyph.Span{
					Text:  text[lastSplit:wordStart],
					Style: span.Style,
				})
			}
			result = append(result, glyph.Span{
				Text:  word,
				Style: span.Style.Underline(),
			})
			lastSplit = i
		}
	}

	if len(result) == 0 {
		return []glyph.Span{span}
	}

	// add remaining text after last misspelled word
	if lastSplit < len(text) {
		result = append(result, glyph.Span{
			Text:  text[lastSplit:],
			Style: span.Style,
		})
	}

	return result
}

// getTemplateForBlock returns the template name for a block type from document styles
func (e *Editor) getTemplateForBlock(blockType BlockType) string {
	// for headings, look up the shared "h1" element (all headings share one template setting)
	element := string(blockType)
	category := categoryForBlockType(blockType)
	if category != "" {
		categoryElement := categoryToElement(category)
		if categoryElement != "" {
			element = categoryElement
		}
	}

	for _, sc := range e.doc.Meta.Styles {
		if sc.Element == element {
			return sc.Template
		}
	}
	return ""
}

// setTemplateForElement sets the template for an element type
func (e *Editor) setTemplateForElement(element, template string) {
	for i, sc := range e.doc.Meta.Styles {
		if sc.Element == element {
			e.doc.Meta.Styles[i].Template = template
			return
		}
	}
	// not found, add new
	e.doc.Meta.Styles = append(e.doc.Meta.Styles, StyleChoice{
		Element:  element,
		Template: template,
	})
}

// categoryForBlockType maps block types to template categories
func categoryForBlockType(bt BlockType) string {
	switch bt {
	case BlockH1, BlockH2, BlockH3, BlockH4, BlockH5, BlockH6:
		return "headings"
	case BlockQuote:
		return "quotes"
	case BlockDivider:
		return "dividers"
	case BlockListItem:
		return "lists"
	case BlockCodeLine:
		return "code"
	case BlockCallout:
		return "callouts"
	case BlockFrontMatter:
		return "frontmatter"
	case BlockTable:
		return "tables"
	case BlockDialogue:
		return "dialogue"
	default:
		return ""
	}
}

// CycleTemplate cycles to the next template for the given category
func (e *Editor) CycleTemplate(category string) string {
	cat := e.templates.GetCategory(category)
	if cat == nil {
		return ""
	}

	templates := cat.List()
	if len(templates) == 0 {
		return ""
	}

	// find element name for this category
	element := categoryToElement(category)
	if element == "" {
		return ""
	}

	// get current template
	current := ""
	for _, sc := range e.doc.Meta.Styles {
		if sc.Element == element {
			current = sc.Template
			break
		}
	}

	// find next template
	nextIdx := 0
	for i, name := range templates {
		if name == current {
			nextIdx = (i + 1) % len(templates)
			break
		}
	}

	next := templates[nextIdx]
	e.setTemplateForElement(element, next)
	e.dirty = true
	e.InvalidateCache()
	return next
}

// categoryToElement maps category names to element names used in styles
func categoryToElement(category string) string {
	switch category {
	case "headings":
		return "h1" // apply to all headings
	case "quotes":
		return "quote"
	case "dividers":
		return "divider"
	case "lists":
		return "li"
	case "code":
		return "code"
	case "callouts":
		return "callout"
	case "frontmatter":
		return "frontmatter"
	case "tables":
		return "table"
	case "dialogue":
		return "dialogue"
	default:
		return ""
	}
}

// ApplyBundle applies a style bundle to the document
func (e *Editor) ApplyBundle(bundleName string) bool {
	bundle := e.templates.GetBundle(bundleName)
	if bundle == nil {
		return false
	}

	for category, template := range bundle.Templates {
		element := categoryToElement(category)
		if element != "" {
			e.setTemplateForElement(element, template)
		}
	}
	e.dirty = true
	e.InvalidateCache()
	return true
}

// renderBlock renders a block, returning multiple lines
// renderBlockRaw renders a block with visible markdown syntax and styling
// renderBlockRaw renders a block in raw mode. since the document text already
// renderBlockRaw derives block type and inline styles from the text content.
// the text is the source of truth — styling follows from what's written.
func (e *Editor) renderBlockRaw(block *Block, blockIdx int) [][]glyph.Span {
	text := block.Text()

	// derive block type from text for block-level styling
	derivedType, _ := deriveBlockTypeFromText(text)
	blockStyle := e.theme.StyleForBlock(derivedType)

	var lines [][]glyph.Span

	if strings.Contains(text, "\n") {
		// multiline blocks (code fences, front matter)
		for _, lineText := range strings.Split(text, "\n") {
			spans := []glyph.Span{{Text: lineText, Style: blockStyle}}
			lines = append(lines, spans)
		}
	}

	if len(lines) == 0 {
		// parse inline markdown keeping markers visible
		rawRuns := parseInlineMarkdownRaw(text)
		spans := e.runsToSpansWithBase(rawRuns, blockStyle)
		lines = [][]glyph.Span{spans}
	}

	lines = e.applyFocusDimming(lines, blockIdx)
	return lines
}

func (e *Editor) renderBlock(block *Block, blockIdx int) [][]glyph.Span {
	// raw mode: show markdown syntax with styling, skip templates
	if e.rawMode {
		return e.renderBlockRaw(block, blockIdx)
	}

	blockStyle := e.theme.StyleForBlock(block.Type)

	var lines [][]glyph.Span

	switch block.Type {
	case BlockH1, BlockH2, BlockH3, BlockH4, BlockH5, BlockH6:
		// headings get their distinct color
		spans := e.runsToSpansWithBase(block.Runs, blockStyle)
		templateName := e.getTemplateForBlock(block.Type)
		if tmpl := e.templates.GetHeadingTemplate(templateName); tmpl != nil {
			lines = tmpl.Render(spans, blockStyle, contentWidth, block.Type)
		}
		if len(lines) == 0 {
			lines = [][]glyph.Span{spans}
		}
	case BlockQuote:
		// blockquotes use quote template
		spans := e.runsToSpansWithBase(block.Runs, blockStyle)
		templateName := e.getTemplateForBlock(block.Type)
		if tmpl := e.templates.GetQuoteTemplate(templateName); tmpl != nil {
			lines = tmpl.Render(spans, blockStyle, contentWidth, block.Type)
		}
		if len(lines) == 0 {
			// fallback: bar style
			marker := glyph.Span{Text: "│ ", Style: e.theme.ListBullet}
			lines = [][]glyph.Span{append([]glyph.Span{marker}, spans...)}
		}
	case BlockListItem:
		// list items use list template for bullets, custom for numbers
		spans := e.runsToSpans(block.Runs)
		if block.Attrs != nil && block.Attrs["marker"] == "number" {
			// numbered list - use number from attrs
			num := block.Attrs["number"]
			if num == "" {
				num = "1"
			}
			markerSpan := glyph.Span{Text: num + ". ", Style: e.theme.ListBullet}
			lines = [][]glyph.Span{append([]glyph.Span{markerSpan}, spans...)}
		} else {
			// bullet list - use template
			templateName := e.getTemplateForBlock(block.Type)
			if tmpl := e.templates.GetListTemplate(templateName); tmpl != nil {
				lines = tmpl.Render(spans, e.theme.ListBullet, contentWidth, block.Type)
			}
			if len(lines) == 0 {
				// fallback: bullet
				markerSpan := glyph.Span{Text: "• ", Style: e.theme.ListBullet}
				lines = [][]glyph.Span{append([]glyph.Span{markerSpan}, spans...)}
			}
		}
	case BlockCodeLine:
		lines = e.renderCodeLine(block, blockIdx)
	case BlockCallout:
		spans := e.runsToSpansWithBase(block.Runs, blockStyle)
		templateName := e.getTemplateForBlock(block.Type)
		if tmpl := e.templates.GetCalloutTemplate(templateName); tmpl != nil {
			lines = tmpl.Render(spans, blockStyle, contentWidth, block.Type)
		}
		if len(lines) == 0 {
			lines = [][]glyph.Span{spans}
		}
	case BlockDivider:
		// dividers use template
		templateName := e.getTemplateForBlock(block.Type)
		if tmpl := e.templates.GetDividerTemplate(templateName); tmpl != nil {
			lines = tmpl.Render(nil, blockStyle, contentWidth, block.Type)
		}
		if len(lines) == 0 {
			// fallback: simple line
			lines = [][]glyph.Span{{{Text: strings.Repeat("─", contentWidth), Style: blockStyle}}}
		}
	case BlockFrontMatter:
		lines = e.renderFrontMatter(block)
	case BlockTable:
		// tables use table template
		spans := e.runsToSpans(block.Runs)
		templateName := e.getTemplateForBlock(block.Type)
		if tmpl := e.templates.GetTemplate("tables", templateName); tmpl != nil {
			lines = tmpl.Render(spans, blockStyle, contentWidth, block.Type)
		}
		if len(lines) == 0 {
			// fallback: just show raw text
			lines = [][]glyph.Span{spans}
		}
	case BlockDialogue:
		// dialogue uses dialogue template
		// build spans with character name first, then dialogue content
		character := ""
		if block.Attrs != nil {
			character = block.Attrs["character"]
		}

		// check if this is a continuation (previous block is same character)
		isContinuation := false
		if blockIdx > 0 {
			prevBlock := &e.doc.Blocks[blockIdx-1]
			if prevBlock.Type == BlockDialogue && prevBlock.Attrs != nil {
				if prevBlock.Attrs["character"] == character {
					isContinuation = true
				}
			}
		}

		// don't show character name for continuation paragraphs
		displayChar := character
		if isContinuation {
			displayChar = ""
		}

		charSpan := glyph.Span{Text: displayChar, Style: e.theme.DialogueCharacter}
		dialogueSpans := e.runsToSpans(block.Runs)
		allSpans := append([]glyph.Span{charSpan}, dialogueSpans...)

		templateName := e.getTemplateForBlock(block.Type)
		if tmpl := e.templates.GetDialogueTemplate(templateName); tmpl != nil {
			lines = tmpl.Render(allSpans, blockStyle, contentWidth, block.Type)
		}
		if len(lines) == 0 {
			// fallback: simple character: dialogue format
			lines = [][]glyph.Span{{charSpan, {Text: ": ", Style: blockStyle}}}
			lines[0] = append(lines[0], dialogueSpans...)
		}
	case BlockParenthetical:
		// parentheticals are italicized stage directions, positioned to match dialogue template
		text := block.Text()
		parenStyle := e.theme.DialogueParenthetical
		// wrap in parentheses if not already
		if !strings.HasPrefix(text, "(") {
			text = "(" + text + ")"
		}

		// match the current dialogue template's indentation
		// default is "stageplay" if not explicitly set
		templateName := e.getTemplateForBlock(BlockDialogue)
		var padding int
		if templateName == "screenplay" {
			// screenplay: center the parenthetical
			textWidth := utf8.RuneCountInString(text)
			padding = (contentWidth - textWidth) / 2
			if padding < 0 {
				padding = 0
			}
		} else {
			// stageplay (default): indent to dialogue column (14 char name + 2 gap)
			padding = 16
		}
		// match dialogue template pattern: padding + text in one span
		indentedText := strings.Repeat(" ", padding) + text
		if e.debugDialogue {
			fmt.Fprintf(os.Stderr, "[PAREN] template=%q padding=%d text=%q result=%q\n", templateName, padding, text, indentedText)
		}
		lines = [][]glyph.Span{{{Text: indentedText, Style: parenStyle}}}
	case BlockSceneHeading:
		// scene headings are uppercase, bold - standard screenplay format
		text := strings.ToUpper(block.Text())
		sceneStyle := blockStyle.Bold()
		lines = [][]glyph.Span{{{Text: text, Style: sceneStyle}}}
	default:
		// check if text contains newlines (e.g., from markdown parsing)
		text := block.Text()
		if strings.Contains(text, "\n") {
			lines = e.renderMultilineBlock(block)
		}
	}

	if len(lines) == 0 {
		spans := e.runsToSpans(block.Runs)
		lines = [][]glyph.Span{spans}
	}

	// apply focus mode dimming if active
	// NOTE: spell check underlines applied AFTER wrapSpans in RenderLines
	lines = e.applyFocusDimming(lines, blockIdx)

	return lines
}

// getListMarker returns the appropriate marker for a list item
func (e *Editor) getListMarker(block *Block) string {
	if block.Attrs == nil {
		return "• "
	}
	marker := block.Attrs["marker"]
	switch marker {
	case "number":
		num := block.Attrs["number"]
		if num == "" {
			num = "1"
		}
		return num + ". "
	case "bullet":
		return "• "
	default:
		return "• "
	}
}

// renderCodeLine renders a single code line block, adding group decorations
// (top/bottom edges) when this block is the first/last in a consecutive run
// of BlockCodeLine blocks.
func (e *Editor) renderCodeLine(block *Block, blockIdx int) [][]glyph.Span {
	codeStyle := e.theme.CodeBlock
	text := block.Text()

	// determine group boundaries
	isFirst := blockIdx == 0 || e.doc.Blocks[blockIdx-1].Type != BlockCodeLine
	isLast := blockIdx >= len(e.doc.Blocks)-1 || e.doc.Blocks[blockIdx+1].Type != BlockCodeLine

	// find max width across the group for uniform padding
	maxWidth := utf8.RuneCountInString(text)
	for j := blockIdx - 1; j >= 0 && e.doc.Blocks[j].Type == BlockCodeLine; j-- {
		if w := utf8.RuneCountInString(e.doc.Blocks[j].Text()); w > maxWidth {
			maxWidth = w
		}
	}
	for j := blockIdx + 1; j < len(e.doc.Blocks) && e.doc.Blocks[j].Type == BlockCodeLine; j++ {
		if w := utf8.RuneCountInString(e.doc.Blocks[j].Text()); w > maxWidth {
			maxWidth = w
		}
	}
	maxWidth += 2 // visual padding

	// pad this line to group width
	w := utf8.RuneCountInString(text)
	paddedLine := text
	if w < maxWidth {
		paddedLine = text + repeatSpace(maxWidth-w)
	}

	templateName := e.getTemplateForBlock(BlockCodeLine)
	tmpl := e.templates.GetCodeTemplate(templateName)
	useSoftEdges := templateName == "sidebar"

	mainBg := glyph.Hex(0x111111)
	codeBg := glyph.Hex(0x1a1a1a)
	barColor := glyph.Hex(0xcccccc)
	bandWidth := contentWidth - 1

	var lines [][]glyph.Span

	// top edge for first block in group
	if isFirst && useSoftEdges {
		topEdge := []glyph.Span{
			{Text: "╷", Style: glyph.Style{FG: barColor, BG: mainBg}},
			{Text: strings.Repeat("▄", bandWidth), Style: glyph.Style{FG: codeBg, BG: mainBg}},
		}
		lines = append(lines, topEdge)
	}

	lineSpan := []glyph.Span{{Text: paddedLine, Style: codeStyle}}
	if tmpl != nil {
		rendered := tmpl.Render(lineSpan, codeStyle, contentWidth, BlockCodeLine)
		lines = append(lines, rendered...)
	} else {
		lines = append(lines, lineSpan)
	}

	// bottom edge for last block in group
	if isLast && useSoftEdges {
		bottomEdge := []glyph.Span{
			{Text: "╵", Style: glyph.Style{FG: barColor, BG: mainBg}},
			{Text: strings.Repeat("▀", bandWidth), Style: glyph.Style{FG: codeBg, BG: mainBg}},
		}
		lines = append(lines, bottomEdge)
	}

	return lines
}

// renderMultilineBlock renders a block that contains newlines
func (e *Editor) renderMultilineBlock(block *Block) [][]glyph.Span {
	text := block.Text()
	textLines := strings.Split(text, "\n")

	var lines [][]glyph.Span
	style := e.theme.BaseStyle()

	// try to preserve run styles for first line, use base for rest
	if len(block.Runs) > 0 {
		style = e.theme.StyleForRun(block.Runs[0].Style)
	}

	for _, line := range textLines {
		spans := []glyph.Span{{Text: line, Style: style}}
		lines = append(lines, spans)
	}

	if len(lines) == 0 {
		lines = [][]glyph.Span{{}}
	}

	return lines
}

// renderFrontMatter renders YAML front matter with typewriter red colons
func (e *Editor) renderFrontMatter(block *Block) [][]glyph.Span {
	text := block.Text()
	textLines := strings.Split(text, "\n")

	var lines [][]glyph.Span
	keyStyle := e.theme.FrontMatterKey
	valueStyle := e.theme.FrontMatterValue

	for _, line := range textLines {
		colonIdx := strings.Index(line, ":")
		if colonIdx >= 0 {
			// key: value format - color the colon in typewriter red
			key := line[:colonIdx]
			rest := ""
			if colonIdx+1 < len(line) {
				rest = line[colonIdx+1:]
			}
			spans := []glyph.Span{
				{Text: key, Style: valueStyle},
				{Text: ":", Style: keyStyle},
				{Text: rest, Style: valueStyle},
			}
			lines = append(lines, spans)
		} else {
			// no colon - just render as regular text
			lines = append(lines, []glyph.Span{{Text: line, Style: valueStyle}})
		}
	}

	if len(lines) == 0 {
		lines = [][]glyph.Span{{}}
	}

	return lines
}

// applySelectionHighlight applies inverse styling to the selected portion
// selStart and selEnd are in runes (characters), not bytes
func (e *Editor) applySelectionHighlight(spans []glyph.Span, selStart, selEnd int) []glyph.Span {
	var result []glyph.Span
	pos := 0 // position in runes

	for _, span := range spans {
		runes := []rune(span.Text)
		spanLen := len(runes)
		spanEnd := pos + spanLen

		if spanEnd <= selStart || pos >= selEnd {
			// span entirely outside selection
			result = append(result, span)
		} else {
			// span overlaps with selection
			// part before selection
			if pos < selStart {
				beforeLen := selStart - pos
				result = append(result, glyph.Span{
					Text:  string(runes[:beforeLen]),
					Style: span.Style,
				})
			}

			// selected part
			start := 0
			if selStart > pos {
				start = selStart - pos
			}
			end := spanLen
			if selEnd < spanEnd {
				end = selEnd - pos
			}
			if start < end {
				result = append(result, glyph.Span{
					Text:  string(runes[start:end]),
					Style: span.Style.Inverse(),
				})
			}

			// part after selection
			if spanEnd > selEnd {
				afterStart := selEnd - pos
				result = append(result, glyph.Span{
					Text:  string(runes[afterStart:]),
					Style: span.Style,
				})
			}
		}

		pos = spanEnd
	}

	return result
}

// wrapSpans breaks spans into lines that fit within maxWidth
// uses display width to handle CJK double-width characters
func wrapSpans(spans []glyph.Span, maxWidth int) [][]glyph.Span {
	if maxWidth <= 0 {
		return [][]glyph.Span{spans}
	}

	// fast path: if total width of all spans fits, no wrapping needed
	totalWidth := 0
	fits := true
	for _, span := range spans {
		totalWidth += runewidth.StringWidth(span.Text)
		if totalWidth > maxWidth {
			fits = false
			break
		}
	}
	if fits {
		return [][]glyph.Span{spans}
	}

	var lines [][]glyph.Span
	var currentLine []glyph.Span
	currentWidth := 0

	for _, span := range spans {
		text := span.Text
		// work with byte offsets into the string to avoid []rune allocation
		pos := 0
		for pos < len(text) {
			remaining := maxWidth - currentWidth
			if remaining <= 0 {
				lines = append(lines, currentLine)
				currentLine = nil
				currentWidth = 0
				remaining = maxWidth
			}

			// strip leading spaces at start of line
			if currentWidth == 0 {
				for pos < len(text) && text[pos] == ' ' {
					pos++
				}
				if pos >= len(text) {
					break
				}
			}

			// measure remaining span text from current position
			rest := text[pos:]
			spanWidth := runewidth.StringWidth(rest)
			if spanWidth <= remaining {
				currentLine = append(currentLine, glyph.Span{Text: rest, Style: span.Style})
				currentWidth += spanWidth
				break
			}

			// find wrap point by walking runes with byte offsets
			wrapByte := 0
			lastSpaceByte := -1
			widthSoFar := 0
			for byteIdx := 0; byteIdx < len(rest); {
				r, size := utf8.DecodeRuneInString(rest[byteIdx:])
				rw := runewidth.RuneWidth(r)
				if widthSoFar+rw > remaining {
					break
				}
				widthSoFar += rw
				byteIdx += size
				wrapByte = byteIdx
				if r == ' ' {
					lastSpaceByte = byteIdx
				}
			}

			// prefer word boundary
			if lastSpaceByte > 0 {
				wrapByte = lastSpaceByte
			}

			// no word boundary found in this span fragment — avoid mid-word
			// breaks by flushing the current line and retrying on a fresh line.
			// this keeps wrapping consistent with calculateWrapPointsForWidth
			// which operates on flat text and always wraps at word boundaries.
			if lastSpaceByte <= 0 && currentWidth > 0 {
				lines = append(lines, currentLine)
				currentLine = nil
				currentWidth = 0
				continue
			}
			if wrapByte == 0 {
				_, size := utf8.DecodeRuneInString(rest)
				wrapByte = size // force at least one char
			}

			currentLine = append(currentLine, glyph.Span{Text: rest[:wrapByte], Style: span.Style})
			lines = append(lines, currentLine)
			currentLine = nil
			currentWidth = 0

			pos += wrapByte
			// strip leading spaces on new line
			for pos < len(text) && text[pos] == ' ' {
				pos++
			}
		}
	}

	if len(currentLine) > 0 || len(lines) == 0 {
		lines = append(lines, currentLine)
	}

	return lines
}

type blockLineMapping struct {
	screenLine int
	lineCount  int
}

// InvalidateCache marks the render cache as stale.
// Call this when content changes (edits, theme change, etc.)
func (e *Editor) InvalidateCache() {
	e.cacheValid = false
}

// UpdateDisplay refreshes the layer content
func (e *Editor) UpdateDisplay() {
	if e.screenWidth <= 0 || e.screenHeight <= 0 {
		return
	}

	if e.layer == nil {
		e.layer = glyph.NewLayer()
	}

	// account for sidebar when calculating available width and margin
	sidebarOffset := 0
	if e.browserVisible {
		sidebarOffset = 40 // sidebarWidth from buildView
	}
	availableWidth := e.screenWidth - sidebarOffset
	margin := (availableWidth - contentWidth) / 2
	if margin < 0 {
		margin = 0
	}

	baseStyle := e.theme.BaseStyle()

	// focus mode dims based on cursor position - can't use cache
	// only invalidate when actually applying focus (insert mode)
	if e.focusModeActive() {
		e.cacheValid = false
	}

	// rebuild cache if invalid, width changed, or sidebar state changed
	sidebarHidden := !e.browserVisible
	if !e.cacheValid || e.cacheWidth != e.screenWidth || e.cacheSidebarHidden != sidebarHidden {
		Debug("UpdateDisplay: cache miss (valid=%v width=%v/%v sidebar=%v/%v)",
			e.cacheValid, e.cacheWidth, e.screenWidth, e.cacheSidebarHidden, sidebarHidden)
		e.rebuildRenderCache()
		e.cacheSidebarHidden = sidebarHidden
	}

	// size layer to available area
	e.layer.EnsureSize(availableWidth, e.screenHeight)

	if debugRender {
		fmt.Fprintf(os.Stderr, "\n=== RENDER DEBUG ===\n")
		fmt.Fprintf(os.Stderr, "screen: %dx%d, topLine: %d, cachedLines: %d, blocks: %d\n",
			e.screenWidth, e.screenHeight, e.topLine, len(e.cachedLines), len(e.doc.Blocks))
	}

	// blit visible lines, applying selection highlight only to visible lines
	for i := 0; i < e.screenHeight; i++ {
		docLine := e.topLine + i
		if docLine >= 0 && docLine < len(e.cachedLines) {
			line := e.cachedLines[docLine]
			// apply selection highlight if in visual mode
			if e.mode == ModeVisual {
				line = e.applyVisibleLineSelection(docLine, line)
			}
			e.layer.SetLineAt(i, margin, line, baseStyle)

			if debugRender && i < 5 {
				var text strings.Builder
				for _, span := range line {
					text.WriteString(span.Text)
				}
				fmt.Fprintf(os.Stderr, "  line %d (doc %d): %q\n", i, docLine, text.String())
			}
		} else {
			e.layer.SetLineAt(i, 0, nil, baseStyle)
		}
	}
}

// rebuildRenderCache rebuilds the cached rendered lines
func (e *Editor) rebuildRenderCache() {
	cacheStart := time.Now()
	defer func() {
		Debug("rebuildRenderCache: %v (%d blocks)", time.Since(cacheStart), len(e.doc.Blocks))
	}()
	e.blockLines = make([]blockLineMapping, len(e.doc.Blocks))
	screenLine := 0

	var allLines [][]glyph.Span

	for i, block := range e.doc.Blocks {
		// add gap between dialogue/parenthetical blocks based on speaker changes
		if !e.rawMode && (block.Type == BlockDialogue || block.Type == BlockParenthetical) && i > 0 {
			prevBlock := &e.doc.Blocks[i-1]
			if prevBlock.Type == BlockDialogue || prevBlock.Type == BlockParenthetical {
				// get speaker context for both blocks
				prevChar := e.getBlockSpeaker(i - 1)
				currChar := e.getBlockSpeaker(i)

				// get content length (parentheticals always have content if they exist)
				prevHasContent := prevBlock.Length() > 0 || prevBlock.Type == BlockParenthetical

				// add gap when different speakers and previous has content
				differentSpeaker := prevChar != currChar && prevChar != "" && currChar != ""

				// same speaker continuation only for dialogue-to-dialogue with content
				sameSpeakerContinuation := prevChar == currChar &&
					prevBlock.Type == BlockDialogue && block.Type == BlockDialogue &&
					prevBlock.Length() > 0 && block.Length() > 0

				// no gap between dialogue and its parenthetical (same speaker flow)
				dialogueToParenthetical := prevBlock.Type == BlockDialogue && block.Type == BlockParenthetical
				parentheticalToDialogue := prevBlock.Type == BlockParenthetical && block.Type == BlockDialogue && prevChar == currChar

				if differentSpeaker && prevHasContent && !dialogueToParenthetical && !parentheticalToDialogue {
					allLines = append(allLines, []glyph.Span{})
					screenLine++
				} else if sameSpeakerContinuation {
					allLines = append(allLines, []glyph.Span{})
					screenLine++
				}
			}
		}

		blockLines := e.renderBlock(&block, i)

		var wrappedBlockLines [][]glyph.Span
		// tables, code blocks, dialogue, and headings should not be wrapped - they have fixed formatting
		// dialogue/parenthetical templates handle their own wrapping internally
		// if a template returns multiple lines, it handled its own wrapping (e.g. monograph quote, H1 band)
		// in raw mode, block.Type is stale — wrap everything uniformly
		skipWrap := !e.rawMode && (block.Type == BlockTable || block.Type == BlockCodeLine || block.Type == BlockDialogue || block.Type == BlockParenthetical)
		templateHandledWrap := !e.rawMode && len(blockLines) > 1 && (block.Type == BlockQuote || block.Type == BlockH1 || block.Type == BlockH2)
		for _, line := range blockLines {
			if skipWrap || templateHandledWrap {
				wrappedBlockLines = append(wrappedBlockLines, line)
			} else {
				wrapped := wrapSpans(line, contentWidth)
				wrappedBlockLines = append(wrappedBlockLines, wrapped...)
			}
		}

		// apply spell check underlines AFTER wrapping (for prose blocks, not in raw mode)
		if !e.rawMode && (block.Type == BlockParagraph || block.Type == BlockDialogue) {
			for j, line := range wrappedBlockLines {
				wrappedBlockLines[j] = e.addSpellCheckUnderlines(line)
			}
		}

		e.blockLines[i] = blockLineMapping{
			screenLine: screenLine,
			lineCount:  len(wrappedBlockLines),
		}

		for _, lineSpans := range wrappedBlockLines {
			allLines = append(allLines, lineSpans)
			screenLine++
		}
	}

	e.cachedLines = allLines
	e.cacheWidth = e.screenWidth
	e.cacheValid = true
}

// applyVisibleLineSelection applies selection highlight to a single visible line
func (e *Editor) applyVisibleLineSelection(docLine int, line []glyph.Span) []glyph.Span {
	// find which block this docLine belongs to
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

	selStart, selEnd, hasSel := e.VisualSelectionInBlock(blockIdx)
	if !hasSel {
		return line
	}

	block := &e.doc.Blocks[blockIdx]
	prefixLen := e.blockPrefixLength(block)

	// calculate offset for this wrapped line within the block
	offset := 0
	for j := 0; j < lineInBlock; j++ {
		prevLine := e.cachedLines[e.blockLines[blockIdx].screenLine+j]
		for _, span := range prevLine {
			offset += utf8.RuneCountInString(span.Text)
		}
		if j == 0 {
			offset -= prefixLen
		}
	}

	lineLen := 0
	for _, span := range line {
		lineLen += utf8.RuneCountInString(span.Text)
	}

	effectivePrefixLen := 0
	if lineInBlock == 0 {
		effectivePrefixLen = prefixLen
	}

	contentLen := lineLen - effectivePrefixLen

	lineSelStart := selStart - offset
	lineSelEnd := selEnd - offset

	if lineSelStart < 0 {
		lineSelStart = 0
	}
	if lineSelEnd > contentLen {
		lineSelEnd = contentLen
	}

	if lineSelStart < contentLen && lineSelEnd > 0 && lineSelStart < lineSelEnd {
		return e.applySelectionHighlight(line, lineSelStart+effectivePrefixLen, lineSelEnd+effectivePrefixLen)
	}

	return line
}

// pre-computed space strings for common widths
var spaceCache [129]string

func init() {
	for i := range spaceCache {
		b := make([]byte, i)
		for j := range b {
			b[j] = ' '
		}
		spaceCache[i] = string(b)
	}
}

// leadingWhitespace returns the leading tabs/spaces from a string
func leadingWhitespace(s string) string {
	for i, r := range s {
		if r != ' ' && r != '\t' {
			return s[:i]
		}
	}
	return s
}

func repeatSpace(n int) string {
	if n <= 0 {
		return ""
	}
	if n < len(spaceCache) {
		return spaceCache[n]
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// blockPrefixLength returns the number of characters added before content by templates
func (e *Editor) blockPrefixLength(b *Block) int {
	if b == nil {
		return 0
	}

	// in raw mode, prefixes are embedded in document text — no decoration offset
	if e.rawMode {
		return 0
	}

	switch b.Type {
	case BlockListItem:
		if b.Attrs != nil && b.Attrs["marker"] == "number" {
			num := b.Attrs["number"]
			if num == "" {
				num = "1"
			}
			return len(num) + 2 // "1. " = number + ". "
		}
		// bullet - "• " is 2 display chars (• is 1 rune + space)
		return 2
	case BlockQuote:
		// "│ " is 2 display chars
		return 2
	case BlockDialogue:
		// 14 char column + 2 gap = 16
		return 16
	case BlockCodeLine:
		_, xOff := e.codeLineScreenOffset(0) // xOff is constant regardless of position
		return xOff
	default:
		return 0
	}
}

// codeLineScreenOffset returns the Y offset (decorative lines before content)
// and X offset (template prefix chars) for a code line block.
// yOffset is only non-zero for the first block in a code group (top decoration).
func (e *Editor) codeLineScreenOffset(blockIdx int) (yOffset, xOffset int) {
	if e.rawMode {
		return 0, 0
	}
	isFirst := blockIdx == 0 || e.doc.Blocks[blockIdx-1].Type != BlockCodeLine
	templateName := e.getTemplateForBlock(BlockCodeLine)
	switch templateName {
	case "sidebar":
		yOff := 0
		if isFirst {
			yOff = 1 // top edge line
		}
		return yOff, 2 // "│ " prefix
	case "boxed":
		yOff := 0
		if isFirst {
			yOff = 1 // top border
		}
		return yOff, 2 // "│ " prefix
	default:
		tmpl := e.templates.GetCodeTemplate(templateName)
		if tmpl == nil {
			return 0, 0
		}
		return 0, 2 // most templates add "  " indent
	}
}

// CursorScreenPos returns the screen x,y for the cursor
func (e *Editor) CursorScreenPos() (int, int) {
	// account for sidebar when calculating margin
	sidebarOffset := 0
	if e.browserVisible {
		sidebarOffset = 40 // sidebarWidth from buildView
	}
	availableWidth := e.screenWidth - sidebarOffset
	margin := (availableWidth - contentWidth) / 2
	if margin < 0 {
		margin = 0
	}
	margin += sidebarOffset // add sidebar offset to get screen X

	if e.cursor.Block < 0 || e.cursor.Block >= len(e.blockLines) {
		return margin, 0
	}

	mapping := e.blockLines[e.cursor.Block]

	col := e.cursor.Col
	wrappedLine := 0
	remainingCol := col

	b := e.CurrentBlock()
	prefixLen := e.blockPrefixLength(b)

	// handle dialogue blocks specially
	if b != nil && b.Type == BlockDialogue && !e.rawMode {
		y := mapping.screenLine - e.topLine

		if e.dialogueCharMode {
			// editing character name - right-aligned in 14-char column
			charWidth := 14
			character := ""
			if b.Attrs != nil {
				character = b.Attrs["character"]
			}
			charRunes := []rune(character)
			charDisplayWidth := runewidth.StringWidth(character)
			// cursor position within right-aligned name - use display width
			padding := charWidth - charDisplayWidth
			if padding < 0 {
				padding = 0
			}
			// display width of chars before cursor
			cursorDisplayWidth := runewidth.StringWidth(string(charRunes[:col]))
			x := margin + padding + cursorDisplayWidth
			return x, y
		}

		// editing dialogue text - starts at column 16 (14 char + 2 gap)
		dialogueOffset := 16
		dialogueWidth := contentWidth - dialogueOffset
		if dialogueWidth < 20 {
			dialogueWidth = 20
		}

		// calculate wrap points for dialogue width
		text := b.Text()
		textRunes := []rune(text)
		wrapPoints := e.calculateWrapPointsForWidth(textRunes, dialogueWidth)

		// find which wrapped line the cursor is on
		lineStart := 0
		for i, wp := range wrapPoints {
			if col < wp {
				wrappedLine = i
				break
			}
			lineStart = wp
			wrappedLine = i + 1
		}

		// calculate display width from line start to cursor
		displayWidth := 0
		for i := lineStart; i < col && i < len(textRunes); i++ {
			if textRunes[i] != '\n' {
				displayWidth += runewidth.RuneWidth(textRunes[i])
			}
		}

		x := margin + dialogueOffset + displayWidth
		y = mapping.screenLine + wrappedLine - e.topLine
		return x, y
	}

	// handle parenthetical blocks - same offset as dialogue
	if b != nil && b.Type == BlockParenthetical && !e.rawMode {
		y := mapping.screenLine - e.topLine
		// parentheticals use same 16-char offset as dialogue
		parenOffset := 16
		// add 1 for the opening paren that's auto-added during render
		x := margin + parenOffset + col + 1
		return x, y
	}

	// handle code line blocks — decorative lines and template prefix offset
	if b != nil && b.Type == BlockCodeLine && !e.rawMode {
		yOff, xOff := e.codeLineScreenOffset(e.cursor.Block)

		// code lines are single-line blocks, just compute display width
		textRunes := []rune(b.Text())
		displayWidth := 0
		for i := 0; i < col && i < len(textRunes); i++ {
			displayWidth += runewidth.RuneWidth(textRunes[i])
		}

		x := margin + xOff + displayWidth
		y := mapping.screenLine + yOff - e.topLine
		return x, y
	}

	if b != nil {
		text := b.Text()
		textRunes := []rune(text)
		wrapWidth := e.blockWrapWidth(b)
		wrapPoints := e.calculateWrapPointsForWidth(textRunes, wrapWidth)

		// find which wrapped line the cursor is on
		lineStart := 0
		for i, wp := range wrapPoints {
			if col < wp {
				wrappedLine = i
				break
			}
			lineStart = wp
			wrappedLine = i + 1
		}

		// calculate display width from line start to cursor
		displayWidth := 0
		for i := lineStart; i < col && i < len(textRunes); i++ {
			if textRunes[i] != '\n' {
				displayWidth += runewidth.RuneWidth(textRunes[i])
			}
		}
		remainingCol = displayWidth
	}

	x := margin + remainingCol
	// add prefix offset only on first line (where decoration is rendered)
	if wrappedLine == 0 {
		x += prefixLen
	}
	y := mapping.screenLine + wrappedLine - e.topLine

	return x, y
}

// =============================================================================
// Spell Checking
// =============================================================================

// CheckBlockSpelling queues all words in a block for async spell checking
func (e *Editor) CheckBlockSpelling(b *Block) {
	if e.spellChecker == nil || b == nil {
		return
	}

	words := extractWords(b.Text())
	for _, word := range words {
		word = strings.ToLower(word)
		// skip if already cached or pending
		e.spellMu.RLock()
		_, cached := e.misspelledWord[word]
		pending := e.spellPending[word]
		e.spellMu.RUnlock()
		if cached || pending {
			continue
		}
		// queue for async check
		e.spellMu.Lock()
		e.spellPending[word] = true
		e.spellMu.Unlock()
		e.spellChecker.Check(word)
	}
}

// CheckCurrentBlockSpelling checks spelling in current block
func (e *Editor) CheckCurrentBlockSpelling() {
	e.CheckBlockSpelling(e.CurrentBlock())
}

// CheckAllBlocksSpelling checks spelling in all blocks
func (e *Editor) CheckAllBlocksSpelling() {
	for i := range e.doc.Blocks {
		e.CheckBlockSpelling(&e.doc.Blocks[i])
	}
}

// IsMisspelled returns true if word is misspelled (cache only, queues unknown words)
// Use for rendering - non-blocking
func (e *Editor) IsMisspelled(word string) bool {
	if e.spellChecker == nil {
		return false
	}
	word = strings.ToLower(word)
	e.spellMu.RLock()
	misspelled, ok := e.misspelledWord[word]
	pending := e.spellPending[word]
	approved := e.userApproved[word]
	e.spellMu.RUnlock()
	if approved {
		Debug("spell: IsMisspelled %q -> false (user approved)", word)
		return false
	}
	if ok {
		Debug("spell: IsMisspelled cache hit %q -> %v", word, misspelled)
		return misspelled
	}
	if pending {
		Debug("spell: pending skip %q", word)
		return false
	}
	// not cached and not pending - queue for async check
	e.spellMu.Lock()
	e.spellPending[word] = true
	e.spellMu.Unlock()
	Debug("spell: queuing %q", word)
	e.spellChecker.Check(word)
	return false
}

// StartSpellResultWorker starts a goroutine that consumes spell results
// and requests re-renders when results arrive
func (e *Editor) StartSpellResultWorker(requestRender func()) {
	if e.spellChecker == nil {
		return
	}
	e.onSpellUpdate = requestRender
	go func() {
		for result := range e.spellChecker.Results() {
			word := strings.ToLower(result.Word)
			Debug("spell: result %q correct=%v", word, result.Correct)
			e.spellMu.Lock()
			// never overwrite user-approved words
			if e.userApproved[word] {
				Debug("spell: skipped %q (user approved)", word)
				delete(e.spellPending, word)
				e.spellMu.Unlock()
				continue
			}
			e.misspelledWord[word] = !result.Correct
			Debug("spell: set %q misspelled=%v", word, !result.Correct)
			delete(e.spellPending, word)
			e.spellMu.Unlock()
			// invalidate render cache so underlines get redrawn
			// (only if not already invalid - avoids redundant work)
			if e.cacheValid {
				e.InvalidateCache()
			}
			if e.onSpellUpdate != nil {
				e.onSpellUpdate()
			}
		}
	}()
}

// GetSuggestions returns spelling suggestions for a word
func (e *Editor) GetSuggestions(word string) []string {
	if e.syncSpellChecker == nil {
		return nil
	}
	Debug("spell: getting suggestions for %q", word)
	result := e.syncSpellChecker.Check(word)
	Debug("spell: got %d suggestions for %q (correct=%v)", len(result.Suggestions), word, result.Correct)
	return result.Suggestions
}

// NextMisspelled moves cursor to the next misspelled word
func (e *Editor) NextMisspelled() {
	if e.spellChecker == nil {
		return
	}

	// start from current position
	startBlock := e.cursor.Block
	startCol := e.cursor.Col

	// search forward through blocks
	for blockIdx := startBlock; blockIdx < len(e.doc.Blocks); blockIdx++ {
		b := &e.doc.Blocks[blockIdx]
		if b.Type != BlockParagraph && b.Type != BlockDialogue {
			continue
		}

		text := b.Text()
		runes := []rune(text)

		// start position for this block
		searchStart := 0
		if blockIdx == startBlock {
			// skip past current word if we're inside one
			searchStart = startCol
			for searchStart < len(runes) && isSpellWordChar(runes[searchStart]) {
				searchStart++
			}
		}

		// find words in this block
		i := searchStart
		for i < len(runes) {
			// skip non-word chars
			for i < len(runes) && !isSpellWordChar(runes[i]) {
				i++
			}
			wordStart := i
			// collect word
			for i < len(runes) && isSpellWordChar(runes[i]) {
				i++
			}
			if i > wordStart {
				word := string(runes[wordStart:i])
				if e.IsMisspelled(word) {
					e.moveCursor(Pos{Block: blockIdx, Col: wordStart})
					return
				}
			}
		}
	}

	// wrap around to beginning
	for blockIdx := 0; blockIdx <= startBlock; blockIdx++ {
		b := &e.doc.Blocks[blockIdx]
		if b.Type != BlockParagraph && b.Type != BlockDialogue {
			continue
		}

		text := b.Text()
		runes := []rune(text)

		endCol := len(runes)
		if blockIdx == startBlock {
			endCol = startCol
		}

		i := 0
		for i < endCol {
			for i < endCol && !isSpellWordChar(runes[i]) {
				i++
			}
			wordStart := i
			for i < endCol && isSpellWordChar(runes[i]) {
				i++
			}
			if i > wordStart {
				word := string(runes[wordStart:i])
				if e.IsMisspelled(word) {
					e.moveCursor(Pos{Block: blockIdx, Col: wordStart})
					return
				}
			}
		}
	}
}

// PrevMisspelled moves cursor to the previous misspelled word
func (e *Editor) PrevMisspelled() {
	if e.spellChecker == nil {
		return
	}

	startBlock := e.cursor.Block
	startCol := e.cursor.Col

	// search backward through blocks
	for blockIdx := startBlock; blockIdx >= 0; blockIdx-- {
		b := &e.doc.Blocks[blockIdx]
		if b.Type != BlockParagraph && b.Type != BlockDialogue {
			continue
		}

		text := b.Text()
		runes := []rune(text)

		// find all words and their positions
		var words []struct {
			start int
			end   int
			word  string
		}

		i := 0
		for i < len(runes) {
			for i < len(runes) && !isSpellWordChar(runes[i]) {
				i++
			}
			wordStart := i
			for i < len(runes) && isSpellWordChar(runes[i]) {
				i++
			}
			if i > wordStart {
				words = append(words, struct {
					start int
					end   int
					word  string
				}{wordStart, i, string(runes[wordStart:i])})
			}
		}

		// search backwards through words
		for j := len(words) - 1; j >= 0; j-- {
			w := words[j]
			// skip if cursor is at or inside this word
			if blockIdx == startBlock && w.start <= startCol && startCol < w.end {
				continue
			}
			// skip if word starts at or after cursor
			if blockIdx == startBlock && w.start >= startCol {
				continue
			}
			if e.IsMisspelled(w.word) {
				e.moveCursor(Pos{Block: blockIdx, Col: w.start})
				return
			}
		}
	}

	// wrap around to end
	for blockIdx := len(e.doc.Blocks) - 1; blockIdx >= startBlock; blockIdx-- {
		b := &e.doc.Blocks[blockIdx]
		if b.Type != BlockParagraph && b.Type != BlockDialogue {
			continue
		}

		text := b.Text()
		runes := []rune(text)

		var words []struct {
			start int
			end   int
			word  string
		}

		i := 0
		for i < len(runes) {
			for i < len(runes) && !isSpellWordChar(runes[i]) {
				i++
			}
			wordStart := i
			for i < len(runes) && isSpellWordChar(runes[i]) {
				i++
			}
			if i > wordStart {
				words = append(words, struct {
					start int
					end   int
					word  string
				}{wordStart, i, string(runes[wordStart:i])})
			}
		}

		for j := len(words) - 1; j >= 0; j-- {
			w := words[j]
			// skip if cursor is at or inside this word
			if blockIdx == startBlock && w.start <= startCol && startCol < w.end {
				continue
			}
			// skip if word starts at or after cursor
			if blockIdx == startBlock && w.start >= startCol {
				continue
			}
			if e.IsMisspelled(w.word) {
				e.moveCursor(Pos{Block: blockIdx, Col: w.start})
				return
			}
		}
	}
}

// AddWordToDictionary adds the word at cursor to personal dictionary
func (e *Editor) AddWordToDictionary() {
	if e.spellChecker == nil {
		return
	}
	word := e.wordAtCursor()
	if word == "" {
		return
	}
	lowerWord := strings.ToLower(word)
	if e.syncSpellChecker != nil {
		e.syncSpellChecker.AddWord(word)
	}
	// mark as user approved and correct in cache
	e.spellMu.Lock()
	e.userApproved[lowerWord] = true
	e.misspelledWord[lowerWord] = false
	e.spellMu.Unlock()
	e.InvalidateCache()
	Debug("zg: approved %q, userApproved map size: %d", lowerWord, len(e.userApproved))
}

// ReplaceWordAtCursor replaces the word under the cursor with new text
func (e *Editor) ReplaceWordAtCursor(newWord string) {
	b := e.CurrentBlock()
	if b == nil {
		return
	}
	text := b.Text()
	runes := []rune(text)
	col := e.cursor.Col

	if col >= len(runes) {
		col = len(runes) - 1
	}
	if col < 0 {
		return
	}

	// find word boundaries
	start := col
	for start > 0 && isSpellWordChar(runes[start-1]) {
		start--
	}
	end := col
	for end < len(runes) && isSpellWordChar(runes[end]) {
		end++
	}

	if start >= end {
		return
	}

	e.saveUndo()
	newRunes := append(runes[:start], append([]rune(newWord), runes[end:]...)...)
	b.Runs = []Run{{Text: string(newRunes)}}
	e.cursor.Col = start + len([]rune(newWord))
	e.InvalidateCache()
}

// wordAtCursor returns the word under the cursor
func (e *Editor) wordAtCursor() string {
	b := e.CurrentBlock()
	if b == nil {
		return ""
	}
	text := b.Text()
	runes := []rune(text)
	col := e.cursor.Col

	if col >= len(runes) {
		col = len(runes) - 1
	}
	if col < 0 {
		return ""
	}

	// find word boundaries
	start := col
	for start > 0 && isSpellWordChar(runes[start-1]) {
		start--
	}
	end := col
	for end < len(runes) && isSpellWordChar(runes[end]) {
		end++
	}

	if start >= end {
		return ""
	}
	return string(runes[start:end])
}

// isSpellWordChar returns true if r is part of a word (for spell checking)
// differs from isWordChar in that it excludes numbers/underscore and includes apostrophes
func isSpellWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\'' || r == '\u2019'
}

// extractWords extracts words from text for spell checking
func extractWords(text string) []string {
	var words []string
	runes := []rune(text)
	start := -1

	for i, r := range runes {
		if isSpellWordChar(r) {
			if start == -1 {
				start = i
			}
		} else {
			if start != -1 {
				word := strings.ToLower(string(runes[start:i]))
				// skip very short words and contractions
				if len(word) >= 2 {
					words = append(words, word)
				}
				start = -1
			}
		}
	}
	// handle word at end of text
	if start != -1 {
		word := strings.ToLower(string(runes[start:]))
		if len(word) >= 2 {
			words = append(words, word)
		}
	}

	return words
}

