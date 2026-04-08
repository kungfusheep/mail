package compose

import (
	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// RegisterNormalMode registers all pure editor keybindings on the given router.
// Call this after any app-level bindings so they take priority.
func RegisterNormalMode(router *riffkey.Router, app *glyph.App, ed *Editor, enterInsert func(), enterVisual func()) {
	// movement
	router.Handle("h", func(m riffkey.Match) { ed.Left(m.Count) })
	router.Handle("l", func(m riffkey.Match) { ed.Right(m.Count) })
	router.Handle("j", func(m riffkey.Match) { ed.Down(m.Count) })
	router.Handle("k", func(m riffkey.Match) { ed.Up(m.Count) })
	router.Handle("<Left>", func(m riffkey.Match) { ed.Left(m.Count) })
	router.Handle("<Right>", func(m riffkey.Match) { ed.Right(m.Count) })
	router.Handle("<Down>", func(m riffkey.Match) { ed.Down(m.Count) })
	router.Handle("<Up>", func(m riffkey.Match) { ed.Up(m.Count) })
	router.Handle("gj", func(m riffkey.Match) { ed.BlockDown(m.Count) })
	router.Handle("gk", func(m riffkey.Match) { ed.BlockUp(m.Count) })
	router.Handle("0", func(_ riffkey.Match) { ed.LineStart() })
	router.Handle("$", func(_ riffkey.Match) { ed.LineEnd() })
	router.Handle("^", func(_ riffkey.Match) { ed.FirstNonBlank() })
	router.Handle("gg", func(_ riffkey.Match) { ed.DocStart() })
	router.Handle("G", func(_ riffkey.Match) { ed.DocEnd() })
	router.Handle("w", func(m riffkey.Match) { ed.NextWordStart(m.Count) })
	router.Handle("b", func(m riffkey.Match) { ed.PrevWordStart(m.Count) })
	router.Handle("e", func(m riffkey.Match) { ed.NextWordEnd(m.Count) })

	// scrolling
	router.Handle("<C-d>", func(_ riffkey.Match) { ed.ScrollHalfPageDown() })
	router.Handle("<C-u>", func(_ riffkey.Match) { ed.ScrollHalfPageUp() })
	router.Handle("<C-f>", func(_ riffkey.Match) { ed.ScrollPageDown() })
	router.Handle("<C-b>", func(_ riffkey.Match) { ed.ScrollPageUp() })
	router.Handle("<C-e>", func(_ riffkey.Match) { ed.ScrollLineDown() })
	router.Handle("<C-y>", func(_ riffkey.Match) { ed.ScrollLineUp() })
	router.Handle("zz", func(_ riffkey.Match) { ed.ScrollCenter() })
	router.Handle("zt", func(_ riffkey.Match) { ed.ScrollTop() })
	router.Handle("zb", func(_ riffkey.Match) { ed.ScrollBottom() })

	// view modes
	router.Handle("zT", func(_ riffkey.Match) { ed.ToggleTypewriterMode() })
	router.Handle("zf", func(_ riffkey.Match) { ed.ToggleFocusMode() })
	router.Handle("zF", func(_ riffkey.Match) { ed.CycleFocusScope() })
	router.Handle("gz", func(_ riffkey.Match) { ed.ToggleZenMode() })
	router.Handle("zr", func(_ riffkey.Match) { ed.ToggleRawMode() })
	router.Handle("\\t", func(_ riffkey.Match) { ed.ToggleTheme() })

	// spell check
	router.Handle("zg", func(_ riffkey.Match) { ed.AddWordToDictionary() })
	router.Handle("]e", func(_ riffkey.Match) { ed.NextMisspelled() })
	router.Handle("[e", func(_ riffkey.Match) { ed.PrevMisspelled() })

	// section navigation
	router.Handle("]S", func(_ riffkey.Match) { ed.NextSameLevel() })
	router.Handle("[S", func(_ riffkey.Match) { ed.PrevSameLevel() })

	// heading promote/demote
	router.Handle("g>", func(_ riffkey.Match) { ed.PromoteHeading() })
	router.Handle("g<", func(_ riffkey.Match) { ed.DemoteHeading() })

	// move blocks
	router.Handle("<A-j>", func(_ riffkey.Match) { ed.MoveBlockDown() })
	router.Handle("<A-k>", func(_ riffkey.Match) { ed.MoveBlockUp() })

	// visual mode
	router.Handle("v", func(_ riffkey.Match) { ed.EnterVisual(); enterVisual() })
	router.Handle("V", func(_ riffkey.Match) { ed.EnterVisualLine(); enterVisual() })
	router.Handle("<C-v>", func(_ riffkey.Match) { ed.EnterVisualBlock(); enterVisual() })

	// insert mode
	router.Handle("i", func(_ riffkey.Match) { ed.EnterInsert(); enterInsert() })
	router.Handle("a", func(_ riffkey.Match) { ed.EnterInsertAfter(); enterInsert() })
	router.Handle("I", func(_ riffkey.Match) { ed.EnterInsertLineStart(); enterInsert() })
	router.Handle("A", func(_ riffkey.Match) { ed.EnterInsertLineEnd(); enterInsert() })
	router.Handle("o", func(_ riffkey.Match) { ed.OpenBelow(); enterInsert() })
	router.Handle("O", func(_ riffkey.Match) { ed.OpenAbove(); enterInsert() })

	// editing
	router.Handle("x", func(m riffkey.Match) {
		for range m.Count {
			ed.DeleteChar()
		}
	})

	replaceRouter := riffkey.NewRouter().Name("replace").NoCounts()
	replaceRouter.Handle("<Esc>", func(_ riffkey.Match) { app.Pop() })
	replaceRouter.HandleUnmatched(func(k riffkey.Key) bool {
		if k.Rune != 0 && k.Mod == 0 {
			ed.ReplaceChar(k.Rune)
			app.Pop()
			return true
		}
		return false
	})
	replaceRouter.AddOnAfter(func() { ed.Refresh() })
	router.Handle("r", func(_ riffkey.Match) { app.Push(replaceRouter) })

	router.Handle("dd", func(m riffkey.Match) {
		for range m.Count {
			ed.DeleteLine()
		}
	})
	router.Handle("dj", func(m riffkey.Match) {
		for range m.Count + 1 {
			ed.DeleteLine()
		}
	})
	router.Handle("dk", func(m riffkey.Match) {
		for range m.Count + 1 {
			if ed.Cursor().Block > 0 {
				ed.BlockUp(1)
			}
			ed.DeleteLine()
		}
	})
	router.Handle("D", func(_ riffkey.Match) {
		ed.Delete(Range{Start: ed.Cursor(), End: Pos{Block: ed.Cursor().Block, Col: ed.CurrentBlock().Length()}})
	})
	router.Handle("J", func(m riffkey.Match) {
		for range m.Count {
			ed.JoinLines()
		}
	})
	router.Handle("~", func(_ riffkey.Match) { ed.ToggleCase() })
	router.Handle("u", func(_ riffkey.Match) { ed.Undo() })
	router.Handle("<C-r>", func(_ riffkey.Match) { ed.Redo() })
	router.Handle("yy", func(_ riffkey.Match) { ed.Yank(ed.InnerBlock()) })
	router.Handle("p", func(_ riffkey.Match) { ed.Put() })
	router.Handle("P", func(_ riffkey.Match) { ed.PutBefore() })
	router.Handle("cc", func(_ riffkey.Match) { ed.Change(ed.InnerBlock()); enterInsert() })
	router.Handle("cj", func(m riffkey.Match) {
		for range m.Count {
			ed.DeleteLine()
		}
		ed.Change(ed.InnerBlock())
		enterInsert()
	})
	router.Handle("ck", func(m riffkey.Match) {
		for range m.Count {
			if ed.Cursor().Block > 0 {
				ed.BlockUp(1)
			}
			ed.DeleteLine()
		}
		ed.Change(ed.InnerBlock())
		enterInsert()
	})
	router.Handle("C", func(_ riffkey.Match) {
		ed.Change(Range{Start: ed.Cursor(), End: Pos{Block: ed.Cursor().Block, Col: ed.CurrentBlock().Length()}})
		enterInsert()
	})

	// f/F/t/T
	findChar := func(action func(rune)) {
		cr := riffkey.NewRouter().Name("char").NoCounts()
		cr.Handle("<Esc>", func(_ riffkey.Match) { app.Pop() })
		cr.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				action(k.Rune)
				app.Pop()
				return true
			}
			return false
		})
		app.Push(cr)
	}
	router.Handle("f", func(_ riffkey.Match) { findChar(ed.FindChar) })
	router.Handle("F", func(_ riffkey.Match) { findChar(ed.FindCharBack) })
	router.Handle("t", func(_ riffkey.Match) { findChar(ed.TillChar) })
	router.Handle("T", func(_ riffkey.Match) { findChar(ed.TillCharBack) })
	router.Handle(";", func(_ riffkey.Match) { ed.RepeatFind() })
	router.Handle(",", func(_ riffkey.Match) { ed.RepeatFindReverse() })

	// marks
	markPrompt := func(action func(rune)) {
		mr := riffkey.NewRouter().Name("mark").NoCounts()
		mr.Handle("<Esc>", func(_ riffkey.Match) { app.Pop() })
		mr.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune >= 'a' && k.Rune <= 'z' && k.Mod == 0 {
				action(k.Rune)
				app.Pop()
				return true
			}
			return false
		})
		app.Push(mr)
	}
	router.Handle("m", func(_ riffkey.Match) { markPrompt(ed.SetMark) })
	router.Handle("'", func(_ riffkey.Match) { markPrompt(func(r rune) { ed.GotoMarkLine(r) }) })
	router.Handle("`", func(_ riffkey.Match) { markPrompt(func(r rune) { ed.GotoMark(r) }) })

	// screen jumps
	router.Handle("H", func(_ riffkey.Match) { ed.GotoScreenTop() })
	router.Handle("M", func(_ riffkey.Match) { ed.GotoScreenMiddle() })
	router.Handle("L", func(_ riffkey.Match) { ed.GotoScreenBottom() })
	router.Handle("<C-o>", func(_ riffkey.Match) { ed.JumpBack() })
	router.Handle("<C-i>", func(_ riffkey.Match) { ed.JumpForward() })
	router.Handle("-", func(_ riffkey.Match) { ed.PrevLineFirstNonBlank() })
	router.Handle("+", func(_ riffkey.Match) { ed.NextLineFirstNonBlank() })
	router.Handle("<C-a>", func(_ riffkey.Match) { ed.IncrementNumber(1) })
	router.Handle("<C-x>", func(_ riffkey.Match) { ed.IncrementNumber(-1) })
	router.Handle(".", func(_ riffkey.Match) { ed.RepeatLastAction() })

	// sentences
	router.Handle(")", func(m riffkey.Match) {
		for range m.Count {
			ed.NextSentence()
		}
	})
	router.Handle("(", func(m riffkey.Match) {
		for range m.Count {
			ed.PrevSentence()
		}
	})
	router.Handle("gs)", func(_ riffkey.Match) { ed.SwapSentenceNext() })
	router.Handle("gs(", func(_ riffkey.Match) { ed.SwapSentencePrev() })

	// operator × text object combos
	RegisterOperatorTextObjects(app, ed)

	// block type
	router.Handle("g0", func(_ riffkey.Match) { ed.SetBlockType(BlockParagraph) })
	router.Handle("g1", func(_ riffkey.Match) { ed.SetBlockType(BlockH1) })
	router.Handle("g2", func(_ riffkey.Match) { ed.SetBlockType(BlockH2) })
	router.Handle("g3", func(_ riffkey.Match) { ed.SetBlockType(BlockH3) })
	router.Handle("g4", func(_ riffkey.Match) { ed.SetBlockType(BlockH4) })
	router.Handle("g5", func(_ riffkey.Match) { ed.SetBlockType(BlockH5) })
	router.Handle("g6", func(_ riffkey.Match) { ed.SetBlockType(BlockH6) })
	router.Handle("g-", func(_ riffkey.Match) { ed.InsertDivider() })
	router.Handle("gyiw", func(_ riffkey.Match) { ed.YankStyle() })
	router.Handle("gpiw", func(_ riffkey.Match) { ed.PasteStyle(ed.InnerWord()) })

	// templates
	router.Handle("gth", func(_ riffkey.Match) { ed.CycleTemplate("headings") })
	router.Handle("gtq", func(_ riffkey.Match) { ed.CycleTemplate("quotes") })
	router.Handle("gtd", func(_ riffkey.Match) { ed.CycleTemplate("dividers") })
	router.Handle("gtl", func(_ riffkey.Match) { ed.CycleTemplate("lists") })
	router.Handle("gtc", func(_ riffkey.Match) { ed.CycleTemplate("code") })
	router.Handle("gtt", func(_ riffkey.Match) { ed.CycleTemplate("tables") })
	router.Handle("gt@", func(_ riffkey.Match) { ed.CycleTemplate("dialogue") })
	router.Handle("gBt", func(_ riffkey.Match) { ed.ApplyBundle("typewriter") })
	router.Handle("gBm", func(_ riffkey.Match) { ed.ApplyBundle("minimal") })
	router.Handle("gBa", func(_ riffkey.Match) { ed.ApplyBundle("academic") })
	router.Handle("gBc", func(_ riffkey.Match) { ed.ApplyBundle("creative") })

	// matchers
	RegisterMatchers(app, ed)
}

// RegisterInsertMode creates and pushes an insert mode router.
// onAfter is called after every keystroke (for syncing external state).
func RegisterInsertMode(app *glyph.App, ed *Editor, onAfter func()) {
	r := riffkey.NewRouter().Name("insert").NoCounts()

	r.Handle("<Esc>", func(_ riffkey.Match) {
		ed.EnterNormal()
		app.Pop()
	})
	r.Handle("<CR>", func(_ riffkey.Match) { ed.NewLine() })
	r.Handle("<S-CR>", func(_ riffkey.Match) { ed.YieldToNextSpeaker() })
	r.Handle("<C-n>", func(_ riffkey.Match) { ed.YieldToNextSpeaker() })
	r.Handle("<BS>", func(_ riffkey.Match) { ed.Backspace() })
	r.Handle("<Del>", func(_ riffkey.Match) { ed.DeleteChar() })
	r.Handle("<Left>", func(_ riffkey.Match) { ed.Left(1) })
	r.Handle("<Right>", func(_ riffkey.Match) { ed.Right(1) })
	r.Handle("<Up>", func(_ riffkey.Match) { ed.Up(1) })
	r.Handle("<Down>", func(_ riffkey.Match) { ed.Down(1) })
	r.Handle("<Space>", func(_ riffkey.Match) { ed.InsertChar(' ') })
	r.Handle("<C-w>", func(_ riffkey.Match) { ed.DeleteWordBack() })
	r.Handle("<C-u>", func(_ riffkey.Match) { ed.DeleteToLineStart() })

	r.Handle("<Tab>", func(_ riffkey.Match) {
		b := ed.CurrentBlock()
		if b != nil && b.Type == BlockDialogue {
			ed.ToggleDialogueMode()
		} else {
			ed.InsertText("    ")
		}
	})

	r.HandleUnmatched(func(k riffkey.Key) bool {
		if k.IsPaste() {
			ed.InsertText(k.Paste)
			ed.Refresh()
			return true
		}
		if k.Rune != 0 && k.Mod == 0 {
			ed.InsertChar(k.Rune)
			ed.Refresh()
			return true
		}
		return false
	})

	r.AddOnAfter(func() {
		ed.Refresh()
		if onAfter != nil {
			onAfter()
		}
	})
	app.Push(r)
}

// RegisterVisualMode creates and pushes a visual mode router.
func RegisterVisualMode(app *glyph.App, ed *Editor) {
	r := riffkey.NewRouter().Name("visual").NoCounts()

	r.Handle("<Esc>", func(_ riffkey.Match) { ed.ExitVisual(); app.Pop() })
	r.Handle("v", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == VisualChar {
			ed.ExitVisual()
			app.Pop()
		} else {
			ed.SetVisualMode(VisualChar)
		}
	})
	r.Handle("V", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == VisualLine {
			ed.ExitVisual()
			app.Pop()
		} else {
			ed.SetVisualMode(VisualLine)
		}
	})
	r.Handle("<C-v>", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == VisualBlock {
			ed.ExitVisual()
			app.Pop()
		} else {
			ed.SetVisualMode(VisualBlock)
		}
	})

	r.Handle("h", func(_ riffkey.Match) { ed.Left(1) })
	r.Handle("l", func(_ riffkey.Match) { ed.Right(1) })
	r.Handle("j", func(_ riffkey.Match) { ed.Down(1) })
	r.Handle("k", func(_ riffkey.Match) { ed.Up(1) })
	r.Handle("<Left>", func(_ riffkey.Match) { ed.Left(1) })
	r.Handle("<Right>", func(_ riffkey.Match) { ed.Right(1) })
	r.Handle("<Up>", func(_ riffkey.Match) { ed.Up(1) })
	r.Handle("<Down>", func(_ riffkey.Match) { ed.Down(1) })
	r.Handle("w", func(_ riffkey.Match) { ed.NextWordStart(1) })
	r.Handle("b", func(_ riffkey.Match) { ed.PrevWordStart(1) })
	r.Handle("e", func(_ riffkey.Match) { ed.NextWordEnd(1) })
	r.Handle("0", func(_ riffkey.Match) { ed.LineStart() })
	r.Handle("$", func(_ riffkey.Match) { ed.LineEnd() })
	r.Handle("^", func(_ riffkey.Match) { ed.FirstNonBlank() })
	r.Handle("gg", func(_ riffkey.Match) { ed.DocStart() })
	r.Handle("G", func(_ riffkey.Match) { ed.DocEnd() })
	r.Handle("o", func(_ riffkey.Match) { ed.SwapVisualEnds() })
	r.Handle("O", func(_ riffkey.Match) { ed.SwapVisualEnds() })

	RegisterVisualTextObjects(r, ed)
	RegisterVisualOperators(r, app, ed)

	r.Handle("~", func(_ riffkey.Match) { ed.ToggleCaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })
	r.Handle("U", func(_ riffkey.Match) { ed.UppercaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })
	r.Handle("u", func(_ riffkey.Match) { ed.LowercaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })

	r.AddOnAfter(func() { ed.Refresh() })
	app.Push(r)
}
