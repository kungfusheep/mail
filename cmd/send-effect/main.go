// send-effect is a tuning harness for the "sending email" overlay.
//
// Runs a mock mailbox/compose screen with a centred send-in-flight card.
// Press `s` to trigger a simulated send; the card pops for the configured
// duration with a spinglow rim, then fades back out. Tweak the constants
// at the top of this file and re-run with:
//
//	go run ./cmd/send-effect
//
// No IMAP, no SMTP, no cache — just the visual loop.
package main

import (
	"log"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// ----- tuning knobs -----
//
// Edit, save, rerun. The whole reason this binary exists is to make the
// edit/observe loop as tight as possible.

var (
	// sendDuration is how long the "sending" state holds before settling.
	// Real SMTP takes 0.5s–3s; pick a value that matches your feel.
	sendDuration = 2 * time.Second

	// Tuning carried over from palette-canvas after we settled on a
	// "calm + triumph + clearly-in-progress" balance.
	//
	// spinSpeed 2.2 ≈ 2.7s per rotation — fast enough to read as "this
	// is happening right now" within an SMTP send window, slow enough
	// not to spike anxiety.
	spinSpeed = 2.2

	// spinStrength stays atmospheric: enough colour to signal progress,
	// not enough to feel like an alarm.
	spinStrength = 0.42

	// spinRadius + falloff — halo-only mode. Backdrop dims compose
	// text behind the card, so a generous radius is fine — bleed
	// lands on dimmed cells, not readable text. Falloff keeps
	// intensity tight to the card edge.
	spinRadius = 8
	spinFall   = 4

	// useRim on — glyph's paintRim no longer leaves a rune-gate (it
	// always owns the perimeter cells now), so the rim is a continuous
	// stroke regardless of compose text behind. With ember's contrast
	// the rim still shows visible conic rotation; that's now a palette
	// choice, not a geometry bug.
	useRim = true

	// card/effect timing. The active glow exits as soon as sending
	// completes; the card remains briefly as a confident "sent" receipt.
	cardIn         = 320 * time.Millisecond
	cardOut        = 850 * time.Millisecond
	settleDuration = 950 * time.Millisecond
	glowIn         = 520 * time.Millisecond
	glowOut        = cardOut

	// `morning-tide` palette: calm teal/green as the nervous-system anchor,
	// with warm gold + cream carrying the optimistic "this is going well"
	// signal. Less fireplace, more morning light over water.
	palette = []Color{
		RGB(45, 75, 85),    // deep teal anchor
		RGB(95, 145, 135),  // softened sea green
		RGB(245, 190, 90),  // warm gold (the optimistic lift)
		RGB(255, 238, 200), // soft cream
	}

	// theme colours copied from the real mail app so the harness looks
	// like what you'll see in production.
	bg      = Hex(0x1a1a1a)
	bright  = Hex(0xeeeeee)
	fg      = Hex(0xb0b0b0)
	subtle  = Hex(0x777777)
	muted   = Hex(0x3a3a3a)
	accent  = Hex(0xe60012)
	groupBG = Hex(0x242424)
)

// ----- state -----

type sendState int

const (
	stateIdle sendState = iota
	stateSending
	stateSettled // brief post-send moment before returning to idle
)

func main() {
	app := NewApp()
	app.SetDefaultStyle(Style{FG: fg, BG: bg})

	var (
		state      sendState
		stateStart time.Time
		cardIcon   = "-> "
		cardVerb   = "sending to "
		cardTone   = Hex(0xe8a860)
	)

	// mock data so the background feels real
	mockTo := "alice@example.com"
	mockSubject := "Re: Tuesday plans"

	var statusText string
	updateStatus := func() {
		switch state {
		case stateSending:
			statusText = "· sending"
			cardIcon = "-> "
			cardVerb = "sending to "
			cardTone = Hex(0xe8a860)
		case stateSettled:
			statusText = "✓ sent · returning..."
			cardIcon = "✓ "
			cardVerb = "sent to "
			cardTone = Hex(0xf5d28a)
		default:
			statusText = "press s to send"
			cardIcon = "-> "
			cardVerb = "sending to "
			cardTone = Hex(0xe8a860)
		}
	}
	updateStatus()

	// continuous render so Animate.* actually animates
	go func() {
		ticker := time.NewTicker(16 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			app.RequestRender()
		}
	}()

	// state machine driver: tick whenever we render. Plain time checks
	// against stateStart, no goroutines needed.
	tickState := func() {
		switch state {
		case stateSending:
			if time.Since(stateStart) >= sendDuration {
				state = stateSettled
				stateStart = time.Now()
			}
		case stateSettled:
			if time.Since(stateStart) >= settleDuration {
				state = stateIdle
			}
		}
	}
	_ = tickState // called in the view closure below

	// single overlay flag drives the If gate in the view. It stays true
	// through both sending and settled states so the fade-out is visible.
	showCard := func() bool {
		return state == stateSending || state == stateSettled
	}
	cardVisible := false

	Option := func(label, key string) Component {
		return HBox.Gap(1)(Text(label), Text(key).Inverse())
	}

	makeView := func(label string, effects func(*NodeRef) []Component) Component {
		var cardRef NodeRef
		cardChildren := []Component{
			VBox(
				HBox(
					Space(),
					Text(&cardIcon).FG(&cardTone),
					Text(&cardVerb).FG(subtle),
					Text(&mockTo).FG(&cardTone),
					Text("  "),
					Space(),
				),
				HBox.Gap(2)(
					Space(),
					// Option("do it", "s"),
					Option("cancel", "<Esc>"),
					Space(),
				),
			),
		}
		cardChildren = append(cardChildren, effects(&cardRef)...)

		return VBox.Fill(bg)(
			// header strip
			HBox(
				Text("mail-effect-harness").FG(bright).Bold(),
				SpaceW(2),
				Text("·").FG(subtle),
				SpaceW(2),
				Text(&statusText).FG(subtle),
				SpaceW(2),
				Text("·").FG(subtle),
				SpaceW(2),
				Text(label).FG(subtle),
			),

			// mock compose body, typewriter-style: vertically centred via
			// a Space before + after. Approximates the real composer's
			// feel where content hovers around mid-screen.
			VBox.Grow(1).Fill(bg)(
				Space(),
				HBox(Space(), VBox.Width(65)(
					HBox(Text("To: ").FG(subtle), Text(&mockTo).FG(fg)),
					HBox(Text("Subject: ").FG(subtle), Text(&mockSubject).FG(bright)),
					SpaceH(1),
					Text("Dear Alice,").FG(fg),
					SpaceH(1),
					Text("I hope this finds you well. Tuesday at 3pm works for me - looking").FG(fg),
					Text("forward to it.").FG(fg),
					SpaceH(1),
					Text("-P.G").FG(fg),
				), Space()),
				Space(),
			),

			HRule().Style(Style{FG: muted}),
			Text(" s TRIGGER · h bloom · j afterglow · k quiet · l release · r reset · q quit").FG(muted),

			If(&cardVisible).Then(
				Overlay.Centered().Opacity(
					In(Animate.From(0.0).Duration(cardIn).Ease(EaseOutQuad)(1.0)).
						Out(Animate.Duration(cardOut).Ease(EaseOutCubic)(0.0)),
				)(
					VBox.Width(52).
						PaddingVH(1, 2).
						Fill(Hex(0x080808)).
						NodeRef(&cardRef)(cardChildren...),
				),
			),
		)
	}

	effect := func(eff Effect) Component {
		return ScreenEffect(eff)
	}

	bloomView := makeView("h bloom", func(cardRef *NodeRef) []Component {
		return []Component{
			effect(SEVignette().Dodge(cardRef).Smooth().Strength(
				In(Animate.From(0.0).Duration(cardIn).Ease(EaseOutQuad)(0.26)).
					Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(0.0)),
			)),
			effect(SESpinGlow(cardRef, palette...).
				Speed(1.18).
				Falloff(1.2).
				Radius(12).
				Strength(
					In(Animate.From(0.0).Duration(glowIn).Ease(EaseOutQuad)(0.34)).
						Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(0.0)),
				)),
		}
	})

	afterglowView := makeView("j afterglow", func(cardRef *NodeRef) []Component {
		return []Component{
			effect(SEVignette().Dodge(cardRef).Smooth().Strength(
				In(Animate.From(0.0).Duration(cardIn).Ease(EaseOutQuad)(0.36)).
					Out(Animate.Duration(glowOut).Ease(EaseOutQuad)(0.0)),
			)),
			effect(SESpinGlow(cardRef, palette...).
				Speed(
					In(spinSpeed).Out(
						Animate.From(spinSpeed).Duration(glowOut).Ease(EaseInQuad)(4.2),
					),
				).
				Falloff(spinFall).
				Radius(spinRadius).
				Strength(
					In(Animate.From(0.0).Duration(glowIn).Ease(EaseOutQuad)(&spinStrength)).
						Out(Animate.Duration(glowOut).Ease(EaseOutQuad)(0.0)),
				).
				Rim(useRim)),
		}
	})

	quietView := makeView("k quiet", func(cardRef *NodeRef) []Component {
		return []Component{
			effect(SEVignette().Dodge(cardRef).Smooth().Strength(
				In(Animate.From(0.0).Duration(cardIn).Ease(EaseOutQuad)(0.24)).
					Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(0.0)),
			)),
			effect(SETint(RGB(95, 145, 135)).Dodge(cardRef).Strength(
				In(Animate.From(0.0).Duration(glowIn).Ease(EaseOutQuad)(0.10)).
					Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(0.0)),
			)),
			effect(SEDesaturate().Dodge(cardRef).Strength(
				In(Animate.From(0.0).Duration(glowIn).Ease(EaseOutQuad)(0.18)).
					Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(0.0)),
			)),
		}
	})

	releaseView := makeView("l release", func(cardRef *NodeRef) []Component {
		return []Component{
			effect(SEVignette().Dodge(cardRef).Smooth().Strength(
				In(Animate.From(0.0).Duration(cardIn).Ease(EaseOutQuad)(0.42)).
					Out(Animate.Duration(1000 * time.Millisecond).Ease(EaseOutCubic)(0.0)),
			)),
			effect(SESpinGlow(cardRef, palette...).
				Speed(spinSpeed * 0.8).
				Falloff(
					In(5.0).Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(1.0)),
				).
				Radius(
					In(int16(5)).Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(int16(13))),
				).
				Strength(
					In(Animate.From(0.0).Duration(glowIn).Ease(EaseOutQuad)(0.52)).
						Out(Animate.Duration(glowOut).Ease(EaseOutCubic)(0.0)),
				).
				Rim(true)),
		}
	})

	trigger := func(_ riffkey.Match) {
		if state == stateIdle {
			state = stateSending
			stateStart = time.Now()
			cardVisible = true
		}
	}
	reset := func(_ riffkey.Match) {
		state = stateIdle
		cardVisible = false
	}
	switchTo := func(name, label string) func(riffkey.Match) {
		return func(_ riffkey.Match) {
			state = stateIdle
			cardVisible = false
			app.Go(name)
		}
	}
	attach := func(name string, view Component) {
		app.View(name, view).
			Handle("s", trigger).
			Handle("h", switchTo("bloom", "h bloom")).
			Handle("j", switchTo("afterglow", "j afterglow")).
			Handle("k", switchTo("quiet", "k quiet")).
			Handle("l", switchTo("release", "l release")).
			Handle("r", reset).
			Handle("q", func(_ riffkey.Match) { app.Stop() }).
			Handle("<C-c>", func(_ riffkey.Match) { app.Stop() })
	}

	attach("bloom", bloomView)
	attach("afterglow", afterglowView)
	attach("quiet", quietView)
	attach("release", releaseView)

	// per-render hook: drives the state machine and keeps strengthVal +
	// cardVisible in sync with state. Router.AddOnAfter only fires on
	// matched key events; OnAfterRender fires every frame, which is what
	// an animation needs.
	app.OnAfterRender(func() {
		tickState()
		cardVisible = showCard()
		updateStatus()
	})

	if err := app.RunFrom("afterglow"); err != nil {
		log.Fatal(err)
	}
}
