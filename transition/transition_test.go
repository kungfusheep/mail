package transition

import (
	"math"
	"testing"
	"time"

	. "github.com/kungfusheep/glyph"
)

func assertColorNear(t *testing.T, got, want Color) {
	t.Helper()
	if got.Mode != ColorRGB {
		t.Fatalf("color mode = %v, want ColorRGB", got.Mode)
	}
	if math.Abs(float64(got.R)-float64(want.R)) > 2 ||
		math.Abs(float64(got.G)-float64(want.G)) > 2 ||
		math.Abs(float64(got.B)-float64(want.B)) > 2 {
		t.Fatalf("color = rgb(%d,%d,%d), want near rgb(%d,%d,%d)",
			got.R, got.G, got.B, want.R, want.G, want.B)
	}
}

func lerpTestColor(a, b Color, t float64) Color {
	return Color{
		Mode: ColorRGB,
		R:    uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G:    uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B:    uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
	}
}

func TestTargetTransitionSeedsFakeCursorBeforeFade(t *testing.T) {
	bg := Hex(0x101010)
	textFG := Hex(0xa0b0c0)
	textBG := Hex(0x304050)
	cursor := Hex(0x5af78e)

	tr := New(time.Second, bg, Hex(0x3a3a3a))
	tr.active = true
	tr.startTime = time.Now().Add(-725 * time.Millisecond)
	tr.CursorOverlay(func() (int, int, Color, bool) {
		return 1, 0, cursor, true
	})

	buf := NewBuffer(3, 1)
	buf.Set(1, 0, Cell{
		Rune:  'p',
		Style: Style{FG: textFG, BG: textBG},
	})

	targetTransitionEffect{tr: tr}.Apply(buf, PostContext{Width: 3, Height: 1})

	got := buf.Get(1, 0)
	if got.Rune != 'p' {
		t.Fatalf("cursor cell rune = %q, want %q", got.Rune, 'p')
	}

	const phase1End = 0.45
	p2 := (0.725 - phase1End) / (1 - phase1End)
	eased := p2 * p2 * (3 - 2*p2)

	assertColorNear(t, got.Style.FG, lerpTestColor(bg, textFG, eased))
	assertColorNear(t, got.Style.BG, lerpTestColor(bg, cursor, eased))
}

func TestSourceTransitionCapturesFakeCursorWithoutMutatingLiveBuffer(t *testing.T) {
	bg := Hex(0x101010)
	textFG := Hex(0xa0b0c0)
	textBG := Hex(0x304050)
	cursor := Hex(0x5af78e)

	tr := New(time.Second, bg, Hex(0x3a3a3a))
	tr.CursorOverlay(func() (int, int, Color, bool) {
		return 1, 0, cursor, true
	})

	buf := NewBuffer(3, 1)
	buf.Set(1, 0, Cell{
		Rune:  'p',
		Style: Style{FG: textFG, BG: textBG},
	})

	sourceTransitionEffect{tr: tr}.Apply(buf, PostContext{Width: 3, Height: 1})

	live := buf.Get(1, 0)
	if live.Style.BG != textBG {
		t.Fatalf("live buffer cursor BG = %#v, want original %#v", live.Style.BG, textBG)
	}

	captured := tr.oldCells[1]
	if captured.Rune != 'p' {
		t.Fatalf("captured cursor cell rune = %q, want %q", captured.Rune, 'p')
	}
	if captured.Style.FG != textFG {
		t.Fatalf("captured cursor FG = %#v, want %#v", captured.Style.FG, textFG)
	}
	if captured.Style.BG != cursor {
		t.Fatalf("captured cursor BG = %#v, want cursor %#v", captured.Style.BG, cursor)
	}
}

func TestTransitionSetColorsUpdatesFadeBackground(t *testing.T) {
	darkBG := Hex(0x101010)
	lightBG := Hex(0xf6f6f6)
	textFG := Hex(0xa0b0c0)
	textBG := Hex(0x304050)

	tr := New(time.Second, darkBG, Hex(0x3a3a3a))
	tr.SetColors(lightBG, Hex(0xdddddd))
	tr.active = true
	tr.startTime = time.Now().Add(-225 * time.Millisecond)
	tr.oldW = 1
	tr.oldH = 1
	tr.oldCells = []Cell{{
		Rune:  'x',
		Style: Style{FG: textFG, BG: textBG},
	}}

	buf := NewBuffer(1, 1)
	targetTransitionEffect{tr: tr}.Apply(buf, PostContext{Width: 1, Height: 1})

	got := buf.Get(0, 0)
	const phase1End = 0.45
	p1 := 0.225 / phase1End
	eased := p1 * p1 * (3 - 2*p1)

	assertColorNear(t, got.Style.FG, lerpTestColor(textFG, lightBG, eased))
	assertColorNear(t, got.Style.BG, lerpTestColor(textBG, lightBG, eased))
}
