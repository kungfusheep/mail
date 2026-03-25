package compose

import (
	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
	"strings"
)

// =============================================================================
// Last Matcher State - for } and { repeat navigation
// =============================================================================

type lastMatcherState struct {
	isBlock bool         // true = block matcher, false = text matcher
	block   BlockMatcher // last block matcher used
	text    TextMatcher  // last text matcher used
	name    string       // for display purposes
}

var lastMatcher lastMatcherState

// =============================================================================
// Block Matchers - identify blocks by type
// =============================================================================

// BlockMatcher returns true if the block matches the criteria
type BlockMatcher func(b *Block) bool

// BlockMatcherDef pairs a key with a block matcher
type BlockMatcherDef struct {
	Key     string
	Name    string
	Icon    string
	Matcher BlockMatcher
}

// blockMatchers is the canonical list of block-level navigation targets
// adding one here automatically enables ]x, [x navigation AND document mapping
var blockMatchers = []BlockMatcherDef{
	{"h", "Heading", "󰉫", func(b *Block) bool { return isHeadingType(b.Type) }},
	{"l", "List Item", "󰉹", func(b *Block) bool { return b.Type == BlockListItem }},
	{"q", "Quote", "󰸢", func(b *Block) bool { return b.Type == BlockQuote }},
	{"c", "Code", "󰅩", func(b *Block) bool { return b.Type == BlockCodeLine }},
	{"t", "Table", "󱁉", func(b *Block) bool { return b.Type == BlockTable }},
	{"d", "Dialogue", "󰑈", func(b *Block) bool { return b.Type == BlockDialogue }},
	{"P", "Parenthetical", "󰅲", func(b *Block) bool { return b.Type == BlockParenthetical }},
	{"s", "Scene Heading", "󰕐", func(b *Block) bool { return b.Type == BlockSceneHeading }},
}

func isHeadingType(t BlockType) bool {
	return t == BlockH1 || t == BlockH2 || t == BlockH3 ||
		t == BlockH4 || t == BlockH5 || t == BlockH6
}

// =============================================================================
// Text Matchers - identify patterns within text
// =============================================================================

// TextMatcher finds a pattern at or near the given column position
// returns (start, end, found) - the range of the match if found
type TextMatcher func(text string, col int) (start, end int, found bool)

// TextMatcherDef pairs a key with a text matcher
type TextMatcherDef struct {
	Key     string
	Name    string
	Icon    string
	Matcher TextMatcher
}

// textMatchers is the canonical list of text-level navigation targets
var textMatchers = []TextMatcherDef{
	{"f", "Filler Word", "󰗊", matchFillerWord},
	// text object navigation - jump to next/prev quote or bracket pair
	{"\"", "Double Quote", "󰉾", makeQuoteMatcher('"')},
	{"'", "Single Quote", "󰉾", makeQuoteMatcher('\'')},
	{"`", "Backtick", "󰉾", makeQuoteMatcher('`')},
	{"(", "Parentheses", "󰅲", makeBracketMatcher('(', ')')},
	{")", "Parentheses", "󰅲", makeBracketMatcher('(', ')')},
	{"[", "Square Brackets", "󰅪", makeBracketMatcher('[', ']')},
	{"]", "Square Brackets", "󰅪", makeBracketMatcher('[', ']')},
	{"{", "Curly Braces", "󰅩", makeBracketMatcher('{', '}')},
	{"}", "Curly Braces", "󰅩", makeBracketMatcher('{', '}')},
	{"<", "Angle Brackets", "󰅤", makeBracketMatcher('<', '>')},
	{">", "Angle Brackets", "󰅤", makeBracketMatcher('<', '>')},
}

// =============================================================================
// Quote and Bracket Matchers - for ]"/[" style navigation
// =============================================================================

// makeQuoteMatcher creates a TextMatcher that finds quoted strings
// for navigation: finds the next pair whose opening quote is at or after col
func makeQuoteMatcher(quote rune) TextMatcher {
	return func(text string, col int) (start, end int, found bool) {
		runes := []rune(text)

		// find all quote positions
		var positions []int
		for i, r := range runes {
			if r == quote {
				positions = append(positions, i)
			}
		}

		// pair them up and find first pair whose START is at or after col
		for i := 0; i+1 < len(positions); i += 2 {
			pairStart := positions[i]
			pairEnd := positions[i+1]
			if pairStart >= col {
				return pairStart, pairEnd + 1, true
			}
		}

		return 0, 0, false
	}
}

// makeBracketMatcher creates a TextMatcher that finds bracketed content
func makeBracketMatcher(open, close rune) TextMatcher {
	return func(text string, col int) (start, end int, found bool) {
		runes := []rune(text)

		// search forward from col for opening bracket
		for i := col; i < len(runes); i++ {
			if runes[i] == open {
				// found opening, find matching close with nesting
				depth := 1
				for j := i + 1; j < len(runes); j++ {
					if runes[j] == open {
						depth++
					} else if runes[j] == close {
						depth--
						if depth == 0 {
							return i, j + 1, true
						}
					}
				}
			}
		}

		return 0, 0, false
	}
}

// fillerWords are words that often weaken prose
var fillerWords = []string{
	"very", "really", "just", "quite", "rather", "somewhat",
	"basically", "actually", "literally", "definitely", "certainly",
	"probably", "possibly", "maybe", "perhaps", "simply",
	"extremely", "totally", "completely", "absolutely", "utterly",
	"stuff", "things", "got", "get", "thing",
}

// matchFillerWord finds a filler word at or after the given position
func matchFillerWord(text string, col int) (start, end int, found bool) {
	lower := strings.ToLower(text)

	// find word boundaries at/after col
	for i := col; i < len(text); i++ {
		// skip to start of next word
		for i < len(text) && !isWordChar(rune(text[i])) {
			i++
		}
		if i >= len(text) {
			break
		}

		// find word end
		wordStart := i
		for i < len(text) && isWordChar(rune(text[i])) {
			i++
		}
		wordEnd := i

		// check if this word is a filler
		word := lower[wordStart:wordEnd]
		for _, filler := range fillerWords {
			if word == filler {
				return wordStart, wordEnd, true
			}
		}
	}

	return 0, 0, false
}

// isWordChar is defined in editor.go

// =============================================================================
// MapEntry - represents a found item for document mapping
// =============================================================================

// MapEntry represents an item found by a matcher
type MapEntry struct {
	BlockIdx int
	Col      int
	Text     string // preview text to show
}

// =============================================================================
// Registration - wire up matchers to keybindings
// =============================================================================

// registerBlockNavigation sets up ]x/[x navigation for all block matchers
func RegisterBlockNavigation(app *glyph.App, ed *Editor) {
	for _, def := range blockMatchers {
		d := def // capture
		app.Handle("]"+d.Key, func(_ riffkey.Match) {
			lastMatcher = lastMatcherState{isBlock: true, block: d.Matcher, name: d.Name}
			ed.NextBlockMatching(d.Matcher)
		})
		app.Handle("["+d.Key, func(_ riffkey.Match) {
			lastMatcher = lastMatcherState{isBlock: true, block: d.Matcher, name: d.Name}
			ed.PrevBlockMatching(d.Matcher)
		})
	}
}

// registerTextNavigation sets up ]x/[x navigation for all text matchers
func RegisterTextNavigation(app *glyph.App, ed *Editor) {
	for _, def := range textMatchers {
		d := def // capture
		app.Handle("]"+d.Key, func(_ riffkey.Match) {
			lastMatcher = lastMatcherState{isBlock: false, text: d.Matcher, name: d.Name}
			ed.NextTextMatching(d.Matcher)
		})
		app.Handle("["+d.Key, func(_ riffkey.Match) {
			lastMatcher = lastMatcherState{isBlock: false, text: d.Matcher, name: d.Name}
			ed.PrevTextMatching(d.Matcher)
		})
	}
}

// registerRepeatNavigation sets up } and { to repeat the last matcher navigation
func RegisterRepeatNavigation(app *glyph.App, ed *Editor) {
	app.Handle("}", func(_ riffkey.Match) {
		ed.RepeatMatcherNext()
	})
	app.Handle("{", func(_ riffkey.Match) {
		ed.RepeatMatcherPrev()
	})
}

// RepeatMatcherNext jumps to the next match using the last used matcher (})
func (e *Editor) RepeatMatcherNext() {
	if lastMatcher.block == nil && lastMatcher.text == nil {
		return
	}
	if lastMatcher.isBlock {
		e.NextBlockMatching(lastMatcher.block)
	} else {
		e.NextTextMatching(lastMatcher.text)
	}
}

// RepeatMatcherPrev jumps to the previous match using the last used matcher ({)
func (e *Editor) RepeatMatcherPrev() {
	if lastMatcher.block == nil && lastMatcher.text == nil {
		return
	}
	if lastMatcher.isBlock {
		e.PrevBlockMatching(lastMatcher.block)
	} else {
		e.PrevTextMatching(lastMatcher.text)
	}
}

// registerMatchers sets up all matcher-based navigation
func RegisterMatchers(app *glyph.App, ed *Editor) {
	RegisterBlockNavigation(app, ed)
	RegisterTextNavigation(app, ed)
	RegisterRepeatNavigation(app, ed)
}

// registerDocumentMappers sets up gm{x} commands for document mapping
func RegisterDocumentMappers(app *glyph.App, ed *Editor) {
	// block mappers: gmh, gml, gmq, gmc, gmt
	for _, def := range blockMatchers {
		d := def // capture
		app.Handle("gm"+d.Key, func(_ riffkey.Match) {
			openBlockMap(app, ed, d.Name, d.Icon, d.Matcher, nil)
		})
	}

	// text mappers: gmf
	for _, def := range textMatchers {
		d := def // capture
		app.Handle("gm"+d.Key, func(_ riffkey.Match) {
			openTextMap(app, ed, d.Name, d.Icon, d.Matcher, nil)
		})
	}
}

// =============================================================================
// Omnibox Generation - auto-generate entries from registries
// =============================================================================

// GenerateMatcherOmniboxItems creates omnibox entries for all matchers
// onBack is called when Esc is pressed in a map view (to return to main omnibox)
func GenerateMatcherOmniboxItems(app *glyph.App, ed *Editor, onBack func()) []OmniboxItem {
	var items []OmniboxItem

	// Block matchers: Next X, Prev X, Map X
	for _, def := range blockMatchers {
		d := def // capture
		items = append(items,
			OmniboxItem{
				Label:       "Next " + d.Name,
				Icon:        d.Icon,
				Description: "]" + d.Key,
				Action:      func() { ed.NextBlockMatching(d.Matcher) },
			},
			OmniboxItem{
				Label:       "Prev " + d.Name,
				Icon:        d.Icon,
				Description: "[" + d.Key,
				Action:      func() { ed.PrevBlockMatching(d.Matcher) },
			},
			OmniboxItem{
				Label:       "Map " + d.Name + "s",
				Icon:        d.Icon,
				Description: "gm" + d.Key,
				Action:      func() { openBlockMap(app, ed, d.Name, d.Icon, d.Matcher, onBack) },
			},
		)
	}

	// Text matchers: Next X, Prev X, Map X
	// dedupe by name since bracket pairs (e.g. "(" and ")") share the same name
	seenNames := make(map[string]bool)
	for _, def := range textMatchers {
		if seenNames[def.Name] {
			continue
		}
		seenNames[def.Name] = true
		d := def // capture
		items = append(items,
			OmniboxItem{
				Label:       "Next " + d.Name,
				Icon:        d.Icon,
				Description: "]" + d.Key,
				Action:      func() { ed.NextTextMatching(d.Matcher) },
			},
			OmniboxItem{
				Label:       "Prev " + d.Name,
				Icon:        d.Icon,
				Description: "[" + d.Key,
				Action:      func() { ed.PrevTextMatching(d.Matcher) },
			},
			OmniboxItem{
				Label:       "Map " + d.Name + "s",
				Icon:        d.Icon,
				Description: "gm" + d.Key,
				Action:      func() { openTextMap(app, ed, d.Name, d.Icon, d.Matcher, onBack) },
			},
		)
	}

	// Repeat navigation
	items = append(items,
		OmniboxItem{
			Label:       "Repeat Next",
			Icon:        "󰑑",
			Description: "}",
			Action:      func() { ed.RepeatMatcherNext() },
		},
		OmniboxItem{
			Label:       "Repeat Prev",
			Icon:        "󰑐",
			Description: "{",
			Action:      func() { ed.RepeatMatcherPrev() },
		},
	)

	return items
}

// =============================================================================
// Document Mapping - show all matches in omnibox
// =============================================================================

// openBlockMap opens an omnibox showing all blocks matching the given matcher
// with live preview - selection changes jump to that location, Esc restores original
// if onCancel is provided, it's called on Esc instead of just closing (for "back" navigation)
func openBlockMap(app *glyph.App, ed *Editor, name, icon string, matcher BlockMatcher, onCancel func()) {
	entries := ed.AllBlocksMatching(matcher)
	if len(entries) == 0 {
		return
	}

	// save original position for Esc restore
	originalPos := ed.Cursor()

	var items []OmniboxItem
	for _, entry := range entries {
		blockIdx := entry.BlockIdx
		items = append(items, OmniboxItem{
			Label:       entry.Text,
			Icon:        icon,
			Description: "",
			Action: func() {
				ed.GotoBlock(blockIdx)
			},
		})
	}

	ed.omnibox.SetItems(items)
	ed.omnibox.SetPrompt(icon + " " + name + ": ")
	ed.omnibox.Show()
	app.SetView(buildView(ed))
	app.HideCursor()

	// preview helper - jump to the currently selected entry
	// note: Selected is index into Filtered, not original entries
	preview := func() {
		if ed.omnibox.Selected >= 0 && ed.omnibox.Selected < len(ed.omnibox.Filtered) {
			// find matching entry by label
			selectedLabel := ed.omnibox.Filtered[ed.omnibox.Selected].Label
			for _, entry := range entries {
				if entry.Text == selectedLabel {
					ed.GotoBlock(entry.BlockIdx)
					return
				}
			}
		}
	}

	// initial preview
	preview()

	mapRouter := riffkey.NewRouter().Name("map").NoCounts()

	mapRouter.Handle("<Esc>", func(_ riffkey.Match) {
		ed.moveCursor(originalPos)
		ed.omnibox.Hide()
		app.Pop()
		if onCancel != nil {
			onCancel()
		}
	})

	mapRouter.Handle("<CR>", func(_ riffkey.Match) {
		ed.omnibox.Hide()
		app.Pop()
	})

	// navigation with preview
	mapRouter.Handle("<Up>", func(_ riffkey.Match) { ed.omnibox.SelectPrev(); preview() })
	mapRouter.Handle("<C-p>", func(_ riffkey.Match) { ed.omnibox.SelectPrev(); preview() })
	mapRouter.Handle("<C-k>", func(_ riffkey.Match) { ed.omnibox.SelectPrev(); preview() })
	mapRouter.Handle("<Down>", func(_ riffkey.Match) { ed.omnibox.SelectNext(); preview() })
	mapRouter.Handle("<C-j>", func(_ riffkey.Match) { ed.omnibox.SelectNext(); preview() })
	mapRouter.Handle("<C-n>", func(_ riffkey.Match) { ed.omnibox.SelectNext(); preview() })
	mapRouter.Handle("<BS>", func(_ riffkey.Match) { ed.omnibox.Backspace() })

	mapRouter.HandleUnmatched(func(k riffkey.Key) bool {
		if k.Rune != 0 && k.Mod == 0 {
			ed.omnibox.InsertChar(k.Rune)
			return true
		}
		return false
	})

	mapRouter.AddOnAfter(func() {
		ed.refresh()
	})
	app.Push(mapRouter)
}

// openTextMap opens an omnibox showing all text matches
// with live preview - selection changes jump to that location, Esc restores original
// if onCancel is provided, it's called on Esc instead of just closing (for "back" navigation)
func openTextMap(app *glyph.App, ed *Editor, name, icon string, matcher TextMatcher, onCancel func()) {
	entries := ed.AllTextMatching(matcher)
	if len(entries) == 0 {
		return
	}

	// save original position for Esc restore
	originalPos := ed.Cursor()

	var items []OmniboxItem
	for _, entry := range entries {
		blockIdx, col := entry.BlockIdx, entry.Col
		items = append(items, OmniboxItem{
			Label:       entry.Text,
			Icon:        icon,
			Description: "",
			Action: func() {
				ed.moveCursor(Pos{Block: blockIdx, Col: col})
			},
		})
	}

	ed.omnibox.SetItems(items)
	ed.omnibox.SetPrompt(icon + " " + name + ": ")
	ed.omnibox.Show()
	app.SetView(buildView(ed))
	app.HideCursor()

	// preview helper - jump to the currently selected entry
	// note: Selected is index into Filtered, not original entries
	preview := func() {
		if ed.omnibox.Selected >= 0 && ed.omnibox.Selected < len(ed.omnibox.Filtered) {
			// find matching entry by label
			selectedLabel := ed.omnibox.Filtered[ed.omnibox.Selected].Label
			for _, entry := range entries {
				if entry.Text == selectedLabel {
					ed.moveCursor(Pos{Block: entry.BlockIdx, Col: entry.Col})
					return
				}
			}
		}
	}

	// initial preview
	preview()

	mapRouter := riffkey.NewRouter().Name("textmap").NoCounts()

	mapRouter.Handle("<Esc>", func(_ riffkey.Match) {
		ed.moveCursor(originalPos)
		ed.omnibox.Hide()
		app.Pop()
		if onCancel != nil {
			onCancel()
		}
	})

	mapRouter.Handle("<CR>", func(_ riffkey.Match) {
		ed.omnibox.Hide()
		app.Pop()
	})

	// navigation with preview
	mapRouter.Handle("<Up>", func(_ riffkey.Match) { ed.omnibox.SelectPrev(); preview() })
	mapRouter.Handle("<C-p>", func(_ riffkey.Match) { ed.omnibox.SelectPrev(); preview() })
	mapRouter.Handle("<C-k>", func(_ riffkey.Match) { ed.omnibox.SelectPrev(); preview() })
	mapRouter.Handle("<Down>", func(_ riffkey.Match) { ed.omnibox.SelectNext(); preview() })
	mapRouter.Handle("<C-j>", func(_ riffkey.Match) { ed.omnibox.SelectNext(); preview() })
	mapRouter.Handle("<C-n>", func(_ riffkey.Match) { ed.omnibox.SelectNext(); preview() })
	mapRouter.Handle("<BS>", func(_ riffkey.Match) { ed.omnibox.Backspace() })

	mapRouter.HandleUnmatched(func(k riffkey.Key) bool {
		if k.Rune != 0 && k.Mod == 0 {
			ed.omnibox.InsertChar(k.Rune)
			return true
		}
		return false
	})

	mapRouter.AddOnAfter(func() {
		ed.refresh()
	})
	app.Push(mapRouter)
}
