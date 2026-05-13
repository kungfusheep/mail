package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/mailbox"
)

func TestPreviewTemplateRendersConversationSpans(t *testing.T) {
	messages := []mailbox.ConversationMessage{{
		Sender: "Alice",
		Date:   "13 May 09:30",
		BodySpans: []Span{
			{Text: "Receipt", Style: Style{Attr: AttrBold}},
			{Text: "\n\n"},
			{Text: "Hello "},
			{Text: "Alex", Style: Style{Attr: AttrBold}},
			{Text: "\n\n"},
			{Text: "old reply", Style: Style{Attr: AttrDim | AttrItalic}},
		},
	}}

	view := VBox(
		ForEach(&messages, func(msg *mailbox.ConversationMessage) Component {
			return VBox(
				HBox(
					Text(&msg.Sender),
					SpaceW(1),
					Text(&msg.Date),
				),
				SpaceH(1),
				Rich(&msg.BodySpans),
				SpaceH(1),
			)
		}),
	)

	buf := NewBuffer(40, 12)
	Build(view).Execute(buf, 40, 12)

	rendered := buf.String()
	for _, want := range []string{"Alice", "Receipt", "Hello Alex", "old reply"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered preview = %q, want to contain %q", rendered, want)
		}
	}
	if strings.Index(rendered, "Receipt") >= strings.Index(rendered, "old reply") {
		t.Fatalf("rendered preview = %q, want body content in order", rendered)
	}
}
