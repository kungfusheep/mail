package compose

import (
	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// TextObjectFunc returns the range for a text object
type TextObjectFunc func(ed *Editor) Range

// TextObjectDef pairs a key with its text object function.
// Name is the human-readable label used in help; empty = alias (omitted from help).
type TextObjectDef struct {
	Key  string
	Name string
	Fn   TextObjectFunc
}

// OperatorFunc applies an operation to a range
type OperatorFunc func(ed *Editor, r Range)

// StyleOperatorDef pairs a key with a style operation
type StyleOperatorDef struct {
	Key   string
	Name  string
	Apply OperatorFunc
}

// EditOperatorDef pairs a key with an edit operation
type EditOperatorDef struct {
	Key         string
	Name        string
	Apply       OperatorFunc
	EnterInsert bool // true for 'c' - enters insert mode after
}

// TextObjects is the canonical list of all text objects.
// adding one here automatically makes it work with all operators.
// Name is shown in help; leave empty for aliases (same action, different key).
var TextObjects = []TextObjectDef{
	// word
	{"iw", "inner word", (*Editor).InnerWord},
	{"aw", "around word", (*Editor).AWord},

	// sentence
	{"is", "inner sentence", (*Editor).InnerSentence},
	{"as", "around sentence", (*Editor).ASentence},

	// clause (text between commas, semicolons, colons, dashes)
	{"i,", "inner clause", (*Editor).InnerClause},
	{"a,", "around clause", (*Editor).AClause},

	// section (capital S - structural document sections)
	{"iS", "inner section", (*Editor).InnerSection},
	{"aS", "around section", (*Editor).ASection},

	// paragraph
	{"ip", "inner paragraph", (*Editor).InnerParagraph},
	{"ap", "around paragraph", (*Editor).AParagraph},

	// quotes (double, single, backtick)
	{"i\"", "inner double quotes", func(ed *Editor) Range { return ed.InnerQuote('"') }},
	{"a\"", "around double quotes", func(ed *Editor) Range { return ed.AroundQuote('"') }},
	{"i'", "inner single quotes", func(ed *Editor) Range { return ed.InnerQuote('\'') }},
	{"a'", "around single quotes", func(ed *Editor) Range { return ed.AroundQuote('\'') }},
	{"i`", "inner backticks", func(ed *Editor) Range { return ed.InnerQuote('`') }},
	{"a`", "around backticks", func(ed *Editor) Range { return ed.AroundQuote('`') }},

	// parentheses (multiple key variants for vim compat)
	{"i(", "inner parens", (*Editor).InnerParen},
	{"a(", "around parens", (*Editor).AroundParen},
	{"i)", "", (*Editor).InnerParen},
	{"a)", "", (*Editor).AroundParen},
	{"ib", "", (*Editor).InnerParen},
	{"ab", "", (*Editor).AroundParen},

	// square brackets
	{"i[", "inner square brackets", (*Editor).InnerSquare},
	{"a[", "around square brackets", (*Editor).AroundSquare},
	{"i]", "", (*Editor).InnerSquare},
	{"a]", "", (*Editor).AroundSquare},

	// curly braces
	{"i{", "inner curly braces", (*Editor).InnerCurly},
	{"a{", "around curly braces", (*Editor).AroundCurly},
	{"i}", "", (*Editor).InnerCurly},
	{"a}", "", (*Editor).AroundCurly},
	{"iB", "", (*Editor).InnerCurly},
	{"aB", "", (*Editor).AroundCurly},

	// angle brackets
	{"i<", "inner angle brackets", (*Editor).InnerAngle},
	{"a<", "around angle brackets", (*Editor).AroundAngle},
	{"i>", "", (*Editor).InnerAngle},
	{"a>", "", (*Editor).AroundAngle},

	// tags (HTML/XML)
	{"it", "inner tag", (*Editor).InnerTag},
	{"at", "around tag", (*Editor).AroundTag},

	// linewise motions
	{"G", "to document end", (*Editor).ToDocEnd},
	{"gg", "to document start", (*Editor).ToDocStart},
	{"gG", "whole document", (*Editor).WholeDoc},

	// line-partial motions
	{"$", "to line end", (*Editor).ToLineEnd},
	{"0", "to line start", (*Editor).ToLineStart},
}

// StyleOperators is the canonical list of style operators.
// adding one here automatically makes it work with all text objects.
var StyleOperators = []StyleOperatorDef{
	{"gb", "bold", func(ed *Editor, r Range) { ed.ApplyStyle(r, StyleBold) }},
	{"gi", "italic", func(ed *Editor, r Range) { ed.ApplyStyle(r, StyleItalic) }},
	{"gu", "underline", func(ed *Editor, r Range) { ed.ApplyStyle(r, StyleUnderline) }},
	{"gs", "strikethrough", func(ed *Editor, r Range) { ed.ApplyStyle(r, StyleStrikethrough) }},
	{"gc", "clear style", func(ed *Editor, r Range) { ed.ClearStyle(r) }},
}

// EditOperators is the canonical list of edit operators.
// adding one here automatically makes it work with all text objects.
var EditOperators = []EditOperatorDef{
	{"d", "delete", func(ed *Editor, r Range) { ed.Delete(r) }, false},
	{"c", "change", func(ed *Editor, r Range) { ed.Change(r) }, true},
	{"y", "yank", func(ed *Editor, r Range) { ed.Yank(r) }, false},
}

// registerOperatorTextObjects sets up all operator+textobject combinations
// this is the cartesian product: N operators x M text objects = N*M handlers
func RegisterOperatorTextObjects(app *glyph.App, ed *Editor) {
	// style operators (gb, gi, gu, gs, gc) + text objects
	for _, op := range StyleOperators {
		for _, obj := range TextObjects {
			pattern := op.Key + obj.Key
			opFn, objFn := op.Apply, obj.Fn
			app.Handle(pattern, func(_ riffkey.Match) {
				opFn(ed, objFn(ed))
			})
		}
	}

	// edit operators (d, c, y) + text objects
	for _, op := range EditOperators {
		for _, obj := range TextObjects {
			pattern := op.Key + obj.Key
			opFn, objFn, enterIns := op.Apply, obj.Fn, op.EnterInsert
			app.Handle(pattern, func(_ riffkey.Match) {
				opFn(ed, objFn(ed))
				if enterIns {
					enterInsertMode(app, ed)
				}
			})
		}
	}
}

// registerVisualTextObjects sets up text object selection in visual mode
func RegisterVisualTextObjects(router *riffkey.Router, ed *Editor) {
	for _, obj := range TextObjects {
		objFn := obj.Fn
		router.Handle(obj.Key, func(_ riffkey.Match) {
			r := objFn(ed)
			ed.SelectVisualRange(r)
		})
	}
}

// registerVisualOperators sets up operators that work on visual selection
func RegisterVisualOperators(router *riffkey.Router, app *glyph.App, ed *Editor) {
	// style operators on selection
	for _, op := range StyleOperators {
		opFn := op.Apply
		router.Handle(op.Key, func(_ riffkey.Match) {
			opFn(ed, ed.VisualRange())
			ed.ExitVisual()
			app.Pop()
		})
	}

	// edit operators on selection
	for _, op := range EditOperators {
		opFn, enterIns := op.Apply, op.EnterInsert
		router.Handle(op.Key, func(_ riffkey.Match) {
			opFn(ed, ed.VisualRange())
			ed.ExitVisual()
			app.Pop()
			if enterIns {
				enterInsertMode(app, ed)
			}
		})
	}

	// x is alias for d in visual mode
	router.Handle("x", func(_ riffkey.Match) {
		ed.Delete(ed.VisualRange())
		ed.ExitVisual()
		app.Pop()
	})
}
