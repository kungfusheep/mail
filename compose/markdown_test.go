package compose

import (
	"strings"
	"testing"
)

func TestParseMarkdownHeadings(t *testing.T) {
	input := `# Heading 1
## Heading 2
### Heading 3
#### Heading 4
##### Heading 5
###### Heading 6`

	doc := ParseMarkdown(input)

	if doc.Meta.Title != "Heading 1" {
		t.Errorf("expected title 'Heading 1', got %q", doc.Meta.Title)
	}

	expected := []BlockType{BlockH1, BlockH2, BlockH3, BlockH4, BlockH5, BlockH6}
	if len(doc.Blocks) != len(expected) {
		t.Fatalf("expected %d blocks, got %d", len(expected), len(doc.Blocks))
	}

	for i, bt := range expected {
		if doc.Blocks[i].Type != bt {
			t.Errorf("block %d: expected type %s, got %s", i, bt, doc.Blocks[i].Type)
		}
	}
}

func TestParseMarkdownInlineStyles(t *testing.T) {
	tests := []struct {
		input    string
		expected []Run
	}{
		{
			input: "plain text",
			expected: []Run{
				{Text: "plain text", Style: StyleNone},
			},
		},
		{
			input: "**bold**",
			expected: []Run{
				{Text: "bold", Style: StyleBold},
			},
		},
		{
			input: "*italic*",
			expected: []Run{
				{Text: "italic", Style: StyleItalic},
			},
		},
		{
			input: "`code`",
			expected: []Run{
				{Text: "code", Style: StyleCode},
			},
		},
		{
			input: "~~strikethrough~~",
			expected: []Run{
				{Text: "strikethrough", Style: StyleStrikethrough},
			},
		},
		{
			input: "some **bold** text",
			expected: []Run{
				{Text: "some ", Style: StyleNone},
				{Text: "bold", Style: StyleBold},
				{Text: " text", Style: StyleNone},
			},
		},
		{
			input: "**bold and *italic* inside**",
			expected: []Run{
				{Text: "bold and ", Style: StyleBold},
				{Text: "italic", Style: StyleBold | StyleItalic},
				{Text: " inside", Style: StyleBold},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			runs := parseInlineMarkdown(tc.input)
			if len(runs) != len(tc.expected) {
				t.Fatalf("expected %d runs, got %d: %+v", len(tc.expected), len(runs), runs)
			}
			for i, exp := range tc.expected {
				if runs[i].Text != exp.Text {
					t.Errorf("run %d: expected text %q, got %q", i, exp.Text, runs[i].Text)
				}
				if runs[i].Style != exp.Style {
					t.Errorf("run %d: expected style %d, got %d", i, exp.Style, runs[i].Style)
				}
			}
		})
	}
}

func TestParseMarkdownLists(t *testing.T) {
	input := `- bullet one
- bullet two
* bullet three
1. ordered one
2. ordered two`

	doc := ParseMarkdown(input)

	if len(doc.Blocks) != 5 {
		t.Fatalf("expected 5 blocks, got %d", len(doc.Blocks))
	}

	// first 3 are bullet, last 2 are numbered
	for i := 0; i < 3; i++ {
		if doc.Blocks[i].Type != BlockListItem {
			t.Errorf("block %d: expected list-item, got %s", i, doc.Blocks[i].Type)
		}
		if doc.Blocks[i].Attrs["marker"] != "bullet" {
			t.Errorf("block %d: expected marker 'bullet', got %q", i, doc.Blocks[i].Attrs["marker"])
		}
	}

	for i := 3; i < 5; i++ {
		if doc.Blocks[i].Type != BlockListItem {
			t.Errorf("block %d: expected list-item, got %s", i, doc.Blocks[i].Type)
		}
		if doc.Blocks[i].Attrs["marker"] != "number" {
			t.Errorf("block %d: expected marker 'number', got %q", i, doc.Blocks[i].Attrs["marker"])
		}
	}
}

func TestParseMarkdownCodeBlock(t *testing.T) {
	input := "```go\nfunc main() {\n    fmt.Println(\"hi\")\n}\n```"

	doc := ParseMarkdown(input)

	// should produce 3 BlockCodeLine blocks (one per code line)
	if len(doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(doc.Blocks))
	}

	for i, b := range doc.Blocks {
		if b.Type != BlockCodeLine {
			t.Errorf("block %d: expected BlockCodeLine, got %s", i, b.Type)
		}
		if b.Attrs["lang"] != "go" {
			t.Errorf("block %d: expected lang 'go', got %q", i, b.Attrs["lang"])
		}
	}
	if doc.Blocks[0].Text() != "func main() {" {
		t.Errorf("block 0: got %q", doc.Blocks[0].Text())
	}
	if doc.Blocks[1].Text() != "    fmt.Println(\"hi\")" {
		t.Errorf("block 1: got %q", doc.Blocks[1].Text())
	}
	if doc.Blocks[2].Text() != "}" {
		t.Errorf("block 2: got %q", doc.Blocks[2].Text())
	}
}

func docToMarkdownString(doc *Document) string {
	var buf strings.Builder
	WriteMarkdown(doc, &buf)
	return buf.String()
}

func TestCodeBlockMarkdownRoundTrip(t *testing.T) {
	input := "# Header\n\n```go\nfunc main() {\n    fmt.Println(\"hi\")\n}\n```\n\nSome text after."

	doc := ParseMarkdown(input)
	output := docToMarkdownString(doc)

	// re-parse
	doc2 := ParseMarkdown(output)
	output2 := docToMarkdownString(doc2)

	if output != output2 {
		t.Errorf("round-trip mismatch:\nfirst:  %q\nsecond: %q", output, output2)
	}

	// verify code blocks are present
	hasCode := false
	for _, b := range doc2.Blocks {
		if b.Type == BlockCodeLine {
			hasCode = true
			break
		}
	}
	if !hasCode {
		t.Error("no code line blocks found after round-trip")
	}
}

func TestEmptyCodeBlockMarkdownRoundTrip(t *testing.T) {
	input := "```go\n```"

	doc := ParseMarkdown(input)
	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block for empty code block, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Type != BlockCodeLine {
		t.Errorf("expected BlockCodeLine, got %v", doc.Blocks[0].Type)
	}
	if doc.Blocks[0].Text() != "" {
		t.Errorf("expected empty text, got %q", doc.Blocks[0].Text())
	}

	output := docToMarkdownString(doc)
	if !strings.Contains(output, "```go") {
		t.Errorf("expected opening fence in output: %q", output)
	}
}

func TestParseMarkdownBlockquote(t *testing.T) {
	input := "> This is a quote"

	doc := ParseMarkdown(input)

	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Blocks))
	}

	if doc.Blocks[0].Type != BlockQuote {
		t.Errorf("expected blockquote, got %s", doc.Blocks[0].Type)
	}
}

func TestIsMarkdownFile(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"README.md", true},
		{"notes.markdown", true},
		{"FILE.MD", true},
		{"document.txt", false},
		{"code.go", false},
		{"markdown", false},
	}

	for _, tc := range tests {
		if got := IsMarkdownFile(tc.filename); got != tc.expected {
			t.Errorf("IsMarkdownFile(%q) = %v, want %v", tc.filename, got, tc.expected)
		}
	}
}

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"**bold**", "bold"},
		{"*italic*", "italic"},
		{"`code`", "code"},
		{"~~strike~~", "strike"},
		{"**bold** and *italic*", "bold and italic"},
	}

	for _, tc := range tests {
		if got := stripMarkdown(tc.input); got != tc.expected {
			t.Errorf("stripMarkdown(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestParseDividers(t *testing.T) {
	input := `above

---

***

___

below`

	doc := ParseMarkdown(input)

	// count dividers
	dividerCount := 0
	for _, block := range doc.Blocks {
		if block.Type == BlockDivider {
			dividerCount++
		}
	}

	if dividerCount != 3 {
		t.Errorf("expected 3 dividers, got %d", dividerCount)
	}
}

func TestParseFrontMatter(t *testing.T) {
	input := `---
title: My Document
author: Test Author
date: 2024-01-15
---

# Hello World

This is content.`

	doc := ParseMarkdown(input)

	// debug: print all blocks
	for i, b := range doc.Blocks {
		t.Logf("Block %d: Type=%s, Len=%d, Text=%q", i, b.Type, b.Length(), b.Text())
	}

	// should have 5 blocks: front matter, spacer, h1, empty (spacer), paragraph
	if len(doc.Blocks) != 5 {
		t.Fatalf("expected 5 blocks, got %d", len(doc.Blocks))
	}

	// first block should be front matter
	if doc.Blocks[0].Type != BlockFrontMatter {
		t.Errorf("expected first block to be frontmatter, got %s", doc.Blocks[0].Type)
	}

	fmText := doc.Blocks[0].Text()
	if !strings.Contains(fmText, "title: My Document") {
		t.Errorf("front matter should contain 'title: My Document', got %q", fmText)
	}

	// second block should be spacer (empty paragraph)
	if doc.Blocks[1].Type != BlockParagraph || doc.Blocks[1].Length() != 0 {
		t.Errorf("expected second block to be empty spacer, got %s with len %d", doc.Blocks[1].Type, doc.Blocks[1].Length())
	}

	// third block should be h1
	if doc.Blocks[2].Type != BlockH1 {
		t.Errorf("expected third block to be h1, got %s", doc.Blocks[2].Type)
	}

	// fifth block should be paragraph with content
	if doc.Blocks[4].Type != BlockParagraph {
		t.Errorf("expected fifth block to be paragraph, got %s", doc.Blocks[4].Type)
	}
}

func TestFrontMatterRoundTrip(t *testing.T) {
	input := `---
title: My Document
author: Test
---

# Hello`

	doc := ParseMarkdown(input)

	var sb strings.Builder
	if err := WriteMarkdown(doc, &sb); err != nil {
		t.Fatalf("WriteMarkdown failed: %v", err)
	}

	output := sb.String()

	// output should contain the front matter delimiters
	if !strings.Contains(output, "---\ntitle: My Document") {
		t.Errorf("output should contain front matter, got %q", output)
	}
	if !strings.Contains(output, "# Hello") {
		t.Errorf("output should contain heading, got %q", output)
	}
}

func TestNoFrontMatter(t *testing.T) {
	input := `# Just a Heading

Some text.`

	doc := ParseMarkdown(input)

	// should have 3 blocks: h1, empty spacer, paragraph
	if len(doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(doc.Blocks))
	}

	// first block should be h1, not front matter
	if doc.Blocks[0].Type != BlockH1 {
		t.Errorf("expected first block to be h1, got %s", doc.Blocks[0].Type)
	}

	// no front matter block should exist
	for i, b := range doc.Blocks {
		if b.Type == BlockFrontMatter {
			t.Errorf("block %d should not be front matter", i)
		}
	}
}

func TestParseDialogue(t *testing.T) {
	input := `# Test Scene

Some prose.

@@ MIRANDA
I do not know one of my sex; no woman's face remember,
save, from my glass, mine own.

@@ FERDINAND
Admired Miranda! Indeed the top of admiration!

More prose below.`

	doc := ParseMarkdown(input)

	// find dialogue blocks
	var dialogues []Block
	for _, b := range doc.Blocks {
		if b.Type == BlockDialogue {
			dialogues = append(dialogues, b)
		}
	}

	if len(dialogues) != 2 {
		t.Fatalf("expected 2 dialogue blocks, got %d", len(dialogues))
	}

	// check first dialogue
	if dialogues[0].Attrs["character"] != "MIRANDA" {
		t.Errorf("expected character 'MIRANDA', got %q", dialogues[0].Attrs["character"])
	}
	if !strings.Contains(dialogues[0].Text(), "I do not know") {
		t.Errorf("expected dialogue to contain 'I do not know', got %q", dialogues[0].Text())
	}

	// check second dialogue
	if dialogues[1].Attrs["character"] != "FERDINAND" {
		t.Errorf("expected character 'FERDINAND', got %q", dialogues[1].Attrs["character"])
	}
	if !strings.Contains(dialogues[1].Text(), "Admired Miranda") {
		t.Errorf("expected dialogue to contain 'Admired Miranda', got %q", dialogues[1].Text())
	}
}

func TestDialogueRoundTrip(t *testing.T) {
	input := `@@ MIRANDA
I do not know one of my sex.

@@ FERDINAND
Admired Miranda!`

	doc := ParseMarkdown(input)

	var sb strings.Builder
	if err := WriteMarkdown(doc, &sb); err != nil {
		t.Fatalf("WriteMarkdown failed: %v", err)
	}

	output := sb.String()

	// verify the output preserves dialogue format
	if !strings.Contains(output, "@@ MIRANDA") {
		t.Errorf("output should contain '@@ MIRANDA', got %q", output)
	}
	if !strings.Contains(output, "@@ FERDINAND") {
		t.Errorf("output should contain '@@ FERDINAND', got %q", output)
	}
	if !strings.Contains(output, "I do not know") {
		t.Errorf("output should contain dialogue text, got %q", output)
	}
}

func TestParseSceneHeading(t *testing.T) {
	input := `# Test Screenplay

INT. COFFEE SHOP - DAY

Some prose.

EXT. BEACH - SUNSET

More prose.

I/E. CAR - NIGHT`

	doc := ParseMarkdown(input)

	// find scene heading blocks
	var scenes []Block
	for _, b := range doc.Blocks {
		if b.Type == BlockSceneHeading {
			scenes = append(scenes, b)
		}
	}

	if len(scenes) != 3 {
		t.Fatalf("expected 3 scene heading blocks, got %d", len(scenes))
	}

	if !strings.Contains(scenes[0].Text(), "COFFEE SHOP") {
		t.Errorf("expected first scene to contain 'COFFEE SHOP', got %q", scenes[0].Text())
	}
	if !strings.Contains(scenes[1].Text(), "BEACH") {
		t.Errorf("expected second scene to contain 'BEACH', got %q", scenes[1].Text())
	}
	if !strings.Contains(scenes[2].Text(), "CAR") {
		t.Errorf("expected third scene to contain 'CAR', got %q", scenes[2].Text())
	}
}

func TestSceneHeadingRoundTrip(t *testing.T) {
	input := `INT. COFFEE SHOP - DAY

EXT. BEACH - SUNSET`

	doc := ParseMarkdown(input)

	var sb strings.Builder
	if err := WriteMarkdown(doc, &sb); err != nil {
		t.Fatalf("WriteMarkdown failed: %v", err)
	}

	output := sb.String()

	if !strings.Contains(output, "INT. COFFEE SHOP - DAY") {
		t.Errorf("output should contain INT scene, got %q", output)
	}
	if !strings.Contains(output, "EXT. BEACH - SUNSET") {
		t.Errorf("output should contain EXT scene, got %q", output)
	}
}
