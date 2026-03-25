package compose

import (
	"fmt"
	"strings"
	"testing"
	"github.com/kungfusheep/glyph"
)

func TestInsertTextSingleLine(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: "hello world"}}},
			},
		},
		cursor: Pos{Block: 0, Col: 5},
	}

	ed.InsertText(" there")

	got := ed.doc.Blocks[0].Text()
	want := "hello there world"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertTextMultiLine(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: "start end"}}},
			},
		},
		cursor: Pos{Block: 0, Col: 5},
	}

	ed.InsertText("line1\nline2\nline3")

	if len(ed.doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(ed.doc.Blocks))
	}

	tests := []struct {
		idx  int
		want string
	}{
		{0, "startline1"},
		{1, "line2"},
		{2, "line3 end"},
	}

	for _, tt := range tests {
		got := ed.doc.Blocks[tt.idx].Text()
		if got != tt.want {
			t.Errorf("block[%d]: got %q, want %q", tt.idx, got, tt.want)
		}
	}
}

func TestInsertTextUTF8(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: "hello world"}}},
			},
		},
		cursor: Pos{Block: 0, Col: 5},
	}

	ed.InsertText(" café")

	got := ed.doc.Blocks[0].Text()
	want := "hello café world"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertTextCurlyQuotes(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
			},
		},
		cursor: Pos{Block: 0, Col: 0},
	}

	// test with curly quotes which are multi-byte UTF-8
	ed.InsertText("\u201cHello\u201d said the man.")

	got := ed.doc.Blocks[0].Text()
	want := "\u201cHello\u201d said the man."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertTextPreservesSpaces(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
			},
		},
		cursor: Pos{Block: 0, Col: 0},
	}

	ed.InsertText("Standard Ebooks")

	got := ed.doc.Blocks[0].Text()
	want := "Standard Ebooks"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertTextMultiLineWithSpaces(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
			},
		},
		cursor: Pos{Block: 0, Col: 0},
	}

	ed.InsertText("Standard Ebooks\nAct IV\npublic domain")

	if len(ed.doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(ed.doc.Blocks))
	}

	tests := []struct {
		idx  int
		want string
	}{
		{0, "Standard Ebooks"},
		{1, "Act IV"},
		{2, "public domain"},
	}

	for _, tt := range tests {
		got := ed.doc.Blocks[tt.idx].Text()
		if got != tt.want {
			t.Errorf("block[%d]: got %q, want %q", tt.idx, got, tt.want)
		}
	}
}

func TestCleanPasteTextPreservesSpaces(t *testing.T) {
	input := "Standard Ebooks"
	got := cleanPasteText(input)
	if got != input {
		t.Errorf("cleanPasteText(%q) = %q, want %q", input, got, input)
	}
}

func TestCleanPasteTextStripsEscapes(t *testing.T) {
	// CSI sequence embedded in text
	input := "hello\x1b[31mworld"
	got := cleanPasteText(input)
	want := "helloworld"
	if got != want {
		t.Errorf("cleanPasteText(%q) = %q, want %q", input, got, want)
	}
}

func TestCleanPasteTextConvertsTabs(t *testing.T) {
	// TABs should be converted to 4 spaces to avoid cursor tracking issues
	input := "hello\tworld"
	got := cleanPasteText(input)
	want := "hello    world" // tab becomes 4 spaces
	if got != want {
		t.Errorf("cleanPasteText(%q) = %q, want %q", input, got, want)
	}

	// multiple tabs
	input2 := "\t\tindented"
	got2 := cleanPasteText(input2)
	want2 := "        indented" // 8 spaces (2 tabs)
	if got2 != want2 {
		t.Errorf("cleanPasteText(%q) = %q, want %q", input2, got2, want2)
	}
}

func TestCleanPasteTextHandlesCRLF(t *testing.T) {
	// Windows CRLF should become single LF
	input := "line1\r\nline2\r\nline3"
	got := cleanPasteText(input)
	want := "line1\nline2\nline3"
	if got != want {
		t.Errorf("cleanPasteText(%q) = %q, want %q", input, got, want)
	}

	// standalone CR (old Mac) should become LF
	input2 := "line1\rline2\rline3"
	got2 := cleanPasteText(input2)
	want2 := "line1\nline2\nline3"
	if got2 != want2 {
		t.Errorf("cleanPasteText(%q) = %q, want %q", input2, got2, want2)
	}
}

func TestLargeDocumentRender(t *testing.T) {
	// test rendering a large document with many blocks
	ed := &Editor{
		doc:          &Document{},
		screenWidth:  80,
		screenHeight: 24,
		theme:        DefaultTheme(),
	}

	// create 1500 blocks like the Shakespeare paste
	for i := 0; i < 1500; i++ {
		text := fmt.Sprintf("Line %d: Your charm so strongly works 'em That if you now beheld them.", i)
		ed.doc.Blocks = append(ed.doc.Blocks, Block{
			Type: BlockParagraph,
			Runs: []Run{{Text: text}},
		})
	}

	// force rebuild render cache
	ed.InvalidateCache()
	ed.rebuildRenderCache()

	// verify some blocks render correctly
	for _, idx := range []int{0, 500, 1000, 1499} {
		if idx >= len(ed.doc.Blocks) {
			continue
		}

		origText := ed.doc.Blocks[idx].Text()

		// find this block in cached lines
		mapping := ed.blockLines[idx]

		// reconstruct text from cached lines
		var renderedText string
		for lineNum := 0; lineNum < mapping.lineCount; lineNum++ {
			lineIdx := mapping.screenLine + lineNum
			if lineIdx < len(ed.cachedLines) {
				for _, span := range ed.cachedLines[lineIdx] {
					renderedText += span.Text
				}
			}
		}

		// remove wrapping artifacts (spaces at line breaks)
		origClean := strings.ReplaceAll(origText, " ", "")
		renderedClean := strings.ReplaceAll(renderedText, " ", "")

		if origClean != renderedClean {
			t.Errorf("block %d mismatch:\n  orig:     %q\n  rendered: %q", idx, origText, renderedText)
		}
	}
}

func TestLargeDocumentFullRenderPipeline(t *testing.T) {
	// test the FULL render pipeline including layer blit to screen buffer
	ed := &Editor{
		doc:          &Document{},
		screenWidth:  80,
		screenHeight: 24,
		theme:        DefaultTheme(),
		layer:        glyph.NewLayer(),
	}

	// create 1500 blocks like the Shakespeare paste
	for i := 0; i < 1500; i++ {
		text := fmt.Sprintf("Line %d: Your charm so strongly works 'em That if you now beheld them.", i)
		ed.doc.Blocks = append(ed.doc.Blocks, Block{
			Type: BlockParagraph,
			Runs: []Run{{Text: text}},
		})
	}

	// simulate paste: insert text and refresh
	ed.InvalidateCache()
	ed.UpdateDisplay()

	// create a screen buffer to blit into (simulating what App does)
	screenBuf := glyph.NewBuffer(80, 24)

	// blit from layer to screen buffer (simulating template execution)
	ed.layer.SetViewport(80, 24)
	layerBuf := ed.layer.Buffer()
	if layerBuf != nil {
		screenBuf.Blit(layerBuf, 0, ed.layer.ScrollY(), 0, 0, 80, 24)
	}

	// verify visible lines are correct by reading from screen buffer
	// cachedLines includes virtual padding at start, so simple indexing works
	for screenY := 0; screenY < 24; screenY++ {
		lineIdx := ed.topLine + screenY
		if lineIdx >= len(ed.cachedLines) {
			break
		}

		// reconstruct what we expect to see on this screen line
		expectedLine := ed.cachedLines[lineIdx]

		// read actual content from screen buffer
		margin := (80 - contentWidth) / 2
		var actualText strings.Builder
		for x := margin; x < 80; x++ {
			cell := screenBuf.Get(x, screenY)
			if cell.Rune == 0 {
				break
			}
			actualText.WriteRune(cell.Rune)
		}

		// build expected text from spans
		var expectedText strings.Builder
		for _, span := range expectedLine {
			expectedText.WriteString(span.Text)
		}

		// trim trailing spaces for comparison (buffer may have padding)
		actual := strings.TrimRight(actualText.String(), " ")
		expected := strings.TrimRight(expectedText.String(), " ")

		if actual != expected {
			t.Errorf("screen line %d (cache line %d) mismatch:\n  expected: %q\n  actual:   %q",
				screenY, lineIdx, expected, actual)
		}
	}

	// test scrolling to middle of document
	ed.topLine = 750
	ed.UpdateDisplay()

	// blit again
	screenBuf.Clear()
	layerBuf = ed.layer.Buffer()
	if layerBuf != nil {
		screenBuf.Blit(layerBuf, 0, ed.layer.ScrollY(), 0, 0, 80, 24)
	}

	// verify scrolled view
	for screenY := 0; screenY < 24; screenY++ {
		lineIdx := ed.topLine + screenY
		if lineIdx >= len(ed.cachedLines) {
			break
		}

		expectedLine := ed.cachedLines[lineIdx]

		margin := (80 - contentWidth) / 2
		var actualText strings.Builder
		for x := margin; x < 80; x++ {
			cell := screenBuf.Get(x, screenY)
			if cell.Rune == 0 {
				break
			}
			actualText.WriteRune(cell.Rune)
		}

		var expectedText strings.Builder
		for _, span := range expectedLine {
			expectedText.WriteString(span.Text)
		}

		// trim trailing spaces for comparison
		actual := strings.TrimRight(actualText.String(), " ")
		expected := strings.TrimRight(expectedText.String(), " ")

		if actual != expected {
			t.Errorf("scrolled screen line %d (cache line %d) mismatch:\n  expected: %q\n  actual:   %q",
				screenY, lineIdx, expected, actual)
		}
	}
}

func TestParseMarkdownLongLine(t *testing.T) {
	// test parsing and rendering a long line with curly quotes
	content := "Your charm so strongly works 'em That if you now beheld them, your affections Would become tender."

	doc := ParseMarkdown(content)

	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Blocks))
	}

	blockText := doc.Blocks[0].Text()
	if blockText != content {
		t.Errorf("block text mismatch:\n  got:  %q\n  want: %q", blockText, content)
	}
}

func TestWrapSpansWithCurlyQuotes(t *testing.T) {
	// test wrapping with curly quotes (multi-byte UTF-8)
	spans := []glyph.Span{{Text: "Your charm so strongly works 'em That if you now beheld them, your affections Would become tender."}}

	wrapped := wrapSpans(spans, 72)

	// reconstruct the full text from wrapped lines
	var result string
	for i, line := range wrapped {
		for _, span := range line {
			result += span.Text
		}
		if i < len(wrapped)-1 {
			result += "|" // mark line breaks
		}
	}

	// verify no characters are lost
	original := "Your charm so strongly works 'em That if you now beheld them, your affections Would become tender."
	resultClean := strings.ReplaceAll(result, "|", "")

	// count runes
	origRunes := []rune(original)
	resultRunes := []rune(resultClean)

	if len(origRunes) != len(resultRunes) {
		t.Errorf("rune count mismatch: original=%d, result=%d", len(origRunes), len(resultRunes))
		t.Errorf("original: %q", original)
		t.Errorf("result: %q", resultClean)
	}

	// check each character
	for i := 0; i < len(origRunes) && i < len(resultRunes); i++ {
		if origRunes[i] != resultRunes[i] {
			t.Errorf("mismatch at position %d: original=%q result=%q", i, string(origRunes[i]), string(resultRunes[i]))
		}
	}
}

func TestInsertTextWebPageLikePaste(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
			},
		},
		cursor: Pos{Block: 0, Col: 0},
	}

	// simulate typical web page copy with multiple newlines and spaces
	paste := "    Standard Ebooks\n    Back to ebook\n\nThe Tempest\n\nBy William Shakespeare.\nTable of Contents"

	ed.InsertText(paste)

	tests := []struct {
		idx  int
		want string
	}{
		{0, "    Standard Ebooks"},
		{1, "    Back to ebook"},
		{2, ""},
		{3, "The Tempest"},
		{4, ""},
		{5, "By William Shakespeare."},
		{6, "Table of Contents"},
	}

	if len(ed.doc.Blocks) != len(tests) {
		t.Fatalf("expected %d blocks, got %d", len(tests), len(ed.doc.Blocks))
	}

	for _, tt := range tests {
		got := ed.doc.Blocks[tt.idx].Text()
		if got != tt.want {
			t.Errorf("block[%d]: got %q, want %q", tt.idx, got, tt.want)
		}
	}
}

func TestInsertTextLargePaste(t *testing.T) {
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
			},
		},
		cursor: Pos{Block: 0, Col: 0},
	}

	// simulate a large paste (similar to web page content)
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString(fmt.Sprintf("Line %d with some content and spaces here\n", i))
	}
	paste := sb.String()

	ed.InsertText(paste)

	// should have 1001 blocks (1000 lines + empty from trailing newline, merged with original empty)
	if len(ed.doc.Blocks) != 1001 {
		t.Fatalf("expected 1001 blocks, got %d", len(ed.doc.Blocks))
	}

	// spot check some blocks
	for _, i := range []int{0, 100, 500, 999} {
		text := ed.doc.Blocks[i].Text()
		want := fmt.Sprintf("Line %d with some content and spaces here", i)
		if text != want {
			t.Errorf("block[%d]: got %q, want %q", i, text, want)
		}
	}
}

func TestInsertTextVerifyNoNCorruption(t *testing.T) {
	// specifically test that spaces don't become 'n'
	ed := &Editor{
		doc: &Document{
			Blocks: []Block{
				{Type: BlockParagraph, Runs: []Run{{Text: ""}}},
			},
		},
		cursor: Pos{Block: 0, Col: 0},
	}

	ed.InsertText("Standard Ebooks\nAct IV\npublic")

	// check none of the blocks contain unexpected 'n' corruption
	corruptions := []struct {
		bad  string
		good string
	}{
		{"StandardnEbooks", "Standard Ebooks"},
		{"ActnIV", "Act IV"},
		{"ActaIV", "Act IV"},
		{"pu'lic", "public"},
	}

	for i := range ed.doc.Blocks {
		text := ed.doc.Blocks[i].Text()
		for _, c := range corruptions {
			if text == c.bad {
				t.Errorf("block[%d] has corruption %q, expected %q", i, c.bad, c.good)
			}
		}
	}
}
