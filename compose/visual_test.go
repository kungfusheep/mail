package compose

import (
	"strings"
	"testing"

	"github.com/kungfusheep/glyph"
)

func TestSelectVisualRange(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "Hello world, this is a test paragraph.", Style: StyleNone}},
			},
		},
	}

	ed := NewEditor(doc, "")

	// put cursor in the middle of the block
	ed.cursor = Pos{Block: 0, Col: 15}

	// enter visual mode
	ed.EnterVisual()

	// verify initial visualStart is at cursor
	if ed.visualStart.Col != 15 {
		t.Errorf("after EnterVisual: visualStart.Col = %d, want 15", ed.visualStart.Col)
	}

	// now simulate vap - select around paragraph
	r := ed.AParagraph()

	// verify the range returned by AParagraph
	if r.Start.Col != 0 {
		t.Errorf("AParagraph Start.Col = %d, want 0", r.Start.Col)
	}
	blockLen := doc.Blocks[0].Length()
	if r.End.Col != blockLen {
		t.Errorf("AParagraph End.Col = %d, want %d", r.End.Col, blockLen)
	}

	// call SelectVisualRange
	ed.SelectVisualRange(r)

	// verify visualStart was updated
	if ed.visualStart.Col != 0 {
		t.Errorf("after SelectVisualRange: visualStart.Col = %d, want 0", ed.visualStart.Col)
	}

	// verify cursor was set to end-1
	expectedCursor := blockLen - 1
	if ed.cursor.Col != expectedCursor {
		t.Errorf("after SelectVisualRange: cursor.Col = %d, want %d", ed.cursor.Col, expectedCursor)
	}

	// get the visual range that would be used for rendering
	vr := ed.VisualRange()

	// verify the visual range covers the entire block
	if vr.Start.Col != 0 {
		t.Errorf("VisualRange Start.Col = %d, want 0", vr.Start.Col)
	}
	if vr.End.Col != blockLen {
		t.Errorf("VisualRange End.Col = %d, want %d", vr.End.Col, blockLen)
	}

	// verify VisualSelectionInBlock returns correct range
	selStart, selEnd, hasSel := ed.VisualSelectionInBlock(0)
	if !hasSel {
		t.Error("VisualSelectionInBlock returned hasSel=false")
	}
	if selStart != 0 {
		t.Errorf("VisualSelectionInBlock selStart = %d, want 0", selStart)
	}
	if selEnd != blockLen {
		t.Errorf("VisualSelectionInBlock selEnd = %d, want %d", selEnd, blockLen)
	}
}

func TestApplySelectionHighlight(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "Hello world, this is a test.", Style: StyleNone}},
			},
		},
	}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.layer = glyph.NewLayer()

	// enter visual mode and select the entire paragraph
	ed.cursor = Pos{Block: 0, Col: 10}
	ed.EnterVisual()
	r := ed.InnerParagraph()
	ed.SelectVisualRange(r)

	// render the block and wrap (mimics UpdateDisplay flow)
	lines := ed.renderBlock(&doc.Blocks[0], 0)
	var wrappedLines [][]glyph.Span
	for _, line := range lines {
		wrapped := wrapSpans(line, contentWidth)
		wrappedLines = append(wrappedLines, wrapped...)
	}

	// apply selection highlight after wrapping (as UpdateDisplay does)
	if selStart, selEnd, hasSel := ed.VisualSelectionInBlock(0); hasSel {
		prefixLen := ed.blockPrefixLength(&doc.Blocks[0])
		offset := 0
		for j := range wrappedLines {
			lineLen := 0
			for _, span := range wrappedLines[j] {
				lineLen += len([]rune(span.Text))
			}
			effectivePrefixLen := 0
			if j == 0 {
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
				wrappedLines[j] = ed.applySelectionHighlight(wrappedLines[j], lineSelStart+effectivePrefixLen, lineSelEnd+effectivePrefixLen)
			}
			offset += contentLen
		}
	}

	// verify we have at least one line
	if len(wrappedLines) == 0 {
		t.Fatal("wrappedLines is empty")
	}

	// count how many spans have inverse style (selection)
	inverseCount := 0
	totalLen := 0
	for _, span := range wrappedLines[0] {
		totalLen += len([]rune(span.Text))
		t.Logf("span: %q, style attr: %d", span.Text, span.Style.Attr)
		if span.Style.Attr != 0 {
			inverseCount += len([]rune(span.Text))
		}
	}

	blockLen := doc.Blocks[0].Length()
	t.Logf("totalLen=%d, blockLen=%d, inverseCount=%d", totalLen, blockLen, inverseCount)

	// the entire text should be selected (inverse)
	if inverseCount != blockLen {
		t.Errorf("expected %d chars to be inverted, got %d", blockLen, inverseCount)
	}
}

func TestApplySelectionHighlightWithWrapping(t *testing.T) {
	// use a long text that will wrap
	longText := "Terminal user interfaces represent a unique intersection of simplicity and power in the computing world. Unlike graphical applications, they operate within the constraints of text-based display, requiring careful consideration of character-based rendering."

	doc := &Document{
		Blocks: []Block{
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: longText, Style: StyleNone}},
			},
		},
	}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.layer = glyph.NewLayer()

	// put cursor somewhere in the middle
	ed.cursor = Pos{Block: 0, Col: 120}
	ed.EnterVisual()
	r := ed.InnerParagraph()
	ed.SelectVisualRange(r)

	// render and wrap (mimics UpdateDisplay flow)
	lines := ed.renderBlock(&doc.Blocks[0], 0)
	var wrappedLines [][]glyph.Span
	for _, line := range lines {
		wrapped := wrapSpans(line, contentWidth)
		wrappedLines = append(wrappedLines, wrapped...)
	}

	// apply selection highlight after wrapping
	blockLen := doc.Blocks[0].Length()
	if selStart, selEnd, hasSel := ed.VisualSelectionInBlock(0); hasSel {
		prefixLen := ed.blockPrefixLength(&doc.Blocks[0])
		offset := 0
		for j := range wrappedLines {
			lineLen := 0
			for _, span := range wrappedLines[j] {
				lineLen += len([]rune(span.Text))
			}
			effectivePrefixLen := 0
			if j == 0 {
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
				wrappedLines[j] = ed.applySelectionHighlight(wrappedLines[j], lineSelStart+effectivePrefixLen, lineSelEnd+effectivePrefixLen)
			}
			offset += contentLen
		}
	}

	t.Logf("after wrapping: %d visual lines", len(wrappedLines))

	// count selected chars across all wrapped lines
	totalInverse := 0
	for i, line := range wrappedLines {
		lineInverse := 0
		for _, span := range line {
			if span.Style.Attr != 0 {
				lineInverse += len([]rune(span.Text))
			}
		}
		t.Logf("  line %d: %d inverse chars", i, lineInverse)
		totalInverse += lineInverse
	}

	if totalInverse != blockLen {
		t.Errorf("after wrap: expected %d total inverse chars, got %d", blockLen, totalInverse)
	}
}

func TestApplySelectionHighlightMultiline(t *testing.T) {
	// test a block with embedded newlines
	multilineText := "First line\nSecond line\nThird line"

	doc := &Document{
		Blocks: []Block{
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: multilineText, Style: StyleNone}},
			},
		},
	}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.layer = glyph.NewLayer()

	// enter visual mode and select entire block
	ed.cursor = Pos{Block: 0, Col: 15} // somewhere in second line
	ed.EnterVisual()
	r := ed.InnerParagraph()
	ed.SelectVisualRange(r)

	// render and wrap
	lines := ed.renderBlock(&doc.Blocks[0], 0)
	var wrappedLines [][]glyph.Span
	for _, line := range lines {
		wrapped := wrapSpans(line, contentWidth)
		wrappedLines = append(wrappedLines, wrapped...)
	}

	// we should have 3 lines (split on \n)
	if len(wrappedLines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(wrappedLines))
	}

	// apply selection highlight after wrapping
	blockLen := doc.Blocks[0].Length()
	if selStart, selEnd, hasSel := ed.VisualSelectionInBlock(0); hasSel {
		prefixLen := ed.blockPrefixLength(&doc.Blocks[0])
		offset := 0
		for j := range wrappedLines {
			lineLen := 0
			for _, span := range wrappedLines[j] {
				lineLen += len([]rune(span.Text))
			}
			effectivePrefixLen := 0
			if j == 0 {
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
				wrappedLines[j] = ed.applySelectionHighlight(wrappedLines[j], lineSelStart+effectivePrefixLen, lineSelEnd+effectivePrefixLen)
			}
			offset += contentLen
		}
	}

	// count inverse chars in each line
	totalInverse := 0
	for i, line := range wrappedLines {
		lineInverse := 0
		for _, span := range line {
			if span.Style.Attr != 0 {
				lineInverse += len([]rune(span.Text))
			}
		}
		t.Logf("line %d: %d inverse chars", i, lineInverse)
		totalInverse += lineInverse
	}

	// total selected should equal text length minus newlines
	textLenWithoutNewlines := len([]rune("First line")) + len([]rune("Second line")) + len([]rune("Third line"))
	t.Logf("blockLen=%d, totalInverse=%d, textWithoutNewlines=%d", blockLen, totalInverse, textLenWithoutNewlines)

	if totalInverse != textLenWithoutNewlines {
		t.Errorf("expected %d inverse chars, got %d", textLenWithoutNewlines, totalInverse)
	}
}

func TestSelectVisualRangeInnerParagraph(t *testing.T) {
	// consecutive non-empty blocks should be treated as one paragraph
	doc := &Document{
		Blocks: []Block{
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "First line of paragraph.", Style: StyleNone}},
			},
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "Second line of same paragraph.", Style: StyleNone}},
			},
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "", Style: StyleNone}}, // empty block = paragraph break
			},
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "Different paragraph.", Style: StyleNone}},
			},
		},
	}

	ed := NewEditor(doc, "")

	// put cursor in block 1 (second line of first paragraph)
	ed.cursor = Pos{Block: 1, Col: 10}

	// enter visual mode and select inner paragraph
	ed.EnterVisual()
	r := ed.InnerParagraph()
	ed.SelectVisualRange(r)

	// verify selection covers blocks 0-1 (the whole first paragraph)
	if ed.visualStart.Block != 0 || ed.visualStart.Col != 0 {
		t.Errorf("visualStart = {%d, %d}, want {0, 0}", ed.visualStart.Block, ed.visualStart.Col)
	}

	vr := ed.VisualRange()

	if vr.Start.Block != 0 || vr.Start.Col != 0 {
		t.Errorf("VisualRange Start = {%d, %d}, want {0, 0}", vr.Start.Block, vr.Start.Col)
	}
	// should end at block 1 (last non-empty before the empty block)
	block1Len := doc.Blocks[1].Length()
	if vr.End.Block != 1 || vr.End.Col != block1Len {
		t.Errorf("VisualRange End = {%d, %d}, want {1, %d}", vr.End.Block, vr.End.Col, block1Len)
	}
}

func TestVisualLineDownStep(t *testing.T) {
	// test step-by-step movement with j
	blocks := []Block{
		{Type: BlockH1, Runs: []Run{{Text: "Chapter 1", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "First paragraph with some text.", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "", Style: StyleNone}}}, // empty
		{Type: BlockParagraph, Runs: []Run{{Text: "Second paragraph.", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "Third paragraph.", Style: StyleNone}}},
	}
	doc := &Document{Blocks: blocks}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	// start at block 0
	ed.cursor = Pos{Block: 0, Col: 0}

	// press j repeatedly and track where cursor goes
	for i := 0; i < 10; i++ {
		prevBlock := ed.cursor.Block
		prevCol := ed.cursor.Col
		ed.Down(1)
		t.Logf("step %d: cursor moved from {%d,%d} to {%d,%d}",
			i, prevBlock, prevCol, ed.cursor.Block, ed.cursor.Col)

		// if cursor didn't move at all, that's a problem
		if ed.cursor.Block == prevBlock && ed.cursor.Col == prevCol {
			// check if we're at the last block
			if ed.cursor.Block < len(blocks)-1 {
				t.Errorf("cursor stuck at block %d but there are %d blocks",
					ed.cursor.Block, len(blocks))
			}
		}
	}

	// should reach the last block
	if ed.cursor.Block != len(blocks)-1 {
		t.Errorf("cursor.Block = %d, want %d", ed.cursor.Block, len(blocks)-1)
	}
}

func TestScrollToBottom(t *testing.T) {
	// create a document with many blocks
	var blocks []Block
	for i := 0; i < 50; i++ {
		blocks = append(blocks, Block{
			Type: BlockParagraph,
			Runs: []Run{{Text: "Line " + string(rune('0'+i%10)), Style: StyleNone}},
		})
	}
	doc := &Document{Blocks: blocks}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	// simulate moving down many times
	for i := 0; i < 100; i++ {
		ed.Down(1)
	}

	// cursor should be at the last block
	if ed.cursor.Block != len(blocks)-1 {
		t.Errorf("after 100 Down(): cursor.Block = %d, want %d", ed.cursor.Block, len(blocks)-1)
	}

	// test that G also works
	ed.cursor = Pos{Block: 0, Col: 0}
	ed.DocEnd()
	if ed.cursor.Block != len(blocks)-1 {
		t.Errorf("after DocEnd(): cursor.Block = %d, want %d", ed.cursor.Block, len(blocks)-1)
	}
}

func TestScrollToBottomWithUpdates(t *testing.T) {
	// test scroll behavior with UpdateDisplay calls (like real usage)
	// use a realistic document structure with headings and paragraphs
	blocks := []Block{
		{Type: BlockH1, Runs: []Run{{Text: "Chapter 1: Introduction to Terminal UIs", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "Terminal user interfaces have experienced a renaissance in recent years. Developers are rediscovering the power of text-based applications.", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "", Style: StyleNone}}}, // empty paragraph
		{Type: BlockParagraph, Runs: []Run{{Text: "Unlike graphical applications, terminal apps work over SSH, consume minimal resources, and can be automated through scripting.", Style: StyleNone}}},
		{Type: BlockH2, Runs: []Run{{Text: "Why Build Terminal Apps?", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "There are many reasons to build terminal applications. They are fast, efficient, and can be used anywhere.", Style: StyleNone}}},
	}
	// Add more content
	for i := 0; i < 20; i++ {
		blocks = append(blocks, Block{
			Type: BlockParagraph,
			Runs: []Run{{Text: "This is paragraph number " + string(rune('0'+i%10)) + " with some additional text to make it longer and possibly wrap.", Style: StyleNone}},
		})
	}
	doc := &Document{Blocks: blocks}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	// initial display
	ed.UpdateDisplay()

	// simulate pressing j many times with display updates
	for i := 0; i < 100; i++ {
		ed.Down(1)
		ed.UpdateDisplay()
	}

	// cursor should be at the last block
	if ed.cursor.Block != len(blocks)-1 {
		t.Errorf("cursor.Block = %d, want %d", ed.cursor.Block, len(blocks)-1)
	}

	// topLine should be positioned to show the last line
	totalLines := 0
	for i := range ed.blockLines {
		totalLines = ed.blockLines[i].screenLine + ed.blockLines[i].lineCount
	}
	t.Logf("totalLines=%d, topLine=%d, screenHeight=%d", totalLines, ed.topLine, ed.screenHeight)

	// the last line should be visible (topLine + screenHeight >= totalLines)
	if ed.topLine+ed.screenHeight < totalLines {
		t.Errorf("last line not visible: topLine=%d, screenHeight=%d, totalLines=%d",
			ed.topLine, ed.screenHeight, totalLines)
	}
}

func TestSelectVisualRangeMultiBlockParagraph(t *testing.T) {
	// test that cursor in any part of multi-block paragraph selects all of it
	doc := &Document{
		Blocks: []Block{
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "Line one.", Style: StyleNone}},
			},
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "Line two.", Style: StyleNone}},
			},
			{
				Type: BlockParagraph,
				Runs: []Run{{Text: "Line three.", Style: StyleNone}},
			},
		},
	}

	ed := NewEditor(doc, "")

	// cursor on first block
	ed.cursor = Pos{Block: 0, Col: 5}
	r := ed.InnerParagraph()
	if r.Start.Block != 0 || r.End.Block != 2 {
		t.Errorf("from block 0: got range {%d,%d} to {%d,%d}, want blocks 0-2",
			r.Start.Block, r.Start.Col, r.End.Block, r.End.Col)
	}

	// cursor on middle block
	ed.cursor = Pos{Block: 1, Col: 5}
	r = ed.InnerParagraph()
	if r.Start.Block != 0 || r.End.Block != 2 {
		t.Errorf("from block 1: got range {%d,%d} to {%d,%d}, want blocks 0-2",
			r.Start.Block, r.Start.Col, r.End.Block, r.End.Col)
	}

	// cursor on last block
	ed.cursor = Pos{Block: 2, Col: 5}
	r = ed.InnerParagraph()
	if r.Start.Block != 0 || r.End.Block != 2 {
		t.Errorf("from block 2: got range {%d,%d} to {%d,%d}, want blocks 0-2",
			r.Start.Block, r.Start.Col, r.End.Block, r.End.Col)
	}
}

func TestScrollWithJStepByStep(t *testing.T) {
	// test scroll behavior step by step with j, checking topLine changes
	// use many short blocks so we can see scroll happening
	var blocks []Block
	for i := 0; i < 50; i++ {
		blocks = append(blocks, Block{
			Type: BlockParagraph,
			Runs: []Run{{Text: "Line " + string(rune('A'+i%26)), Style: StyleNone}},
		})
	}
	doc := &Document{Blocks: blocks}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 10 // small screen to trigger scrolling early

	// initial display to populate blockLines
	ed.UpdateDisplay()

	t.Logf("initial: cursor.Block=%d, topLine=%d", ed.cursor.Block, ed.topLine)

	// track when scrolling happens
	scrollEvents := 0
	prevTopLine := ed.topLine

	// press j many times
	for i := 0; i < 40; i++ {
		ed.Down(1)
		ed.UpdateDisplay()

		if ed.topLine != prevTopLine {
			scrollEvents++
			t.Logf("scroll at step %d: cursor.Block=%d, topLine=%d (was %d)",
				i, ed.cursor.Block, ed.topLine, prevTopLine)
			prevTopLine = ed.topLine
		}
	}

	t.Logf("final: cursor.Block=%d, topLine=%d, scrollEvents=%d",
		ed.cursor.Block, ed.topLine, scrollEvents)

	// we should have scrolled multiple times
	if scrollEvents == 0 {
		t.Errorf("expected scroll events, got none. Final topLine=%d", ed.topLine)
	}

	// cursor should have moved to block 40 (or close to it)
	if ed.cursor.Block < 30 {
		t.Errorf("cursor.Block = %d, expected >= 30", ed.cursor.Block)
	}

	// topLine should have increased to keep cursor visible
	if ed.topLine < 20 {
		t.Errorf("topLine = %d, expected >= 20 to keep cursor in view", ed.topLine)
	}
}

func TestScrollWithoutInitialUpdate(t *testing.T) {
	// test scroll behavior when pressing j without initial UpdateDisplay
	// this simulates the real app where first key press may happen before display
	var blocks []Block
	for i := 0; i < 50; i++ {
		blocks = append(blocks, Block{
			Type: BlockParagraph,
			Runs: []Run{{Text: "Line " + string(rune('A'+i%26)), Style: StyleNone}},
		})
	}
	doc := &Document{Blocks: blocks}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 10

	// NO initial UpdateDisplay - this is the key difference
	// blockLines will be empty/stale

	// press j a few times
	for i := 0; i < 15; i++ {
		ed.Down(1)
		// simulate real app: UpdateDisplay after each key
		ed.UpdateDisplay()
	}

	t.Logf("after 15 moves: cursor.Block=%d, topLine=%d", ed.cursor.Block, ed.topLine)

	// cursor should be at block 15
	if ed.cursor.Block != 15 {
		t.Errorf("cursor.Block = %d, want 15", ed.cursor.Block)
	}

	// topLine should have scrolled to keep cursor visible
	// with screenHeight=10 and scrollMargin, cursor at block 15 should require scroll
	if ed.topLine == 0 {
		t.Errorf("expected scroll but topLine=0")
	}
}

func TestFindMatchingPairForwardSearch(t *testing.T) {
	// vim behavior: if not inside a pair, search forward on the line
	tests := []struct {
		name      string
		text      string
		cursorCol int
		open      rune
		close     rune
		wantStart int
		wantEnd   int
		wantFound bool
	}{
		// quotes - cursor before quoted string
		{"quote: cursor before pair", `hello "world" there`, 0, '"', '"', 6, 12, true},
		{"quote: cursor inside pair", `hello "world" there`, 8, '"', '"', 6, 12, true},
		{"quote: cursor on opening quote", `hello "world" there`, 6, '"', '"', 6, 12, true},
		{"quote: cursor after all quotes", `hello "world" there`, 15, '"', '"', 0, 0, false},
		{"quote: multiple pairs, cursor before first", `"one" and "two"`, 0, '"', '"', 0, 4, true},
		{"quote: multiple pairs, cursor between", `"one" and "two"`, 7, '"', '"', 10, 14, true},

		// brackets - cursor before bracketed content
		{"paren: cursor before pair", `foo (bar) baz`, 0, '(', ')', 4, 8, true},
		{"paren: cursor inside pair", `foo (bar) baz`, 6, '(', ')', 4, 8, true},
		{"paren: nested, cursor before outer", `foo ((inner)) baz`, 0, '(', ')', 4, 12, true},
		{"paren: nested, cursor inside inner", `foo ((inner)) baz`, 6, '(', ')', 5, 11, true},
		{"paren: cursor after all parens", `foo (bar) baz`, 12, '(', ')', 0, 0, false},

		// square brackets
		{"square: cursor before pair", `array[index] here`, 0, '[', ']', 5, 11, true},
		{"square: cursor inside", `array[index] here`, 7, '[', ']', 5, 11, true},

		// curly braces
		{"curly: cursor before pair", `map{key} here`, 0, '{', '}', 3, 7, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &Document{
				Blocks: []Block{
					{Type: BlockParagraph, Runs: []Run{{Text: tt.text, Style: StyleNone}}},
				},
			}
			ed := NewEditor(doc, "")
			ed.cursor = Pos{Block: 0, Col: tt.cursorCol}

			start, end, found := ed.findMatchingPair(tt.open, tt.close)

			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
			if found && tt.wantFound {
				if start != tt.wantStart || end != tt.wantEnd {
					t.Errorf("got range [%d, %d], want [%d, %d]", start, end, tt.wantStart, tt.wantEnd)
				}
			}
		})
	}
}

func TestSameLevelSectionNavigation(t *testing.T) {
	// document structure:
	// 0: # Chapter 1 (H1)
	// 1: Intro text
	// 2: ## Section 1.1 (H2)
	// 3: Section 1.1 content
	// 4: ### Subsection (H3)
	// 5: Subsection content
	// 6: ## Section 1.2 (H2)
	// 7: Section 1.2 content
	// 8: # Chapter 2 (H1)
	// 9: Chapter 2 content
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH1, Runs: []Run{{Text: "Chapter 1", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Intro text", Style: StyleNone}}},
			{Type: BlockH2, Runs: []Run{{Text: "Section 1.1", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Section 1.1 content", Style: StyleNone}}},
			{Type: BlockH3, Runs: []Run{{Text: "Subsection", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Subsection content", Style: StyleNone}}},
			{Type: BlockH2, Runs: []Run{{Text: "Section 1.2", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Section 1.2 content", Style: StyleNone}}},
			{Type: BlockH1, Runs: []Run{{Text: "Chapter 2", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Chapter 2 content", Style: StyleNone}}},
		},
	}

	t.Run("]S from H2 skips H3 to next H2", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 2, Col: 0} // on "## Section 1.1"

		ed.NextSameLevel()
		// should skip H3 (block 4) and land on H2 (block 6)
		if ed.cursor.Block != 6 {
			t.Errorf("]S from H2: cursor.Block = %d, want 6", ed.cursor.Block)
		}
	})

	t.Run("]S from H1 skips H2/H3 to next H1", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 0} // on "# Chapter 1"

		ed.NextSameLevel()
		// should skip H2s and H3 and land on H1 (block 8)
		if ed.cursor.Block != 8 {
			t.Errorf("]S from H1: cursor.Block = %d, want 8", ed.cursor.Block)
		}
	})

	t.Run("[S from H2 goes to previous H2", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 6, Col: 0} // on "## Section 1.2"

		ed.PrevSameLevel()
		// should land on previous H2 (block 2)
		if ed.cursor.Block != 2 {
			t.Errorf("[S from H2: cursor.Block = %d, want 2", ed.cursor.Block)
		}
	})

	t.Run("]S from body uses section's heading level", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 3, Col: 0} // in Section 1.1 content (under H2)

		ed.NextSameLevel()
		// section is H2, should jump to next H2 (block 6)
		if ed.cursor.Block != 6 {
			t.Errorf("]S from body: cursor.Block = %d, want 6", ed.cursor.Block)
		}
	})
}

func TestQuoteMatcherNavigation(t *testing.T) {
	// test that ]" and [" navigate between quoted strings
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: `first "one" then "two" finally`, Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: `another "three" here`, Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")

	// start at beginning
	ed.cursor = Pos{Block: 0, Col: 0}

	// make the quote matcher
	quoteMatcher := makeQuoteMatcher('"')

	// ]" should jump to first quote
	ed.NextTextMatching(quoteMatcher)
	if ed.cursor.Block != 0 || ed.cursor.Col != 6 {
		t.Errorf("first ]\" = {%d, %d}, want {0, 6}", ed.cursor.Block, ed.cursor.Col)
	}

	// ]" again should jump to second quote
	ed.NextTextMatching(quoteMatcher)
	if ed.cursor.Block != 0 || ed.cursor.Col != 17 {
		t.Errorf("second ]\" = {%d, %d}, want {0, 17}", ed.cursor.Block, ed.cursor.Col)
	}

	// ]" again should jump to quote in next block
	ed.NextTextMatching(quoteMatcher)
	if ed.cursor.Block != 1 || ed.cursor.Col != 8 {
		t.Errorf("third ]\" = {%d, %d}, want {1, 8}", ed.cursor.Block, ed.cursor.Col)
	}

	// [" should go back
	ed.PrevTextMatching(quoteMatcher)
	if ed.cursor.Block != 0 || ed.cursor.Col != 17 {
		t.Errorf("first [\" = {%d, %d}, want {0, 17}", ed.cursor.Block, ed.cursor.Col)
	}
}

func TestSectionTextObjects(t *testing.T) {
	// document structure:
	// 0: # Chapter 1 (H1)
	// 1: Intro text
	// 2: ## Section 1.1 (H2)
	// 3: Section 1.1 content
	// 4: ## Section 1.2 (H2)
	// 5: Section 1.2 content
	// 6: # Chapter 2 (H1)
	// 7: Chapter 2 content
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH1, Runs: []Run{{Text: "Chapter 1", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Intro text", Style: StyleNone}}},
			{Type: BlockH2, Runs: []Run{{Text: "Section 1.1", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Section 1.1 content", Style: StyleNone}}},
			{Type: BlockH2, Runs: []Run{{Text: "Section 1.2", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Section 1.2 content", Style: StyleNone}}},
			{Type: BlockH1, Runs: []Run{{Text: "Chapter 2", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Chapter 2 content", Style: StyleNone}}},
		},
	}

	t.Run("iS on H1 heading", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 0} // on "# Chapter 1"

		r := ed.InnerSection()
		// should select content after heading until next H1 (blocks 1-5)
		if r.Start.Block != 1 {
			t.Errorf("iS on H1: Start.Block = %d, want 1", r.Start.Block)
		}
		if r.End.Block != 5 {
			t.Errorf("iS on H1: End.Block = %d, want 5", r.End.Block)
		}
	})

	t.Run("aS on H1 heading", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 0} // on "# Chapter 1"

		r := ed.ASection()
		// should select heading + all content until next H1 (blocks 0-5)
		if r.Start.Block != 0 {
			t.Errorf("aS on H1: Start.Block = %d, want 0", r.Start.Block)
		}
		if r.End.Block != 5 {
			t.Errorf("aS on H1: End.Block = %d, want 5", r.End.Block)
		}
	})

	t.Run("iS on H2 heading", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 2, Col: 0} // on "## Section 1.1"

		r := ed.InnerSection()
		// should select content after heading until next H2 or higher (block 3 only)
		if r.Start.Block != 3 {
			t.Errorf("iS on H2: Start.Block = %d, want 3", r.Start.Block)
		}
		if r.End.Block != 3 {
			t.Errorf("iS on H2: End.Block = %d, want 3", r.End.Block)
		}
	})

	t.Run("aS on H2 heading", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 2, Col: 0} // on "## Section 1.1"

		r := ed.ASection()
		// should select heading + content until next H2 (blocks 2-3)
		if r.Start.Block != 2 {
			t.Errorf("aS on H2: Start.Block = %d, want 2", r.Start.Block)
		}
		if r.End.Block != 3 {
			t.Errorf("aS on H2: End.Block = %d, want 3", r.End.Block)
		}
	})

	t.Run("iS in body (intro)", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 1, Col: 0} // on "Intro text"

		r := ed.InnerSection()
		// should select immediate content until first sub-heading (block 1 only)
		if r.Start.Block != 1 {
			t.Errorf("iS in body: Start.Block = %d, want 1", r.Start.Block)
		}
		if r.End.Block != 1 {
			t.Errorf("iS in body: End.Block = %d, want 1", r.End.Block)
		}
	})

	t.Run("aS in body (intro)", func(t *testing.T) {
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 1, Col: 0} // on "Intro text"

		r := ed.ASection()
		// should select entire section including heading (blocks 0-5, until Chapter 2)
		if r.Start.Block != 0 {
			t.Errorf("aS in body: Start.Block = %d, want 0", r.Start.Block)
		}
		if r.End.Block != 5 {
			t.Errorf("aS in body: End.Block = %d, want 5", r.End.Block)
		}
	})
}

func TestClauseTextObjects(t *testing.T) {
	mkDoc := func(text string) *Document {
		return &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: text, Style: StyleNone}}},
			},
		}
	}

	t.Run("i, middle clause between commas", func(t *testing.T) {
		doc := mkDoc("The quick brown fox, who was very lazy, jumped over the fence.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 25} // on 'v' in "very"

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "who was very lazy" {
			t.Errorf("i, middle clause: got %q, want %q", got, "who was very lazy")
		}
	})

	t.Run("i, first clause (no leading delimiter)", func(t *testing.T) {
		doc := mkDoc("The quick brown fox, who was very lazy, jumped.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 5} // on 'u' in "quick"

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "The quick brown fox" {
			t.Errorf("i, first clause: got %q, want %q", got, "The quick brown fox")
		}
	})

	t.Run("i, last clause before sentence end", func(t *testing.T) {
		doc := mkDoc("The quick brown fox, who was very lazy, jumped over the fence.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 45} // on 'o' in "over"

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "jumped over the fence" {
			t.Errorf("i, last clause: got %q, want %q", got, "jumped over the fence")
		}
	})

	t.Run("i, semicolon delimiter", func(t *testing.T) {
		doc := mkDoc("The sun set; darkness fell over the valley.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 15} // on 'k' in "darkness"

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "darkness fell over the valley" {
			t.Errorf("i, semicolon: got %q, want %q", got, "darkness fell over the valley")
		}
	})

	t.Run("i, colon delimiter", func(t *testing.T) {
		doc := mkDoc("He had one rule: never look back.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 20} // on 'l' in "look"

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "never look back" {
			t.Errorf("i, colon: got %q, want %q", got, "never look back")
		}
	})

	t.Run("i, em-dash delimiter", func(t *testing.T) {
		doc := mkDoc("The fox\u2014who was hungry\u2014jumped over the dog.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 12} // on 'w' in "was"

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "who was hungry" {
			t.Errorf("i, em-dash: got %q, want %q", got, "who was hungry")
		}
	})

	t.Run("i, en-dash delimiter", func(t *testing.T) {
		doc := mkDoc("The fox\u2013who was hungry\u2013jumped over the dog.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 12}

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "who was hungry" {
			t.Errorf("i, en-dash: got %q, want %q", got, "who was hungry")
		}
	})

	t.Run("i, no delimiters returns whole text", func(t *testing.T) {
		doc := mkDoc("Just a simple sentence")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 7}

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "Just a simple sentence" {
			t.Errorf("i, no delimiters: got %q, want %q", got, "Just a simple sentence")
		}
	})

	t.Run("i, cursor on delimiter selects clause before", func(t *testing.T) {
		doc := mkDoc("The quick brown fox, who was lazy, jumped.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 19} // on the first comma

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "The quick brown fox" {
			t.Errorf("i, cursor on delimiter: got %q, want %q", got, "The quick brown fox")
		}
	})

	t.Run("i, empty block", func(t *testing.T) {
		doc := mkDoc("")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 0}

		r := ed.InnerClause()
		if r.Start != r.End {
			t.Errorf("i, empty block: expected empty range, got %v-%v", r.Start, r.End)
		}
	})

	t.Run("i, mixed delimiters", func(t *testing.T) {
		doc := mkDoc("first, second; third: fourth.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 16} // on 'h' in "third"

		r := ed.InnerClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "third" {
			t.Errorf("i, mixed: got %q, want %q", got, "third")
		}
	})

	// a, tests
	t.Run("a, middle clause grabs trailing delimiter + space", func(t *testing.T) {
		doc := mkDoc("The quick brown fox, who was very lazy, jumped over the fence.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 25}

		r := ed.AClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "who was very lazy, " {
			t.Errorf("a, middle: got %q, want %q", got, "who was very lazy, ")
		}
	})

	t.Run("a, first clause grabs trailing delimiter + space", func(t *testing.T) {
		doc := mkDoc("The quick brown fox, who was lazy, jumped.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 5}

		r := ed.AClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "The quick brown fox, " {
			t.Errorf("a, first: got %q, want %q", got, "The quick brown fox, ")
		}
	})

	t.Run("a, last clause grabs leading delimiter", func(t *testing.T) {
		doc := mkDoc("The quick brown fox, jumped over the fence.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 25} // on 'o' in "over"

		r := ed.AClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != ", jumped over the fence" {
			t.Errorf("a, last: got %q, want %q", got, ", jumped over the fence")
		}
	})

	t.Run("a, em-dash grabs trailing delimiter", func(t *testing.T) {
		doc := mkDoc("The fox\u2014who was hungry\u2014jumped.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 12}

		r := ed.AClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "who was hungry\u2014" {
			t.Errorf("a, em-dash: got %q, want %q", got, "who was hungry\u2014")
		}
	})

	t.Run("a, no delimiters returns whole text", func(t *testing.T) {
		doc := mkDoc("Just a simple sentence")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 7}

		r := ed.AClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "Just a simple sentence" {
			t.Errorf("a, no delimiters: got %q, want %q", got, "Just a simple sentence")
		}
	})

	t.Run("a, semicolon trailing", func(t *testing.T) {
		doc := mkDoc("The sun set; darkness fell.")
		ed := NewEditor(doc, "")
		ed.cursor = Pos{Block: 0, Col: 5} // on 'u' in "sun"

		r := ed.AClause()
		got := string([]rune(doc.Blocks[0].Text())[r.Start.Col:r.End.Col])
		if got != "The sun set; " {
			t.Errorf("a, semicolon trailing: got %q, want %q", got, "The sun set; ")
		}
	})
}

func TestBracketMatcherNavigation(t *testing.T) {
	// test that ]( and [( navigate between parenthesized content
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: `call foo(a, b) and bar(c)`, Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: `nested ((inner)) here`, Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")

	// start at beginning
	ed.cursor = Pos{Block: 0, Col: 0}

	// make the bracket matcher
	parenMatcher := makeBracketMatcher('(', ')')

	// ]( should jump to first paren
	ed.NextTextMatching(parenMatcher)
	if ed.cursor.Block != 0 || ed.cursor.Col != 8 {
		t.Errorf("first ]( = {%d, %d}, want {0, 8}", ed.cursor.Block, ed.cursor.Col)
	}

	// ]( again should jump to second paren
	ed.NextTextMatching(parenMatcher)
	if ed.cursor.Block != 0 || ed.cursor.Col != 22 {
		t.Errorf("second ]( = {%d, %d}, want {0, 22}", ed.cursor.Block, ed.cursor.Col)
	}

	// ]( again should jump to outer paren in next block (nested)
	ed.NextTextMatching(parenMatcher)
	if ed.cursor.Block != 1 || ed.cursor.Col != 7 {
		t.Errorf("third ]( = {%d, %d}, want {1, 7}", ed.cursor.Block, ed.cursor.Col)
	}
}

func TestTextObjectForwardSearchIntegration(t *testing.T) {
	// test that di", ci(, etc. work when cursor is before the pair
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: `set value = "hello world" here`, Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")

	// cursor at start of line, before the quotes
	ed.cursor = Pos{Block: 0, Col: 0}

	// di" should find and delete inside the quotes
	r := ed.InnerQuote('"')
	if r.Start.Col == r.End.Col {
		t.Fatal("InnerQuote returned empty range when cursor before quotes")
	}

	// verify the range is correct (inside "hello world")
	if r.Start.Col != 13 || r.End.Col != 24 {
		t.Errorf("InnerQuote range = [%d, %d], want [13, 24]", r.Start.Col, r.End.Col)
	}

	// now test brackets
	doc2 := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: `call function(arg1, arg2) here`, Style: StyleNone}}},
		},
	}
	ed2 := NewEditor(doc2, "")
	ed2.cursor = Pos{Block: 0, Col: 0}

	r2 := ed2.InnerParen()
	if r2.Start.Col == r2.End.Col {
		t.Fatal("InnerParen returned empty range when cursor before parens")
	}

	// verify the range is correct (inside "arg1, arg2")
	if r2.Start.Col != 14 || r2.End.Col != 24 {
		t.Errorf("InnerParen range = [%d, %d], want [14, 24]", r2.Start.Col, r2.End.Col)
	}
}

func TestWrapPointsMatchRenderedLines(t *testing.T) {
	// verify calculateWrapPoints line count matches rendered line count
	// this is critical for scroll calculations
	blocks := []Block{
		{Type: BlockH1, Runs: []Run{{Text: "A Heading", Style: StyleNone}}},
		{Type: BlockH2, Runs: []Run{{Text: "Subheading", Style: StyleNone}}},
		{Type: BlockH3, Runs: []Run{{Text: "H3 Heading", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "Short paragraph.", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "A longer paragraph that might wrap depending on the content width setting which is 72 characters by default.", Style: StyleNone}}},
		{Type: BlockParagraph, Runs: []Run{{Text: "Line one.\nLine two.\nLine three.", Style: StyleNone}}}, // embedded newlines
		{Type: BlockQuote, Runs: []Run{{Text: "A quote block with some text.", Style: StyleNone}}},
		{Type: BlockCallout, Runs: []Run{{Text: "This is a callout.", Style: StyleNone}}},
		{Type: BlockCodeLine, Runs: []Run{{Text: "func main() {", Style: StyleNone}}},
		{Type: BlockCodeLine, Runs: []Run{{Text: "\tfmt.Println(\"hello\")", Style: StyleNone}}},
		{Type: BlockCodeLine, Runs: []Run{{Text: "}", Style: StyleNone}}},
		{Type: BlockDivider, Runs: []Run{{Text: "", Style: StyleNone}}},
		{Type: BlockListItem, Runs: []Run{{Text: "A list item", Style: StyleNone}}},
	}
	doc := &Document{Blocks: blocks}

	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.UpdateDisplay()

	mismatches := 0
	for i, block := range ed.doc.Blocks {
		text := block.Text()
		wrapPoints := ed.calculateWrapPoints(text)
		calculatedLines := len(wrapPoints) + 1

		actualLines := ed.blockLines[i].lineCount

		if calculatedLines != actualLines {
			t.Logf("block %d (%s): calculateWrapPoints=%d lines, rendered=%d lines",
				i, block.Type, calculatedLines, actualLines)
			t.Logf("  text: %q", text)
			mismatches++
		}
	}

	if mismatches > 0 {
		t.Errorf("found %d blocks with mismatched line counts - this will cause scroll issues!", mismatches)
	}
}

func TestDialogueGaps(t *testing.T) {
	// helper to count blank lines in cached render output
	countBlankLines := func(ed *Editor) int {
		count := 0
		for _, line := range ed.cachedLines {
			if len(line) == 0 {
				count++
			}
		}
		return count
	}

	t.Run("gap between different speakers with content", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello!"}}, Attrs: map[string]string{"character": "ME"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi!"}}, Attrs: map[string]string{"character": "HER"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.screenWidth = 80
		ed.screenHeight = 24
		ed.rebuildRenderCache()

		gaps := countBlankLines(ed)
		if gaps != 1 {
			t.Errorf("expected 1 gap between different speakers, got %d", gaps)
		}
	})

	t.Run("gap between same speaker paragraphs", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "First paragraph."}}, Attrs: map[string]string{"character": "ME"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Second paragraph."}}, Attrs: map[string]string{"character": "ME"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.screenWidth = 80
		ed.screenHeight = 24
		ed.rebuildRenderCache()

		gaps := countBlankLines(ed)
		if gaps != 1 {
			t.Errorf("expected 1 gap between same speaker paragraphs, got %d", gaps)
		}
	})

	t.Run("no double gap with empty dialogue before new speaker", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello!"}}, Attrs: map[string]string{"character": "ME"}},
				{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": "ME"}}, // empty continuation
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi!"}}, Attrs: map[string]string{"character": "HER"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.screenWidth = 80
		ed.screenHeight = 24
		ed.rebuildRenderCache()

		gaps := countBlankLines(ed)
		// empty dialogue still shows character name, so no blank lines
		// gap logic: gap only added when BOTH prev and current have content
		// - ME(Hello!) → ME(empty): no gap (empty has Length()==0)
		// - ME(empty) → HER(Hi!): no gap (empty has Length()==0)
		if gaps != 0 {
			t.Errorf("expected 0 blank lines (empty dialogue shows character name), got %d", gaps)
		}
	})

	t.Run("multiple speakers with continuation", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi!"}}, Attrs: map[string]string{"character": "ME"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello!"}}, Attrs: map[string]string{"character": "HER"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "How are you?"}}, Attrs: map[string]string{"character": "ME"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "I'm good."}}, Attrs: map[string]string{"character": "ME"}}, // continuation
			},
		}
		ed := NewEditor(doc, "")
		ed.screenWidth = 80
		ed.screenHeight = 24
		ed.rebuildRenderCache()

		gaps := countBlankLines(ed)
		// ME→HER (1), HER→ME (1), ME→ME continuation (1) = 3 gaps
		if gaps != 3 {
			t.Errorf("expected 3 gaps for speaker changes and continuation, got %d", gaps)
		}
	})
}

func TestDialogueToggle(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockDialogue, Runs: []Run{{Text: "Hello!"}}, Attrs: map[string]string{"character": "ME"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 0, Col: 0}

	// initially should be in dialogue mode (not char mode)
	t.Logf("Initial dialogueCharMode: %v", ed.dialogueCharMode)

	// toggle to character mode
	ed.ToggleDialogueMode()
	t.Logf("After first toggle dialogueCharMode: %v, cursor.Col: %d", ed.dialogueCharMode, ed.cursor.Col)

	if !ed.dialogueCharMode {
		t.Errorf("Expected dialogueCharMode=true after toggle, got false")
	}
	if ed.cursor.Col != 2 { // "ME" is 2 chars
		t.Errorf("Expected cursor.Col=2 (end of 'ME'), got %d", ed.cursor.Col)
	}

	// toggle back to dialogue mode
	ed.ToggleDialogueMode()
	t.Logf("After second toggle dialogueCharMode: %v, cursor.Col: %d", ed.dialogueCharMode, ed.cursor.Col)

	if ed.dialogueCharMode {
		t.Errorf("Expected dialogueCharMode=false after second toggle, got true")
	}
	if ed.cursor.Col != 0 {
		t.Errorf("Expected cursor.Col=0 (start of dialogue), got %d", ed.cursor.Col)
	}
}

func TestDialogueCursorPosition(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": "HIM"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.dialogueCharMode = true
	ed.cursor = Pos{Block: 0, Col: 3} // at end of "HIM"
	ed.rebuildRenderCache()

	x, y := ed.CursorScreenPos()

	// margin = (80 - 64) / 2 = 8 (contentWidth is 64)
	// charWidth = 14
	// charLen = 3
	// padding = 14 - 3 = 11
	// x should be 8 + 11 + 3 = 22
	margin := (80 - 64) / 2
	expectedX := margin + (14 - 3) + 3

	t.Logf("dialogueCharMode=%v, cursor.Col=%d, x=%d, y=%d, expectedX=%d",
		ed.dialogueCharMode, ed.cursor.Col, x, y, expectedX)

	if x != expectedX {
		t.Errorf("cursor x=%d, expected %d", x, expectedX)
	}
}

func TestDialogueUpgradeFlow(t *testing.T) {
	// simulates typing "@@ HI" and checking the state
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.rebuildRenderCache()

	// type "@@ "
	ed.InsertChar('@')
	ed.InsertChar('@')
	ed.InsertChar(' ')

	t.Logf("After typing '@@ ': block type=%s, dialogueCharMode=%v, cursor.Col=%d",
		ed.doc.Blocks[0].Type, ed.dialogueCharMode, ed.cursor.Col)

	if ed.doc.Blocks[0].Type != BlockDialogue {
		t.Errorf("Expected block type BlockDialogue, got %s", ed.doc.Blocks[0].Type)
	}
	if !ed.dialogueCharMode {
		t.Errorf("Expected dialogueCharMode=true after upgrade")
	}

	// now type "HI"
	ed.InsertChar('H')
	ed.InsertChar('I')

	t.Logf("After typing 'HI': character=%q, dialogueCharMode=%v, cursor.Col=%d",
		ed.doc.Blocks[0].Attrs["character"], ed.dialogueCharMode, ed.cursor.Col)

	if ed.doc.Blocks[0].Attrs["character"] != "HI" {
		t.Errorf("Expected character='HI', got %q", ed.doc.Blocks[0].Attrs["character"])
	}
	if !ed.dialogueCharMode {
		t.Errorf("Expected dialogueCharMode=true while typing character name")
	}

	// check cursor position
	ed.rebuildRenderCache()
	x, y := ed.CursorScreenPos()

	// margin = (80 - 64) / 2 = 8
	// padding = 14 - 2 = 12 (right-aligned "HI" in 14-char column)
	// col = 2
	// expected x = 8 + 12 + 2 = 22
	expectedX := 8 + 12 + 2

	t.Logf("Cursor position: x=%d, y=%d, expected x=%d", x, y, expectedX)

	if x != expectedX {
		t.Errorf("cursor x=%d, expected %d", x, expectedX)
	}
}

func TestDialogueCharacterHistory(t *testing.T) {
	t.Run("rebuilds history from document", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello"}}, Attrs: map[string]string{"character": "BOB"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "How are you?"}}, Attrs: map[string]string{"character": "ALICE"}},
			},
		}
		ed := NewEditor(doc, "")

		if len(ed.characterHistory) != 2 {
			t.Errorf("expected 2 unique characters, got %d", len(ed.characterHistory))
		}
		if ed.characterHistory[0] != "ALICE" {
			t.Errorf("expected first character ALICE, got %s", ed.characterHistory[0])
		}
		if ed.characterHistory[1] != "BOB" {
			t.Errorf("expected second character BOB, got %s", ed.characterHistory[1])
		}
	})

	t.Run("suggests other speaker", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello"}}, Attrs: map[string]string{"character": "BOB"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.cursor.Block = 1 // on BOB's line

		suggested := ed.getSuggestedCharacter()
		if suggested != "ALICE" {
			t.Errorf("expected suggestion ALICE (the other speaker), got %q", suggested)
		}
	})

	t.Run("new dialogue block gets suggestion", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": "BOB"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.mode = ModeInsert
		ed.cursor.Block = 1
		ed.dialogueCharMode = false // in dialogue mode, empty text

		// press Enter to create new block (yield to next speaker)
		ed.NewLine()

		if ed.cursor.Block != 2 {
			t.Errorf("expected cursor on block 2, got %d", ed.cursor.Block)
		}
		if !ed.dialogueCharMode {
			t.Error("expected dialogueCharMode=true")
		}
		if !ed.dialogueSuggestionMode {
			t.Error("expected dialogueSuggestionMode=true")
		}

		newBlock := &ed.doc.Blocks[2]
		if newBlock.Attrs["character"] != "ALICE" {
			t.Errorf("expected pre-filled character ALICE, got %q", newBlock.Attrs["character"])
		}
	})

	t.Run("backslash cycles characters", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello"}}, Attrs: map[string]string{"character": "BOB"}},
				{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": "ALICE"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.mode = ModeInsert
		ed.cursor.Block = 2
		ed.dialogueCharMode = true
		ed.dialogueSuggestionMode = true
		ed.suggestionIndex = 0 // currently on ALICE

		// press backslash to cycle
		ed.InsertChar('\\')

		block := &ed.doc.Blocks[2]
		if block.Attrs["character"] != "BOB" {
			t.Errorf("expected character to cycle to BOB, got %q", block.Attrs["character"])
		}
	})

	t.Run("other char clears suggestion", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": "ALICE"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.mode = ModeInsert
		ed.cursor.Block = 1
		ed.dialogueCharMode = true
		ed.dialogueSuggestionMode = true

		// type 'J' to start typing a new name
		ed.InsertChar('J')

		block := &ed.doc.Blocks[1]
		if block.Attrs["character"] != "J" {
			t.Errorf("expected character to be J, got %q", block.Attrs["character"])
		}
		if ed.dialogueSuggestionMode {
			t.Error("expected dialogueSuggestionMode=false after typing")
		}
		if ed.cursor.Col != 1 {
			t.Errorf("expected cursor at col 1, got %d", ed.cursor.Col)
		}
	})

	t.Run("backspace clears suggestion", func(t *testing.T) {
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": "ALICE"}},
			},
		}
		ed := NewEditor(doc, "")
		ed.mode = ModeInsert
		ed.cursor.Block = 1
		ed.cursor.Col = 5 // at end of ALICE
		ed.dialogueCharMode = true
		ed.dialogueSuggestionMode = true

		// press backspace
		ed.Backspace()

		block := &ed.doc.Blocks[1]
		if block.Attrs["character"] != "" {
			t.Errorf("expected character to be empty, got %q", block.Attrs["character"])
		}
		if ed.dialogueSuggestionMode {
			t.Error("expected dialogueSuggestionMode=false after backspace")
		}
		if ed.cursor.Col != 0 {
			t.Errorf("expected cursor at col 0, got %d", ed.cursor.Col)
		}
	})

	t.Run("backslash enters suggestion mode from empty", func(t *testing.T) {
		// setup: two characters in history, new dialogue block with empty character
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "BOB"}},
				{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": ""}},
			},
		}
		ed := NewEditor(doc, "")
		ed.mode = ModeInsert
		ed.cursor.Block = 2
		ed.cursor.Col = 0
		ed.dialogueCharMode = true
		ed.dialogueSuggestionMode = false // not in suggestion mode

		// press backslash
		ed.InsertChar('\\')

		block := &ed.doc.Blocks[2]
		// should enter suggestion mode and show most recent character (BOB spoke last)
		if !ed.dialogueSuggestionMode {
			t.Error("expected dialogueSuggestionMode=true after backslash")
		}
		if block.Attrs["character"] != "BOB" {
			t.Errorf("expected character='BOB' (most recent in history), got %q", block.Attrs["character"])
		}
		if ed.cursor.Col != 3 {
			t.Errorf("expected cursor at col 3 (len of BOB), got %d", ed.cursor.Col)
		}
	})

	t.Run("backslash cycles after entering suggestion mode", func(t *testing.T) {
		// setup: two characters in history, new dialogue block with empty character
		doc := &Document{
			Blocks: []Block{
				{Type: BlockDialogue, Runs: []Run{{Text: "Hello"}}, Attrs: map[string]string{"character": "ALICE"}},
				{Type: BlockDialogue, Runs: []Run{{Text: "Hi"}}, Attrs: map[string]string{"character": "BOB"}},
				{Type: BlockDialogue, Runs: []Run{{Text: ""}}, Attrs: map[string]string{"character": ""}},
			},
		}
		ed := NewEditor(doc, "")
		ed.mode = ModeInsert
		ed.cursor.Block = 2
		ed.cursor.Col = 0
		ed.dialogueCharMode = true
		ed.dialogueSuggestionMode = false

		// press backslash twice: BOB (most recent) -> ALICE (older)
		ed.InsertChar('\\')
		ed.InsertChar('\\')

		block := &ed.doc.Blocks[2]
		// should have cycled to second character (ALICE, older in recency order)
		if block.Attrs["character"] != "ALICE" {
			t.Errorf("expected character='ALICE' after second backslash, got %q", block.Attrs["character"])
		}
		if ed.cursor.Col != 5 {
			t.Errorf("expected cursor at col 5 (len of ALICE), got %d", ed.cursor.Col)
		}
	})
}

// =============================================================================
// Raw Mode Tests
// =============================================================================

func TestRawInlineMarkers(t *testing.T) {
	t.Run("bold markers", func(t *testing.T) {
		if got := rawInlinePrefix(StyleBold); got != "**" {
			t.Errorf("bold prefix: got %q, want %q", got, "**")
		}
		if got := rawInlineSuffix(StyleBold); got != "**" {
			t.Errorf("bold suffix: got %q, want %q", got, "**")
		}
	})

	t.Run("italic markers", func(t *testing.T) {
		if got := rawInlinePrefix(StyleItalic); got != "*" {
			t.Errorf("italic prefix: got %q, want %q", got, "*")
		}
		if got := rawInlineSuffix(StyleItalic); got != "*" {
			t.Errorf("italic suffix: got %q, want %q", got, "*")
		}
	})

	t.Run("bold+italic markers", func(t *testing.T) {
		s := StyleBold | StyleItalic
		if got := rawInlinePrefix(s); got != "***" {
			t.Errorf("bold+italic prefix: got %q, want %q", got, "***")
		}
		if got := rawInlineSuffix(s); got != "***" {
			t.Errorf("bold+italic suffix: got %q, want %q", got, "***")
		}
	})

	t.Run("strikethrough markers", func(t *testing.T) {
		if got := rawInlinePrefix(StyleStrikethrough); got != "~~" {
			t.Errorf("strikethrough prefix: got %q, want %q", got, "~~")
		}
		if got := rawInlineSuffix(StyleStrikethrough); got != "~~" {
			t.Errorf("strikethrough suffix: got %q, want %q", got, "~~")
		}
	})

	t.Run("code markers", func(t *testing.T) {
		if got := rawInlinePrefix(StyleCode); got != "`" {
			t.Errorf("code prefix: got %q, want %q", got, "`")
		}
		if got := rawInlineSuffix(StyleCode); got != "`" {
			t.Errorf("code suffix: got %q, want %q", got, "`")
		}
	})

	t.Run("no style returns empty", func(t *testing.T) {
		if got := rawInlinePrefix(StyleNone); got != "" {
			t.Errorf("none prefix: got %q, want %q", got, "")
		}
		if got := rawInlineSuffix(StyleNone); got != "" {
			t.Errorf("none suffix: got %q, want %q", got, "")
		}
	})

	t.Run("strikethrough+bold markers", func(t *testing.T) {
		s := StyleStrikethrough | StyleBold
		if got := rawInlinePrefix(s); got != "~~**" {
			t.Errorf("strike+bold prefix: got %q, want %q", got, "~~**")
		}
		if got := rawInlineSuffix(s); got != "**~~" {
			t.Errorf("strike+bold suffix: got %q, want %q", got, "**~~")
		}
	})
}

func TestRawBlockPrefix(t *testing.T) {
	tests := []struct {
		name   string
		block  Block
		expect string
	}{
		{"h1", Block{Type: BlockH1}, "# "},
		{"h2", Block{Type: BlockH2}, "## "},
		{"h3", Block{Type: BlockH3}, "### "},
		{"h4", Block{Type: BlockH4}, "#### "},
		{"h5", Block{Type: BlockH5}, "##### "},
		{"h6", Block{Type: BlockH6}, "###### "},
		{"quote", Block{Type: BlockQuote}, "> "},
		{"bullet list", Block{Type: BlockListItem}, "- "},
		{"numbered list", Block{
			Type:  BlockListItem,
			Attrs: map[string]string{"marker": "number", "number": "3"},
		}, "3. "},
		{"paragraph", Block{Type: BlockParagraph}, ""},
		{"dialogue", Block{
			Type:  BlockDialogue,
			Attrs: map[string]string{"character": "Alice"},
		}, "@@ Alice "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rawBlockPrefix(&tt.block)
			if got != tt.expect {
				t.Errorf("rawBlockPrefix(%s): got %q, want %q", tt.name, got, tt.expect)
			}
		})
	}
}

func TestRawModeExpandDocument(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Hello ", Style: StyleNone},
				{Text: "bold", Style: StyleBold},
				{Text: " world", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.cursor = Pos{Block: 0, Col: 8} // on 'l' in "bold"

	ed.ToggleRawMode()

	// text should now include ** markers
	got := doc.Blocks[0].Text()
	want := "Hello **bold** world"
	if got != want {
		t.Errorf("expanded text: got %q, want %q", got, want)
	}

	// cursor should have shifted right by 2 (for the leading **)
	if ed.cursor.Col != 10 {
		t.Errorf("cursor after expand: got %d, want 10", ed.cursor.Col)
	}
}

func TestRawModeCompressDocument(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Hello ", Style: StyleNone},
				{Text: "bold", Style: StyleBold},
				{Text: " world", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")

	// toggle on then off
	ed.ToggleRawMode()
	ed.ToggleRawMode()

	got := doc.Blocks[0].Text()
	want := "Hello bold world"
	if got != want {
		t.Errorf("compressed text: got %q, want %q", got, want)
	}

	// verify inline styles are restored
	if len(doc.Blocks[0].Runs) < 2 {
		t.Fatalf("expected multiple runs after compress, got %d", len(doc.Blocks[0].Runs))
	}
	for _, r := range doc.Blocks[0].Runs {
		if r.Text == "bold" && !r.Style.Has(StyleBold) {
			t.Error("expected 'bold' run to have StyleBold after round-trip")
		}
	}
}

func TestRawModeHeadingExpansion(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH2, Runs: []Run{{Text: "My Heading", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	got := doc.Blocks[0].Text()
	want := "## My Heading"
	if got != want {
		t.Errorf("expanded heading: got %q, want %q", got, want)
	}
}

func TestRawModeCodeBlockExpansion(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "fmt.Println()", Style: StyleNone}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	// should expand to 3 blocks: opening fence, code, closing fence
	if len(doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks after expand, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Text() != "```go" {
		t.Errorf("opening fence: got %q, want %q", doc.Blocks[0].Text(), "```go")
	}
	if doc.Blocks[1].Text() != "fmt.Println()" {
		t.Errorf("code line: got %q, want %q", doc.Blocks[1].Text(), "fmt.Println()")
	}
	if doc.Blocks[2].Text() != "```" {
		t.Errorf("closing fence: got %q, want %q", doc.Blocks[2].Text(), "```")
	}

	// round-trip: compress back
	ed.ToggleRawMode()
	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block after compress, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Text() != "fmt.Println()" {
		t.Errorf("compressed code: got %q, want %q", doc.Blocks[0].Text(), "fmt.Println()")
	}
	if doc.Blocks[0].Type != BlockCodeLine {
		t.Errorf("expected BlockCodeLine, got %v", doc.Blocks[0].Type)
	}
	if doc.Blocks[0].Attrs["lang"] != "go" {
		t.Error("expected lang attr to survive round-trip")
	}
}

func TestRawModeQuoteExpansion(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockQuote, Runs: []Run{{Text: "Some wisdom", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	got := doc.Blocks[0].Text()
	want := "> Some wisdom"
	if got != want {
		t.Errorf("expanded quote: got %q, want %q", got, want)
	}
}

func TestRawModeListExpansion(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockListItem, Runs: []Run{{Text: "Buy milk", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	got := doc.Blocks[0].Text()
	want := "- Buy milk"
	if got != want {
		t.Errorf("expanded list: got %q, want %q", got, want)
	}
}

func TestRawModeDividerExpansion(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockDivider, Runs: nil},
		},
	}
	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	got := doc.Blocks[0].Text()
	if got != "---" {
		t.Errorf("expanded divider: got %q, want %q", got, "---")
	}
}

func TestRawModeMultipleStylesRoundTrip(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Say ", Style: StyleNone},
				{Text: "hello", Style: StyleItalic},
				{Text: " and ", Style: StyleNone},
				{Text: "goodbye", Style: StyleBold},
			}},
		},
	}
	ed := NewEditor(doc, "")

	// expand
	ed.ToggleRawMode()
	got := doc.Blocks[0].Text()
	want := "Say *hello* and **goodbye**"
	if got != want {
		t.Errorf("expanded: got %q, want %q", got, want)
	}

	// compress
	ed.ToggleRawMode()
	got = doc.Blocks[0].Text()
	want = "Say hello and goodbye"
	if got != want {
		t.Errorf("compressed: got %q, want %q", got, want)
	}
}

func TestRawModeToggle(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "Hello", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")

	if ed.RawModeEnabled() {
		t.Error("raw mode should be off by default")
	}

	ed.ToggleRawMode()
	if !ed.RawModeEnabled() {
		t.Error("raw mode should be on after toggle")
	}

	ed.ToggleRawMode()
	if ed.RawModeEnabled() {
		t.Error("raw mode should be off after second toggle")
	}
}

func TestRawModeBlockPrefixInText(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH1, Runs: []Run{{Text: "Heading", Style: StyleNone}}},
			{Type: BlockH2, Runs: []Run{{Text: "Sub", Style: StyleNone}}},
			{Type: BlockQuote, Runs: []Run{{Text: "Quote", Style: StyleNone}}},
			{Type: BlockListItem, Runs: []Run{{Text: "Item", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Para", Style: StyleNone}}},
		},
	}

	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	// prefixes should now be in the text, not in blockPrefixLength
	tests := []struct {
		blockIdx int
		expected string
		desc     string
	}{
		{0, "# Heading", "h1"},
		{1, "## Sub", "h2"},
		{2, "> Quote", "quote"},
		{3, "- Item", "list"},
		{4, "Para", "paragraph"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := doc.Blocks[tt.blockIdx].Text()
			if got != tt.expected {
				t.Errorf("block[%d] text: got %q, want %q", tt.blockIdx, got, tt.expected)
			}
			// blockPrefixLength must be 0 in raw mode — prefix is in the text
			pl := ed.blockPrefixLength(&doc.Blocks[tt.blockIdx])
			if pl != 0 {
				t.Errorf("blockPrefixLength(%s) = %d in raw mode, want 0", tt.desc, pl)
			}
		})
	}
}

func TestRawModeRendersBlockPrefixes(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH1, Runs: []Run{{Text: "Title", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// after expansion, text should be "# Title"
	if doc.Blocks[0].Text() != "# Title" {
		t.Fatalf("expected '# Title', got %q", doc.Blocks[0].Text())
	}

	lines := ed.renderBlockRaw(&doc.Blocks[0], 0)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

	// the full text "# Title" should be in the rendered spans
	got := spansText(lines[0])
	if got != "# Title" {
		t.Errorf("rendered text: got %q, want %q", got, "# Title")
	}
}

func TestRawModeRendersInlineMarkers(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Hello ", Style: StyleNone},
				{Text: "world", Style: StyleBold},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// after expansion, text should be "Hello **world**"
	if doc.Blocks[0].Text() != "Hello **world**" {
		t.Fatalf("expected 'Hello **world**', got %q", doc.Blocks[0].Text())
	}

	lines := ed.renderBlockRaw(&doc.Blocks[0], 0)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

	// markers are in the text, so rendered text should include them
	got := spansText(lines[0])
	if got != "Hello **world**" {
		t.Errorf("rendered text: got %q, want %q", got, "Hello **world**")
	}
}

func spansText(spans []glyph.Span) string {
	var s string
	for _, sp := range spans {
		s += sp.Text
	}
	return s
}

func TestRawModeWrapPointsAfterExpand(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Hello ", Style: StyleNone},
				{Text: "bold", Style: StyleBold},
				{Text: " world", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	// after expansion: "Hello **bold** world" = 20 chars
	text := doc.Blocks[0].Text()
	if text != "Hello **bold** world" {
		t.Fatalf("unexpected text: %q", text)
	}

	// standard wrap points should work on the expanded text
	runes := []rune(text)
	wrapPoints := ed.calculateWrapPointsForWidth(runes, 15)

	if len(wrapPoints) == 0 {
		t.Fatal("expected at least one wrap point for width 15")
	}
	for _, wp := range wrapPoints {
		if wp < 0 || wp > len(runes) {
			t.Errorf("wrap point %d out of range [0, %d]", wp, len(runes))
		}
	}
}

// TestRawModeSaveExitsRawMode removed — Save() not available in compose package

func TestDeriveBlockTypeFromText(t *testing.T) {
	tests := []struct {
		text     string
		wantType BlockType
		wantAttr map[string]string
		desc     string
	}{
		{"# Hello", BlockH1, nil, "h1"},
		{"## Hello", BlockH2, nil, "h2"},
		{"### Hello", BlockH3, nil, "h3"},
		{"#### Hello", BlockH4, nil, "h4"},
		{"##### Hello", BlockH5, nil, "h5"},
		{"###### Hello", BlockH6, nil, "h6"},
		{"> Quote", BlockQuote, nil, "quote"},
		{"- Item", BlockListItem, nil, "bullet list"},
		{"* Item", BlockListItem, nil, "star list"},
		{"1. Item", BlockListItem, map[string]string{"marker": "number", "number": "1"}, "numbered list"},
		{"42. Item", BlockListItem, map[string]string{"marker": "number", "number": "42"}, "numbered list 42"},
		{"---", BlockDivider, nil, "divider"},
		{"***", BlockDivider, nil, "divider stars"},
		{"@@ Alice Hello", BlockDialogue, map[string]string{"character": "Alice"}, "dialogue"},
		{"Just text", BlockParagraph, nil, "paragraph"},
		{"", BlockParagraph, nil, "empty"},
		{"#NoSpace", BlockParagraph, nil, "hash without space"},
		{"```go\nfunc main() {}\n```", BlockCodeLine, map[string]string{"lang": "go"}, "code block"},
		{"---\ntitle: Test\n---", BlockFrontMatter, nil, "front matter"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotType, gotAttrs := deriveBlockTypeFromText(tt.text)
			if gotType != tt.wantType {
				t.Errorf("type: got %v, want %v", gotType, tt.wantType)
			}
			if tt.wantAttr != nil {
				for k, v := range tt.wantAttr {
					if gotAttrs[k] != v {
						t.Errorf("attr[%s]: got %q, want %q", k, gotAttrs[k], v)
					}
				}
			}
		})
	}
}

func TestParseInlineMarkdownRaw(t *testing.T) {
	tests := []struct {
		input string
		want  []Run
		desc  string
	}{
		{
			"plain text",
			[]Run{{Text: "plain text", Style: StyleNone}},
			"no markers",
		},
		{
			"hello **bold** world",
			[]Run{
				{Text: "hello ", Style: StyleNone},
				{Text: "**bold**", Style: StyleBold},
				{Text: " world", Style: StyleNone},
			},
			"bold",
		},
		{
			"hello *italic* world",
			[]Run{
				{Text: "hello ", Style: StyleNone},
				{Text: "*italic*", Style: StyleItalic},
				{Text: " world", Style: StyleNone},
			},
			"italic",
		},
		{
			"hello `code` world",
			[]Run{
				{Text: "hello ", Style: StyleNone},
				{Text: "`code`", Style: StyleCode},
				{Text: " world", Style: StyleNone},
			},
			"code",
		},
		{
			"hello ~~struck~~ world",
			[]Run{
				{Text: "hello ", Style: StyleNone},
				{Text: "~~struck~~", Style: StyleStrikethrough},
				{Text: " world", Style: StyleNone},
			},
			"strikethrough",
		},
		{
			"hello ***bolditalic*** world",
			[]Run{
				{Text: "hello ", Style: StyleNone},
				{Text: "***bolditalic***", Style: StyleBold | StyleItalic},
				{Text: " world", Style: StyleNone},
			},
			"bold+italic",
		},
		{
			"no closing **marker",
			[]Run{{Text: "no closing **marker", Style: StyleNone}},
			"unclosed bold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := parseInlineMarkdownRaw(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("run count: got %d, want %d\ngot: %+v", len(got), len(tt.want), got)
			}
			for j, r := range got {
				if r.Text != tt.want[j].Text || r.Style != tt.want[j].Style {
					t.Errorf("run[%d]: got {%q, %v}, want {%q, %v}", j, r.Text, r.Style, tt.want[j].Text, tt.want[j].Style)
				}
			}
		})
	}
}

func TestRawModeRenderDerivedStyle(t *testing.T) {
	// start with a paragraph, expand to raw mode, then simulate editing
	// the text to add a heading prefix — the render should use heading style
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH2, Runs: []Run{{Text: "Title", Style: StyleNone}}},
		},
	}

	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	// text should be "## Title"
	text := doc.Blocks[0].Text()
	if text != "## Title" {
		t.Fatalf("expanded text: got %q, want %q", text, "## Title")
	}

	// render should derive h2 style from text
	lines := ed.renderBlockRaw(&doc.Blocks[0], 0)
	if len(lines) == 0 || len(lines[0]) == 0 {
		t.Fatal("expected rendered spans")
	}
	h2Style := ed.theme.StyleForBlock(BlockH2)
	if lines[0][0].Style.FG != h2Style.FG {
		t.Errorf("expected h2 foreground colour")
	}

	// now simulate user editing: remove the "## " prefix
	doc.Blocks[0].Runs = []Run{{Text: "Title", Style: StyleNone}}

	// render should now derive paragraph style
	lines = ed.renderBlockRaw(&doc.Blocks[0], 0)
	paraStyle := ed.theme.StyleForBlock(BlockParagraph)
	if lines[0][0].Style.FG != paraStyle.FG {
		t.Errorf("after removing ##, expected paragraph foreground colour, got %v want %v",
			lines[0][0].Style.FG, paraStyle.FG)
	}
}

func TestRawModeCompressDeriveType(t *testing.T) {
	// start with paragraph, expand to raw mode, edit text to be a heading,
	// then compress — block type should change to heading
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "Hello", Style: StyleNone}}},
		},
	}

	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	// simulate user adding "# " prefix
	doc.Blocks[0].Runs = []Run{{Text: "# Hello", Style: StyleNone}}

	// toggle off — compress should derive BlockH1
	ed.ToggleRawMode()

	if doc.Blocks[0].Type != BlockH1 {
		t.Errorf("block type: got %v, want BlockH1", doc.Blocks[0].Type)
	}
	if doc.Blocks[0].Text() != "Hello" {
		t.Errorf("block text: got %q, want %q", doc.Blocks[0].Text(), "Hello")
	}
}

func TestRawModeCompressTypeChange(t *testing.T) {
	// heading → remove prefix → compress → should become paragraph
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH1, Runs: []Run{{Text: "Big", Style: StyleNone}}},
		},
	}

	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	// text is now "# Big", simulate removing the "# "
	doc.Blocks[0].Runs = []Run{{Text: "Big", Style: StyleNone}}

	ed.ToggleRawMode()

	if doc.Blocks[0].Type != BlockParagraph {
		t.Errorf("block type: got %v, want BlockParagraph", doc.Blocks[0].Type)
	}
	if doc.Blocks[0].Text() != "Big" {
		t.Errorf("block text: got %q, want %q", doc.Blocks[0].Text(), "Big")
	}
}

func TestRawModeQuoteToListRoundTrip(t *testing.T) {
	// start as quote, edit prefix in raw mode to make it a list, compress
	doc := &Document{
		Blocks: []Block{
			{Type: BlockQuote, Runs: []Run{{Text: "Item", Style: StyleNone}}},
		},
	}

	ed := NewEditor(doc, "")
	ed.ToggleRawMode()

	// text is "> Item", change to "- Item"
	doc.Blocks[0].Runs = []Run{{Text: "- Item", Style: StyleNone}}

	ed.ToggleRawMode()

	if doc.Blocks[0].Type != BlockListItem {
		t.Errorf("block type: got %v, want BlockListItem", doc.Blocks[0].Type)
	}
	if doc.Blocks[0].Text() != "Item" {
		t.Errorf("block text: got %q, want %q", doc.Blocks[0].Text(), "Item")
	}
}

func TestRawModeMotions(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH1, Runs: []Run{{Text: "First", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Second line here", Style: StyleNone}}},
			{Type: BlockQuote, Runs: []Run{{Text: "Quoted", Style: StyleNone}}},
			{Type: BlockListItem, Runs: []Run{{Text: "Item", Style: StyleNone}}},
		},
	}

	ed := NewEditor(doc, "")
	ed.screenWidth = 120
	ed.screenHeight = 40
	ed.ToggleRawMode()

	// after toggle, cursor should be at block 0
	// original col was 0, prefix "# " (2 runes) added, so col should be 2
	t.Logf("initial cursor: block=%d col=%d", ed.cursor.Block, ed.cursor.Col)
	t.Logf("block[0] text: %q (len=%d)", doc.Blocks[0].Text(), doc.Blocks[0].Length())
	t.Logf("block[1] text: %q (len=%d)", doc.Blocks[1].Text(), doc.Blocks[1].Length())
	t.Logf("block[2] text: %q (len=%d)", doc.Blocks[2].Text(), doc.Blocks[2].Length())
	t.Logf("block[3] text: %q (len=%d)", doc.Blocks[3].Text(), doc.Blocks[3].Length())

	// test h/l (left/right)
	startCol := ed.cursor.Col
	ed.Right(1)
	if ed.cursor.Col != startCol+1 {
		t.Errorf("Right: cursor col got %d, want %d", ed.cursor.Col, startCol+1)
	}
	ed.Left(1)
	if ed.cursor.Col != startCol {
		t.Errorf("Left: cursor col got %d, want %d", ed.cursor.Col, startCol)
	}

	// test j (down) — should move to block 1
	ed.cursor.Col = 0 // start of line
	ed.Down(1)
	t.Logf("after Down(1): block=%d col=%d", ed.cursor.Block, ed.cursor.Col)
	if ed.cursor.Block != 1 {
		t.Errorf("Down: cursor block got %d, want 1", ed.cursor.Block)
	}

	// test j again — should move to block 2
	ed.Down(1)
	t.Logf("after Down(2): block=%d col=%d", ed.cursor.Block, ed.cursor.Col)
	if ed.cursor.Block != 2 {
		t.Errorf("Down: cursor block got %d, want 2", ed.cursor.Block)
	}

	// test k (up) — should move back to block 1
	ed.Up(1)
	t.Logf("after Up(1): block=%d col=%d", ed.cursor.Block, ed.cursor.Col)
	if ed.cursor.Block != 1 {
		t.Errorf("Up: cursor block got %d, want 1", ed.cursor.Block)
	}

	// test w (word forward)
	ed.cursor.Block = 1
	ed.cursor.Col = 0
	startCol = ed.cursor.Col
	ed.NextWordStart(1)
	t.Logf("after NextWordStart: block=%d col=%d", ed.cursor.Block, ed.cursor.Col)
	if ed.cursor.Col <= startCol {
		t.Errorf("NextWordStart: cursor didn't move forward, col=%d", ed.cursor.Col)
	}

	// test b (word backward)
	ed.cursor.Block = 1
	ed.cursor.Col = 10
	startCol = ed.cursor.Col
	ed.PrevWordStart(1)
	t.Logf("after PrevWordStart: block=%d col=%d", ed.cursor.Block, ed.cursor.Col)
	if ed.cursor.Col >= startCol {
		t.Errorf("PrevWordStart: cursor didn't move backward, col=%d", ed.cursor.Col)
	}
}

func TestRawModeOperatorTextObjects(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH2, Runs: []Run{{Text: "Hello world", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{
				{Text: "some ", Style: StyleNone},
				{Text: "bold", Style: StyleBold},
				{Text: " text", Style: StyleNone},
			}},
		},
	}

	ed := NewEditor(doc, "")
	ed.screenWidth = 120
	ed.screenHeight = 40
	ed.ToggleRawMode()

	// block[0] text should be "## Hello world"
	t.Logf("block[0] text: %q", doc.Blocks[0].Text())
	t.Logf("block[1] text: %q", doc.Blocks[1].Text())

	// diw on "Hello" (cursor at col 3, on 'H' in "## Hello world")
	ed.cursor = Pos{Block: 0, Col: 3}
	r := ed.InnerWord()
	t.Logf("InnerWord at col 3: start=%d end=%d (text=%q)", r.Start.Col, r.End.Col,
		doc.Blocks[0].Text()[r.Start.Col:r.End.Col])
	if r.Start.Col != 3 || r.End.Col != 8 {
		t.Errorf("InnerWord: got [%d,%d), want [3,8)", r.Start.Col, r.End.Col)
	}

	// actually delete it
	ed.Delete(r)
	got := doc.Blocks[0].Text()
	if got != "##  world" {
		t.Errorf("after diw: got %q, want %q", got, "##  world")
	}

	// d$ on block 1 (delete to end of line)
	ed.cursor = Pos{Block: 1, Col: 5}
	r = ed.ToLineEnd()
	t.Logf("ToLineEnd at col 5: start=%d end=%d", r.Start.Col, r.End.Col)
	ed.Delete(r)
	got = doc.Blocks[1].Text()
	if got != "some " {
		t.Errorf("after d$: got %q, want %q", got, "some ")
	}

	// ciw — the text object part
	doc2 := &Document{
		Blocks: []Block{
			{Type: BlockQuote, Runs: []Run{{Text: "Change this word", Style: StyleNone}}},
		},
	}
	ed2 := NewEditor(doc2, "")
	ed2.screenWidth = 120
	ed2.screenHeight = 40
	ed2.ToggleRawMode()
	// text should be "> Change this word"
	t.Logf("ciw test text: %q", doc2.Blocks[0].Text())
	ed2.cursor = Pos{Block: 0, Col: 9} // on 'C' in "Change"... wait, "> Change" starts at 0
	// col 9 should be 't' in "this"
	r = ed2.InnerWord()
	t.Logf("InnerWord at col 9: start=%d end=%d (text=%q)", r.Start.Col, r.End.Col,
		doc2.Blocks[0].Text()[r.Start.Col:r.End.Col])
	ed2.Change(r)
	got = doc2.Blocks[0].Text()
	// "this" should be deleted, leaving "> Change  word"
	if got != "> Change  word" {
		t.Errorf("after ciw: got %q, want %q", got, "> Change  word")
	}
}

func TestRawModeStyleOperators(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "Hello world today", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 120
	ed.screenHeight = 40
	ed.ToggleRawMode()

	// gbiw — bold inner word on "world" (cursor at col 6)
	ed.cursor = Pos{Block: 0, Col: 6}
	r := ed.InnerWord()
	ed.ApplyStyle(r, StyleBold)
	got := doc.Blocks[0].Text()
	if got != "Hello **world** today" {
		t.Errorf("after gbiw: got %q, want %q", got, "Hello **world** today")
	}

	// toggle off — gbiw again on "world" (cursor now at col 8, inside **world**)
	ed.cursor = Pos{Block: 0, Col: 8}
	r = ed.InnerWord()
	t.Logf("toggle-off InnerWord: start=%d end=%d text=%q", r.Start.Col, r.End.Col, got[r.Start.Col:r.End.Col])
	ed.ApplyStyle(r, StyleBold)
	got = doc.Blocks[0].Text()
	if got != "Hello world today" {
		t.Errorf("after toggle off: got %q, want %q", got, "Hello world today")
	}

	// giiw — italic inner word
	ed.cursor = Pos{Block: 0, Col: 6}
	r = ed.InnerWord()
	ed.ApplyStyle(r, StyleItalic)
	got = doc.Blocks[0].Text()
	if got != "Hello *world* today" {
		t.Errorf("after giiw: got %q, want %q", got, "Hello *world* today")
	}

	// gciw — clear style (strip markers from word)
	// cursor on "world" which is now inside *...*
	ed.cursor = Pos{Block: 0, Col: 7}
	r = ed.InnerWord()
	// clear should strip the * markers
	ed.ClearStyle(r)
	got = doc.Blocks[0].Text()
	// after clear, markers should be gone
	if got != "Hello world today" {
		t.Errorf("after gciw: got %q, want %q", got, "Hello world today")
	}
}

// extractDimmedAndFocused takes a flat list of spans and the editor's dimmed FG color,
// and returns the concatenated dimmed text and focused text.
func extractDimmedAndFocused(spans []glyph.Span, dimmedFG glyph.Color) (dimmed, focused string) {
	for _, sp := range spans {
		if sp.Style.FG == dimmedFG {
			dimmed += sp.Text
		} else {
			focused += sp.Text
		}
	}
	return
}

func TestRawModeFocusDimmingSentence(t *testing.T) {
	// plain text, three sentences, cursor in the second
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "The first sentence. The second sentence. The third sentence.", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 120
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// enable focus mode with sentence scope
	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	// place cursor on 's' in "second" (position 24 in "The first sentence. The second sentence. The third sentence.")
	ed.cursor = Pos{Block: 0, Col: 24}

	// verify InnerSentence returns what we expect
	sr := ed.InnerSentence()
	text := doc.Blocks[0].Text()
	sentenceText := string([]rune(text)[sr.Start.Col:sr.End.Col])
	expectedSentence := "The second sentence."
	if sentenceText != expectedSentence {
		t.Fatalf("InnerSentence: got %q, want %q (start=%d, end=%d)", sentenceText, expectedSentence, sr.Start.Col, sr.End.Col)
	}

	// render the block in raw mode (this calls applyFocusDimming)
	lines := ed.renderBlockRaw(&doc.Blocks[0], 0)
	if len(lines) == 0 {
		t.Fatal("renderBlockRaw returned no lines")
	}

	// flatten all lines to get all spans
	var allSpans []glyph.Span
	for _, line := range lines {
		allSpans = append(allSpans, line...)
	}

	dimmedFG := ed.theme.Dimmed.FG
	dimmed, focused := extractDimmedAndFocused(allSpans, dimmedFG)

	expectedFocused := "The second sentence."
	expectedDimmed := "The first sentence.  The third sentence."

	if focused != expectedFocused {
		t.Errorf("focused text: got %q, want %q", focused, expectedFocused)
	}
	if dimmed != expectedDimmed {
		t.Errorf("dimmed text: got %q, want %q", dimmed, expectedDimmed)
	}
}

func TestRawModeFocusDimmingWithMarkdown(t *testing.T) {
	// text with markdown markers, three sentences, cursor in the second (which has bold)
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "The first sentence. The ", Style: StyleNone},
				{Text: "second", Style: StyleBold},
				{Text: " sentence. The third sentence.", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 120
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// after expansion, text should include ** markers
	expandedText := doc.Blocks[0].Text()
	expectedExpanded := "The first sentence. The **second** sentence. The third sentence."
	if expandedText != expectedExpanded {
		t.Fatalf("expanded text: got %q, want %q", expandedText, expectedExpanded)
	}

	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	// cursor on 's' in "**second**" — position 26 (after "The first sentence. The **")
	ed.cursor = Pos{Block: 0, Col: 26}

	sr := ed.InnerSentence()
	sentenceText := string([]rune(expandedText)[sr.Start.Col:sr.End.Col])
	expectedSentence := "The **second** sentence."
	if sentenceText != expectedSentence {
		t.Fatalf("InnerSentence: got %q, want %q (start=%d, end=%d)", sentenceText, expectedSentence, sr.Start.Col, sr.End.Col)
	}

	lines := ed.renderBlockRaw(&doc.Blocks[0], 0)
	if len(lines) == 0 {
		t.Fatal("renderBlockRaw returned no lines")
	}

	var allSpans []glyph.Span
	for _, line := range lines {
		allSpans = append(allSpans, line...)
	}

	dimmedFG := ed.theme.Dimmed.FG
	dimmed, focused := extractDimmedAndFocused(allSpans, dimmedFG)

	expectedFocused := "The **second** sentence."
	expectedDimmed := "The first sentence.  The third sentence."

	if focused != expectedFocused {
		t.Errorf("focused text: got %q, want %q", focused, expectedFocused)
	}
	if dimmed != expectedDimmed {
		t.Errorf("dimmed text: got %q, want %q", dimmed, expectedDimmed)
	}
}

func TestFocusDimmingAfterWrapping(t *testing.T) {
	// reproduce the bug: focus dimming boundaries shift after wrapping
	// text with three sentences, cursor in the third — verifies the
	// wrapped output has correct dim/focus boundaries
	text := "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are noticeable. This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor."
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: text, Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	// place cursor on "optimize" in the third sentence
	runes := []rune(text)
	cursorPos := -1
	for i := 0; i < len(runes)-8; i++ {
		if string(runes[i:i+8]) == "optimize" {
			cursorPos = i
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find 'optimize' in text")
	}
	ed.cursor = Pos{Block: 0, Col: cursorPos}

	// verify InnerSentence returns the correct third sentence
	sr := ed.InnerSentence()
	sentenceText := string(runes[sr.Start.Col:sr.End.Col])
	expectedSentence := "This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor."
	if sentenceText != expectedSentence {
		t.Fatalf("InnerSentence: got %q, want %q (start=%d, end=%d)",
			sentenceText, expectedSentence, sr.Start.Col, sr.End.Col)
	}

	// rebuild the render cache (this does renderBlock + applyFocusDimming + wrapSpans)
	ed.rebuildRenderCache()

	// collect all text from wrapped lines, categorized as dimmed or focused
	dimmedFG := ed.theme.Dimmed.FG
	var totalDimmed, totalFocused string
	for _, line := range ed.cachedLines {
		for _, sp := range line {
			if sp.Style.FG == dimmedFG {
				totalDimmed += sp.Text
			} else {
				totalFocused += sp.Text
			}
		}
	}

	if totalFocused != expectedSentence {
		t.Errorf("focused text after wrapping:\ngot:  %q\nwant: %q", totalFocused, expectedSentence)
	}

	// verify each wrapped line has the correct dim/focus transition
	// specifically: "noticeable." should be DIMMED (end of sentence 2)
	// and "This" should be FOCUSED (start of sentence 3)
	for lineIdx, line := range ed.cachedLines {
		for _, sp := range line {
			if sp.Style.FG != dimmedFG {
				// focused span should not contain text from sentence 1 or 2
				if strings.Contains(sp.Text, "noticeable") {
					t.Errorf("line %d: 'noticeable' should be dimmed but is focused in span %q", lineIdx, sp.Text)
				}
				if strings.Contains(sp.Text, "motion") {
					t.Errorf("line %d: 'motion' should be dimmed but is focused in span %q", lineIdx, sp.Text)
				}
			}
		}
	}
}

func TestFocusDimmingAfterWrappingMultiRun(t *testing.T) {
	// text with bold run spanning sentences — this creates multiple spans
	// which applyFocusDimming must track charOffset across correctly
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are ", Style: StyleNone},
				{Text: "noticeable", Style: StyleBold},
				{Text: ". This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor.", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	fullText := doc.Blocks[0].Text()
	runes := []rune(fullText)

	// place cursor on "optimize" in the third sentence
	cursorPos := -1
	for i := 0; i < len(runes)-8; i++ {
		if string(runes[i:i+8]) == "optimize" {
			cursorPos = i
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find 'optimize' in text")
	}
	ed.cursor = Pos{Block: 0, Col: cursorPos}

	// verify InnerSentence
	sr := ed.InnerSentence()
	sentenceText := string(runes[sr.Start.Col:sr.End.Col])
	expectedSentence := "This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor."
	if sentenceText != expectedSentence {
		t.Fatalf("InnerSentence: got %q, want %q (start=%d, end=%d)",
			sentenceText, expectedSentence, sr.Start.Col, sr.End.Col)
	}

	ed.rebuildRenderCache()

	dimmedFG := ed.theme.Dimmed.FG
	var totalFocused string
	for _, line := range ed.cachedLines {
		for _, sp := range line {
			if sp.Style.FG != dimmedFG {
				totalFocused += sp.Text
			}
		}
	}

	if totalFocused != expectedSentence {
		t.Errorf("focused text after wrapping (multi-run):\ngot:  %q\nwant: %q", totalFocused, expectedSentence)
	}

	// verify "noticeable" is dimmed (it's in sentence 2)
	for lineIdx, line := range ed.cachedLines {
		for _, sp := range line {
			if sp.Style.FG != dimmedFG && strings.Contains(sp.Text, "noticeable") {
				t.Errorf("line %d: 'noticeable' should be dimmed but found in focused span %q", lineIdx, sp.Text)
			}
		}
	}
}

func TestFocusDimmingMultiBlock(t *testing.T) {
	// multiple blocks — previous blocks should be fully dimmed,
	// current block should have correct per-sentence dimming
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Above 500ms, they lose focus. Our target should be sub-16ms frame times for truly fluid editing.", Style: StyleNone},
			}},
			{Type: BlockParagraph, Runs: []Run{
				{Text: "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are ", Style: StyleNone},
				{Text: "noticeable", Style: StyleBold},
				{Text: ". This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor.", Style: StyleNone},
			}},
			{Type: BlockH2, Runs: []Run{
				{Text: "Technical Deep Dive", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	// cursor in block 1, on "optimize"
	block1Text := doc.Blocks[1].Text()
	runes := []rune(block1Text)
	cursorPos := -1
	for i := 0; i < len(runes)-8; i++ {
		if string(runes[i:i+8]) == "optimize" {
			cursorPos = i
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find 'optimize' in text")
	}
	ed.cursor = Pos{Block: 1, Col: cursorPos}

	ed.rebuildRenderCache()

	dimmedFG := ed.theme.Dimmed.FG

	// check block 0: should be entirely dimmed
	block0Start := ed.blockLines[0].screenLine
	block0End := block0Start + ed.blockLines[0].lineCount
	for lineIdx := block0Start; lineIdx < block0End; lineIdx++ {
		for _, sp := range ed.cachedLines[lineIdx] {
			if sp.Text != "" && sp.Style.FG != dimmedFG {
				t.Errorf("block 0 line %d: expected all dimmed, got focused span %q", lineIdx, sp.Text)
			}
		}
	}

	// check block 1: focused text should be exactly the third sentence
	block1Start := ed.blockLines[1].screenLine
	block1End := block1Start + ed.blockLines[1].lineCount
	var block1Focused string
	for lineIdx := block1Start; lineIdx < block1End; lineIdx++ {
		for _, sp := range ed.cachedLines[lineIdx] {
			if sp.Style.FG != dimmedFG {
				block1Focused += sp.Text
			}
		}
	}

	expectedSentence := "This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor."
	if block1Focused != expectedSentence {
		t.Errorf("block 1 focused text:\ngot:  %q\nwant: %q", block1Focused, expectedSentence)
	}

	// check block 2: should be entirely dimmed
	block2Start := ed.blockLines[2].screenLine
	block2End := block2Start + ed.blockLines[2].lineCount
	for lineIdx := block2Start; lineIdx < block2End; lineIdx++ {
		for _, sp := range ed.cachedLines[lineIdx] {
			if sp.Text != "" && sp.Style.FG != dimmedFG {
				t.Errorf("block 2 line %d: expected all dimmed, got focused span %q", lineIdx, sp.Text)
			}
		}
	}
}

func TestFocusDimmingExactScreenshot(t *testing.T) {
	// exact reproduction of the screenshot scenario — paragraph with bold "noticeable"
	// and a dash in the text, multiple blocks, sentence focus
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Above 500ms, they lose focus. Our target should be sub-16ms frame times for truly fluid editing.", Style: StyleNone},
			}},
			{Type: BlockParagraph, Runs: []Run{
				{Text: "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are ", Style: StyleNone},
				{Text: "noticeable", Style: StyleBold},
				{Text: ". This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor.", Style: StyleNone},
			}},
			{Type: BlockH2, Runs: []Run{
				{Text: "Technical Deep Dive", Style: StyleNone},
			}},
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Let's explore the technical aspects of text editor performance in detail.", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	// cursor on "optimize" in block 1
	block1Text := doc.Blocks[1].Text()
	runes := []rune(block1Text)
	cursorPos := -1
	for i := 0; i < len(runes)-8; i++ {
		if string(runes[i:i+8]) == "optimize" {
			cursorPos = i
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find 'optimize' in text")
	}
	ed.cursor = Pos{Block: 1, Col: cursorPos}

	// verify the sentence detection
	sr := ed.InnerSentence()
	sentenceText := string(runes[sr.Start.Col:sr.End.Col])
	expectedSentence := "This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor."
	if sentenceText != expectedSentence {
		t.Fatalf("InnerSentence: got %q (start=%d end=%d), want %q",
			sentenceText, sr.Start.Col, sr.End.Col, expectedSentence)
	}

	ed.rebuildRenderCache()

	// dump all wrapped lines for block 1 with dim/focus markers
	block1Start := ed.blockLines[1].screenLine
	block1End := block1Start + ed.blockLines[1].lineCount
	dimmedFG := ed.theme.Dimmed.FG

	var totalFocused, totalDimmed string
	for lineIdx := block1Start; lineIdx < block1End; lineIdx++ {
		line := ed.cachedLines[lineIdx]
		for _, sp := range line {
			if sp.Style.FG == dimmedFG {
				totalDimmed += sp.Text
			} else {
				totalFocused += sp.Text
			}
		}
	}

	if totalFocused != expectedSentence {
		// dump line details for debugging
		for lineIdx := block1Start; lineIdx < block1End; lineIdx++ {
			line := ed.cachedLines[lineIdx]
			var lineDesc string
			for _, sp := range line {
				marker := "F" // focused
				if sp.Style.FG == dimmedFG {
					marker = "D" // dimmed
				}
				lineDesc += "[" + marker + ":" + sp.Text + "]"
			}
			t.Logf("  wrapped line %d: %s", lineIdx-block1Start, lineDesc)
		}
		t.Errorf("block 1 focused text:\ngot:  %q\nwant: %q\ndimmed: %q", totalFocused, expectedSentence, totalDimmed)
	}
}

func TestWrapSpansNoMidWordBreakAcrossSpans(t *testing.T) {
	// core bug: wrapSpans used to split words mid-character when a styled span
	// (like bold text) had no internal spaces and didn't fit on the remaining line.
	// this caused visual wrap points to differ from calculateWrapPointsForWidth,
	// breaking focus line dimming which uses the text-based wrap points.
	spans := []glyph.Span{
		{Text: "scrolling through a document, even slight stutters are ", Style: glyph.Style{}},
		{Text: "noticeable", Style: glyph.Style{}.Bold()},
		{Text: ". This is why we optimize.", Style: glyph.Style{}},
	}
	lines := wrapSpans(spans, 64)

	// "noticeable" should NOT be split mid-word. The entire word should
	// start on a new line because it doesn't fit on the remaining space.
	for _, line := range lines {
		for _, sp := range line {
			if sp.Text == "noticeabl" || sp.Text == "ble" {
				t.Errorf("word 'noticeable' was split mid-word: found span %q", sp.Text)
			}
		}
	}

	// verify "noticeable" appears as a complete word on some line
	found := false
	for _, line := range lines {
		for _, sp := range line {
			if sp.Text == "noticeable" {
				found = true
			}
		}
	}
	if !found {
		// dump lines for debugging
		for i, line := range lines {
			var desc string
			for _, sp := range line {
				desc += "[" + sp.Text + "]"
			}
			t.Logf("  line %d: %s", i, desc)
		}
		t.Error("expected 'noticeable' as a complete span, not found")
	}
}

func TestWrapSpansConsistentWithWrapPoints(t *testing.T) {
	// verify that wrapSpans and calculateWrapPointsForWidth produce the
	// same line boundaries for text with styled spans
	text := "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are noticeable. This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor."
	runs := []Run{
		{Text: "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are ", Style: StyleNone},
		{Text: "noticeable", Style: StyleBold},
		{Text: ". This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor.", Style: StyleNone},
	}
	// convert runs to spans (mimicking runsToSpans)
	spans := make([]glyph.Span, len(runs))
	for i, r := range runs {
		spans[i] = glyph.Span{Text: r.Text, Style: glyph.Style{}}
	}

	// wrap with both algorithms
	wrappedSpans := wrapSpans(spans, contentWidth)

	ed := NewEditor(&Document{Blocks: []Block{{Type: BlockParagraph, Runs: runs}}}, "")
	wrapPoints := ed.calculateWrapPointsForWidth([]rune(text), contentWidth)

	// extract line boundaries from wrapSpans
	var spanLineStarts []int
	charPos := 0
	for lineIdx, line := range wrappedSpans {
		if lineIdx > 0 {
			spanLineStarts = append(spanLineStarts, charPos)
		}
		for _, sp := range line {
			charPos += len([]rune(sp.Text))
		}
	}

	// wrapPoints from calculateWrapPointsForWidth are the start positions of each line after line 0
	if len(spanLineStarts) != len(wrapPoints) {
		t.Logf("wrapPoints: %v", wrapPoints)
		t.Logf("spanLineStarts: %v", spanLineStarts)
		for i, line := range wrappedSpans {
			var desc string
			for _, sp := range line {
				desc += "[" + sp.Text + "]"
			}
			t.Logf("  wrapped line %d: %s", i, desc)
		}
		t.Fatalf("different number of lines: wrapSpans=%d, calculateWrapPoints=%d",
			len(spanLineStarts)+1, len(wrapPoints)+1)
	}

	for i, wp := range wrapPoints {
		if i < len(spanLineStarts) && spanLineStarts[i] != wp {
			t.Errorf("line %d start: wrapSpans=%d, calculateWrapPoints=%d", i+1, spanLineStarts[i], wp)
		}
	}
}

func TestFocusDimmingBoldAtWrapBoundary(t *testing.T) {
	// specifically test the case where a bold word is at the wrap point
	// and the focus boundary is right after it — this is the exact scenario
	// from the screenshot where "noticeable" (bold) wraps across lines
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "When scrolling through a document, even slight stutters are ", Style: StyleNone},
				{Text: "noticeable", Style: StyleBold},
				{Text: ". This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor.", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	fullText := doc.Blocks[0].Text()
	runes := []rune(fullText)

	// cursor on "optimize"
	cursorPos := -1
	for i := 0; i < len(runes)-8; i++ {
		if string(runes[i:i+8]) == "optimize" {
			cursorPos = i
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find 'optimize'")
	}
	ed.cursor = Pos{Block: 0, Col: cursorPos}

	// get pre-wrap rendering to check applyFocusDimming directly
	block := &doc.Blocks[0]
	preWrapLines := ed.renderBlock(block, 0)
	if len(preWrapLines) != 1 {
		t.Fatalf("expected 1 pre-wrap line, got %d", len(preWrapLines))
	}

	dimmedFG := ed.theme.Dimmed.FG
	t.Log("pre-wrap spans:")
	for i, sp := range preWrapLines[0] {
		marker := "FOCUSED"
		if sp.Style.FG == dimmedFG {
			marker = "DIMMED"
		}
		t.Logf("  span %d: [%s] %q (bold=%v)", i, marker, sp.Text, sp.Style.Attr&glyph.AttrBold != 0)
	}

	// verify pre-wrap: "noticeable" must be dimmed (it's in the previous sentence)
	for _, sp := range preWrapLines[0] {
		if strings.Contains(sp.Text, "noticeable") && sp.Style.FG != dimmedFG {
			t.Errorf("PRE-WRAP BUG: 'noticeable' should be dimmed but is focused: %q", sp.Text)
		}
	}

	// now check after wrapping
	ed.rebuildRenderCache()

	t.Log("wrapped lines:")
	var totalFocused string
	for lineIdx, line := range ed.cachedLines {
		var lineDesc string
		for _, sp := range line {
			marker := "F"
			if sp.Style.FG == dimmedFG {
				marker = "D"
			} else {
				totalFocused += sp.Text
			}
			lineDesc += "[" + marker + ":" + sp.Text + "]"
		}
		t.Logf("  line %d: %s", lineIdx, lineDesc)
	}

	expectedSentence := "This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor."
	if totalFocused != expectedSentence {
		t.Errorf("focused text:\ngot:  %q\nwant: %q", totalFocused, expectedSentence)
	}

	// verify no part of "noticeable" is focused in any wrapped line
	for lineIdx, line := range ed.cachedLines {
		for _, sp := range line {
			if sp.Style.FG != dimmedFG && strings.Contains(sp.Text, "noticeabl") {
				t.Errorf("line %d: 'noticeabl' should be dimmed but found in focused span %q", lineIdx, sp.Text)
			}
		}
	}
}

func TestFocusDimmingRawModeWithBoldAtWrap(t *testing.T) {
	// in raw mode, bold "noticeable" becomes "**noticeable**" which shifts positions
	// and changes wrapping. The ** markers add 4 chars, potentially breaking
	// the alignment between InnerSentence() positions and rendered positions.
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are ", Style: StyleNone},
				{Text: "noticeable", Style: StyleBold},
				{Text: ". This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor.", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// after expansion, text should include ** markers
	expandedText := doc.Blocks[0].Text()
	expectedExpanded := "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are **noticeable**. This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor."
	if expandedText != expectedExpanded {
		t.Fatalf("expanded text: got %q, want %q", expandedText, expectedExpanded)
	}

	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	// find "optimize" in the expanded text
	runes := []rune(expandedText)
	cursorPos := -1
	for i := 0; i < len(runes)-8; i++ {
		if string(runes[i:i+8]) == "optimize" {
			cursorPos = i
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find 'optimize'")
	}
	ed.cursor = Pos{Block: 0, Col: cursorPos}

	// verify sentence detection in raw mode
	sr := ed.InnerSentence()
	sentenceText := string(runes[sr.Start.Col:sr.End.Col])
	expectedSentence := "This is why we optimize so aggressively - not for benchmarks, but for the feel of the editor."
	if sentenceText != expectedSentence {
		t.Fatalf("InnerSentence (raw): got %q (start=%d end=%d), want %q",
			sentenceText, sr.Start.Col, sr.End.Col, expectedSentence)
	}

	// check pre-wrap rendering
	block := &doc.Blocks[0]
	preWrapLines := ed.renderBlockRaw(block, 0)
	dimmedFG := ed.theme.Dimmed.FG

	t.Log("pre-wrap spans (raw mode):")
	for i, sp := range preWrapLines[0] {
		marker := "FOCUSED"
		if sp.Style.FG == dimmedFG {
			marker = "DIMMED"
		}
		t.Logf("  span %d: [%s] %q", i, marker, sp.Text)
	}

	// verify "**noticeable**" is dimmed pre-wrap
	for _, sp := range preWrapLines[0] {
		if strings.Contains(sp.Text, "noticeable") && sp.Style.FG != dimmedFG {
			t.Errorf("PRE-WRAP BUG: span containing 'noticeable' should be dimmed: %q", sp.Text)
		}
	}

	// rebuild cache (renders + wraps)
	ed.rebuildRenderCache()

	t.Log("wrapped lines (raw mode):")
	var totalFocused string
	for lineIdx, line := range ed.cachedLines {
		var lineDesc string
		for _, sp := range line {
			marker := "F"
			if sp.Style.FG == dimmedFG {
				marker = "D"
			} else {
				totalFocused += sp.Text
			}
			lineDesc += "[" + marker + ":" + sp.Text + "]"
		}
		t.Logf("  line %d: %s", lineIdx, lineDesc)
	}

	if totalFocused != expectedSentence {
		t.Errorf("focused text (raw mode):\ngot:  %q\nwant: %q", totalFocused, expectedSentence)
	}

	// verify no part of "noticeable" or "**" is focused
	for lineIdx, line := range ed.cachedLines {
		for _, sp := range line {
			if sp.Style.FG != dimmedFG {
				if strings.Contains(sp.Text, "noticeable") || strings.Contains(sp.Text, "**") {
					t.Errorf("line %d: should be dimmed but focused: %q", lineIdx, sp.Text)
				}
			}
		}
	}
}

func TestFocusDimmingAfterWrappingRawMode(t *testing.T) {
	// same test but in raw mode — text is identical for plain paragraph
	text := "The human visual system is remarkably sensitive to motion. When scrolling through a document, even slight stutters are noticeable. This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor."
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{
				{Text: text, Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.ToggleRawMode()
	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	runes := []rune(text)
	cursorPos := -1
	for i := 0; i < len(runes)-8; i++ {
		if string(runes[i:i+8]) == "optimize" {
			cursorPos = i
			break
		}
	}
	if cursorPos < 0 {
		t.Fatal("could not find 'optimize' in text")
	}
	ed.cursor = Pos{Block: 0, Col: cursorPos}

	ed.rebuildRenderCache()

	dimmedFG := ed.theme.Dimmed.FG
	var totalFocused string
	for _, line := range ed.cachedLines {
		for _, sp := range line {
			if sp.Style.FG != dimmedFG {
				totalFocused += sp.Text
			}
		}
	}

	expectedSentence := "This is why we optimize so aggressively, not for benchmarks, but for the feel of the editor."
	if totalFocused != expectedSentence {
		t.Errorf("focused text after wrapping (raw mode):\ngot:  %q\nwant: %q", totalFocused, expectedSentence)
	}
}

func TestRawModeFocusDimmingHeading(t *testing.T) {
	// heading in raw mode — text starts with "# " prefix
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH1, Runs: []Run{
				{Text: "First sentence. Second sentence. Third sentence.", Style: StyleNone},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 120
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// after expansion, text should include "# " prefix
	expandedText := doc.Blocks[0].Text()
	expectedExpanded := "# First sentence. Second sentence. Third sentence."
	if expandedText != expectedExpanded {
		t.Fatalf("expanded text: got %q, want %q", expandedText, expectedExpanded)
	}

	ed.focusMode = true
	ed.focusScope = FocusScopeSentence
	ed.mode = ModeInsert

	// cursor on 'S' in "Second" — position 19 (after "# First sentence. ")
	ed.cursor = Pos{Block: 0, Col: 19}

	sr := ed.InnerSentence()
	sentenceText := string([]rune(expandedText)[sr.Start.Col:sr.End.Col])
	expectedSentence := "Second sentence."
	if sentenceText != expectedSentence {
		t.Fatalf("InnerSentence: got %q, want %q (start=%d, end=%d)", sentenceText, expectedSentence, sr.Start.Col, sr.End.Col)
	}

	lines := ed.renderBlockRaw(&doc.Blocks[0], 0)
	if len(lines) == 0 {
		t.Fatal("renderBlockRaw returned no lines")
	}

	var allSpans []glyph.Span
	for _, line := range lines {
		allSpans = append(allSpans, line...)
	}

	dimmedFG := ed.theme.Dimmed.FG
	dimmed, focused := extractDimmedAndFocused(allSpans, dimmedFG)

	expectedFocused := "Second sentence."
	expectedDimmed := "# First sentence.  Third sentence."

	if focused != expectedFocused {
		t.Errorf("focused text: got %q, want %q", focused, expectedFocused)
	}
	if dimmed != expectedDimmed {
		t.Errorf("dimmed text: got %q, want %q", dimmed, expectedDimmed)
	}
}

func TestRawModeApplyStyleMultiBlock(t *testing.T) {
	// giiS (italic inner section) in raw mode should work across multiple blocks
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH2, Runs: []Run{{Text: "My Heading", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "First paragraph.", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "Second paragraph.", Style: StyleNone}}},
			{Type: BlockH2, Runs: []Run{{Text: "Next Heading", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// cursor in block 1 (first paragraph)
	ed.cursor = Pos{Block: 1, Col: 0}

	// InnerSection should return blocks 1-2
	sr := ed.InnerSection()
	if sr.Start.Block != 1 || sr.End.Block != 2 {
		t.Fatalf("InnerSection: got blocks %d-%d, want 1-2", sr.Start.Block, sr.End.Block)
	}

	// apply italic to the section
	ed.ApplyStyle(sr, StyleItalic)

	// both blocks should now have italic markers
	if doc.Blocks[1].Text() != "*First paragraph.*" {
		t.Errorf("block 1: got %q, want %q", doc.Blocks[1].Text(), "*First paragraph.*")
	}
	if doc.Blocks[2].Text() != "*Second paragraph.*" {
		t.Errorf("block 2: got %q, want %q", doc.Blocks[2].Text(), "*Second paragraph.*")
	}

	// heading blocks should be untouched
	if !strings.Contains(doc.Blocks[0].Text(), "My Heading") {
		t.Errorf("block 0 (heading) modified unexpectedly: %q", doc.Blocks[0].Text())
	}
}

func TestRawModeClearStyleMultiBlock(t *testing.T) {
	// gciS (clear style inner section) in raw mode should work across blocks
	doc := &Document{
		Blocks: []Block{
			{Type: BlockH2, Runs: []Run{{Text: "My Heading", Style: StyleNone}}},
			{Type: BlockParagraph, Runs: []Run{
				{Text: "First ", Style: StyleNone},
				{Text: "bold", Style: StyleBold},
				{Text: " paragraph.", Style: StyleNone},
			}},
			{Type: BlockParagraph, Runs: []Run{
				{Text: "Second ", Style: StyleNone},
				{Text: "italic", Style: StyleItalic},
				{Text: " paragraph.", Style: StyleNone},
			}},
			{Type: BlockH2, Runs: []Run{{Text: "Next Heading", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.ToggleRawMode()

	// after expansion, blocks should have markdown markers
	if !strings.Contains(doc.Blocks[1].Text(), "**bold**") {
		t.Fatalf("block 1 expansion: got %q", doc.Blocks[1].Text())
	}
	if !strings.Contains(doc.Blocks[2].Text(), "*italic*") {
		t.Fatalf("block 2 expansion: got %q", doc.Blocks[2].Text())
	}

	ed.cursor = Pos{Block: 1, Col: 0}
	sr := ed.InnerSection()

	// clear all styles in the section
	ed.ClearStyle(sr)

	// markers should be stripped from both blocks
	if strings.Contains(doc.Blocks[1].Text(), "**") {
		t.Errorf("block 1 still has bold markers: %q", doc.Blocks[1].Text())
	}
	if strings.Contains(doc.Blocks[2].Text(), "*") {
		t.Errorf("block 2 still has italic markers: %q", doc.Blocks[2].Text())
	}
}

func TestRawModeApplyStyleSingleBlockToggle(t *testing.T) {
	// applying bold twice to same single-block range should toggle off
	// (toggle detection works on single-block selections where markers surround the range)
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "Hello world", Style: StyleNone}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.ToggleRawMode()

	r := Range{
		Start: Pos{Block: 0, Col: 0},
		End:   Pos{Block: 0, Col: 11},
	}

	// apply bold
	ed.ApplyStyle(r, StyleBold)
	if doc.Blocks[0].Text() != "**Hello world**" {
		t.Errorf("after apply: got %q, want %q", doc.Blocks[0].Text(), "**Hello world**")
	}

	// apply bold again with inner range (between markers) — should toggle off
	r2 := Range{
		Start: Pos{Block: 0, Col: 2},  // after "**"
		End:   Pos{Block: 0, Col: 13}, // before "**"
	}
	ed.ApplyStyle(r2, StyleBold)
	if doc.Blocks[0].Text() != "Hello world" {
		t.Errorf("after toggle: got %q, want %q", doc.Blocks[0].Text(), "Hello world")
	}
}

// =============================================================================
// Code Block Editing Tests
// =============================================================================

func TestCodeLineNewLine(t *testing.T) {
	// enter in a code line block splits into two code line blocks
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "line1line2", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	ed.cursor = Pos{Block: 0, Col: 5}
	ed.NewLine()

	if len(doc.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Type != BlockCodeLine {
		t.Fatalf("block 0: expected BlockCodeLine, got %v", doc.Blocks[0].Type)
	}
	if doc.Blocks[1].Type != BlockCodeLine {
		t.Fatalf("block 1: expected BlockCodeLine, got %v", doc.Blocks[1].Type)
	}
	if doc.Blocks[0].Text() != "line1" {
		t.Errorf("block 0: got %q, want %q", doc.Blocks[0].Text(), "line1")
	}
	if doc.Blocks[1].Text() != "line2" {
		t.Errorf("block 1: got %q, want %q", doc.Blocks[1].Text(), "line2")
	}
	if doc.Blocks[1].Attrs["lang"] != "go" {
		t.Error("new code line should inherit lang attr")
	}
	if ed.cursor.Block != 1 || ed.cursor.Col != 0 {
		t.Errorf("cursor: got (%d,%d), want (1,0)", ed.cursor.Block, ed.cursor.Col)
	}
}

func TestCodeLineNewLineAtEnd(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "hello", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	ed.cursor = Pos{Block: 0, Col: 5}
	ed.NewLine()

	if len(doc.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(doc.Blocks))
	}
	if doc.Blocks[1].Text() != "" {
		t.Errorf("new block: got %q, want empty", doc.Blocks[1].Text())
	}
	if doc.Blocks[1].Type != BlockCodeLine {
		t.Fatalf("new block should be BlockCodeLine")
	}
}

func TestCodeLineBackspaceAtGroupStart(t *testing.T) {
	// backspace at col 0 on first code line should not merge with paragraph above
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "above"}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "code here", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	ed.cursor = Pos{Block: 1, Col: 0}
	ed.Backspace()

	if len(doc.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(doc.Blocks))
	}
	if doc.Blocks[1].Type != BlockCodeLine {
		t.Fatalf("block 1 should still be BlockCodeLine")
	}
	if doc.Blocks[1].Text() != "code here" {
		t.Errorf("block 1 text: got %q, want %q", doc.Blocks[1].Text(), "code here")
	}
}

func TestCodeLineBackspaceMergesCodeLines(t *testing.T) {
	// backspace at col 0 on second code line merges with previous code line
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "line1", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "line2", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	ed.cursor = Pos{Block: 1, Col: 0}
	ed.Backspace()

	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Text() != "line1line2" {
		t.Errorf("got %q, want %q", doc.Blocks[0].Text(), "line1line2")
	}
	if ed.cursor.Col != 5 {
		t.Errorf("cursor col: got %d, want 5", ed.cursor.Col)
	}
}

func TestCodeLineInsertText(t *testing.T) {
	// pasting multiline text into a code line creates multiple code line blocks
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "existing", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	ed.cursor = Pos{Block: 0, Col: 8}
	ed.InsertText("\nfunc main() {\n    fmt.Println(\"hello\")\n}")

	// should create 4 blocks (existing + 3 new lines from paste)
	if len(doc.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(doc.Blocks))
	}
	for i, b := range doc.Blocks {
		if b.Type != BlockCodeLine {
			t.Errorf("block %d: expected BlockCodeLine, got %v", i, b.Type)
		}
	}
	if doc.Blocks[0].Text() != "existing" {
		t.Errorf("block 0: got %q", doc.Blocks[0].Text())
	}
	if doc.Blocks[1].Text() != "func main() {" {
		t.Errorf("block 1: got %q", doc.Blocks[1].Text())
	}
}

func TestCodeLineOpenBelow(t *testing.T) {
	// o in a code line should create a new code line, not a paragraph
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "line1", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "line2", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 0, Col: 0}

	ed.OpenBelow()

	if len(doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(doc.Blocks))
	}
	if doc.Blocks[1].Type != BlockCodeLine {
		t.Errorf("new block should be BlockCodeLine, got %v", doc.Blocks[1].Type)
	}
	if doc.Blocks[1].Attrs["lang"] != "go" {
		t.Error("new code line should inherit lang attr")
	}
	if ed.cursor.Block != 1 {
		t.Errorf("cursor block: got %d, want 1", ed.cursor.Block)
	}
}

func TestCodeLineDeleteLine(t *testing.T) {
	// dd on a code line should delete just that line
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "line1", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "line2", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "line3", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 1, Col: 0}

	ed.DeleteLine()

	if len(doc.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Text() != "line1" {
		t.Errorf("block 0: got %q", doc.Blocks[0].Text())
	}
	if doc.Blocks[1].Text() != "line3" {
		t.Errorf("block 1: got %q", doc.Blocks[1].Text())
	}
}

func TestCodeLineJoinLines(t *testing.T) {
	// J on a code line should join it with the next code line
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "line1", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "line2", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 0, Col: 0}

	ed.JoinLines()

	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Text() != "line1 line2" {
		t.Errorf("got %q, want %q", doc.Blocks[0].Text(), "line1 line2")
	}
}

func TestCodeLineCursorScreenPos(t *testing.T) {
	// cursor position should account for template prefix
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "above"}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "line1", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "line2", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.rebuildRenderCache()

	// cursor on first code line
	ed.cursor = Pos{Block: 1, Col: 0}
	x1, y1 := ed.CursorScreenPos()

	// cursor on second code line
	ed.cursor = Pos{Block: 2, Col: 0}
	x2, y2 := ed.CursorScreenPos()

	// x should be at template prefix for col 0
	_, xOff := ed.codeLineScreenOffset(1)
	margin := (ed.screenWidth - contentWidth) / 2
	expectedX := margin + xOff
	if x1 != expectedX {
		t.Errorf("x at col 0: got %d, want %d (margin=%d, xOff=%d)", x1, expectedX, margin, xOff)
	}
	if x2 != expectedX {
		t.Errorf("x at line2 col 0: got %d, want %d", x2, expectedX)
	}

	// y should differ (second line is below first)
	if y2 <= y1 {
		t.Errorf("y positions: line1=%d line2=%d, expected line2 > line1", y1, y2)
	}
}

// =============================================================================
// Code Line Auto-Indent Tests
// =============================================================================

func TestCodeLineNewLineAutoIndent(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "\t\tline1stuff", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	// press enter mid-line — new block should inherit the \t\t indent
	ed.cursor = Pos{Block: 0, Col: 7} // after "\t\tline1"
	ed.NewLine()

	if len(doc.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(doc.Blocks))
	}
	// new block text should start with the same indentation
	newText := doc.Blocks[1].Text()
	if !strings.HasPrefix(newText, "\t\t") {
		t.Errorf("new block should start with \\t\\t indent, got %q", newText)
	}
	// cursor should be positioned after the indent
	if ed.cursor.Col != 2 {
		t.Errorf("cursor col: got %d, want 2 (after indent)", ed.cursor.Col)
	}
}

func TestCodeLineNewLineAutoIndentSpaces(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "    return nil", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	ed.cursor = Pos{Block: 0, Col: 14} // at end
	ed.NewLine()

	newText := doc.Blocks[1].Text()
	if !strings.HasPrefix(newText, "    ") {
		t.Errorf("expected 4-space indent, got %q", newText)
	}
	if ed.cursor.Col != 4 {
		t.Errorf("cursor col: got %d, want 4", ed.cursor.Col)
	}
}

func TestCodeLineOpenBelowAutoIndent(t *testing.T) {
	// o on "func main() {" should match the NEXT line's indent ("\tfmt...")
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "func main() {", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "\tfmt.Println()", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
			{Type: BlockCodeLine, Runs: []Run{
				{Text: "}", Style: StyleCode},
			}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 0, Col: 5}

	ed.OpenBelow()

	if len(doc.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(doc.Blocks))
	}
	// new block at index 1 should have the next line's indent (\t)
	if doc.Blocks[1].Text() != "\t" {
		t.Errorf("new block: expected %q, got %q", "\t", doc.Blocks[1].Text())
	}
	if ed.cursor.Col != 1 {
		t.Errorf("cursor col: got %d, want 1", ed.cursor.Col)
	}
}

func TestCodeLineOpenBelowFallsBackToCurrent(t *testing.T) {
	// o on last code line (no next line) falls back to current line's indent
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "\treturn nil", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 0, Col: 0}

	ed.OpenBelow()

	if doc.Blocks[1].Text() != "\t" {
		t.Errorf("new block: expected %q, got %q", "\t", doc.Blocks[1].Text())
	}
}

func TestCodeLineOpenAboveAutoIndent(t *testing.T) {
	// O on "\tfmt.Println()" should match the PREVIOUS line's indent ("func...")
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "func main() {", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "\tfmt.Println()", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 1, Col: 0}

	ed.OpenAbove()

	if len(doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(doc.Blocks))
	}
	// block 1 is the new one (inserted above cursor which was at 1)
	// should have previous line's indent (none — "func main() {" has no indent)
	if doc.Blocks[1].Text() != "" {
		t.Errorf("new block: expected %q, got %q", "", doc.Blocks[1].Text())
	}
	if ed.cursor.Col != 0 {
		t.Errorf("cursor col: got %d, want 0", ed.cursor.Col)
	}
}

func TestCodeLineOpenAboveFallsBackToCurrent(t *testing.T) {
	// O on first code line (no prev) falls back to current line's indent
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "    code", Style: StyleCode}},
				Attrs: map[string]string{"lang": "go"}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.cursor = Pos{Block: 0, Col: 0}

	ed.OpenAbove()

	if doc.Blocks[0].Text() != "    " {
		t.Errorf("new block: expected %q, got %q", "    ", doc.Blocks[0].Text())
	}
	if ed.cursor.Col != 4 {
		t.Errorf("cursor col: got %d, want 4", ed.cursor.Col)
	}
}

func TestCodeLineNoAutoIndentForParagraph(t *testing.T) {
	// paragraphs should NOT get auto-indent
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "   some text"}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24
	ed.mode = ModeInsert

	ed.cursor = Pos{Block: 0, Col: 12}
	ed.NewLine()

	newText := doc.Blocks[1].Text()
	if newText != "" {
		t.Errorf("paragraph newline should not auto-indent, got %q", newText)
	}
	if ed.cursor.Col != 0 {
		t.Errorf("cursor col: got %d, want 0", ed.cursor.Col)
	}
}

// =============================================================================
// Cross-Block Bracket Matching Tests
// =============================================================================

func TestCrossBlockBracketMatchingCurly(t *testing.T) {
	// func main() {
	//     fmt.Println("hello")
	// }
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "func main() {", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "    fmt.Println(\"hello\")", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "}", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	// cursor inside the braces, on the middle line
	ed.cursor = Pos{Block: 1, Col: 4}
	r := ed.InnerCurly()

	// inner should span from after { (col 12) to before } (col 0)
	if r.Start.Block != 0 || r.Start.Col != 13 {
		t.Errorf("inner curly start: got (%d,%d), want (0,13)", r.Start.Block, r.Start.Col)
	}
	if r.End.Block != 2 || r.End.Col != 0 {
		t.Errorf("inner curly end: got (%d,%d), want (2,0)", r.End.Block, r.End.Col)
	}
}

func TestCrossBlockBracketMatchingAround(t *testing.T) {
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "func main() {", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "    return", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "}", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	ed.cursor = Pos{Block: 1, Col: 0}
	r := ed.AroundCurly()

	// around should include the braces themselves
	if r.Start.Block != 0 || r.Start.Col != 12 {
		t.Errorf("around curly start: got (%d,%d), want (0,12)", r.Start.Block, r.Start.Col)
	}
	if r.End.Block != 2 || r.End.Col != 1 {
		t.Errorf("around curly end: got (%d,%d), want (2,1)", r.End.Block, r.End.Col)
	}
}

func TestCrossBlockNestedBrackets(t *testing.T) {
	// if x {
	//     if y {
	//         z
	//     }
	// }
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "if x {", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "    if y {", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "        z", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "    }", Style: StyleCode}}},
			{Type: BlockCodeLine, Runs: []Run{{Text: "}", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	// cursor on "z" — innermost braces are the inner if
	ed.cursor = Pos{Block: 2, Col: 8}
	r := ed.InnerCurly()

	if r.Start.Block != 1 || r.Start.Col != 10 { // col 9 is {, col 10 is inner start
		t.Errorf("nested inner start: got (%d,%d), want (1,10)", r.Start.Block, r.Start.Col)
	}
	if r.End.Block != 3 || r.End.Col != 4 {
		t.Errorf("nested inner end: got (%d,%d), want (3,4)", r.End.Block, r.End.Col)
	}
}

func TestSingleBlockBracketStillWorks(t *testing.T) {
	// same-line brackets should still work without cross-block
	doc := &Document{
		Blocks: []Block{
			{Type: BlockCodeLine, Runs: []Run{{Text: "x := map[string]int{}", Style: StyleCode}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	ed.cursor = Pos{Block: 0, Col: 20} // on the closing }
	r := ed.InnerCurly()

	if r.Start.Block != 0 || r.Start.Col != 20 {
		t.Errorf("single-block inner: got (%d,%d) to (%d,%d)", r.Start.Block, r.Start.Col, r.End.Block, r.End.Col)
	}
}

func TestCrossBlockBracketParagraphDoesNotCross(t *testing.T) {
	// paragraph blocks should NOT cross block boundaries
	doc := &Document{
		Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "Some text {"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "more text"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "} end"}}},
		},
	}
	ed := NewEditor(doc, "")
	ed.screenWidth = 80
	ed.screenHeight = 24

	ed.cursor = Pos{Block: 1, Col: 0}
	r := ed.InnerCurly()

	// should NOT find a match (paragraphs don't cross blocks)
	if r.Start != r.End {
		t.Errorf("paragraph should not cross blocks, got range (%d,%d)-(%d,%d)",
			r.Start.Block, r.Start.Col, r.End.Block, r.End.Col)
	}
}
