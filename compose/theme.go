package compose

import "github.com/kungfusheep/glyph"

// CursorColors defines cursor colors for different modes
type CursorColors struct {
	Normal glyph.Color
	Insert glyph.Color
	Visual glyph.Color
}

// Theme defines the visual appearance of the editor
type Theme struct {
	Name       string
	Background glyph.Color
	Text       glyph.Color
	Cursor     CursorColors

	// inline text styles
	Bold          glyph.Style
	Italic        glyph.Style
	Underline     glyph.Style
	Strikethrough glyph.Style
	Code          glyph.Style

	// accent color (typewriter red)
	Accent glyph.Style

	// block styles
	Heading1   glyph.Style
	Heading2   glyph.Style
	Heading3   glyph.Style
	Heading4   glyph.Style
	Heading5   glyph.Style
	Heading6   glyph.Style
	Blockquote glyph.Style
	CodeBlock  glyph.Style
	ListBullet glyph.Style
	Callout    glyph.Style
	Divider    glyph.Style

	// dialogue
	DialogueCharacter     glyph.Style
	DialogueText          glyph.Style
	DialogueParenthetical glyph.Style

	// front matter
	FrontMatterKey   glyph.Style
	FrontMatterValue glyph.Style

	// focus mode - dimmed style for unfocused content
	Dimmed glyph.Style
}

// DefaultTheme returns a clean document-focused theme
func DefaultTheme() Theme {
	bg := glyph.Hex(0xfaf8f5) // warm off-white
	fg := glyph.Hex(0x2d2d2d) // soft black

	baseStyle := glyph.Style{FG: fg, BG: bg}

	// subtle background for code
	codeBg := glyph.Hex(0xf0ede8)

	// typewriter red - warm, slightly muted
	typewriterRed := glyph.Hex(0xc41e3a) // classic typewriter ribbon red

	return Theme{
		Name:       "default",
		Background: bg,
		Text:       fg,
		Cursor: CursorColors{
			Normal: glyph.Hex(0x526eff), // blue - visible against light bg
			Insert: glyph.Hex(0x50a14f), // green - creative mode
			Visual: glyph.Hex(0xc18401), // gold - selection mode
		},

		// inline styles - just formatting, no colors
		Bold:          baseStyle.Bold(),
		Italic:        baseStyle.Italic(),
		Underline:     baseStyle.Underline(),
		Strikethrough: baseStyle.Strikethrough(),
		Code:          glyph.Style{FG: fg, BG: codeBg},

		// accent - typewriter red
		Accent: glyph.Style{FG: typewriterRed, BG: bg},

		// headings - just bold, same color as text
		Heading1: baseStyle.Bold(),
		Heading2: baseStyle.Bold(),
		Heading3: baseStyle.Bold(),
		Heading4: baseStyle.Bold(),
		Heading5: baseStyle.Bold(),
		Heading6: baseStyle.Bold(),

		// blocks - minimal styling
		Blockquote: baseStyle.Italic(),
		CodeBlock:  glyph.Style{FG: fg, BG: codeBg},
		ListBullet: baseStyle,
		Callout:    baseStyle,
		Divider:    glyph.Style{FG: glyph.Hex(0xcccccc), BG: bg},

		// dialogue - character names bold, dialogue normal, parentheticals italic
		DialogueCharacter:     baseStyle.Bold(),
		DialogueText:          baseStyle,
		DialogueParenthetical: baseStyle.Italic(),

		// front matter - keys in red, values normal
		FrontMatterKey:   glyph.Style{FG: typewriterRed, BG: bg},
		FrontMatterValue: baseStyle,

		// focus mode - dimmed text
		Dimmed: glyph.Style{FG: glyph.Hex(0xb0b0b0), BG: bg}, // muted gray
	}
}

// DarkTheme returns a dark document-focused theme
func DarkTheme() Theme {
	bg := glyph.Hex(0x1a1a1a) // soft black (iA Writer style)
	fg := glyph.Hex(0xe8e8e8) // warm white - balanced contrast

	baseStyle := glyph.Style{FG: fg, BG: bg}

	// subtle background for code
	codeBg := glyph.Hex(0x282828)

	// warm red accent
	accentRed := glyph.Hex(0xff5555)

	return Theme{
		Name:       "dark",
		Background: bg,
		Text:       fg,
		Cursor: CursorColors{
			Normal: glyph.Hex(0xe8e8e8), // matches text
			Insert: glyph.Hex(0x5af78e), // green
			Visual: glyph.Hex(0xf4f99d), // yellow
		},

		// inline styles - just formatting, no colors
		Bold:          baseStyle.Bold(),
		Italic:        baseStyle.Italic(),
		Underline:     baseStyle.Underline(),
		Strikethrough: baseStyle.Strikethrough(),
		Code:          glyph.Style{FG: fg, BG: codeBg},

		// accent - bright red
		Accent: glyph.Style{FG: accentRed, BG: bg},

		// headings - just bold
		Heading1: baseStyle.Bold(),
		Heading2: baseStyle.Bold(),
		Heading3: baseStyle.Bold(),
		Heading4: baseStyle.Bold(),
		Heading5: baseStyle.Bold(),
		Heading6: baseStyle.Bold(),

		// blocks - minimal styling
		Blockquote: baseStyle.Italic(),
		CodeBlock:  glyph.Style{FG: fg, BG: codeBg},
		ListBullet: baseStyle,
		Callout:    baseStyle,
		Divider:    glyph.Style{FG: glyph.Hex(0x3a3a3a), BG: bg},

		// dialogue - character names bold, dialogue normal, parentheticals italic
		DialogueCharacter:     baseStyle.Bold(),
		DialogueText:          baseStyle,
		DialogueParenthetical: baseStyle.Italic(),

		// front matter - keys in red, values normal
		FrontMatterKey:   glyph.Style{FG: accentRed, BG: bg},
		FrontMatterValue: baseStyle,

		// focus mode - dimmed text
		Dimmed: glyph.Style{FG: glyph.Hex(0x5a5a5a), BG: bg},
	}
}

// MonographTheme returns a technical documentation theme matching CSS dark mode
func MonographTheme() Theme {
	// CSS dark mode values
	bg := glyph.Hex(0x111111)          // --bg: #111
	fg := glyph.Hex(0xe0e0e0)          // --fg: #e0e0e0
	muted := glyph.Hex(0xaaaaaa)       // --fg-muted: #aaa
	shadeBg := glyph.Hex(0x1a1a1a)     // --shade-bg: #1a1a1a (H2 band, code bg)
	codeFg := glyph.Hex(0xcccccc)      // --code-fg: #ccc
	link := glyph.Hex(0x6699ff)        // --link: #6699ff (blockquote bar)
	borderLight := glyph.Hex(0x444444) // --border-light: #444

	baseStyle := glyph.Style{FG: fg, BG: bg}

	return Theme{
		Name:       "monograph",
		Background: bg,
		Text:       fg,
		Cursor: CursorColors{
			Normal: fg,
			Insert: glyph.Hex(0x5af78e),
			Visual: glyph.Hex(0xf4f99d),
		},

		// inline styles
		Bold:          baseStyle.Bold(),
		Italic:        baseStyle.Italic(),
		Underline:     baseStyle.Underline(),
		Strikethrough: baseStyle.Strikethrough(),
		Code:          glyph.Style{FG: codeFg, BG: shadeBg},

		// accent - use link color
		Accent: glyph.Style{FG: link, BG: bg},

		// headings - template handles the actual rendering
		Heading1: baseStyle.Bold(),
		Heading2: glyph.Style{FG: fg, BG: shadeBg},
		Heading3: baseStyle.Bold(),
		Heading4: baseStyle.Bold(),
		Heading5: baseStyle,
		Heading6: baseStyle,

		// blocks
		Blockquote: glyph.Style{FG: muted, BG: bg}.Italic(),
		CodeBlock:  glyph.Style{FG: codeFg, BG: shadeBg},
		ListBullet: glyph.Style{FG: muted, BG: bg},
		Callout:    baseStyle,
		Divider:    glyph.Style{FG: borderLight, BG: bg},

		// dialogue
		DialogueCharacter:     baseStyle.Bold(),
		DialogueText:          baseStyle,
		DialogueParenthetical: baseStyle.Italic(),

		// front matter
		FrontMatterKey:   glyph.Style{FG: link, BG: bg},
		FrontMatterValue: baseStyle,

		// focus mode
		Dimmed: glyph.Style{FG: glyph.Hex(0x555555), BG: bg},
	}
}

// BaseStyle returns the base text style for the theme
func (t Theme) BaseStyle() glyph.Style {
	return glyph.Style{FG: t.Text, BG: t.Background}
}

// StyleForRun returns the appropriate glyph.Style for an inline style
func (t Theme) StyleForRun(s InlineStyle) glyph.Style {
	style := t.BaseStyle()

	if s.Has(StyleBold) {
		style = style.Bold()
	}
	if s.Has(StyleItalic) {
		style = style.Italic()
	}
	if s.Has(StyleUnderline) {
		style = style.Underline()
	}
	if s.Has(StyleStrikethrough) {
		style = style.Strikethrough()
	}
	if s.Has(StyleCode) {
		style.FG = t.Code.FG
		style.BG = t.Code.BG
	}

	return style
}

// StyleForBlock returns the appropriate glyph.Style for a block type
func (t Theme) StyleForBlock(blockType BlockType) glyph.Style {
	switch blockType {
	case BlockH1:
		return t.Heading1
	case BlockH2:
		return t.Heading2
	case BlockH3:
		return t.Heading3
	case BlockH4:
		return t.Heading4
	case BlockH5:
		return t.Heading5
	case BlockH6:
		return t.Heading6
	case BlockQuote:
		return t.Blockquote
	case BlockCodeLine:
		return t.CodeBlock
	case BlockCallout:
		return t.Callout
	case BlockDivider:
		return t.Divider
	case BlockDialogue:
		return t.DialogueText
	default:
		return t.BaseStyle()
	}
}
