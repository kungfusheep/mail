package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/preview"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/ui"
)

func TestPreviewBodyStyleUsesReadableHierarchy(t *testing.T) {
	palette := theme.Dark()

	cases := []struct {
		name string
		kind preview.BlockKind
		want Style
	}{
		{name: "heading", kind: preview.BlockHeading, want: Style{FG: palette.Bright, Attr: AttrBold}},
		{name: "body", kind: preview.BlockParagraph, want: Style{FG: palette.FG}},
		{name: "quote", kind: preview.BlockQuote, want: Style{FG: palette.Subtle, Attr: AttrItalic}},
		{name: "image", kind: preview.BlockImage, want: Style{FG: palette.Dim, Attr: AttrDim | AttrItalic}},
		{name: "divider", kind: preview.BlockDivider, want: Style{FG: palette.Muted, Attr: AttrDim}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := previewBodyStyle(tc.kind, palette); !got.Equal(tc.want) {
				t.Fatalf("previewBodyStyle = %#v, want %#v", got, tc.want)
			}
		})
	}
}

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

func TestPreviewTemplateRendersAttachmentsAboveBody(t *testing.T) {
	palette := theme.Dark()
	messages := []mailbox.ConversationMessage{{
		Sender: "Alice",
		Date:   "13 May 09:30",
		Attachments: []mailbox.AttachmentRow{{
			Icon:     "󰈦",
			Filename: "brief.pdf",
			Metadata: "pdf · 150 KB",
			Display: []Span{
				{Text: "󰈦"},
				{Text: " "},
				{Text: "brief.pdf", Style: Style{Attr: AttrBold}},
				{Text: "  "},
				{Text: "pdf · 150 KB", Style: Style{Attr: AttrDim}},
			},
		}},
		HasAttachments: true,
		BodySpans:      []Span{{Text: "Body starts here"}},
	}}
	view := VBox(
		ForEach(&messages, func(msg *mailbox.ConversationMessage) Component {
			return VBox(
				HBox(
					Text(&msg.Sender),
					SpaceW(1),
					Text(&msg.Date),
				),
				If(&msg.HasAttachments).Then(
					VBox.Gap(1)(
						SpaceH(1),
						ForEach(&msg.Attachments, func(attachment *mailbox.AttachmentRow) Component {
							return HBox.Border(BorderSoft).BorderFG(palette.GroupBG).Fill(attachmentFill(&attachment.Filename, palette)).PaddingVH(0, 2)(
								Rich(&attachment.Display),
							)
						}),
					),
				),
				SpaceH(1),
				Rich(&msg.BodySpans),
			)
		}),
	)

	buf := NewBuffer(40, 10)
	Build(view).Execute(buf, 40, 10)

	rendered := buf.String()
	for _, want := range []string{"brief.pdf", "pdf · 150 KB", "Body starts here"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered preview = %q, want to contain %q", rendered, want)
		}
	}
	if strings.Index(rendered, "brief.pdf") >= strings.Index(rendered, "Body starts here") {
		t.Fatalf("rendered preview = %q, want attachment before body", rendered)
	}
	if strings.Index(rendered, "brief.pdf") >= strings.Index(rendered, "pdf · 150 KB") {
		t.Fatalf("rendered preview = %q, want attachment metadata after filename", rendered)
	}
	wantFill := ReadableTint(palette.BG, Hex(0xd65f5f), palette.Bright, 4.5, 0.40)
	if !bufferHasBG(buf, wantFill) {
		t.Fatalf("rendered preview missing pdf fill tone")
	}
	if got := ContrastRatio(palette.Bright, wantFill); got < 4.5 {
		t.Fatalf("pdf fill contrast = %.2f, want >= 4.5", got)
	}
}

func TestThreadAttachmentChipBorderMatchesSelectedRowBG(t *testing.T) {
	palette := theme.Dark()
	rows := []mailbox.ThreadRow{
		{
			Label:          "receipt",
			Sender:         "sales@example.com",
			Date:           "2h",
			Selected:       true,
			HasAttachments: true,
			Attachments: []mailbox.AttachmentChip{{
				Icon:     "󰈦",
				Filename: "invoice.pdf",
				FillName: "invoice.pdf",
			}},
		},
		{
			Label:          "normal",
			Sender:         "sales@example.com",
			Date:           "3h",
			Selected:       false,
			HasAttachments: true,
			Attachments: []mailbox.AttachmentChip{{
				Icon:     "󰈦",
				Filename: "normal.pdf",
				FillName: "normal.pdf",
			}},
		},
	}
	view := VBox.Fill(palette.ThreadBG).Width(42)(
		ForEach(&rows, func(row *mailbox.ThreadRow) Component {
			itemBG := If(&row.Selected).Then(palette.SelBG).Else(palette.ThreadBG)
			return VBox.Fill(itemBG).PaddingVH(1, 2)(
				HBox(
					Text(&row.Label),
					SpaceW(2),
					Text(&row.Date),
				),
				HBox(
					Text(&row.Sender),
				),
				If(&row.HasAttachments).Then(
					HBox.Gap(1).Fill(itemBG).PaddingTRBL(0, 0, 0, 2)(
						ForEach(&row.Attachments, func(chip *mailbox.AttachmentChip) Component {
							return HBox.Width(22).Border(BorderSoft).BorderFG(
								If(&row.Selected).Then(palette.SelBG).Else(palette.ThreadBG),
							).Fill(attachmentFill(&chip.FillName, palette)).PaddingVH(0, 1)(
								Text(&chip.Icon),
								SpaceW(1),
								Text(&chip.Filename),
							)
						}),
					),
				),
			)
		}),
	)

	buf := NewBuffer(42, 14)
	Build(view).Execute(buf, 42, 14)

	x, y := findFirstRune(buf, '▀')
	if x < 0 {
		t.Fatalf("rendered chip has no soft top border:\n%s", buf.String())
	}
	border := buf.Get(x, y)
	if border.Style.FG != palette.SelBG {
		t.Fatalf("selected chip border fg = %v, want selected row bg %v\n%s", border.Style.FG, palette.SelBG, buf.String())
	}
	if got := buf.Get(0, y+1).Style.BG; got != palette.SelBG {
		t.Fatalf("selected chip row gutter bg = %v, want selected row bg %v\n%s", got, palette.SelBG, buf.String())
	}
	x, y = findRuneAfter(buf, '▀', y+1)
	if x < 0 {
		t.Fatalf("rendered second chip has no soft top border:\n%s", buf.String())
	}
	if got := buf.Get(x, y).Style.FG; got != palette.ThreadBG {
		t.Fatalf("normal chip border fg = %v, want thread bg %v\n%s", got, palette.ThreadBG, buf.String())
	}
}

func TestThreadAttachmentChipsStayInsideNarrowRow(t *testing.T) {
	palette := theme.Dark()
	row := mailbox.ThreadRow{
		Label:          "receipt",
		Sender:         "sales@example.com",
		Date:           "2h",
		Selected:       true,
		HasAttachments: true,
		Attachments: []mailbox.AttachmentChip{
			{Icon: "󰈦", Filename: "invoice.pdf", FillName: "invoice.pdf"},
			{Icon: "󰈦", Filename: "receipt.pdf", FillName: "receipt.pdf"},
		},
	}
	itemBG := If(&row.Selected).Then(palette.SelBG).Else(palette.ThreadBG)
	view := VBox.Fill(palette.ThreadBG).Width(28)(
		VBox.Fill(itemBG).PaddingVH(1, 2)(
			Text(&row.Label),
			If(&row.HasAttachments).Then(
				HBox.Gap(1).Fill(itemBG).PaddingTRBL(0, 0, 0, 2)(
					ForEach(&row.Attachments, func(chip *mailbox.AttachmentChip) Component {
						return HBox.Width(22).Border(BorderSoft).BorderFG(
							If(&row.Selected).Then(palette.SelBG).Else(palette.ThreadBG),
						).Fill(attachmentFill(&chip.FillName, palette)).PaddingVH(0, 1)(
							Text(&chip.Icon),
							SpaceW(1),
							Text(&chip.Filename),
						)
					}),
				),
			),
		),
	)

	buf := NewBuffer(42, 8)
	Build(view).Execute(buf, 42, 8)

	for y := 0; y < buf.Height(); y++ {
		for x := 28; x < buf.Width(); x++ {
			if got := buf.Get(x, y).Style.BG; got != (Color{}) {
				t.Fatalf("cell %d,%d bg = %v, want default outside narrow row\n%s", x, y, got, buf.String())
			}
		}
	}
}

func bufferHasBG(buf *Buffer, want Color) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if buf.Get(x, y).Style.BG == want {
				return true
			}
		}
	}
	return false
}

func findFirstRune(buf *Buffer, want rune) (int, int) {
	return findRuneAfter(buf, want, 0)
}

func findRuneAfter(buf *Buffer, want rune, startY int) (int, int) {
	for y := startY; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if buf.Get(x, y).Rune == want {
				return x, y
			}
		}
	}
	return -1, -1
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
