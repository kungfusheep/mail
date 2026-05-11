package helpdialog

import (
	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/theme"
)

type row struct {
	key  string
	desc string
}

var mailboxNavigationRows = []row{
	{"j / k", "up / down"},
	{"h / l", "pane left / right"},
	{"tab", "next pane"},
	{"enter", "open"},
	{"o", "expand thread"},
	{"/", "search"},
}

var mailboxActionRows = []row{
	{"c", "compose"},
	{"C", "resume draft"},
	{"r", "reply"},
	{"a", "archive"},
	{"d", "delete"},
	{"s", "star"},
	{"e", "toggle read"},
	{"u", "undo"},
}

func Mailbox(open *bool, ref *NodeRef, t theme.Theme) Component {
	return If(open).Then(
		Overlay.Centered()(
			VBox.
				Width(56).
				Fill(t.BG).
				PaddingVH(1, 2).
				NodeRef(ref).
				Opacity(
					In(Animate(1.0)).Out(Animate(0)),
				).
				Gap(1)(
				Text("keyboard").FG(t.Bright).Bold(),
				HBox(
					section("navigate", 3, 8, &mailboxNavigationRows, t),
					section("actions", 2, 3, &mailboxActionRows, t),
				),
				ScreenEffect(
					SEVignette().Strength(
						In(
							Animate.From(0)(0.55),
						).Out(
							Animate(0),
						),
					).Dodge(ref).Smooth(),
					SEDropShadow().Focus(ref),
				),
			),
		),
	)
}

func section(title string, grow int, keyWidth int, rows *[]row, t theme.Theme) Component {
	return VBox.Grow(grow)(
		Text(title).FG(t.Subtle),
		ForEach(rows, func(r *row) Component {
			return HBox.Gap(2)(
				Text(&r.key).FG(t.FG).Width(keyWidth),
				Text(&r.desc).FG(t.Subtle),
			)
		}),
	)
}
