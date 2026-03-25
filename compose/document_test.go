package compose

import (
	"testing"
)

func TestInlineStyle(t *testing.T) {
	tests := []struct {
		name   string
		style  InlineStyle
		check  InlineStyle
		has    bool
	}{
		{"none has bold", StyleNone, StyleBold, false},
		{"bold has bold", StyleBold, StyleBold, true},
		{"bold|italic has bold", StyleBold | StyleItalic, StyleBold, true},
		{"bold|italic has italic", StyleBold | StyleItalic, StyleItalic, true},
		{"bold has underline", StyleBold, StyleUnderline, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.style.Has(tt.check); got != tt.has {
				t.Errorf("Has() = %v, want %v", got, tt.has)
			}
		})
	}
}

func TestInlineStyleWith(t *testing.T) {
	s := StyleNone
	s = s.With(StyleBold)
	if !s.Has(StyleBold) {
		t.Error("expected bold")
	}
	s = s.With(StyleItalic)
	if !s.Has(StyleBold) || !s.Has(StyleItalic) {
		t.Error("expected bold and italic")
	}
}

func TestInlineStyleToggle(t *testing.T) {
	s := StyleBold
	s = s.Toggle(StyleBold)
	if s.Has(StyleBold) {
		t.Error("expected bold to be toggled off")
	}
	s = s.Toggle(StyleBold)
	if !s.Has(StyleBold) {
		t.Error("expected bold to be toggled on")
	}
}

func TestBlockText(t *testing.T) {
	b := Block{
		Type: BlockParagraph,
		Runs: []Run{
			{Text: "Hello ", Style: StyleNone},
			{Text: "world", Style: StyleBold},
		},
	}

	if got := b.Text(); got != "Hello world" {
		t.Errorf("Text() = %q, want %q", got, "Hello world")
	}
}

func TestBlockLength(t *testing.T) {
	b := Block{
		Type: BlockParagraph,
		Runs: []Run{
			{Text: "Hello ", Style: StyleNone},
			{Text: "world", Style: StyleBold},
		},
	}

	if got := b.Length(); got != 11 {
		t.Errorf("Length() = %d, want 11", got)
	}
}

func TestBlockRunAt(t *testing.T) {
	b := Block{
		Type: BlockParagraph,
		Runs: []Run{
			{Text: "Hello ", Style: StyleNone},
			{Text: "world", Style: StyleBold},
		},
	}

	tests := []struct {
		offset    int
		wantRun   int
		wantOff   int
	}{
		{0, 0, 0},
		{3, 0, 3},
		{6, 1, 0},
		{8, 1, 2},
		{11, 1, 5},
	}

	for _, tt := range tests {
		runIdx, runOff := b.RunAt(tt.offset)
		if runIdx != tt.wantRun || runOff != tt.wantOff {
			t.Errorf("RunAt(%d) = (%d, %d), want (%d, %d)",
				tt.offset, runIdx, runOff, tt.wantRun, tt.wantOff)
		}
	}
}

func TestBlockApplyStyle(t *testing.T) {
	b := Block{
		Type: BlockParagraph,
		Runs: []Run{
			{Text: "Hello world", Style: StyleNone},
		},
	}

	// bold "world" (index 6-11)
	b.ApplyStyle(6, 11, StyleBold)

	if len(b.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(b.Runs))
	}
	if b.Runs[0].Text != "Hello " || b.Runs[0].Style != StyleNone {
		t.Errorf("first run: got %q/%v", b.Runs[0].Text, b.Runs[0].Style)
	}
	if b.Runs[1].Text != "world" || b.Runs[1].Style != StyleBold {
		t.Errorf("second run: got %q/%v", b.Runs[1].Text, b.Runs[1].Style)
	}
}

func TestBlockApplyStyleToggle(t *testing.T) {
	b := Block{
		Type: BlockParagraph,
		Runs: []Run{
			{Text: "Hello", Style: StyleBold},
		},
	}

	// toggle bold off
	b.ApplyStyle(0, 5, StyleBold)

	if len(b.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(b.Runs))
	}
	if b.Runs[0].Style != StyleNone {
		t.Errorf("expected style none, got %v", b.Runs[0].Style)
	}
}

func TestBlockMergeAdjacentRuns(t *testing.T) {
	b := Block{
		Type: BlockParagraph,
		Runs: []Run{
			{Text: "Hello", Style: StyleBold},
			{Text: " ", Style: StyleBold},
			{Text: "world", Style: StyleBold},
		},
	}

	b.MergeAdjacentRuns()

	if len(b.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(b.Runs))
	}
	if b.Runs[0].Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", b.Runs[0].Text)
	}
}

func TestBlockClearStyle(t *testing.T) {
	b := Block{
		Type: BlockParagraph,
		Runs: []Run{
			{Text: "Hello world", Style: StyleBold | StyleItalic},
		},
	}

	b.ClearStyle(0, 11)

	if len(b.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(b.Runs))
	}
	if b.Runs[0].Style != StyleNone {
		t.Errorf("expected style none, got %v", b.Runs[0].Style)
	}
}

func TestNewDocument(t *testing.T) {
	doc := NewDocument()

	if doc.Version != 1 {
		t.Errorf("expected version 1, got %d", doc.Version)
	}
	if doc.Theme != "default" {
		t.Errorf("expected theme 'default', got %q", doc.Theme)
	}
	if len(doc.Blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(doc.Blocks))
	}
}

