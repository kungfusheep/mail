// palette-canvas — a side-by-side comparison tool for SESpinGlow palettes.
//
// Runs 6 pills on screen at once, each with its own palette but identical
// speed/strength/radius so the only variable your eye judges is colour.
// Press `t` to toggle light/dark theme so you can evaluate every palette
// on both grounds without restarting.
//
// Edit palettes at the top of this file to iterate. The goal here is to
// pick a colour language for the mail app's send moment — not the motion
// or the shape, just the palette.
//
//	go run ./cmd/palette-canvas
package main

import (
	"log"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// ----- tuning knobs (identical across every pill so palette is the
// only variable in your comparison) -----

var (
	speed    = 1.5 // ~4s per rotation
	strength = 0.4 // atmospheric, not spotlight
	radius   = 18  // halo reach in cells
	falloff  = 5
	useRim   = true // rim painted with conic stroke
)

// ----- palettes under consideration -----

type namedPalette struct {
	name   string
	colors []Color
	// speed overrides the global tuning knob when non-zero. Used for the
	// anxiety-offset row where slow breath-paced rotation is the point —
	// a fast spin reads as urgency even with calm colours.
	speed float64
}

// Six well-known / iconic palettes. Order follows the original logo /
// product reference so the rotation feels "right" to anyone who knows
// the source.
var palettes = []namedPalette{
	{name: "apple-1977", colors: []Color{ // classic six-stripe Apple logo (1977–98)
		RGB(97, 187, 70),  // green
		RGB(253, 184, 39), // yellow
		RGB(245, 130, 31), // orange
		RGB(224, 58, 62),  // red
		RGB(150, 61, 151), // purple
		RGB(0, 157, 220),  // blue
	}},
	{name: "instagram", colors: []Color{ // the 2016+ camera-icon gradient
		RGB(254, 218, 117),
		RGB(250, 126, 30),
		RGB(214, 41, 118),
		RGB(150, 47, 191),
		RGB(79, 91, 213),
	}},
	{name: "google", colors: []Color{ // the google logo palette
		RGB(66, 133, 244), // blue
		RGB(219, 68, 55),  // red
		RGB(244, 180, 0),  // yellow
		RGB(15, 157, 88),  // green
	}},
	{name: "synthwave", colors: []Color{ // 80s miami / retro futurism
		RGB(255, 0, 128),   // hot pink
		RGB(150, 60, 220),  // electric purple
		RGB(0, 229, 255),   // cyan
		RGB(255, 105, 180), // pink accent
	}},
	{name: "aurora", colors: []Color{ // northern-lights atmospheric
		RGB(120, 255, 180), // aurora green
		RGB(80, 200, 230),  // sky
		RGB(155, 107, 255), // violet
		RGB(255, 133, 200), // faint side-glow pink
	}},
	{name: "sunset", colors: []Color{ // warm-to-deep natural sunset
		RGB(255, 153, 102), // peach
		RGB(255, 94, 98),   // coral
		RGB(155, 89, 182),  // dusk violet
		RGB(58, 28, 113),   // deep night
	}},

	// --- anxiety-offset row ---
	//
	// Sending mail is an exhale, but it can also trigger the "did I word
	// that right / send to the wrong person / forget the attachment"
	// gut-clench. These palettes are tuned to push back against THAT
	// feeling, not just to look pretty. Research on calming environments
	// (NICU, waiting rooms, therapy) converges on: warm + low-saturation
	// + few hues + slow rhythmic motion. Palettes here lean into that
	// instead of going cold/clinical or bright/playful.
	//
	// Each carries a speed override — fast rotation reads as urgency
	// even with calm colours, so the breath-paced 0.6 is the point.

	{name: "morning-tide", speed: 2.2, colors: []Color{ // calm teal base + warm morning-gold lift
		RGB(45, 75, 85),    // deep teal anchor
		RGB(95, 145, 135),  // softened sea green
		RGB(245, 190, 90),  // warm gold (the optimistic lift)
		RGB(255, 238, 200), // soft cream
	}},
	{name: "tide", speed: 2.2, colors: []Color{ // ocean — cool depth + warm gold sunbeam glinting through
		RGB(35, 65, 85),    // deep teal
		RGB(90, 130, 145),  // slate
		RGB(240, 215, 130), // soft gold (the triumph)
		RGB(245, 240, 225), // pearl
	}},
	{name: "harvest", speed: 2.2, colors: []Color{ // late afternoon richness — saturated, abundant, warm
		RGB(200, 110, 60),  // burnt orange
		RGB(220, 130, 130), // warm rose
		RGB(255, 200, 100), // soft yellow
		RGB(248, 230, 200), // cream
	}},
}

// ----- themes -----

type themeColors struct {
	bg     Color
	fg     Color
	subtle Color
	muted  Color
}

var (
	darkTheme = themeColors{
		bg:     Hex(0x1a1a1a),
		fg:     Hex(0xb0b0b0),
		subtle: Hex(0x777777),
		muted:  Hex(0x3a3a3a),
	}
	lightTheme = themeColors{
		bg:     Hex(0xf6f6f6),
		fg:     Hex(0x333333),
		subtle: Hex(0x777777),
		muted:  Hex(0xcccccc),
	}
)

func main() {
	app := NewApp()

	// Theme colours held in addressable vars. The view template captures
	// their ADDRESSES (via *Color) so swaps take effect next render.
	// Capturing `themeColors` by value would freeze the template to dark.
	var (
		bg, fg, subtle, muted Color
		themeName             string
	)
	setTheme := func(tc themeColors, name string) {
		bg = tc.bg
		fg = tc.fg
		subtle = tc.subtle
		muted = tc.muted
		themeName = name
		app.SetDefaultStyle(Style{FG: fg, BG: bg})
	}
	setTheme(darkTheme, "dark")

	// one NodeRef per pill so each SESpinGlow has an independent focus.
	refs := make([]NodeRef, len(palettes))

	// each cell: a pill rendered with theme-driven colours (via *Color),
	// its label underneath, and a ScreenEffect carrying the SESpinGlow.
	pillCell := func(i int) Component {
		p := palettes[i]
		return VBox.Grow(1)(
			HBox(
				Space(),
				VBox.Width(28).Fill(&bg).PaddingVH(1, 2).NodeRef(&refs[i])(
					HBox(SpaceW(1), Text(p.name).FG(&fg)),
				),
				Space(),
			),
			HBox(Space(), Text(p.name).FG(&subtle), Space()),
			ScreenEffect(
				func() Effect {
					sp := speed
					if p.speed > 0 {
						sp = p.speed
					}
					eff := SESpinGlow(&refs[i], p.colors...).
						Speed(sp).
						Falloff(falloff).
						Radius(radius).
						Strength(strength)
					if useRim {
						eff = eff.Rim(true)
					}
					return eff
				}(),
			),
		)
	}

	app.View("main",
		VBox.Fill(&bg)(
			HBox(
				Text("palette canvas").FG(&fg).Bold(),
				SpaceW(2),
				Text("·").FG(&subtle),
				SpaceW(2),
				Text(&themeName).FG(&subtle),
				SpaceW(2),
				Text("· t toggle theme · q quit").FG(&muted),
			),
			Space(),
			HBox.Gap(4)(pillCell(0), pillCell(1), pillCell(2)),
			Space(),
			HBox.Gap(4)(pillCell(3), pillCell(4), pillCell(5)),
			Space(),
			HBox.Gap(4)(pillCell(6), pillCell(7), pillCell(8)),
			Space(),
		),
	).
		Handle("t", func(_ riffkey.Match) {
			if themeName == "dark" {
				setTheme(lightTheme, "light")
			} else {
				setTheme(darkTheme, "dark")
			}
		}).
		Handle("q", func(_ riffkey.Match) { app.Stop() }).
		Handle("<C-c>", func(_ riffkey.Match) { app.Stop() })

	// continuous render so the spinglow actually spins
	go func() {
		ticker := time.NewTicker(16 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			app.RequestRender()
		}
	}()

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}
