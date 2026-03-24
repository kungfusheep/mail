package compose

import (
	"strings"
	"testing"
)

func TestNewEditor(t *testing.T) {
	ed := NewEditor()
	if ed.Mode() != ModeNormal {
		t.Errorf("expected ModeNormal, got %v", ed.Mode())
	}
	if len(ed.Doc().Blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(ed.Doc().Blocks))
	}
	if ed.Doc().Blocks[0].Type != BlockParagraph {
		t.Errorf("expected BlockParagraph, got %v", ed.Doc().Blocks[0].Type)
	}
}

func TestInsertChar(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello world" {
		ed.InsertChar(ch)
	}

	text := ed.Doc().Blocks[0].Text()
	if text != "hello world" {
		t.Errorf("expected 'hello world', got %q", text)
	}
	if ed.Cursor().Col != 11 {
		t.Errorf("expected col 11, got %d", ed.Cursor().Col)
	}
}

func TestBackspace(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello" {
		ed.InsertChar(ch)
	}
	ed.Backspace()

	text := ed.Doc().Blocks[0].Text()
	if text != "hell" {
		t.Errorf("expected 'hell', got %q", text)
	}
}

func TestBackspaceMergesBlocks(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello" {
		ed.InsertChar(ch)
	}
	ed.NewLine()
	for _, ch := range "world" {
		ed.InsertChar(ch)
	}

	if len(ed.Doc().Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(ed.Doc().Blocks))
	}

	// move to start of second block and backspace
	ed.EnterNormal()
	ed.SetCursor(Pos{Block: 1, Col: 0})
	ed.EnterInsert()
	ed.Backspace()

	if len(ed.Doc().Blocks) != 1 {
		t.Errorf("expected 1 block after merge, got %d", len(ed.Doc().Blocks))
	}
	if ed.Doc().Blocks[0].Text() != "helloworld" {
		t.Errorf("expected 'helloworld', got %q", ed.Doc().Blocks[0].Text())
	}
}

func TestNewLine(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello" {
		ed.InsertChar(ch)
	}
	ed.NewLine()
	for _, ch := range "world" {
		ed.InsertChar(ch)
	}

	if len(ed.Doc().Blocks) != 2 {
		t.Errorf("expected 2 blocks, got %d", len(ed.Doc().Blocks))
	}
	if ed.Doc().Blocks[0].Text() != "hello" {
		t.Errorf("expected 'hello', got %q", ed.Doc().Blocks[0].Text())
	}
	if ed.Doc().Blocks[1].Text() != "world" {
		t.Errorf("expected 'world', got %q", ed.Doc().Blocks[1].Text())
	}
}

func TestNewLineSplitsBlock(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "helloworld" {
		ed.InsertChar(ch)
	}
	// move cursor to middle
	ed.EnterNormal()
	ed.SetCursor(Pos{Block: 0, Col: 5})
	ed.EnterInsert()
	ed.NewLine()

	if len(ed.Doc().Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(ed.Doc().Blocks))
	}
	if ed.Doc().Blocks[0].Text() != "hello" {
		t.Errorf("expected 'hello', got %q", ed.Doc().Blocks[0].Text())
	}
	if ed.Doc().Blocks[1].Text() != "world" {
		t.Errorf("expected 'world', got %q", ed.Doc().Blocks[1].Text())
	}
}

func TestUndoRedo(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello" {
		ed.InsertChar(ch)
	}
	ed.EnterNormal()

	// delete a word
	ed.SetCursor(Pos{Block: 0, Col: 0})
	r := ed.InnerWord()
	ed.Delete(r)

	if ed.Doc().Blocks[0].Text() != "" {
		t.Errorf("expected empty after delete, got %q", ed.Doc().Blocks[0].Text())
	}

	ed.Undo()
	if ed.Doc().Blocks[0].Text() != "hello" {
		t.Errorf("expected 'hello' after undo, got %q", ed.Doc().Blocks[0].Text())
	}

	ed.Redo()
	if ed.Doc().Blocks[0].Text() != "" {
		t.Errorf("expected empty after redo, got %q", ed.Doc().Blocks[0].Text())
	}
}

func TestMarkdownUpgrade(t *testing.T) {
	tests := []struct {
		input    string
		wantType BlockType
		wantText string
	}{
		{"# heading", BlockH1, "heading"},
		{"## sub", BlockH2, "sub"},
		{"### third", BlockH3, "third"},
		{"> quoted", BlockQuote, "quoted"},
		{"- item", BlockListItem, "item"},
	}

	for _, tt := range tests {
		ed := NewEditor()
		ed.SetSize(80, 24)
		ed.EnterInsert()

		for _, ch := range tt.input {
			ed.InsertChar(ch)
		}

		b := ed.Doc().Blocks[0]
		if b.Type != tt.wantType {
			t.Errorf("input %q: expected type %v, got %v", tt.input, tt.wantType, b.Type)
		}
		if b.Text() != tt.wantText {
			t.Errorf("input %q: expected text %q, got %q", tt.input, tt.wantText, b.Text())
		}
	}
}

func TestStyleApplication(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello world" {
		ed.InsertChar(ch)
	}
	ed.EnterNormal()

	// bold "hello"
	ed.ApplyStyleRange(Range{
		Start: Pos{Block: 0, Col: 0},
		End:   Pos{Block: 0, Col: 5},
	}, StyleBold)

	runs := ed.Doc().Blocks[0].Runs
	if len(runs) < 2 {
		t.Fatalf("expected at least 2 runs, got %d", len(runs))
	}
	if !runs[0].Style.Has(StyleBold) {
		t.Error("expected first run to be bold")
	}
	if runs[1].Style.Has(StyleBold) {
		t.Error("expected second run to not be bold")
	}
}

func TestToHTML(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello" {
		ed.InsertChar(ch)
	}

	html := ed.ToHTML()
	if !strings.Contains(html, "<p>hello</p>") {
		t.Errorf("expected html to contain <p>hello</p>, got %q", html)
	}
}

func TestToHTMLWithStyles(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello world" {
		ed.InsertChar(ch)
	}
	ed.EnterNormal()

	ed.ApplyStyleRange(Range{
		Start: Pos{Block: 0, Col: 0},
		End:   Pos{Block: 0, Col: 5},
	}, StyleBold)

	html := ed.ToHTML()
	if !strings.Contains(html, "<strong>hello</strong>") {
		t.Errorf("expected <strong>hello</strong> in html, got %q", html)
	}
}

func TestToPlainText(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "line one" {
		ed.InsertChar(ch)
	}
	ed.NewLine()
	for _, ch := range "line two" {
		ed.InsertChar(ch)
	}

	text := ed.ToPlainText()
	if text != "line one\nline two" {
		t.Errorf("expected 'line one\\nline two', got %q", text)
	}
}

func TestMovement(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello world" {
		ed.InsertChar(ch)
	}
	ed.EnterNormal()

	// line start
	p := ed.LineStart()
	if p.Col != 0 {
		t.Errorf("LineStart: expected col 0, got %d", p.Col)
	}

	// line end
	p = ed.LineEnd()
	if p.Col != 10 { // normal mode, one less
		t.Errorf("LineEnd: expected col 10, got %d", p.Col)
	}

	// word forward from start
	ed.SetCursor(Pos{Block: 0, Col: 0})
	p = ed.WordForward()
	if p.Col != 6 {
		t.Errorf("WordForward: expected col 6, got %d", p.Col)
	}
}

func TestDeleteRange(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello beautiful world" {
		ed.InsertChar(ch)
	}
	ed.EnterNormal()

	deleted := ed.Delete(Range{
		Start: Pos{Block: 0, Col: 5},
		End:   Pos{Block: 0, Col: 15},
	})

	if deleted != " beautiful" {
		t.Errorf("expected ' beautiful', got %q", deleted)
	}
	if ed.Doc().Blocks[0].Text() != "hello world" {
		t.Errorf("expected 'hello world', got %q", ed.Doc().Blocks[0].Text())
	}
}

func TestListItemContinuation(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	// type "- item one" to create list item
	for _, ch := range "- item one" {
		ed.InsertChar(ch)
	}

	if ed.Doc().Blocks[0].Type != BlockListItem {
		t.Fatalf("expected BlockListItem, got %v", ed.Doc().Blocks[0].Type)
	}

	// enter to continue list
	ed.NewLine()
	if len(ed.Doc().Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(ed.Doc().Blocks))
	}
	if ed.Doc().Blocks[1].Type != BlockListItem {
		t.Errorf("expected new block to be BlockListItem, got %v", ed.Doc().Blocks[1].Type)
	}

	// enter on empty list item exits list
	ed.NewLine()
	if ed.Doc().Blocks[1].Type != BlockParagraph {
		t.Errorf("expected empty list item to become paragraph, got %v", ed.Doc().Blocks[1].Type)
	}
}

func TestVisualRange(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello world" {
		ed.InsertChar(ch)
	}
	ed.EnterNormal()

	ed.SetCursor(Pos{Block: 0, Col: 0})
	ed.EnterVisual()
	ed.SetCursor(Pos{Block: 0, Col: 4})

	r := ed.VisualRange()
	if r.Start.Col != 0 || r.End.Col != 4 {
		t.Errorf("expected range 0-4, got %d-%d", r.Start.Col, r.End.Col)
	}
}

func TestInnerWord(t *testing.T) {
	ed := NewEditor()
	ed.SetSize(80, 24)
	ed.EnterInsert()

	for _, ch := range "hello world" {
		ed.InsertChar(ch)
	}
	ed.EnterNormal()
	ed.SetCursor(Pos{Block: 0, Col: 1}) // inside "hello"

	r := ed.InnerWord()
	if r.Start.Col != 0 || r.End.Col != 5 {
		t.Errorf("expected 0-5, got %d-%d", r.Start.Col, r.End.Col)
	}
}
