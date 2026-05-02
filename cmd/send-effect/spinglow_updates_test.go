// Does SESpinGlow actually animate — do successive Apply calls produce
// different colours as ctx.Time advances? Runs the effect over a small
// buffer with a fixed focus rect, captures a sample cell at two times
// and asserts the palette has rotated between them.
//
// Verified in isolation so we can be certain the harness's visual
// problem isn't "the effect is static" — it's somewhere else.
package main

import (
	"testing"
	"time"

	. "github.com/kungfusheep/glyph"
)

func TestSESpinGlowUpdatesOverTime(t *testing.T) {
	ref := NodeRef{X: 10, Y: 5, W: 20, H: 5}
	eff := SESpinGlow(&ref,
		RGB(255, 0, 0),
		RGB(0, 255, 0),
		RGB(0, 0, 255),
	).Speed(1.0).Radius(10).Strength(0.9).Falloff(0)

	apply := func(at time.Duration) Cell {
		buf := NewBuffer(50, 20)
		// The effect uses lerpIfRGB which bails on ColorDefault. Either
		// the cell has an RGB FG already, or ctx.DefaultFG is RGB so
		// resolveFG can substitute. Set DefaultFG so empty cells become
		// paintable — mirrors what the real app gets from OSC detection.
		ctx := PostContext{
			Width:     50,
			Height:    20,
			Time:      at,
			DefaultFG: RGB(176, 176, 176),
			DefaultBG: RGB(26, 26, 26),
		}
		eff.Apply(buf, ctx)
		// sample a cell just outside the focus rect, top side
		return buf.Get(20, 4)
	}

	c0 := apply(0 * time.Millisecond)
	c1 := apply(500 * time.Millisecond) // speed=1 → half a rotation
	c2 := apply(1000 * time.Millisecond)

	if c0.Style.FG.Mode != ColorRGB {
		t.Fatalf("expected cell FG to be painted RGB, got Mode=%v — effect didn't tint the sample cell at all", c0.Style.FG.Mode)
	}
	if c0.Style.FG == c1.Style.FG {
		t.Errorf("FG unchanged between t=0 and t=0.5s (c0=%v c1=%v) — effect is NOT advancing with time, rotation is frozen", c0.Style.FG, c1.Style.FG)
	}
	if c1.Style.FG == c2.Style.FG {
		t.Errorf("FG unchanged between t=0.5s and t=1.0s — rotation stalled")
	}
}

// Independent of time: does zero strength actually suppress the effect?
// If strength=0 the cell should remain whatever it was before apply.
func TestSESpinGlowZeroStrengthNoPaint(t *testing.T) {
	ref := NodeRef{X: 10, Y: 5, W: 20, H: 5}
	eff := SESpinGlow(&ref, RGB(255, 0, 0)).Speed(1.0).Radius(10).Strength(0.0)

	buf := NewBuffer(50, 20)
	orig := buf.Get(20, 4)
	eff.Apply(buf, PostContext{Width: 50, Height: 20, Time: 500 * time.Millisecond})
	after := buf.Get(20, 4)

	if after.Style.FG != orig.Style.FG {
		t.Errorf("FG changed with strength=0: before=%v after=%v — unexpected; strength gate isn't fully suppressing paint", orig.Style.FG, after.Style.FG)
	}
}
