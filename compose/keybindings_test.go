package compose

import (
	"testing"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

func dispatchPattern(input *riffkey.Input, pattern string) {
	for _, key := range riffkey.ParsePattern(pattern) {
		input.Dispatch(key)
	}
}

func TestRegisterNormalModeBindsOperatorTextObjectsToProvidedRouter(t *testing.T) {
	stubCopyText(t, func(string) error { return nil })

	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello world"}}},
		}},
		cursor: Pos{Block: 0, Col: 7},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()
	enteredInsert := false

	RegisterNormalMode(router, app, ed, func() { enteredInsert = true }, func() {})

	input := riffkey.NewInput(router)
	dispatchPattern(input, "ciw")

	if got, want := ed.doc.Blocks[0].Text(), "hello "; got != want {
		t.Fatalf("ciw text = %q, want %q", got, want)
	}
	if !enteredInsert {
		t.Fatal("ciw did not enter insert mode through the provided callback")
	}
}

func TestRegisterInsertModeCommonConveniences(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "abcd"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "XY"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "wxyz"}}},
		}},
		cursor: Pos{Block: 1, Col: 1},
		mode:   ModeInsert,
	}
	ed.yankText = "paste"

	RegisterInsertMode(app, ed, nil)
	dispatchPattern(app.Input(), "<C-y>")
	if got, want := ed.doc.Blocks[1].Text(), "XbY"; got != want {
		t.Fatalf("C-y text = %q, want %q", got, want)
	}
	dispatchPattern(app.Input(), "<C-e>")
	if got, want := ed.doc.Blocks[1].Text(), "XbyY"; got != want {
		t.Fatalf("C-e text = %q, want %q", got, want)
	}
	dispatchPattern(app.Input(), "<C-r>\"")
	if got, want := ed.doc.Blocks[1].Text(), "XbypasteY"; got != want {
		t.Fatalf("C-r text = %q, want %q", got, want)
	}
	dispatchPattern(app.Input(), "<C-t>")
	if got, want := ed.doc.Blocks[1].Text(), "    XbypasteY"; got != want {
		t.Fatalf("C-t text = %q, want %q", got, want)
	}
	dispatchPattern(app.Input(), "<C-d>")
	if got, want := ed.doc.Blocks[1].Text(), "XbypasteY"; got != want {
		t.Fatalf("C-d text = %q, want %q", got, want)
	}
	dispatchPattern(app.Input(), "<C-h>")
	if got, want := ed.doc.Blocks[1].Text(), "XbypastY"; got != want {
		t.Fatalf("C-h text = %q, want %q", got, want)
	}
	dispatchPattern(app.Input(), "<C-[>")
	if got, want := ed.Mode(), ModeNormal; got != want {
		t.Fatalf("C-[ mode = %v, want %v", got, want)
	}
}

func TestRegisterInsertModeOneNormalCommand(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello"}}},
		}},
		cursor: Pos{Block: 0, Col: 1},
		mode:   ModeInsert,
	}

	RegisterInsertMode(app, ed, nil)
	depth := app.Input().Depth()
	dispatchPattern(app.Input(), "<C-o>l")

	if got, want := ed.Cursor().Col, 2; got != want {
		t.Fatalf("C-o l cursor = %d, want %d", got, want)
	}
	if got, want := ed.Mode(), ModeInsert; got != want {
		t.Fatalf("C-o l mode = %v, want %v", got, want)
	}
	if got := app.Input().Depth(); got != depth {
		t.Fatalf("C-o l input depth = %d, want %d", got, depth)
	}
}

func TestRegisterNormalModeBindsMatchersToProvidedRouter(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "intro"}}},
			{Type: BlockH2, Runs: []Run{{Text: "heading"}}},
		}},
		cursor: Pos{Block: 0, Col: 0},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()

	RegisterNormalMode(router, app, ed, func() {}, func() {})

	input := riffkey.NewInput(router)
	dispatchPattern(input, "]h")

	if got, want := ed.Cursor().Block, 1; got != want {
		t.Fatalf("]h cursor block = %d, want %d", got, want)
	}
}

func TestRegisterNormalModeSubstituteChar(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello"}}},
		}},
		cursor: Pos{Block: 0, Col: 1},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()
	enteredInsert := false

	RegisterNormalMode(router, app, ed, func() { enteredInsert = true }, func() {})

	input := riffkey.NewInput(router)
	dispatchPattern(input, "s")

	if got, want := ed.doc.Blocks[0].Text(), "hllo"; got != want {
		t.Fatalf("s text = %q, want %q", got, want)
	}
	if got, want := ed.Mode(), ModeInsert; got != want {
		t.Fatalf("s mode = %v, want %v", got, want)
	}
	if !enteredInsert {
		t.Fatal("s did not enter insert mode through the provided callback")
	}
}

func TestRegisterNormalModeSubstituteCharCount(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello"}}},
		}},
		cursor: Pos{Block: 0, Col: 1},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()

	RegisterNormalMode(router, app, ed, func() {}, func() {})

	input := riffkey.NewInput(router)
	dispatchPattern(input, "3s")

	if got, want := ed.doc.Blocks[0].Text(), "ho"; got != want {
		t.Fatalf("3s text = %q, want %q", got, want)
	}
	if got, want := ed.Mode(), ModeInsert; got != want {
		t.Fatalf("3s mode = %v, want %v", got, want)
	}
}

func TestRegisterNormalModeSubstituteLine(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello world"}}},
		}},
		cursor: Pos{Block: 0, Col: 6},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()
	enteredInsert := false

	RegisterNormalMode(router, app, ed, func() { enteredInsert = true }, func() {})

	input := riffkey.NewInput(router)
	dispatchPattern(input, "S")

	if got, want := ed.doc.Blocks[0].Text(), ""; got != want {
		t.Fatalf("S text = %q, want %q", got, want)
	}
	if got, want := ed.Cursor(), (Pos{Block: 0, Col: 0}); got != want {
		t.Fatalf("S cursor = %+v, want %+v", got, want)
	}
	if got, want := ed.Mode(), ModeInsert; got != want {
		t.Fatalf("S mode = %v, want %v", got, want)
	}
	if !enteredInsert {
		t.Fatal("S did not enter insert mode through the provided callback")
	}
}

func TestRegisterNormalModeAdditionalVimAliases(t *testing.T) {
	stubCopyText(t, func(string) error { return nil })

	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "  one two (abc)"}}},
		}},
		cursor: Pos{Block: 0, Col: 2},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()
	RegisterNormalMode(router, app, ed, func() {}, func() {})
	input := riffkey.NewInput(router)

	dispatchPattern(input, "Y")
	if ed.yankText != "one two (abc)" {
		t.Fatalf("Y yank = %q, want rest of line", ed.yankText)
	}

	dispatchPattern(input, "g_")
	if got, want := ed.Cursor().Col, len([]rune("  one two (abc)"))-1; got != want {
		t.Fatalf("g_ cursor = %d, want %d", got, want)
	}

	dispatchPattern(input, "3|")
	if got, want := ed.Cursor().Col, 2; got != want {
		t.Fatalf("3| cursor = %d, want %d", got, want)
	}

	dispatchPattern(input, "%")
	if got, want := ed.Cursor().Col, 14; got != want {
		t.Fatalf("%% cursor = %d, want %d", got, want)
	}

	ed.cursor = Pos{Block: 0, Col: 9}
	dispatchPattern(input, "ge")
	if got, want := ed.Cursor().Col, 8; got != want {
		t.Fatalf("ge cursor = %d, want %d", got, want)
	}

	dispatchPattern(input, ">>")
	if got, want := ed.doc.Blocks[0].Text(), "      one two (abc)"; got != want {
		t.Fatalf(">> text = %q, want %q", got, want)
	}
	dispatchPattern(input, "<<")
	if got, want := ed.doc.Blocks[0].Text(), "  one two (abc)"; got != want {
		t.Fatalf("<< text = %q, want %q", got, want)
	}

	ed.cursor = Pos{Block: 0, Col: 2}
	dispatchPattern(input, "X")
	if got, want := ed.doc.Blocks[0].Text(), " one two (abc)"; got != want {
		t.Fatalf("X text = %q, want %q", got, want)
	}
}

func TestRegisterNormalModeRecordsAndReplaysMacro(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "hello"}}},
		}},
		cursor: Pos{Block: 0, Col: 0},
		mode:   ModeNormal,
	}
	RegisterNormalMode(app.Router(), app, ed, func() {}, func() {})

	dispatchPattern(app.Input(), "qalq")
	dispatchPattern(app.Input(), "0")
	dispatchPattern(app.Input(), "@a")

	if got, want := ed.Cursor().Col, 1; got != want {
		t.Fatalf("@a cursor = %d, want %d", got, want)
	}
}

func TestRegisterVisualModeAliasesAndReselect(t *testing.T) {
	stubCopyText(t, func(string) error { return nil })

	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "alpha"}}},
			{Type: BlockParagraph, Runs: []Run{{Text: "beta"}}},
		}},
		cursor: Pos{Block: 0, Col: 0},
		mode:   ModeNormal,
	}
	ed.yankText = "new"
	RegisterNormalMode(app.Router(), app, ed, func() {}, func() { RegisterVisualMode(app, ed) })

	dispatchPattern(app.Input(), "V>")
	if got, want := ed.doc.Blocks[0].Text(), "    alpha"; got != want {
		t.Fatalf("visual > text = %q, want %q", got, want)
	}

	dispatchPattern(app.Input(), "gv<")
	if got, want := ed.doc.Blocks[0].Text(), "alpha"; got != want {
		t.Fatalf("gv < text = %q, want %q", got, want)
	}

	dispatchPattern(app.Input(), "viwp")
	if got, want := ed.doc.Blocks[0].Text(), "new"; got != want {
		t.Fatalf("visual p text = %q, want %q", got, want)
	}

	dispatchPattern(app.Input(), "v=")
	if got, want := ed.Mode(), ModeNormal; got != want {
		t.Fatalf("visual = mode = %v, want %v", got, want)
	}
}

func TestRegisterNormalModeBindsSearchRepeat(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "alpha beta alpha beta alpha"}}},
		}},
		cursor: Pos{Block: 0, Col: 0},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()

	RegisterNormalMode(router, app, ed, func() {}, func() {})

	if !ed.Search("beta", true) {
		t.Fatal("initial search did not find beta")
	}

	input := riffkey.NewInput(router)
	dispatchPattern(input, "n")

	if got, want := ed.Cursor().Col, 17; got != want {
		t.Fatalf("n cursor col = %d, want %d", got, want)
	}

	dispatchPattern(input, "N")

	if got, want := ed.Cursor().Col, 6; got != want {
		t.Fatalf("N cursor col = %d, want %d", got, want)
	}
}

func TestRegisterNormalModeBindsWordUnderCursorSearch(t *testing.T) {
	app := glyph.NewApp()
	ed := &Editor{
		doc: &Document{Blocks: []Block{
			{Type: BlockParagraph, Runs: []Run{{Text: "alpha beta alpha beta"}}},
		}},
		cursor: Pos{Block: 0, Col: 6},
		mode:   ModeNormal,
	}
	router := riffkey.NewRouter()

	RegisterNormalMode(router, app, ed, func() {}, func() {})

	input := riffkey.NewInput(router)
	dispatchPattern(input, "*")

	if got, want := ed.Cursor().Col, 17; got != want {
		t.Fatalf("* cursor col = %d, want %d", got, want)
	}

	dispatchPattern(input, "#")

	if got, want := ed.Cursor().Col, 6; got != want {
		t.Fatalf("# cursor col = %d, want %d", got, want)
	}
}
