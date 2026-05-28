package compose

import "testing"

func stubCopyText(t *testing.T, fn func(string) error) {
	t.Helper()
	oldCopyText := copyText
	copyText = fn
	t.Cleanup(func() { copyText = oldCopyText })
}

func TestYankCopiesToSystemPasteboard(t *testing.T) {
	var copied string
	stubCopyText(t, func(text string) error {
		copied = text
		return nil
	})

	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello world"}}},
		}},
	}

	ed.Yank(Range{Start: Pos{Block: 0, Col: 6}, End: Pos{Block: 0, Col: 11}})

	if got, want := copied, "world"; got != want {
		t.Fatalf("copied text = %q, want %q", got, want)
	}
}

func TestYankSingleBlockAndPutBefore(t *testing.T) {
	stubCopyText(t, func(string) error { return nil })

	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello world"}}},
		}},
		cursor: Pos{Block: 0, Col: 6},
	}

	ed.Yank(Range{Start: Pos{Block: 0, Col: 0}, End: Pos{Block: 0, Col: 5}})
	ed.PutBefore()

	if got, want := ed.doc.Blocks[0].Text(), "hello helloworld"; got != want {
		t.Fatalf("PutBefore text = %q, want %q", got, want)
	}
}

func TestYankMultiBlockAndPutCreatesBlocks(t *testing.T) {
	stubCopyText(t, func(string) error { return nil })

	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "alpha bravo"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "charlie"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "delta echo"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "target end"}}},
		}},
		cursor: Pos{Block: 3, Col: 6},
	}

	ed.Yank(Range{Start: Pos{Block: 0, Col: 6}, End: Pos{Block: 2, Col: 5}})
	ed.Put()

	want := []string{
		"alpha bravo",
		"charlie",
		"delta echo",
		"target bravo",
		"charlie",
		"deltaend",
	}
	if len(ed.doc.Blocks) != len(want) {
		t.Fatalf("block count = %d, want %d", len(ed.doc.Blocks), len(want))
	}
	for i, w := range want {
		if got := ed.doc.Blocks[i].Text(); got != w {
			t.Fatalf("block[%d] = %q, want %q", i, got, w)
		}
	}
}

func TestYankClampsRange(t *testing.T) {
	stubCopyText(t, func(string) error { return nil })

	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello"}}},
		}},
	}

	ed.Yank(Range{Start: Pos{Block: 0, Col: -5}, End: Pos{Block: 0, Col: 99}})

	if got, want := ed.yankText, "hello"; got != want {
		t.Fatalf("yankText = %q, want %q", got, want)
	}
}
