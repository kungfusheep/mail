package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/ui"
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

func TestNotificationRowsRightAlignText(t *testing.T) {
	notifications := []ui.Notification{
		{Text: "short", Kind: ui.NotificationInfo, Opacity: 1},
		{Text: "much longer notification", Kind: ui.NotificationError, Opacity: 1},
	}
	palette := theme.Dark()
	view := VBox.Width(49).Gap(1)(
		ForEach(&notifications, func(item *ui.Notification) Component {
			return notificationRow(item, palette)
		}),
	)

	buf := NewBuffer(49, 3)
	Build(view).Execute(buf, 49, 3)

	shortEnd := findLastRuneX(buf, 0, 't')
	longEnd := findLastRuneX(buf, 1, 'n')
	if shortEnd != 48 || longEnd != 48 {
		t.Fatalf("line ends = short:%d long:%d\n%s", shortEnd, longEnd, buf.String())
	}
	shortBullet := findLastRuneX(buf, 0, '●')
	longBullet := findLastRuneX(buf, 1, '●')
	if shortBullet != 49-StringWidth("● short") || longBullet != 49-StringWidth("● much longer notification") {
		t.Fatalf("bullet did not hug right-aligned text: short:%d long:%d\n%s", shortBullet, longBullet, buf.String())
	}
	if got := buf.Get(shortBullet, 0).Style.FG; got != palette.Info {
		t.Fatalf("info bullet colour = %v, want %v", got, palette.Info)
	}
	if got := buf.Get(longBullet, 1).Style.FG; got != palette.Error {
		t.Fatalf("error bullet colour = %v, want %v", got, palette.Error)
	}
}

func findLastRuneX(buf *Buffer, y int, r rune) int {
	last := -1
	for x := 0; x < buf.Width(); x++ {
		if buf.Get(x, y).Rune == r {
			last = x
		}
	}
	return last
}
