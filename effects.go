package main

import (
	"math"
	"unicode"

	. "github.com/kungfusheep/glyph"
)

// intensityField is a function that returns 0..1 intensity for a cell at
// time t. All shimmer variations share the same Braille-rendering core
// but differ only in their intensity field.
type intensityField func(cx, cy, t float64) float64

// renderShimmer is the shared rendering pass. It walks every cell,
// samples the intensity field, and paints a Braille glint on empty cells
// or a subtle BG tint on content cells — scaling by the brightness delta
// between bgColor and peakColor. opacity (0..1) scales the whole effect.
func renderShimmer(buf *Buffer, ctx PostContext, bgColor, peakColor Color, opacity float64, field intensityField) {
	if opacity <= 0 {
		return
	}
	w, h := ctx.Width, ctx.Height
	t := ctx.Time.Seconds()

	dR := int(peakColor.R) - int(bgColor.R)
	dG := int(peakColor.G) - int(bgColor.G)
	dB := int(peakColor.B) - int(bgColor.B)

	add := func(c Color, amt float64) Color {
		if c.Mode == ColorDefault {
			c = bgColor
		}
		return Color{
			Mode: ColorRGB,
			R:    clampU8(int(c.R) + int(float64(dR)*amt)),
			G:    clampU8(int(c.G) + int(float64(dG)*amt)),
			B:    clampU8(int(c.B) + int(float64(dB)*amt)),
		}
	}

	const fullBlock = rune(0x28FF)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell := buf.Get(x, y)
			i := field(float64(x)+0.5, float64(y)+0.5, t)
			i = i * i * opacity

			if isPaintable(cell.Rune) {
				bg := cell.Style.BG
				if bg.Mode == ColorDefault {
					bg = bgColor
				}
				cell.Rune = fullBlock
				cell.Style.FG = add(bg, i)
				buf.Set(x, y, cell)
			} else {
				cell.Style.BG = add(cell.Style.BG, i*0.4)
				buf.Set(x, y, cell)
			}
		}
	}
}

// ============================================================================
// CATALOGUE OF SHIMMER VARIATIONS
//
// Each entry below is a standalone Effect backed by the shared renderer.
// The only thing that differs is the intensityField — the math that decides
// how bright each cell should be at each moment.
//
// All of these inherit the same subtle colour treatment (delta from the
// cell's actual BG, opacity control, Braille glint + content tint),
// so the VISUAL aesthetic is consistent across the catalogue. What
// changes is the character of the motion and the feeling it evokes.
// ============================================================================

// ----------------------------------------------------------------------------
// 1. DRIFTING LIGHTS — ambient, idle
//
// Several invisible light sources orbit on incommensurate Lissajous paths,
// each casting a soft Gaussian bloom. Because the oscillation frequencies
// are irrational relative to each other, the pattern never repeats visibly.
//
// FEELS LIKE: light dappling through trees as seen from a moving car.
// USE FOR:    ambient "alive but idle" state, background processing, waiting.
// ----------------------------------------------------------------------------

type shimmerDrifting struct {
	bgColor, peakColor Color
	opacityPtr         *float64
	lights             []lightSource
}

type lightSource struct {
	ax, bx, wx, px float64 // x: amplitude, amplitude, freq, phase
	ay, by, wy, py float64
	radius         float64
	strength       float64
}

func ShimmerDrifting(bg, peak Color) shimmerDrifting {
	return shimmerDrifting{
		bgColor:   bg,
		peakColor: peak,
		lights: []lightSource{
			{ax: 60, bx: 50, wx: 0.31, px: 0.0, ay: 15, by: 12, wy: 0.47, py: 1.2, radius: 20, strength: 1.0},
			{ax: 100, bx: 70, wx: 0.23, px: 2.1, ay: 20, by: 15, wy: 0.29, py: 0.3, radius: 26, strength: 0.8},
			{ax: 140, bx: 90, wx: 0.17, px: 4.5, ay: 12, by: 10, wy: 0.41, py: 3.7, radius: 16, strength: 1.2},
			{ax: 40, bx: 35, wx: 0.43, px: 5.8, ay: 18, by: 14, wy: 0.19, py: 2.4, radius: 32, strength: 0.6},
		},
	}
}

func (s shimmerDrifting) Opacity(p *float64) shimmerDrifting { s.opacityPtr = p; return s }

func (s shimmerDrifting) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	field := func(cx, cy, t float64) float64 {
		total := 0.0
		cyAdj := cy * 2.0 // cells are ~2x tall as wide
		for _, l := range s.lights {
			px := l.ax + l.bx*math.Sin(l.wx*t+l.px)
			py := l.ay + l.by*math.Sin(l.wy*t+l.py)
			dx := cx - px
			dy := cyAdj - py*2.0
			total += l.strength * math.Exp(-(dx*dx+dy*dy)/(2*l.radius*l.radius))
		}
		if total > 1 {
			total = 1
		}
		return total
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 2. TUNNEL — directional transition
//
// Concentric rings emanate from screen centre, moving outward (forward) or
// inward (backward). Ring spacing compresses toward centre via a sqrt
// transform to evoke perspective depth. A sharpness curve keeps the rings
// crisp without being hard-edged.
//
// FEELS LIKE: flying through a tunnel, or falling back out of one.
// USE FOR:    view transitions, entering a thread (forward), returning to list (backward).
// ----------------------------------------------------------------------------

type shimmerTunnel struct {
	bgColor, peakColor Color
	opacityPtr         *float64
	speedPtr           *float64 // cell-units per second, +ve = forward, -ve = backward
	wavelength         float64
	sharpness          float64
}

func ShimmerTunnel(bg, peak Color) shimmerTunnel {
	return shimmerTunnel{
		bgColor:    bg,
		peakColor:  peak,
		wavelength: 6.0, // distance between ring crests (in cell-units)
		sharpness:  4.0, // higher = narrower bright rings
	}
}

func (s shimmerTunnel) Opacity(p *float64) shimmerTunnel { s.opacityPtr = p; return s }
func (s shimmerTunnel) Speed(p *float64) shimmerTunnel   { s.speedPtr = p; return s }

func (s shimmerTunnel) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	speed := 4.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	wl := s.wavelength
	sharp := s.sharpness

	field := func(x, y, t float64) float64 {
		// distance from centre (aspect corrected)
		dx := x - cx
		dy := (y - cy) * 2.0
		dist := math.Sqrt(dx*dx + dy*dy)
		// sqrt compresses rings toward centre → sense of depth
		depth := math.Sqrt(dist)
		// phase moves with time; negative speed flips direction
		phase := math.Mod(depth-speed*t, wl)
		if phase < 0 {
			phase += wl
		}
		// pulse function: 1 at phase=0, decays to 0 over the wavelength
		n := 1 - phase/wl
		return math.Pow(n, sharp)
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 3. SWEEP — active processing
//
// A single diagonal wave front sweeps across the screen at a controllable
// speed. Unlike tunnel/drifting which are ambient, this has a strong
// directional feel — you can track the wave with your eye.
//
// FEELS LIKE: a scanner or progress indicator moving through something.
// USE FOR:    definite "work happening now" signal — sync, save, fetch.
// ----------------------------------------------------------------------------

type shimmerSweep struct {
	bgColor, peakColor Color
	opacityPtr         *float64
	speedPtr           *float64
	dirX, dirY         float64 // normalised direction vector
	wavelength         float64
	sharpness          float64
}

func ShimmerSweep(bg, peak Color) shimmerSweep {
	return shimmerSweep{
		bgColor:    bg,
		peakColor:  peak,
		dirX:       1.0,
		dirY:       0.5,
		wavelength: 40.0,
		sharpness:  3.0,
	}
}

func (s shimmerSweep) Opacity(p *float64) shimmerSweep { s.opacityPtr = p; return s }
func (s shimmerSweep) Speed(p *float64) shimmerSweep   { s.speedPtr = p; return s }

func (s shimmerSweep) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	speed := 15.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	field := func(x, y, t float64) float64 {
		// project cell onto direction axis (aspect corrected)
		proj := x*s.dirX + y*2.0*s.dirY
		phase := math.Mod(proj-speed*t, s.wavelength)
		if phase < 0 {
			phase += s.wavelength
		}
		n := 1 - phase/s.wavelength
		return math.Pow(n, s.sharpness)
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 4. PULSE — one-shot signal
//
// A single ring radiating outward from a configurable origin. Intended as
// a one-shot response to an event — trigger, let it run one cycle, done.
// Because the intensity drops to zero once the wave leaves the screen,
// it self-terminates visually.
//
// FEELS LIKE: a notification radiating from a point, a sonar ping.
// USE FOR:    punctuating events — message received, file saved, error occurred.
//             Drive via app-managed time elapsed since trigger.
// ----------------------------------------------------------------------------

type shimmerPulse struct {
	bgColor, peakColor Color
	opacityPtr         *float64
	originX, originY   float64 // in cell-units; if negative, use screen centre
	startedAtPtr       *float64 // seconds since app start when triggered; 0 = inactive
	duration           float64
}

func ShimmerPulse(bg, peak Color) shimmerPulse {
	return shimmerPulse{
		bgColor:   bg,
		peakColor: peak,
		originX:   -1,
		originY:   -1,
		duration:  1.2, // seconds for one complete ripple
	}
}

func (s shimmerPulse) Opacity(p *float64) shimmerPulse     { s.opacityPtr = p; return s }
func (s shimmerPulse) Trigger(p *float64) shimmerPulse     { s.startedAtPtr = p; return s }
func (s shimmerPulse) Origin(x, y float64) shimmerPulse    { s.originX = x; s.originY = y; return s }

func (s shimmerPulse) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	if s.startedAtPtr == nil || *s.startedAtPtr == 0 {
		return
	}
	elapsed := ctx.Time.Seconds() - *s.startedAtPtr
	if elapsed < 0 || elapsed > s.duration {
		return
	}
	progress := elapsed / s.duration // 0..1

	w, h := ctx.Width, ctx.Height
	ox := s.originX
	oy := s.originY
	if ox < 0 {
		ox = float64(w) / 2
	}
	if oy < 0 {
		oy = float64(h) / 2
	}

	// ring radius grows over time; amplitude decays as it expands
	radius := progress * math.Sqrt(float64(w*w)+float64(h*h)) // reach screen corner
	amplitude := 1 - progress                                 // fade as it expands
	thickness := 3.0                                          // ring band width

	field := func(x, y, _ float64) float64 {
		dx := x - ox
		dy := (y - oy) * 2.0
		dist := math.Sqrt(dx*dx + dy*dy)
		// peak intensity at dist == radius; Gaussian falloff either side
		d := dist - radius
		return amplitude * math.Exp(-(d*d)/(2*thickness*thickness))
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 5. NOISE — textured static
//
// Pure multi-octave noise, drifting very slowly. No discernible structure;
// reads as "surface texture" more than "motion." At low opacity it adds
// a faint organic imperfection to the screen — like paper grain.
//
// FEELS LIKE: film grain, paper fibre, ambient static.
// USE FOR:    idle aesthetic layer, "lived-in" feel, subtle depth on flat panels.
// ----------------------------------------------------------------------------

type shimmerNoise struct {
	bgColor, peakColor Color
	opacityPtr         *float64
}

func ShimmerNoise(bg, peak Color) shimmerNoise {
	return shimmerNoise{bgColor: bg, peakColor: peak}
}

func (s shimmerNoise) Opacity(p *float64) shimmerNoise { s.opacityPtr = p; return s }

func (s shimmerNoise) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	field := func(x, y, t float64) float64 {
		// three layers of sin at different scales and drift directions
		a := math.Sin(x*0.15+y*0.09+t*0.3) * 0.4
		b := math.Sin(x*-0.27+y*0.21+t*0.2+1.7) * 0.3
		c := math.Sin(x*0.41+y*-0.33+t*0.5+3.4) * 0.3
		n := (a + b + c + 1.0) / 2.0
		if n < 0 {
			n = 0
		}
		if n > 1 {
			n = 1
		}
		return n
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 6. RAIN — descending streaks
//
// Multiple vertical streaks falling at different speeds. Each column picks
// up or loses streaks based on noise, so the pattern has a "weather"
// quality — never identical, always similar.
//
// FEELS LIKE: rain on a windshield, data flowing in.
// USE FOR:    fetching / downloading / receiving.
// ----------------------------------------------------------------------------

type shimmerRain struct {
	bgColor, peakColor Color
	opacityPtr         *float64
}

func ShimmerRain(bg, peak Color) shimmerRain {
	return shimmerRain{bgColor: bg, peakColor: peak}
}

func (s shimmerRain) Opacity(p *float64) shimmerRain { s.opacityPtr = p; return s }

func (s shimmerRain) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	field := func(x, y, t float64) float64 {
		// per-column speed seeded by x; never a whole number so columns desync
		speed := 6.0 + 3.0*math.Sin(x*0.73)
		// per-column offset so not all streaks fall together
		offset := math.Mod(x*13.7, 11.3)
		// streak position: wraps through screen height
		phase := math.Mod(y*2.0+speed*t+offset, 30.0)
		if phase < 0 {
			phase += 30.0
		}
		// bright at phase 0, fading tail below
		n := 1 - phase/4.0
		if n < 0 {
			return 0
		}
		// per-column intensity variation: some streaks stronger than others
		strength := 0.5 + 0.5*math.Sin(x*0.31+t*0.2)
		return n * n * strength
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 7. BREATH — whole-screen pulse
//
// Slow, even brightening and darkening of the entire screen. No spatial
// variation — just temporal. Matches a resting heart rate.
//
// FEELS LIKE: a slow inhale and exhale. Alive but at rest.
// USE FOR:    foreground presence indicator, "app is yours" signal while idle.
// ----------------------------------------------------------------------------

type shimmerBreath struct {
	bgColor, peakColor Color
	opacityPtr         *float64
	period             float64 // seconds per breath
}

func ShimmerBreath(bg, peak Color) shimmerBreath {
	return shimmerBreath{bgColor: bg, peakColor: peak, period: 5.0}
}

func (s shimmerBreath) Opacity(p *float64) shimmerBreath { s.opacityPtr = p; return s }

func (s shimmerBreath) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	field := func(_, _, t float64) float64 {
		// sin(2πt/period) normalised to 0..1 with ease
		phase := 2 * math.Pi * t / s.period
		n := (math.Sin(phase) + 1) / 2
		return n * n // ease curve — holds at top/bottom briefly
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 8. SPIRAL — rotating wave
//
// Log-spiral wavefronts rotating around screen centre. Creates a sense of
// circulation or gentle pull toward the middle without the abrupt rings of
// the tunnel.
//
// FEELS LIKE: water going down a drain, thought gathering.
// USE FOR:    pending / queued / focusing states.
// ----------------------------------------------------------------------------

type shimmerSpiral struct {
	bgColor, peakColor Color
	opacityPtr         *float64
}

func ShimmerSpiral(bg, peak Color) shimmerSpiral {
	return shimmerSpiral{bgColor: bg, peakColor: peak}
}

func (s shimmerSpiral) Opacity(p *float64) shimmerSpiral { s.opacityPtr = p; return s }

func (s shimmerSpiral) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	field := func(x, y, t float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		dist := math.Sqrt(dx*dx + dy*dy)
		angle := math.Atan2(dy, dx)
		// the key: combine angle with log(dist) for a log-spiral
		phase := angle*3 + math.Log(dist+1)*4 - t*2.0
		n := (math.Sin(phase) + 1) / 2
		// fade toward centre (singularity) and toward edge (out of view)
		edgeDist := math.Min(dist, math.Sqrt(cx*cx+cy*cy*4)-dist)
		edgeFade := math.Min(1, edgeDist/8.0)
		return n * n * edgeFade
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 9. VIGNETTE — edges only
//
// The frame itself glows; the centre stays dark. Useful as a "the room is
// bigger than the document" cue — content remains crisp, periphery breathes.
//
// FEELS LIKE: focused reading, a spotlight on the centre.
// USE FOR:    reading mode, focus mode indicator.
// ----------------------------------------------------------------------------

type shimmerVignette struct {
	bgColor, peakColor Color
	opacityPtr         *float64
}

func ShimmerVignette(bg, peak Color) shimmerVignette {
	return shimmerVignette{bgColor: bg, peakColor: peak}
}

func (s shimmerVignette) Opacity(p *float64) shimmerVignette { s.opacityPtr = p; return s }

func (s shimmerVignette) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	maxDist := math.Sqrt(cx*cx + cy*cy*4)
	field := func(x, y, t float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		dist := math.Sqrt(dx*dx + dy*dy)
		// radial gradient: 0 at centre, 1 at corners
		rad := dist / maxDist
		// slow temporal breath layered on top
		breath := 0.7 + 0.3*math.Sin(t*0.5)
		// sharpen so centre stays dark and only edges glow
		return math.Pow(rad, 2.5) * breath
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 10. SCATTER — random sparks
//
// Individual cells flicker briefly, scattered randomly across the screen.
// Each "spark" has a short lifetime. Completely non-directional — just
// isolated glints like fireflies.
//
// FEELS LIKE: fireflies, fine particulate, electric static.
// USE FOR:    indicating discrete events happening, rare background activity.
// ----------------------------------------------------------------------------

type shimmerScatter struct {
	bgColor, peakColor Color
	opacityPtr         *float64
}

func ShimmerScatter(bg, peak Color) shimmerScatter {
	return shimmerScatter{bgColor: bg, peakColor: peak}
}

func (s shimmerScatter) Opacity(p *float64) shimmerScatter { s.opacityPtr = p; return s }

func (s shimmerScatter) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	field := func(x, y, t float64) float64 {
		// hash cell position to a pseudo-random phase
		h := math.Mod(math.Sin(x*12.9898+y*78.233)*43758.5453, 1.0)
		if h < 0 {
			h += 1
		}
		// each cell has its own lifetime cycle
		period := 3.0 + h*4.0 // 3..7 seconds between flashes
		phase := math.Mod(t+h*period, period)
		// very short flash at the start of each cycle
		if phase > 0.3 {
			return 0
		}
		// triangle wave: peak at 0.15s in, zero at 0 and 0.3
		if phase < 0.15 {
			return phase / 0.15
		}
		return (0.3 - phase) / 0.15
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, opacity, field)
}

// ----------------------------------------------------------------------------
// 11. WORMHOLE — speed-driven spatial transition
//
// Radial rings streaming outward, driven by an externally-controlled speed.
// The effect accumulates distance across frames, so changing speed smoothly
// adjusts pace without jumps. The intensity scales with speed too — at zero
// speed the effect fades to invisible; as speed rises, rings appear and
// brighten. Perfect for load-time-proxy transitions: hook speed to a tween
// that ramps up as work begins and eases down as it completes.
//
// FEELS LIKE: wormhole travel, acceleration through space.
// USE FOR:    any operation where the duration is unknown — loads, syncs,
//             searches. Ramp speed up when work starts, ramp down as it ends.
// ----------------------------------------------------------------------------

type shimmerWormhole struct {
	bgColor, peakColor Color
	speedPtr           *float64
	state              *wormholeState
	wavelength         float64
	sharpness          float64
}

type wormholeState struct {
	distance float64 // accumulated phase — integrates speed*dt each frame
	lastT    float64
}

func ShimmerWormhole(bg, peak Color) shimmerWormhole {
	return shimmerWormhole{
		bgColor:    bg,
		peakColor:  peak,
		state:      &wormholeState{},
		wavelength: 5.0,
		sharpness:  5.0,
	}
}

// Speed wires the programmatic speed input. 0 = stationary (rings fade out),
// higher values = faster motion and brighter rings. Expected range roughly 0..20.
func (s shimmerWormhole) Speed(p *float64) shimmerWormhole { s.speedPtr = p; return s }

func (s shimmerWormhole) Apply(buf *Buffer, ctx PostContext) {
	speed := 0.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}

	t := ctx.Time.Seconds()
	dt := t - s.state.lastT
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	s.state.lastT = t
	s.state.distance += speed * dt

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	dist := s.state.distance

	// Restored thread/flow wormhole — silky radial threads with two interfering
	// flow waves along their length. Pre-halo version that was closest to target.
	_ = t
	const (
		perspectiveScale = 25.0
		minRadius        = 2.0
	)

	field := func(x, y, _ float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		r := math.Sqrt(dx*dx + dy*dy)
		if r < minRadius {
			return 0
		}
		angle := math.Atan2(dy, dx)
		depth := perspectiveScale / r

		threadOffset := math.Sin(angle*7.3)*1.4 +
			math.Sin(angle*13.7+0.8)*0.9 +
			math.Sin(angle*29.1+2.3)*0.5

		flow1 := 0.5 + 0.5*math.Sin(depth*0.9+dist+threadOffset)
		flow2 := 0.5 + 0.5*math.Sin(depth*0.4+dist*0.7+threadOffset*0.5+1.7)
		flow := 0.5 + 0.25*flow1 + 0.25*flow2

		threads := 0.6 +
			0.22*math.Sin(angle*17) +
			0.14*math.Sin(angle*31+1.1) +
			0.1*math.Sin(angle*53+2.7)

		proximity := math.Min(1, r/14.0)

		centreFade := math.Min(1, (r-minRadius)/5.0)
		edgeFade := math.Max(0, 1-r/math.Max(float64(w), float64(h)*2.0))

		return flow * threads * proximity * centreFade * edgeFade
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, 1.0, field)
}

// ----------------------------------------------------------------------------
// 12. WORMHOLE WARP — discrete bright streaks, length driven by speed
//
// Individual bright streaks (not a continuous wave) fly outward from the
// centre. At low speed: short, sparse streaks. At high speed: long, dense,
// overlapping streaks. Reads as classic warp-drive speed indicator.
// ----------------------------------------------------------------------------

type shimmerWormholeWarp struct {
	bgColor, peakColor Color
	speedPtr           *float64
	state              *wormholeState
}

func ShimmerWormholeWarp(bg, peak Color) shimmerWormholeWarp {
	return shimmerWormholeWarp{bgColor: bg, peakColor: peak, state: &wormholeState{}}
}

func (s shimmerWormholeWarp) Speed(p *float64) shimmerWormholeWarp { s.speedPtr = p; return s }

func (s shimmerWormholeWarp) Apply(buf *Buffer, ctx PostContext) {
	speed := 0.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	t := ctx.Time.Seconds()
	dt := t - s.state.lastT
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	s.state.lastT = t
	s.state.distance += speed * dt

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	dist := s.state.distance

	// streak length and density scale with speed
	speedFactor := math.Min(1, speed/15.0)
	trailPower := 7.0 - speedFactor*5.0 // lower power = longer trail

	field := func(x, y, _ float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		r := math.Sqrt(dx*dx + dy*dy)
		if r < 2 {
			return 0
		}
		angle := math.Atan2(dy, dx)
		depth := 25.0 / r

		// base fabric is dim at this variant; streaks do the visual work
		threads := 0.25 + 0.12*math.Sin(angle*17) + 0.08*math.Sin(angle*31+1.1)

		// streaks — sharp leading edge with speed-dependent trail
		phase := depth + dist + math.Sin(angle*7.3)*1.5
		frac := phase - math.Floor(phase)
		streak := math.Pow(1-frac, trailPower)

		centreFade := math.Min(1, (r-2)/5.0)
		edgeFade := math.Max(0, 1-r/math.Max(float64(w), float64(h)*2.0))

		return (threads + streak) * centreFade * edgeFade
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, 1.0, field)
}

// ----------------------------------------------------------------------------
// 13. WORMHOLE DRAG — texture stretches with speed (motion blur)
//
// The thread pattern elongates radially as speed increases. At rest, threads
// look "normal" — short and distinct. At hyperspeed, they stretch into long
// continuous streaks across the tunnel wall. True motion-blur aesthetic.
// ----------------------------------------------------------------------------

type shimmerWormholeDrag struct {
	bgColor, peakColor Color
	speedPtr           *float64
	state              *wormholeState
}

func ShimmerWormholeDrag(bg, peak Color) shimmerWormholeDrag {
	return shimmerWormholeDrag{bgColor: bg, peakColor: peak, state: &wormholeState{}}
}

func (s shimmerWormholeDrag) Speed(p *float64) shimmerWormholeDrag { s.speedPtr = p; return s }

func (s shimmerWormholeDrag) Apply(buf *Buffer, ctx PostContext) {
	speed := 0.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	t := ctx.Time.Seconds()
	dt := t - s.state.lastT
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	s.state.lastT = t
	s.state.distance += speed * dt

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	dist := s.state.distance

	speedFactor := math.Min(1, speed/15.0)
	// depth wavelength shrinks with speed = patterns elongate radially = motion blur
	depthScale := 1.0 - speedFactor*0.85 // 1.0 slow → 0.15 hyperspeed

	field := func(x, y, _ float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		r := math.Sqrt(dx*dx + dy*dy)
		if r < 2 {
			return 0
		}
		angle := math.Atan2(dy, dx)
		depth := 25.0 / r

		threadOffset := math.Sin(angle*7.3)*1.4 + math.Sin(angle*13.7+0.8)*0.9

		// single flow wave, but scaled so at high speed the wavelength stretches
		// far enough that each "crest" becomes a long radial band
		flow := 0.5 + 0.5*math.Sin(depth*depthScale+dist+threadOffset)

		threads := 0.5 + 0.22*math.Sin(angle*17) + 0.14*math.Sin(angle*31+1.1)

		centreFade := math.Min(1, (r-2)/5.0)
		edgeFade := math.Max(0, 1-r/math.Max(float64(w), float64(h)*2.0))

		return flow * threads * centreFade * edgeFade
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, 1.0, field)
}

// ----------------------------------------------------------------------------
// 14. WORMHOLE SURGE — intensity surges with speed
//
// Same base pattern, but the overall brightness ramps significantly with
// speed. At rest: barely visible. At hyperspeed: fully energised, crackling.
// Conveys speed via raw intensity rather than structure.
// ----------------------------------------------------------------------------

type shimmerWormholeSurge struct {
	bgColor, peakColor Color
	speedPtr           *float64
	state              *wormholeState
}

func ShimmerWormholeSurge(bg, peak Color) shimmerWormholeSurge {
	return shimmerWormholeSurge{bgColor: bg, peakColor: peak, state: &wormholeState{}}
}

func (s shimmerWormholeSurge) Speed(p *float64) shimmerWormholeSurge { s.speedPtr = p; return s }

func (s shimmerWormholeSurge) Apply(buf *Buffer, ctx PostContext) {
	speed := 0.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	t := ctx.Time.Seconds()
	dt := t - s.state.lastT
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	s.state.lastT = t
	s.state.distance += speed * dt

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	dist := s.state.distance

	// intensity scales strongly with speed
	intensity := 0.2 + math.Min(1, speed/12.0)*1.2

	field := func(x, y, _ float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		r := math.Sqrt(dx*dx + dy*dy)
		if r < 2 {
			return 0
		}
		angle := math.Atan2(dy, dx)
		depth := 25.0 / r

		threadOffset := math.Sin(angle*7.3)*1.4 + math.Sin(angle*13.7+0.8)*0.9

		flow := 0.5 + 0.25*math.Sin(depth*0.9+dist+threadOffset) +
			0.25*math.Sin(depth*0.4+dist*0.7+threadOffset*0.5+1.7)

		threads := 0.5 + 0.22*math.Sin(angle*17) + 0.14*math.Sin(angle*31+1.1)

		centreFade := math.Min(1, (r-2)/5.0)
		edgeFade := math.Max(0, 1-r/math.Max(float64(w), float64(h)*2.0))

		return flow * threads * centreFade * edgeFade * intensity
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, 1.0, field)
}

// ----------------------------------------------------------------------------
// 15. WORMHOLE CORE — speed pulls light inward toward centre
//
// At rest, pattern is uniform across the tunnel. As speed rises, intensity
// concentrates near the centre — like light being pulled toward the
// vanishing point. Reads as deep perspective acceleration.
// ----------------------------------------------------------------------------

type shimmerWormholeCore struct {
	bgColor, peakColor Color
	speedPtr           *float64
	state              *wormholeState
}

func ShimmerWormholeCore(bg, peak Color) shimmerWormholeCore {
	return shimmerWormholeCore{bgColor: bg, peakColor: peak, state: &wormholeState{}}
}

func (s shimmerWormholeCore) Speed(p *float64) shimmerWormholeCore { s.speedPtr = p; return s }

func (s shimmerWormholeCore) Apply(buf *Buffer, ctx PostContext) {
	speed := 0.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	t := ctx.Time.Seconds()
	dt := t - s.state.lastT
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	s.state.lastT = t
	s.state.distance += speed * dt

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	dist := s.state.distance

	speedFactor := math.Min(1, speed/15.0)

	field := func(x, y, _ float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		r := math.Sqrt(dx*dx + dy*dy)
		if r < 2 {
			return 0
		}
		angle := math.Atan2(dy, dx)
		depth := 25.0 / r

		threadOffset := math.Sin(angle*7.3)*1.4 + math.Sin(angle*13.7+0.8)*0.9
		flow := 0.5 + 0.5*math.Sin(depth*0.9+dist+threadOffset)
		threads := 0.5 + 0.22*math.Sin(angle*17) + 0.14*math.Sin(angle*31+1.1)

		// core pull — invert proximity at high speed so centre is brighter
		maxR := math.Max(float64(w), float64(h)*2.0)
		centric := 1 - r/maxR // 1 at centre, 0 at edge
		pull := (1-speedFactor)*math.Min(1, r/14.0) + speedFactor*math.Pow(centric, 2)

		centreFade := math.Min(1, (r-2)/5.0)
		edgeFade := math.Max(0, 1-r/maxR)

		return flow * threads * pull * centreFade * edgeFade
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, 1.0, field)
}

// ----------------------------------------------------------------------------
// 16. WORMHOLE TURBULENCE — high-frequency instability at speed
//
// Adds fine-grained texture turbulence proportional to speed. At rest, the
// pattern is smooth. At hyperspeed, it crackles and shimmers with finer
// detail — the tunnel feels unstable, stressed by the velocity.
// ----------------------------------------------------------------------------

type shimmerWormholeTurbulence struct {
	bgColor, peakColor Color
	speedPtr           *float64
	state              *wormholeState
}

func ShimmerWormholeTurbulence(bg, peak Color) shimmerWormholeTurbulence {
	return shimmerWormholeTurbulence{bgColor: bg, peakColor: peak, state: &wormholeState{}}
}

func (s shimmerWormholeTurbulence) Speed(p *float64) shimmerWormholeTurbulence {
	s.speedPtr = p
	return s
}

func (s shimmerWormholeTurbulence) Apply(buf *Buffer, ctx PostContext) {
	speed := 0.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	t := ctx.Time.Seconds()
	dt := t - s.state.lastT
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	s.state.lastT = t
	s.state.distance += speed * dt

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	dist := s.state.distance

	speedFactor := math.Min(1, speed/15.0)
	turbulenceAmount := speedFactor * 0.4

	field := func(x, y, _ float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		r := math.Sqrt(dx*dx + dy*dy)
		if r < 2 {
			return 0
		}
		angle := math.Atan2(dy, dx)
		depth := 25.0 / r

		threadOffset := math.Sin(angle*7.3)*1.4 + math.Sin(angle*13.7+0.8)*0.9
		flow := 0.5 + 0.5*math.Sin(depth*0.9+dist+threadOffset)
		threads := 0.5 + 0.22*math.Sin(angle*17) + 0.14*math.Sin(angle*31+1.1)

		// turbulence: high-frequency noise that scales with speed
		turb := math.Sin(angle*53+depth*4+dist*3)*0.5 +
			math.Sin(angle*89-depth*6+dist*4)*0.3
		flow += turb * turbulenceAmount

		centreFade := math.Min(1, (r-2)/5.0)
		edgeFade := math.Max(0, 1-r/math.Max(float64(w), float64(h)*2.0))

		return math.Max(0, flow) * threads * centreFade * edgeFade
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, 1.0, field)
}

// ----------------------------------------------------------------------------
// 17. WORMHOLE LAB — iteration target
//
// Copy of turbulence that we actively modify to push toward the final look.
// Turbulence stays untouched as the baseline we can A/B against.
// ----------------------------------------------------------------------------

type shimmerWormholeLab struct {
	bgColor, peakColor Color
	speedPtr           *float64
	state              *wormholeState
}

func ShimmerWormholeLab(bg, peak Color) shimmerWormholeLab {
	return shimmerWormholeLab{bgColor: bg, peakColor: peak, state: &wormholeState{}}
}

func (s shimmerWormholeLab) Speed(p *float64) shimmerWormholeLab {
	s.speedPtr = p
	return s
}

func (s shimmerWormholeLab) Apply(buf *Buffer, ctx PostContext) {
	speed := 0.0
	if s.speedPtr != nil {
		speed = *s.speedPtr
	}
	t := ctx.Time.Seconds()
	dt := t - s.state.lastT
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	s.state.lastT = t
	s.state.distance += speed * dt

	w, h := ctx.Width, ctx.Height
	cx, cy := float64(w)/2, float64(h)/2
	dist := s.state.distance

	speedFactor := math.Min(1, speed/15.0)
	turbulenceAmount := speedFactor * 0.4

	field := func(x, y, _ float64) float64 {
		dx := x - cx
		dy := (y - cy) * 2.0
		r := math.Sqrt(dx*dx + dy*dy)
		if r < 2 {
			return 0
		}
		angle := math.Atan2(dy, dx)
		depth := 25.0 / r

		threadOffset := math.Sin(angle*7.3)*1.4 + math.Sin(angle*13.7+0.8)*0.9
		flow := 0.5 + 0.5*math.Sin(depth*0.9+dist+threadOffset)
		threads := 0.5 + 0.22*math.Sin(angle*17) + 0.14*math.Sin(angle*31+1.1)

		turb := math.Sin(angle*53+depth*4+dist*3)*0.5 +
			math.Sin(angle*89-depth*6+dist*4)*0.3
		flow += turb * turbulenceAmount

		centreFade := math.Min(1, (r-2)/5.0)
		edgeFade := math.Max(0, 1-r/math.Max(float64(w), float64(h)*2.0))

		return math.Max(0, flow) * threads * centreFade * edgeFade
	}
	renderShimmer(buf, ctx, s.bgColor, s.peakColor, 1.0, field)
}

// ----------------------------------------------------------------------------
// 18. SILHOUETTE ECHO — zoom effect using the UI itself as shape data
//
// Snapshots the current screen's silhouette (which cells have text content),
// then renders faint echoes of that silhouette at multiple scales centred on
// the screen. Each echo drifts in scale over time, creating a gentle zoom
// in/out effect where the UI appears to breathe at different apparent depths.
// No motion, no character size change — just shape projection.
//
// FEELS LIKE: lens depth, the UI being inspected, gentle zoom breathing.
// USE FOR:    focus states, loading, anywhere you want depth implied via the UI itself.
// ----------------------------------------------------------------------------

type shimmerSilhouetteEcho struct {
	bgColor, peakColor Color
	opacityPtr         *float64
}

func ShimmerSilhouetteEcho(bg, peak Color) shimmerSilhouetteEcho {
	return shimmerSilhouetteEcho{bgColor: bg, peakColor: peak}
}

func (s shimmerSilhouetteEcho) Opacity(p *float64) shimmerSilhouetteEcho {
	s.opacityPtr = p
	return s
}

func (s shimmerSilhouetteEcho) Apply(buf *Buffer, ctx PostContext) {
	opacity := 1.0
	if s.opacityPtr != nil {
		opacity = *s.opacityPtr
	}
	if opacity <= 0 {
		return
	}

	w, h := ctx.Width, ctx.Height
	t := ctx.Time.Seconds()
	cx, cy := float64(w)/2, float64(h)/2

	// Snapshot silhouette first so our own writes don't pollute it.
	sil := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := buf.Get(x, y)
			if c.Rune > ' ' && c.Rune < 0x2500 {
				sil[y*w+x] = true
			}
		}
	}

	// Full journey: 1.0 → way in → 1.0 → way out → 1.0. Four phases.
	// Each phase uses smoothstep easing on an exponential scale curve so
	// motion feels like rushing toward / away from the infinity point.
	const (
		cycle   = 10.0 // seconds for the full 4-phase journey
		maxZoom = 25.0 // peak scale in the "in" direction; 1/maxZoom in the "out" direction
	)
	progress := math.Mod(t, cycle) / cycle // 0..1
	phaseLen := 0.25

	var scale float64
	switch {
	case progress < phaseLen: // phase 1: zoom in (1.0 → maxZoom)
		p := progress / phaseLen
		eased := p * p * (3 - 2*p)
		scale = math.Pow(maxZoom, eased)
	case progress < 2*phaseLen: // phase 2: zoom back (maxZoom → 1.0)
		p := (progress - phaseLen) / phaseLen
		eased := p * p * (3 - 2*p)
		scale = math.Pow(maxZoom, 1-eased)
	case progress < 3*phaseLen: // phase 3: zoom out (1.0 → 1/maxZoom)
		p := (progress - 2*phaseLen) / phaseLen
		eased := p * p * (3 - 2*p)
		scale = math.Pow(maxZoom, -eased)
	default: // phase 4: zoom back (1/maxZoom → 1.0)
		p := (progress - 3*phaseLen) / phaseLen
		eased := p * p * (3 - 2*p)
		scale = math.Pow(maxZoom, -(1-eased))
	}

	// brightness fades near scale 1 (would overlap UI) in either direction
	var brightness float64
	distFromUnity := math.Abs(math.Log(scale)) // symmetric measure for >1 and <1
	if distFromUnity < 0.25 {
		brightness = distFromUnity / 0.25
	} else {
		brightness = 1.0
	}
	brightness *= opacity
	if brightness <= 0.01 {
		return
	}

	dR := int(s.peakColor.R) - int(s.bgColor.R)
	dG := int(s.peakColor.G) - int(s.bgColor.G)
	dB := int(s.peakColor.B) - int(s.bgColor.B)
	add := func(c Color, amt float64) Color {
		if c.Mode == ColorDefault {
			c = s.bgColor
		}
		return Color{
			Mode: ColorRGB,
			R:    clampU8(int(c.R) + int(float64(dR)*amt)),
			G:    clampU8(int(c.G) + int(float64(dG)*amt)),
			B:    clampU8(int(c.B) + int(float64(dB)*amt)),
		}
	}

	// Sub-cell Braille sampling: each output cell tests all 8 Braille dots.
	// Each dot samples the source silhouette at its sub-cell precision, so
	// scale transitions interpolate smoothly instead of snapping cell-to-cell.
	invScale := 1.0 / scale

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell := buf.Get(x, y)
			if !isPaintable(cell.Rune) {
				continue
			}

			var pattern uint8
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					// sub-cell position in cell units (dots are 2 wide × 4 tall per cell)
					subX := float64(x) + (float64(dx)+0.5)/2.0
					subY := float64(y) + (float64(dy)+0.5)/4.0
					// reverse project onto the unscaled silhouette
					sxf := (subX-cx)*invScale + cx
					syf := (subY-cy)*invScale + cy
					sxi, syi := int(sxf), int(syf)
					if sxi < 0 || sxi >= w || syi < 0 || syi >= h {
						continue
					}
					if sil[syi*w+sxi] {
						pattern |= 1 << brailleBits[dy][dx]
					}
				}
			}

			if pattern == 0 {
				continue
			}
			bg := cell.Style.BG
			if bg.Mode == ColorDefault {
				bg = s.bgColor
			}
			cell.Rune = rune(0x2800) + rune(pattern)
			cell.Style.FG = add(bg, brightness)
			buf.Set(x, y, cell)
		}
	}
}

// ============================================================================
// SHARED HELPERS
// ============================================================================

// brailleBits maps (row, col) → bit position in a Braille character.
// Unicode Braille layout:
//   dot 1 (col 0, row 0) = bit 0
//   dot 2 (col 0, row 1) = bit 1
//   dot 3 (col 0, row 2) = bit 2
//   dot 7 (col 0, row 3) = bit 6
//   dot 4 (col 1, row 0) = bit 3
//   dot 5 (col 1, row 1) = bit 4
//   dot 6 (col 1, row 2) = bit 5
//   dot 8 (col 1, row 3) = bit 7
var brailleBits = [4][2]uint8{
	{0, 3},
	{1, 4},
	{2, 5},
	{6, 7},
}

// isPaintable returns true for cells the shimmer is allowed to overwrite.
// Spaces and block-element characters are treated as blank; line-drawing
// characters (borders) are preserved since they're structural.
func isPaintable(r rune) bool {
	if r == 0 || unicode.IsSpace(r) {
		return true
	}
	if r >= 0x2580 && r <= 0x259F { // block elements ▀▄▌▐▙▚▛▜
		return true
	}
	return false
}

func clampU8(n int) uint8 {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return uint8(n)
}

func lerpRGB(a, b Color, t float64) Color {
	ar, ag, ab := int(a.R), int(a.G), int(a.B)
	br, bg, bb := int(b.R), int(b.G), int(b.B)
	r := uint8(float64(ar) + float64(br-ar)*t)
	g := uint8(float64(ag) + float64(bg-ag)*t)
	bl := uint8(float64(ab) + float64(bb-ab)*t)
	return Color{Mode: ColorRGB, R: r, G: g, B: bl}
}

// keep the original name as an alias so existing call sites still work
func ShimmerBraille(bg, peak Color) shimmerDrifting { return ShimmerDrifting(bg, peak) }
