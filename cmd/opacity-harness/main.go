// opacity-harness is a small visual harness for glyph's terminal opacity.
//
// It renders contrived backing content, then draws text, container, and overlay
// opacity samples against it. Use `a` and `;` to move the opacity percentage.
//
//	go run ./cmd/opacity-harness
package main

import (
	"fmt"
	"log"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

var (
	bg     = Hex(0x161818)
	panel  = Hex(0x080808)
	fg     = Hex(0xb8b8b8)
	bright = Hex(0xf2f2f2)
	subtle = Hex(0x777777)
	muted  = Hex(0x3a3a3a)
	teal   = Hex(0x5f9187)
	gold   = Hex(0xf5be5a)
	cream  = Hex(0xffeec8)
	rose   = Hex(0xd88383)
	blue   = Hex(0x2d4b55)
)

func main() {
	app := NewApp()
	app.SetDefaultStyle(Style{FG: fg, BG: bg})

	percent := 65
	opacity := float64(percent) / 100
	percentLabel := ""
	dither := false
	modeLabel := ""
	var glowRef NodeRef

	syncLabel := func() {
		opacity = float64(percent) / 100
		percentLabel = fmt.Sprintf("%03d%%", percent)
		if dither {
			modeLabel = "dither"
		} else {
			modeLabel = "smooth"
		}
	}
	syncLabel()

	adjust := func(delta int) {
		percent += delta
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		syncLabel()
	}

	backingBlock := VBox.Width(86)(
		Text("the backing deliberately changes color, glyphs, and density under the opacity sample").FG(subtle),
		SpaceH(1),
		HBox(
			Text("to: alice@example.com").FG(teal),
			Space(),
			Text("subject: opacity over live backing").FG(gold),
		),
		Text("Dear Alice,").FG(fg),
		HBox(
			Text("I hope this patterned row makes blends obvious: ").FG(fg),
			Text("teal").FG(teal),
			Text(" / ").FG(subtle),
			Text("gold").FG(gold),
			Text(" / ").FG(subtle),
			Text("rose").FG(rose),
			Space(),
		),
		Text("..::## blocks behind the card should fade through, not snap to black").FG(Hex(0x505050)),
		SpaceH(1),
		HBox(
			VBox.Width(26).Fill(blue).PaddingVH(1, 2)(
				Text("blue backing").FG(cream),
				Text("under sample").FG(fg),
			),
			SpaceW(2),
			VBox.Width(26).Fill(Hex(0x3a3020)).PaddingVH(1, 2)(
				Text("warm backing").FG(gold),
				Text("under sample").FG(fg),
			),
			SpaceW(2),
			VBox.Width(26).Fill(Hex(0x302430)).PaddingVH(1, 2)(
				Text("rose backing").FG(rose),
				Text("under sample").FG(fg),
			),
		),
		SpaceH(2),
	)

	app.View("main",
		VBox.Fill(bg)(
			HBox(
				Text("opacity harness").FG(bright).Bold(),
				SpaceW(2),
				Text("opacity ").FG(subtle),
				Text(&percentLabel).FG(gold),
				SpaceW(2),
				Text("mode ").FG(subtle),
				Text(&modeLabel).FG(teal),
				SpaceW(2),
				Text("a down | s up | d zero | f full | l mode | q quit").FG(muted),
			),

			Space(),
			HBox(Space(), backingBlock, Space()),
			Space(),

			If(&dither).Then(
				Overlay.At(20, 6).Opacity(&opacity).OpacityMode(OpacityDither)(
					VBox.Border(BorderDouble).Width(52).Fill(Hex(0x100810)).PaddingVH(1, 2)(
						HBox(
							Space(),
							Text("dither ").FG(rose),
							Text("opacity ").FG(subtle),
							Text(&percentLabel).FG(gold).Bold(),
							Text(" over message text").FG(fg),
							Space(),
						),
						Text("text-underlay sample: backing runes should show through gracefully").FG(subtle),
					),
				),
			).Else(
				Overlay.At(20, 6).Opacity(&opacity)(
					VBox.Border(BorderDouble).Width(52).Fill(panel).PaddingVH(1, 2)(
						HBox(
							Space(),
							Text("smooth ").FG(teal),
							Text("opacity ").FG(subtle),
							Text(&percentLabel).FG(gold).Bold(),
							Text(" over message text").FG(fg),
							Space(),
						),
						Text("text-underlay sample: backing runes should show through gracefully").FG(subtle),
					),
				),
			),

			Overlay.At(20, 17).Opacity(&opacity).OpacityMode(OpacityDither)(
				VBox.Width(52).Fill(panel).PaddingVH(1, 2).NodeRef(&glowRef)(
					HBox(
						Space(),
						Text("glow + rim ").FG(teal),
						Text(&percentLabel).FG(gold).Bold(),
						Text(" compositor sample").FG(fg),
						Space(),
					),
					Text("effect opacity should fade halo and rim without black bars").FG(subtle),
					ScreenEffect(
						SESpinGlow(&glowRef,
							RGB(45, 75, 85),
							RGB(95, 145, 135),
							RGB(245, 190, 90),
							RGB(255, 238, 200),
						).
							Strength(0.42).
							OpacityMode(OpacityDither).
							Speed(2.2).
							Falloff(4).
							Radius(8).
							Rim(true),
					),
				),
			),
		),
	).
		Handle("a", func(_ riffkey.Match) { adjust(-5) }).
		Handle("s", func(_ riffkey.Match) { adjust(5) }).
		Handle("d", func(_ riffkey.Match) {
			percent = 0
			syncLabel()
		}).
		Handle("f", func(_ riffkey.Match) {
			percent = 100
			syncLabel()
		}).
		Handle("l", func(_ riffkey.Match) {
			dither = !dither
			syncLabel()
		}).
		Handle("q", func(_ riffkey.Match) { app.Stop() }).
		Handle("<C-c>", func(_ riffkey.Match) { app.Stop() })

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}
