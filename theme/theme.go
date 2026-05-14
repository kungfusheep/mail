package theme

import (
	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/compose"
)

type Theme struct {
	BG       glyph.Color
	Bright   glyph.Color
	FG       glyph.Color
	Subtle   glyph.Color
	Dim      glyph.Color
	Muted    glyph.Color
	Accent   glyph.Color
	Info     glyph.Color
	Success  glyph.Color
	Warning  glyph.Color
	Error    glyph.Color
	SelBG    glyph.Color
	GroupBG  glyph.Color
	ThreadBG glyph.Color
}

var dark = Theme{
	BG:       glyph.Hex(0x1c1c1c),
	Bright:   glyph.Hex(0xe8e6e3),
	FG:       glyph.Hex(0xb8b5b0),
	Subtle:   glyph.Hex(0x8b8780),
	Dim:      glyph.Hex(0x5f5b55),
	Muted:    glyph.Hex(0x3f3c38),
	Accent:   glyph.Hex(0xe8e6e3),
	Info:     glyph.Hex(0x7aa2f7),
	Success:  glyph.Hex(0x9ece6a),
	Warning:  glyph.Hex(0xe0af68),
	Error:    glyph.Hex(0xf7768e),
	SelBG:    glyph.Hex(0x302f2c),
	GroupBG:  glyph.Hex(0x252421),
	ThreadBG: glyph.Hex(0x191918),
}

var light = Theme{
	BG:       glyph.Hex(0xf6f6f6),
	Bright:   glyph.Hex(0x111111),
	FG:       glyph.Hex(0x333333),
	Subtle:   glyph.Hex(0x777777),
	Dim:      glyph.Hex(0xaaaaaa),
	Muted:    glyph.Hex(0xcccccc),
	Accent:   glyph.Hex(0xe60012),
	Info:     glyph.Hex(0x2563eb),
	Success:  glyph.Hex(0x16803c),
	Warning:  glyph.Hex(0xb45309),
	Error:    glyph.Hex(0xc2410c),
	SelBG:    glyph.Hex(0xe8e8e8),
	GroupBG:  glyph.Hex(0xeeeeee),
	ThreadBG: glyph.Hex(0xf1f3f1),
}

func Dark() Theme {
	return dark
}

func Light() Theme {
	return light
}

func ComposeTheme(t Theme) compose.Theme {
	return compose.Theme{
		Name:          "mail",
		Text:          t.FG,
		Background:    t.BG,
		Bold:          glyph.Style{Attr: glyph.AttrBold},
		Italic:        glyph.Style{Attr: glyph.AttrItalic},
		Underline:     glyph.Style{Attr: glyph.AttrUnderline},
		Strikethrough: glyph.Style{Attr: glyph.AttrStrikethrough},
		Code:          glyph.Style{FG: t.FG},
		Accent:        glyph.Style{FG: t.Accent},
		Heading1:      glyph.Style{FG: t.Bright, Attr: glyph.AttrBold},
		Heading2:      glyph.Style{FG: t.Bright, Attr: glyph.AttrBold},
		Heading3:      glyph.Style{FG: t.Bright, Attr: glyph.AttrBold},
		Heading4:      glyph.Style{FG: t.FG, Attr: glyph.AttrBold},
		Heading5:      glyph.Style{FG: t.FG, Attr: glyph.AttrBold},
		Heading6:      glyph.Style{FG: t.FG, Attr: glyph.AttrBold},
		Blockquote:    glyph.Style{FG: t.Subtle, Attr: glyph.AttrItalic},
		CodeBlock:     glyph.Style{FG: t.FG},
		ListBullet:    glyph.Style{FG: t.Subtle},
		Callout:       glyph.Style{FG: t.Accent},
		Divider:       glyph.Style{FG: t.Muted, Attr: glyph.AttrDim},
		Cursor: compose.CursorColors{
			Normal: t.Bright,
			Insert: glyph.Hex(0x5af78e),
			Visual: glyph.Hex(0xf4f99d),
		},
		DialogueCharacter:     glyph.Style{FG: t.Bright, Attr: glyph.AttrBold},
		DialogueText:          glyph.Style{FG: t.FG},
		DialogueParenthetical: glyph.Style{FG: t.Subtle, Attr: glyph.AttrItalic},
		FrontMatterKey:        glyph.Style{FG: t.Subtle, Attr: glyph.AttrBold},
		FrontMatterValue:      glyph.Style{FG: t.FG},
		Dimmed:                glyph.Style{FG: t.Dim, Attr: glyph.AttrDim},
	}
}
