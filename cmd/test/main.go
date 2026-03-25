package main

import (
	"log"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/riffkey"
)

// search view state
var (
	searchQuery  string
	searchPrompt string
	searchFwd    bool
)

func main() {
	app := NewApp()

	doc := compose.NewDocument()
	ed := compose.NewEditor(doc, "")
	ed.SetApp(app)

	// wire the enterInsertMode function variable used by operators.go
	compose.SetEnterInsertMode(func(a *App, e *compose.Editor) {
		enterInsertMode(a, e)
	})

	// start spell check result consumer
	ed.StartSpellResultWorker(app.RequestRender)

	// set view once — LayerView wraps the editor's layer
	app.SetView(VBox(LayerView(ed.Layer()).Grow(1)))

	// render callback — glyph calls this with correct viewport size
	ed.Layer().Render = func() {
		w := ed.Layer().ViewportWidth()
		h := ed.Layer().ViewportHeight()
		if w > 0 && h > 0 {
			ed.SetSize(w, h)
			ed.UpdateDisplay()
		}
	}
	ed.Layer().AlwaysRender = true

	// search view (for / and ?)
	app.View("search",
		VBox(
			HBox(
				Text(&searchPrompt).Bold(),
				Text(&searchQuery),
			),
		),
	).
		Handle("<CR>", func() {
			q := searchQuery
			fwd := searchFwd
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			if q != "" {
				ed.Search(q, fwd)
			}
			ed.Refresh()
		}).
		Handle("<Esc>", func() {
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			ed.Refresh()
		}).
		Handle("<BS>", func() {
			if len(searchQuery) > 0 {
				runes := []rune(searchQuery)
				searchQuery = string(runes[:len(runes)-1])
			}
		}).
		NoCounts()

	if searchRouter, ok := app.ViewRouter("search"); ok {
		searchRouter.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				searchQuery += string(k.Rune)
				app.RequestRender()
				return true
			}
			return false
		})
	}

	// normal mode keybindings (matches wed's setupNormalMode)
	setupNormalMode(app, ed)

	// refresh after every normal mode handler
	app.Router().AddOnAfter(func() {
		ed.Refresh()
	})

	// initial display
	ed.Refresh()

	// start in insert mode
	ed.EnterInsert()
	enterInsertMode(app, ed)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func setupNormalMode(app *App, ed *compose.Editor) {
	// quit
	app.Handle("<C-q>", func(_ riffkey.Match) { app.Stop() })

	// movement
	app.HandleNamed("move_cursor_left", "h", func(m riffkey.Match) { ed.Left(m.Count) })
	app.HandleNamed("move_cursor_right", "l", func(m riffkey.Match) { ed.Right(m.Count) })
	app.HandleNamed("move_cursor_down", "j", func(m riffkey.Match) { ed.Down(m.Count) })
	app.HandleNamed("move_cursor_up", "k", func(m riffkey.Match) { ed.Up(m.Count) })
	app.Handle("<Left>", func(m riffkey.Match) { ed.Left(m.Count) })
	app.Handle("<Right>", func(m riffkey.Match) { ed.Right(m.Count) })
	app.Handle("<Down>", func(m riffkey.Match) { ed.Down(m.Count) })
	app.Handle("<Up>", func(m riffkey.Match) { ed.Up(m.Count) })

	// block-wise movement
	app.HandleNamed("block_down", "gj", func(m riffkey.Match) { ed.BlockDown(m.Count) })
	app.HandleNamed("block_up", "gk", func(m riffkey.Match) { ed.BlockUp(m.Count) })

	// escape in normal mode: exit dialogue if on empty dialogue block
	app.Handle("<Esc>", func(_ riffkey.Match) { ed.ExitDialogueIfEmpty() })

	app.HandleNamed("line_start", "0", func(_ riffkey.Match) { ed.LineStart() })
	app.HandleNamed("line_end", "$", func(_ riffkey.Match) { ed.LineEnd() })
	app.HandleNamed("first_non_blank", "^", func(_ riffkey.Match) { ed.FirstNonBlank() })
	app.HandleNamed("goto_document_start", "gg", func(_ riffkey.Match) { ed.DocStart() })
	app.HandleNamed("goto_document_end", "G", func(_ riffkey.Match) { ed.DocEnd() })
	app.HandleNamed("word_forward", "w", func(m riffkey.Match) { ed.NextWordStart(m.Count) })
	app.HandleNamed("word_backward", "b", func(m riffkey.Match) { ed.PrevWordStart(m.Count) })
	app.HandleNamed("word_end", "e", func(m riffkey.Match) { ed.NextWordEnd(m.Count) })

	// scrolling
	app.HandleNamed("scroll_half_page_down", "<C-d>", func(_ riffkey.Match) { ed.ScrollHalfPageDown() })
	app.HandleNamed("scroll_half_page_up", "<C-u>", func(_ riffkey.Match) { ed.ScrollHalfPageUp() })
	app.HandleNamed("scroll_page_down", "<C-f>", func(_ riffkey.Match) { ed.ScrollPageDown() })
	app.HandleNamed("scroll_page_up", "<C-b>", func(_ riffkey.Match) { ed.ScrollPageUp() })
	app.HandleNamed("scroll_line_down", "<C-e>", func(_ riffkey.Match) { ed.ScrollLineDown() })
	app.HandleNamed("scroll_line_up", "<C-y>", func(_ riffkey.Match) { ed.ScrollLineUp() })
	app.HandleNamed("scroll_center", "zz", func(_ riffkey.Match) { ed.ScrollCenter() })
	app.HandleNamed("scroll_to_top", "zt", func(_ riffkey.Match) { ed.ScrollTop() })
	app.HandleNamed("scroll_to_bottom", "zb", func(_ riffkey.Match) { ed.ScrollBottom() })

	// view modes
	app.HandleNamed("toggle_typewriter_mode", "zT", func(_ riffkey.Match) { ed.ToggleTypewriterMode() })
	app.HandleNamed("toggle_focus_mode", "zf", func(_ riffkey.Match) { ed.ToggleFocusMode() })
	app.HandleNamed("cycle_focus_scope", "zF", func(_ riffkey.Match) { ed.CycleFocusScope() })
	app.HandleNamed("toggle_zen_mode", "gz", func(_ riffkey.Match) { ed.ToggleZenMode() })
	app.HandleNamed("toggle_raw_mode", "zr", func(_ riffkey.Match) { ed.ToggleRawMode() })

	// toggle theme
	app.HandleNamed("toggle_theme", "\\t", func(_ riffkey.Match) { ed.ToggleTheme() })

	// spell check
	app.HandleNamed("add_word_to_dictionary", "zg", func(_ riffkey.Match) { ed.AddWordToDictionary() })
	app.HandleNamed("next_misspelled", "]e", func(_ riffkey.Match) { ed.NextMisspelled() })
	app.HandleNamed("prev_misspelled", "[e", func(_ riffkey.Match) { ed.PrevMisspelled() })

	// same-level section navigation
	app.HandleNamed("next_same_level_section", "]S", func(_ riffkey.Match) { ed.NextSameLevel() })
	app.HandleNamed("prev_same_level_section", "[S", func(_ riffkey.Match) { ed.PrevSameLevel() })

	// heading promote/demote
	app.HandleNamed("promote_heading", "g>", func(_ riffkey.Match) { ed.PromoteHeading() })
	app.HandleNamed("demote_heading", "g<", func(_ riffkey.Match) { ed.DemoteHeading() })

	// move block up/down
	app.HandleNamed("move_block_down", "<A-j>", func(_ riffkey.Match) { ed.MoveBlockDown() })
	app.HandleNamed("move_block_up", "<A-k>", func(_ riffkey.Match) { ed.MoveBlockUp() })

	// visual mode
	app.HandleNamed("visual_mode", "v", func(_ riffkey.Match) { enterVisualMode(app, ed) })
	app.HandleNamed("visual_line_mode", "V", func(_ riffkey.Match) { enterVisualLineMode(app, ed) })
	app.HandleNamed("visual_block_mode", "<C-v>", func(_ riffkey.Match) { enterVisualBlockMode(app, ed) })

	// insert mode entries
	app.HandleNamed("insert", "i", func(_ riffkey.Match) { ed.EnterInsert(); enterInsertMode(app, ed) })
	app.HandleNamed("insert_after", "a", func(_ riffkey.Match) { ed.EnterInsertAfter(); enterInsertMode(app, ed) })
	app.HandleNamed("insert_line_start", "I", func(_ riffkey.Match) { ed.EnterInsertLineStart(); enterInsertMode(app, ed) })
	app.HandleNamed("insert_line_end", "A", func(_ riffkey.Match) { ed.EnterInsertLineEnd(); enterInsertMode(app, ed) })
	app.HandleNamed("open_line_below", "o", func(_ riffkey.Match) { ed.OpenBelow(); enterInsertMode(app, ed) })
	app.HandleNamed("open_line_above", "O", func(_ riffkey.Match) { ed.OpenAbove(); enterInsertMode(app, ed) })

	// editing
	app.HandleNamed("delete_char", "x", func(m riffkey.Match) {
		for range m.Count { ed.DeleteChar() }
	})

	// replace character
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
	app.HandleNamed("replace_char", "r", func(_ riffkey.Match) { app.Push(replaceRouter) })

	// dd, dj, dk, D
	app.HandleNamed("delete_line", "dd", func(m riffkey.Match) {
		for range m.Count { ed.DeleteLine() }
	})
	app.Handle("dj", func(m riffkey.Match) {
		for range m.Count + 1 { ed.DeleteLine() }
	})
	app.Handle("dk", func(m riffkey.Match) {
		for range m.Count + 1 {
			if ed.Cursor().Block > 0 { ed.BlockUp(1) }
			ed.DeleteLine()
		}
	})
	app.HandleNamed("delete_to_line_end", "D", func(_ riffkey.Match) {
		ed.Delete(compose.Range{
			Start: ed.Cursor(),
			End:   compose.Pos{Block: ed.Cursor().Block, Col: ed.CurrentBlock().Length()},
		})
	})

	// join lines
	app.HandleNamed("join_lines", "J", func(m riffkey.Match) {
		for range m.Count { ed.JoinLines() }
	})

	// undo/redo
	app.HandleNamed("undo", "u", func(_ riffkey.Match) { ed.Undo() })
	app.HandleNamed("redo", "<C-r>", func(_ riffkey.Match) { ed.Redo() })

	// yank/paste
	app.HandleNamed("yank_line", "yy", func(_ riffkey.Match) { ed.Yank(ed.InnerBlock()) })
	app.HandleNamed("put_after", "p", func(_ riffkey.Match) { ed.Put() })
	app.HandleNamed("put_before", "P", func(_ riffkey.Match) { ed.PutBefore() })

	// cc, cj, ck, C
	app.HandleNamed("change_line", "cc", func(_ riffkey.Match) {
		ed.Change(ed.InnerBlock())
		enterInsertMode(app, ed)
	})
	app.Handle("cj", func(m riffkey.Match) {
		for range m.Count { ed.DeleteLine() }
		ed.Change(ed.InnerBlock())
		enterInsertMode(app, ed)
	})
	app.Handle("ck", func(m riffkey.Match) {
		for range m.Count {
			if ed.Cursor().Block > 0 { ed.BlockUp(1) }
			ed.DeleteLine()
		}
		ed.Change(ed.InnerBlock())
		enterInsertMode(app, ed)
	})
	app.HandleNamed("change_to_line_end", "C", func(_ riffkey.Match) {
		ed.Change(compose.Range{
			Start: ed.Cursor(),
			End:   compose.Pos{Block: ed.Cursor().Block, Col: ed.CurrentBlock().Length()},
		})
		enterInsertMode(app, ed)
	})

	// f/F/t/T
	setupFindChar(app, ed)

	// marks
	setupMarks(app, ed)

	// search
	setupSearch(app, ed)

	// case toggle
	app.HandleNamed("toggle_case", "~", func(_ riffkey.Match) { ed.ToggleCase() })

	// screen position jumps
	app.HandleNamed("goto_screen_top", "H", func(_ riffkey.Match) { ed.GotoScreenTop() })
	app.HandleNamed("goto_screen_middle", "M", func(_ riffkey.Match) { ed.GotoScreenMiddle() })
	app.HandleNamed("goto_screen_bottom", "L", func(_ riffkey.Match) { ed.GotoScreenBottom() })

	// jump list
	app.HandleNamed("jump_back", "<C-o>", func(_ riffkey.Match) { ed.JumpBack() })
	app.HandleNamed("jump_forward", "<C-i>", func(_ riffkey.Match) { ed.JumpForward() })

	// line navigation
	app.HandleNamed("prev_line", "-", func(_ riffkey.Match) { ed.PrevLineFirstNonBlank() })
	app.HandleNamed("next_line", "+", func(_ riffkey.Match) { ed.NextLineFirstNonBlank() })

	// increment/decrement numbers
	app.HandleNamed("increment_number", "<C-a>", func(_ riffkey.Match) { ed.IncrementNumber(1) })
	app.HandleNamed("decrement_number", "<C-x>", func(_ riffkey.Match) { ed.IncrementNumber(-1) })

	// repeat last action
	app.HandleNamed("repeat_last_action", ".", func(_ riffkey.Match) { ed.RepeatLastAction() })

	// sentence motions
	app.HandleNamed("next_sentence", ")", func(m riffkey.Match) {
		for range m.Count { ed.NextSentence() }
	})
	app.HandleNamed("prev_sentence", "(", func(m riffkey.Match) {
		for range m.Count { ed.PrevSentence() }
	})

	// sentence transposition
	app.HandleNamed("swap_sentence_forward", "gs)", func(_ riffkey.Match) { ed.SwapSentenceNext() })
	app.HandleNamed("swap_sentence_backward", "gs(", func(_ riffkey.Match) { ed.SwapSentencePrev() })

	// style + editing operators via cartesian product
	compose.RegisterOperatorTextObjects(app, ed)

	// block type shortcuts
	app.HandleNamed("set_paragraph", "g0", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockParagraph) })
	app.HandleNamed("set_heading_1", "g1", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH1) })
	app.HandleNamed("set_heading_2", "g2", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH2) })
	app.HandleNamed("set_heading_3", "g3", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH3) })
	app.HandleNamed("set_heading_4", "g4", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH4) })
	app.HandleNamed("set_heading_5", "g5", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH5) })
	app.HandleNamed("set_heading_6", "g6", func(_ riffkey.Match) { ed.SetBlockType(compose.BlockH6) })

	// insert divider
	app.HandleNamed("insert_divider", "g-", func(_ riffkey.Match) { ed.InsertDivider() })

	// style yank/paste
	app.HandleNamed("yank_style", "gyiw", func(_ riffkey.Match) { ed.YankStyle() })
	app.HandleNamed("paste_style", "gpiw", func(_ riffkey.Match) { ed.PasteStyle(ed.InnerWord()) })

	// template cycling
	app.HandleNamed("cycle_heading_template", "gth", func(_ riffkey.Match) { ed.CycleTemplate("headings") })
	app.HandleNamed("cycle_quote_template", "gtq", func(_ riffkey.Match) { ed.CycleTemplate("quotes") })
	app.HandleNamed("cycle_divider_template", "gtd", func(_ riffkey.Match) { ed.CycleTemplate("dividers") })
	app.HandleNamed("cycle_list_template", "gtl", func(_ riffkey.Match) { ed.CycleTemplate("lists") })
	app.HandleNamed("cycle_code_template", "gtc", func(_ riffkey.Match) { ed.CycleTemplate("code") })
	app.HandleNamed("cycle_table_template", "gtt", func(_ riffkey.Match) { ed.CycleTemplate("tables") })
	app.HandleNamed("cycle_dialogue_template", "gt@", func(_ riffkey.Match) { ed.CycleTemplate("dialogue") })

	// bundles
	app.HandleNamed("apply_typewriter_bundle", "gBt", func(_ riffkey.Match) { ed.ApplyBundle("typewriter") })
	app.HandleNamed("apply_minimal_bundle", "gBm", func(_ riffkey.Match) { ed.ApplyBundle("minimal") })
	app.HandleNamed("apply_academic_bundle", "gBa", func(_ riffkey.Match) { ed.ApplyBundle("academic") })
	app.HandleNamed("apply_creative_bundle", "gBc", func(_ riffkey.Match) { ed.ApplyBundle("creative") })

	// block navigation matchers
	compose.RegisterMatchers(app, ed)
}

func enterInsertMode(app *App, ed *compose.Editor) {
	r := riffkey.NewRouter().Name("insert").NoCounts()

	r.Handle("<Esc>", func(_ riffkey.Match) { ed.EnterNormal(); app.Pop() })
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
		if b != nil && b.Type == compose.BlockDialogue {
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

	r.AddOnAfter(func() { ed.Refresh() })
	app.Push(r)
}

func enterVisualMode(app *App, ed *compose.Editor) {
	ed.EnterVisual()
	setupVisualRouter(app, ed)
}

func enterVisualLineMode(app *App, ed *compose.Editor) {
	ed.EnterVisualLine()
	setupVisualRouter(app, ed)
}

func enterVisualBlockMode(app *App, ed *compose.Editor) {
	ed.EnterVisualBlock()
	setupVisualRouter(app, ed)
}

func setupVisualRouter(app *App, ed *compose.Editor) {
	r := riffkey.NewRouter().Name("visual").NoCounts()

	r.Handle("<Esc>", func(_ riffkey.Match) { ed.ExitVisual(); app.Pop() })

	r.Handle("v", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == compose.VisualChar {
			ed.ExitVisual(); app.Pop()
		} else {
			ed.SetVisualMode(compose.VisualChar)
		}
	})
	r.Handle("V", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == compose.VisualLine {
			ed.ExitVisual(); app.Pop()
		} else {
			ed.SetVisualMode(compose.VisualLine)
		}
	})
	r.Handle("<C-v>", func(_ riffkey.Match) {
		if ed.CurrentVisualMode() == compose.VisualBlock {
			ed.ExitVisual(); app.Pop()
		} else {
			ed.SetVisualMode(compose.VisualBlock)
		}
	})

	// movement
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

	// text objects + operators via registry
	compose.RegisterVisualTextObjects(r, ed)
	compose.RegisterVisualOperators(r, app, ed)

	// case operations
	r.Handle("~", func(_ riffkey.Match) { ed.ToggleCaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })
	r.Handle("U", func(_ riffkey.Match) { ed.UppercaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })
	r.Handle("u", func(_ riffkey.Match) { ed.LowercaseRange(ed.VisualRange()); ed.ExitVisual(); app.Pop() })

	r.AddOnAfter(func() { ed.Refresh() })
	app.Push(r)
}

func setupFindChar(app *App, ed *compose.Editor) {
	promptForChar := func(action func(rune)) {
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

	app.Handle("f", func(_ riffkey.Match) { promptForChar(ed.FindChar) })
	app.Handle("F", func(_ riffkey.Match) { promptForChar(ed.FindCharBack) })
	app.Handle("t", func(_ riffkey.Match) { promptForChar(ed.TillChar) })
	app.Handle("T", func(_ riffkey.Match) { promptForChar(ed.TillCharBack) })
	app.Handle(";", func(_ riffkey.Match) { ed.RepeatFind() })
	app.Handle(",", func(_ riffkey.Match) { ed.RepeatFindReverse() })
}

func setupMarks(app *App, ed *compose.Editor) {
	promptForRegister := func(action func(rune)) {
		mr := riffkey.NewRouter().Name("register").NoCounts()
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

	app.Handle("m", func(_ riffkey.Match) { promptForRegister(ed.SetMark) })
	app.Handle("'", func(_ riffkey.Match) {
		promptForRegister(func(reg rune) { ed.GotoMarkLine(reg) })
	})
	app.Handle("`", func(_ riffkey.Match) {
		promptForRegister(func(reg rune) { ed.GotoMark(reg) })
	})
}

func setupSearch(app *App, ed *compose.Editor) {
	startSearch := func(forward bool) {
		if forward {
			searchPrompt = "/"
		} else {
			searchPrompt = "?"
		}
		searchQuery = ""
		searchFwd = forward
		app.HideCursor()
		app.PushView("search")
	}

	app.Handle("/", func(_ riffkey.Match) { startSearch(true) })
	app.Handle("?", func(_ riffkey.Match) { startSearch(false) })
	app.Handle("n", func(_ riffkey.Match) { ed.SearchNext() })
	app.Handle("N", func(_ riffkey.Match) { ed.SearchPrev() })

	// search word under cursor
	app.Handle("*", func(_ riffkey.Match) {
		word := ed.InnerWord()
		if word.Start.Block == word.End.Block {
			b := ed.CurrentBlock()
			if b != nil {
				runes := []rune(b.Text())
				if word.Start.Col < len(runes) && word.End.Col <= len(runes) {
					pattern := string(runes[word.Start.Col:word.End.Col])
					if pattern != "" {
						ed.Search(pattern, true)
					}
				}
			}
		}
	})
	app.Handle("#", func(_ riffkey.Match) {
		word := ed.InnerWord()
		if word.Start.Block == word.End.Block {
			b := ed.CurrentBlock()
			if b != nil {
				runes := []rune(b.Text())
				if word.Start.Col < len(runes) && word.End.Col <= len(runes) {
					pattern := string(runes[word.Start.Col:word.End.Col])
					if pattern != "" {
						ed.Search(pattern, false)
					}
				}
			}
		}
	})
}
