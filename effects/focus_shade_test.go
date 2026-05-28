package effects

import (
	"testing"

	. "github.com/kungfusheep/glyph"
)

func TestFocusShadeDimsTextWithoutChangingBackgrounds(t *testing.T) {
	buf := NewBuffer(8, 2)
	attachment := Hex(0x6f3d3d)
	neutral := Hex(0x302f2c)
	for x := range 8 {
		buf.Set(x, 0, Cell{Rune: 'X', Style: Style{FG: RGB(200, 100, 50), BG: neutral}})
		buf.Set(x, 1, Cell{Rune: ' ', Style: Style{BG: attachment}})
	}
	buf.Set(0, 1, Cell{Rune: '▀', Style: Style{FG: attachment, BG: attachment}})
	buf.Set(1, 1, Cell{Rune: 'P', Style: Style{FG: RGB(200, 100, 50), BG: attachment}})
	buf.Set(2, 1, Cell{Rune: 'D', Style: Style{FG: RGB(200, 100, 50), BG: attachment}})
	buf.Set(4, 0, Cell{Rune: '▀', Style: Style{FG: RGB(200, 100, 50), BG: neutral}})

	effect := NewFocusShade(&NodeRef{X: 0, Y: 0, W: 5, H: 2}).Strength(0.5)
	effect = effect.CompileEffect(testEffectCompiler{}).(FocusShade)
	effect.Apply(buf, PostContext{Width: 8, Height: 2})

	text := buf.Get(0, 0)
	if text.Style.FG != RGB(100, 50, 25) {
		t.Fatalf("text fg = %v, want shaded", text.Style.FG)
	}
	if text.Style.BG != neutral {
		t.Fatalf("neutral bg = %v, want unchanged", text.Style.BG)
	}

	border := buf.Get(0, 1)
	if border.Style.FG != attachment {
		t.Fatalf("attachment soft border fg = %v, want unchanged", border.Style.FG)
	}
	if border.Style.BG != attachment {
		t.Fatalf("soft border bg = %v, want unchanged", border.Style.BG)
	}

	neutralBorder := buf.Get(4, 0)
	if neutralBorder.Style.FG != RGB(200, 100, 50) {
		t.Fatalf("neutral soft border fg = %v, want unchanged", neutralBorder.Style.FG)
	}
	if neutralBorder.Style.BG != neutral {
		t.Fatalf("neutral soft border bg = %v, want unchanged", neutralBorder.Style.BG)
	}

	padded := buf.Get(3, 1)
	if padded.Style.BG != attachment {
		t.Fatalf("padding bg = %v, want unchanged", padded.Style.BG)
	}

	outside := buf.Get(7, 1)
	if outside.Style.BG != attachment {
		t.Fatalf("outside bg = %v, want unchanged", outside.Style.BG)
	}
}

func TestFocusShadeDoesNotShadeTintedThemeBackgrounds(t *testing.T) {
	for name, bg := range map[string]Color{
		"mfd":         Hex(0x151413),
		"gbl-light":   Hex(0x02b582),
		"gbl-dark":    Hex(0x001b1a),
		"lumon":       Hex(0xf7f2dc),
		"nerv":        Hex(0x100616),
		"blackout":    Hex(0x000000),
		"flir-fusion": Hex(0x2d1b69),
	} {
		t.Run(name, func(t *testing.T) {
			fg := Hex(0xd8d4ce)
			buf := NewBuffer(2, 1)
			buf.Set(0, 0, Cell{Rune: ' ', Style: Style{BG: bg}})
			buf.Set(1, 0, Cell{Rune: 'X', Style: Style{FG: fg, BG: bg}})

			effect := NewFocusShade(&NodeRef{X: 0, Y: 0, W: 2, H: 1}).Strength(0.5)
			effect = effect.CompileEffect(testEffectCompiler{}).(FocusShade)
			effect.Apply(buf, PostContext{Width: 2, Height: 1})

			if got := buf.Get(0, 0).Style.BG; got != bg {
				t.Fatalf("blank bg = %v, want unchanged %v", got, bg)
			}
			if got := buf.Get(1, 0).Style.BG; got != bg {
				t.Fatalf("text bg = %v, want unchanged %v", got, bg)
			}
			if got := buf.Get(1, 0).Style.FG; got == fg {
				t.Fatalf("text fg = %v, want shaded", got)
			}
		})
	}
}

func TestFocusShadeDodgesRefs(t *testing.T) {
	buf := NewBuffer(4, 1)
	for x := range 4 {
		buf.Set(x, 0, Cell{Rune: 'X', Style: Style{FG: RGB(200, 100, 50)}})
	}

	dodge := NodeRef{X: 1, Y: 0, W: 2, H: 1}
	effect := NewFocusShade(&NodeRef{X: 0, Y: 0, W: 4, H: 1}).Strength(0.5).Dodge(&dodge)
	effect = effect.CompileEffect(testEffectCompiler{}).(FocusShade)
	effect.Apply(buf, PostContext{Width: 4, Height: 1})

	if got := buf.Get(0, 0).Style.FG; got != RGB(100, 50, 25) {
		t.Fatalf("outside dodge fg = %v, want shaded", got)
	}
	if got := buf.Get(1, 0).Style.FG; got != RGB(200, 100, 50) {
		t.Fatalf("inside dodge fg = %v, want unchanged", got)
	}
	if got := buf.Get(3, 0).Style.FG; got != RGB(100, 50, 25) {
		t.Fatalf("outside dodge fg = %v, want shaded", got)
	}
}

func TestFocusShadeDodgeFollowsRefOpacity(t *testing.T) {
	buf := NewBuffer(4, 1)
	for x := range 4 {
		buf.Set(x, 0, Cell{Rune: 'X', Style: Style{FG: RGB(200, 100, 50)}})
	}

	dodge := NodeRef{X: 1, Y: 0, W: 2, H: 1, Opacity: 0.5}
	effect := NewFocusShade(&NodeRef{X: 0, Y: 0, W: 4, H: 1}).Strength(0.5).Dodge(&dodge)
	effect = effect.CompileEffect(testEffectCompiler{}).(FocusShade)
	effect.Apply(buf, PostContext{Width: 4, Height: 1})

	if got := buf.Get(0, 0).Style.FG; got != RGB(100, 50, 25) {
		t.Fatalf("outside dodge fg = %v, want full shade", got)
	}
	if got := buf.Get(1, 0).Style.FG; got != RGB(150, 75, 38) {
		t.Fatalf("inside fading dodge fg = %v, want half-strength shade", got)
	}
}

type testEffectCompiler struct{}

func (testEffectCompiler) Float64(v any) EffectFloat64 {
	switch val := v.(type) {
	case EffectFloat64:
		return val
	case float64:
		return StaticEffectFloat64(val)
	case float32:
		return StaticEffectFloat64(float64(val))
	case int:
		return StaticEffectFloat64(float64(val))
	default:
		return EffectFloat64{}
	}
}
