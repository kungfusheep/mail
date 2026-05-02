// effect-ghost is a focused harness for debugging retained overlay exit
// painting with vignette dodge + drop shadow.
//
//	go run ./cmd/effect-ghost
package main

import (
	"fmt"
	"log"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

var (
	bg      = Hex(0x191b1b)
	panel   = Hex(0x080808)
	fg      = Hex(0xb8b8b8)
	bright  = Hex(0xeeeeee)
	subtle  = Hex(0x777777)
	muted   = Hex(0x444444)
	teal    = Hex(0x5f9187)
	gold    = Hex(0xf5be5a)
	rose    = Hex(0xd88383)
	blueBG  = Hex(0x263b42)
	warmBG  = Hex(0x3b321f)
	roseBG  = Hex(0x342633)
	shadow  = Hex(0x000000)
	vigIn   = 0.55
	shadowS = 0.28
)

type sampleEffect struct {
	ref   *NodeRef
	bg    Color
	label *string
}

func (s sampleEffect) Apply(buf *Buffer, _ PostContext) {
	if s.ref == nil || s.label == nil || s.ref.W <= 0 || s.ref.H <= 0 {
		return
	}
	lum := func(c Color) int {
		if c.Mode != ColorRGB {
			return 0
		}
		return int(c.R) + int(c.G) + int(c.B)
	}
	cardLum := 255 * 3
	for y := s.ref.Y; y < s.ref.Y+s.ref.H; y++ {
		for x := s.ref.X; x < s.ref.X+s.ref.W; x++ {
			cardLum = min(cardLum, lum(buf.Get(x, y).Style.BG))
		}
	}
	shadowLum := 255 * 3
	radius := 10
	for y := max(0, s.ref.Y-radius); y < min(buf.Height(), s.ref.Y+s.ref.H+radius); y++ {
		for x := max(0, s.ref.X-radius); x < min(buf.Width(), s.ref.X+s.ref.W+radius); x++ {
			if x >= s.ref.X && x < s.ref.X+s.ref.W && y >= s.ref.Y && y < s.ref.Y+s.ref.H {
				continue
			}
			shadowLum = min(shadowLum, lum(buf.Get(x, y).Style.BG))
		}
	}
	state := "card<=shadow"
	if cardLum > shadowLum && s.ref.Opacity > 0 {
		state = "shadow darker"
	}
	*s.label = fmt.Sprintf("ref %.2f  card lum %03d  shadow lum %03d  %s", s.ref.Opacity, cardLum, shadowLum, state)
}

func main() {
	app := NewApp()
	app.SetDefaultStyle(Style{FG: fg, BG: bg})

	var (
		open         = true
		vignetteOn   = true
		shadowOn     = true
		vignetteLive = vigIn
		shadowLive   = shadowS
		modeLabel    string
		stateLabel   string
		triggerCount int
		triggerLabel string
		sampleLabel  string
		cardRef      NodeRef
		lastToggle   = time.Now()
		autoLoop     = false
	)

	syncLabels := func() {
		v := "off"
		if vignetteOn {
			v = "on"
			vignetteLive = vigIn
		} else {
			vignetteLive = 0
		}
		s := "off"
		if shadowOn {
			s = "on"
			shadowLive = shadowS
		} else {
			shadowLive = 0
		}
		state := "closed"
		if open {
			state = "open"
		}
		loop := "manual"
		if autoLoop {
			loop = "loop"
		}
		modeLabel = fmt.Sprintf("vignette %s  shadow %s  %s", v, s, loop)
		stateLabel = state
		triggerLabel = fmt.Sprintf("%03d", triggerCount)
	}
	syncLabels()

	toggleOpen := func() {
		open = !open
		triggerCount++
		lastToggle = time.Now()
		syncLabels()
	}

	go func() {
		ticker := time.NewTicker(16 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if autoLoop && time.Since(lastToggle) > 1400*time.Millisecond {
				toggleOpen()
			}
			app.RequestRender()
		}
	}()

	backing := VBox.Width(92)(
		HBox(
			Text("to: ").FG(subtle),
			Text("alice@example.com").FG(teal),
			Space(),
			Text("subject: retained overlay paint").FG(gold),
		),
		SpaceH(1),
		Text("Dear Alice,").FG(fg),
		Text("This harness deliberately puts coloured blocks, text, and empty cells under the overlay.").FG(fg),
		Text("If cells stop repainting, the old rectangle should stand out against this backing.").FG(subtle),
		SpaceH(1),
		HBox(
			VBox.Width(26).Fill(blueBG).PaddingVH(1, 2)(
				Text("blue backing").FG(bright),
				Text("under card edge").FG(fg),
			),
			SpaceW(2),
			VBox.Width(26).Fill(warmBG).PaddingVH(1, 2)(
				Text("warm backing").FG(gold),
				Text("under card body").FG(fg),
			),
			SpaceW(2),
			VBox.Width(26).Fill(roseBG).PaddingVH(1, 2)(
				Text("rose backing").FG(rose),
				Text("under shadow").FG(fg),
			),
		),
		SpaceH(2),
		Text("abcdefghijklmnopqrstuvwxyz  ABCDEFGHIJKLMNOPQRSTUVWXYZ").FG(muted),
		Text("0123456789  ..::##@@  cells should keep updating during fade-out").FG(muted),
	)

	app.View("main",
		VBox.Fill(bg)(
			HBox(
				Text("effect-ghost").FG(bright).Bold(),
				SpaceW(2),
				Text("state ").FG(subtle),
				Text(&stateLabel).FG(gold),
				SpaceW(2),
				Text(&modeLabel).FG(teal),
				SpaceW(2),
				Text("runs ").FG(subtle),
				Text(&triggerLabel).FG(rose),
				SpaceW(2),
				Text(&sampleLabel).FG(gold),
			),
			HBox(
				Text("space toggle").FG(muted),
				SpaceW(2),
				Text("v vignette").FG(muted),
				SpaceW(2),
				Text("d shadow").FG(muted),
				SpaceW(2),
				Text("b both").FG(muted),
				SpaceW(2),
				Text("l loop").FG(muted),
				SpaceW(2),
				Text("q quit").FG(muted),
			),
			Space(),
			HBox(Space(), backing, Space()),
			Space(),
			If(&open).Then(
				Overlay.Centered()(
					VBox.Width(54).
						Fill(panel).
						PaddingVH(1, 2).
						NodeRef(&cardRef).
						Opacity(
							In(Animate.Duration(220*time.Millisecond).From(0.0)(1.0)).
								Out(Animate.Duration(950*time.Millisecond)(0.0)),
						)(
						HBox(
							Space(),
							Text("-> ").FG(gold),
							Text("debugging ").FG(subtle),
							Text("vignette + shadow").FG(gold),
							Space(),
						),
						Text("watch the area behind this card during exit").FG(subtle),
						ScreenEffect(
							SEVignette().
								Dodge(&cardRef).
								Smooth().
								Strength(
									In(Animate.Duration(220*time.Millisecond).From(0.0)(&vignetteLive)).
										Out(Animate.Duration(950*time.Millisecond)(0.0)),
								),
							SEDropShadow().
								Focus(&cardRef).
								Tint(shadow).
								Radius(10).
								Strength(&shadowLive),
							sampleEffect{ref: &cardRef, bg: bg, label: &sampleLabel},
						),
					),
				),
			),
		),
	).
		NoCounts().
		Handle(" ", func(_ riffkey.Match) { toggleOpen() }).
		Handle("v", func(_ riffkey.Match) {
			vignetteOn = !vignetteOn
			syncLabels()
		}).
		Handle("d", func(_ riffkey.Match) {
			shadowOn = !shadowOn
			syncLabels()
		}).
		Handle("b", func(_ riffkey.Match) {
			vignetteOn = true
			shadowOn = true
			syncLabels()
		}).
		Handle("l", func(_ riffkey.Match) {
			autoLoop = !autoLoop
			lastToggle = time.Now()
			syncLabels()
		}).
		Handle("q", func(_ riffkey.Match) { app.Stop() }).
		Handle("<C-c>", func(_ riffkey.Match) { app.Stop() })

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}
