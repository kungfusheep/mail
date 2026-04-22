package main

import (
	"math"
	"time"

	. "github.com/kungfusheep/glyph"
)

// viewTransition coordinates a two-phase "settle and rise" between views.
// Phase 1 shows the OLD view's silhouette dimming out over an empty field.
// Phase 2 cascade-reveals the NEW view from the centre outward.
//
// Usage:
//
//	// one shared state
//	tr := NewViewTransition(400*time.Millisecond, bg, peak)
//
//	// on the OLD view — continuously records its silhouette when inactive,
//	// and renders the phase-1 overlay when the transition is running.
//	// attach to main's VBox: ScreenEffect(tr.SourceEffect())
//
//	// on the NEW view — renders the phase-2 overlay while running.
//	// attach to compose's VBox: ScreenEffect(tr.TargetEffect())
//
//	// trigger — call just before app.PushView(...)
//	tr.Start()
//	app.PushView("compose")
type viewTransition struct {
	duration time.Duration
	bg, peak Color

	active    bool
	startTime time.Time
	oldCells  []Cell // full capture of the source view's last frame
	oldW      int
	oldH      int
}

func NewViewTransition(duration time.Duration, bg, peak Color) *viewTransition {
	return &viewTransition{
		duration: duration,
		bg:       bg,
		peak:     peak,
	}
}

// Start begins the transition. Call immediately before switching views.
func (t *viewTransition) Start() {
	t.active = true
	t.startTime = time.Now()
}

// progress returns 0..1 over the transition's duration. Returns 1.0 (and
// sets active=false) once complete.
func (t *viewTransition) progress() float64 {
	if !t.active {
		return 0
	}
	p := float64(time.Since(t.startTime)) / float64(t.duration)
	if p >= 1.0 {
		t.active = false
		return 1.0
	}
	return p
}

// ------- effects --------

// SourceEffect sits on the OLD view. While the transition is inactive it
// records the current silhouette. While active (phase 1) it overlays the
// old silhouette fading out, over a cleared background.
func (t *viewTransition) SourceEffect() Effect {
	return sourceTransitionEffect{tr: t}
}

// TargetEffect sits on the NEW view. When active, it uses the transition's
// progress to render phases. Phase 1: clears new view entirely, shows old
// silhouette fading. Phase 2: cascade-reveals the new view from centre out.
func (t *viewTransition) TargetEffect() Effect {
	return targetTransitionEffect{tr: t}
}

// isEmoji returns true for runes that Ghostty (and most modern terminals)
// render as wide glyphs through a separate rasterisation path. Redrawing
// these cells per-frame — which the colour lerp below naturally does — causes
// visible flicker while the transition is in progress. Substituting them
// with a space for the duration of the transition sidesteps the problem
// without touching the app's own buffer state.
func isEmoji(r rune) bool {
	return r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || (r >= 0x2300 && r <= 0x23FF) || (r >= 0x2B00 && r <= 0x2BFF)
}

type sourceTransitionEffect struct{ tr *viewTransition }

func (s sourceTransitionEffect) Apply(buf *Buffer, ctx PostContext) {
	if s.tr.active {
		return // target view handles rendering during the transition
	}
	// snapshot the full frame so we can render and fade the real content
	w, h := ctx.Width, ctx.Height
	if len(s.tr.oldCells) != w*h {
		s.tr.oldCells = make([]Cell, w*h)
	}
	s.tr.oldW, s.tr.oldH = w, h
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s.tr.oldCells[y*w+x] = buf.Get(x, y)
		}
	}
}

type targetTransitionEffect struct{ tr *viewTransition }

func (s targetTransitionEffect) Apply(buf *Buffer, ctx PostContext) {
	p := s.tr.progress()
	if !s.tr.active && p == 0 {
		return
	}

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	maxR := math.Sqrt(cx*cx + cy*cy*4)

	// lerp colour a→b by t (0..1). Treats ColorDefault as bg.
	lerp := func(a, b Color, t float64) Color {
		if a.Mode == ColorDefault {
			a = s.tr.bg
		}
		if b.Mode == ColorDefault {
			b = s.tr.bg
		}
		return Color{
			Mode: ColorRGB,
			R:    uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
			G:    uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
			B:    uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		}
	}

	const phase1End = 0.45

	if p < phase1End {
		// PHASE 1 — the captured mailbox frame fades to background.
		// Real content → bg, same shapes, colours dissolving. Elegant, not garish.
		p1 := p / phase1End
		eased := p1 * p1 * (3 - 2*p1)
		fadeAmt := eased // 0 = full content, 1 = all bg

		if s.tr.oldW != w || s.tr.oldH != h {
			return // dimensions mismatch — skip rather than render garbage
		}

		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				cell := s.tr.oldCells[y*w+x]
				if isEmoji(cell.Rune) {
					cell.Rune = ' '
				}
				cell.Style.FG = lerp(cell.Style.FG, s.tr.bg, fadeAmt)
				cell.Style.BG = lerp(cell.Style.BG, s.tr.bg, fadeAmt)
				buf.Set(x, y, cell)
			}
		}
		return
	}

	// PHASE 2 — compose content fades IN uniformly from background.
	p2 := (p - phase1End) / (1 - phase1End)
	eased := p2 * p2 * (3 - 2*p2)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell := buf.Get(x, y)
			if isEmoji(cell.Rune) {
				cell.Rune = ' '
			}
			cell.Style.FG = lerp(s.tr.bg, cell.Style.FG, eased)
			cell.Style.BG = lerp(s.tr.bg, cell.Style.BG, eased)
			buf.Set(x, y, cell)
		}
	}
	_ = maxR
	_ = cx
	_ = cy
}
