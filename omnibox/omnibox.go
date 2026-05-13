package omnibox

import (
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/theme"
)

type Config struct {
	App   *App
	Theme theme.Theme
	Model *mailbox.UI
}

type OmniBox struct {
	app *App
	t   theme.Theme
	cfg Config

	open    bool
	empty   bool
	maxRows int
	items   []command
	list    *FilterListC[command]
	ref     NodeRef
}

func New(cfg Config) *OmniBox {
	box := &OmniBox{
		app:   cfg.App,
		t:     cfg.Theme,
		cfg:   cfg,
		empty: true,
	}
	box.items = buildCommands(box.actions())

	size := cfg.App.Size()
	box.Resize(size.Width, size.Height)
	box.refresh()

	return box
}

func (b *OmniBox) Open() {
	if b.open {
		return
	}
	if b.list != nil {
		b.list.Clear()
	}
	b.refresh()
	b.open = true
	b.app.HideCursor()
	b.app.RequestRender()
}

func (b *OmniBox) Close() {
	if !b.open {
		return
	}
	if b.list != nil {
		b.list.Clear()
	}
	b.refresh()
	b.open = false
	b.app.HideCursor()
	b.app.RequestRender()
}

func (b *OmniBox) BeforeRender() {
	if b.open {
		b.refresh()
	}
}

func (b *OmniBox) Resize(width, height int) {
	b.maxRows = maxVisibleRows(height)
	if b.list != nil {
		b.list.MaxVisible(b.maxRows)
	}
	_ = width
}

func (b *OmniBox) View() Component {
	b.list = FilterList(&b.items, commandSearchText).
		Placeholder("type a command").
		MaxVisible(b.maxRows).
		Marker("  ").
		Style(Style{BG: b.t.BG}).
		SelectedStyle(Style{FG: b.t.Bright, BG: b.t.SelBG}).
		Render(func(cmd *command) Component {
			return VBox.PaddingVH(1, 2)(
				HBox(
					Text(&cmd.Label).FG(b.t.Bright),
					Space(),
					Text(&cmd.Key).FG(b.t.Subtle),
				),
				HBox(
					Text(&cmd.Section).FG(b.t.Accent).Width(12),
					Text(&cmd.Description).FG(b.t.Subtle),
				),
			)
		})
	b.refresh()

	return If(&b.open).Then(
		Overlay.Centered()(
			VBox.
				Width(86).
				FitContent().
				Fill(b.t.BG).
				PaddingTRBL(1, 2, 1, 2).
				Opacity(In(1).Out(Animate.Duration(500*time.Millisecond)(0.0))).
				NodeRef(&b.ref)(
				On.Modal(
					Key("<CR>", b.exec),
					Key("<Enter>", b.exec),
					Key("<Esc>", b.Close),
					Key("<C-c>", b.Close),
					Key("<C-j>", func() { b.move(1) }),
					Key("<Down>", func() { b.move(1) }),
					Key("<Tab>", func() { b.move(1) }),
					Key("<C-n>", func() { b.move(1) }),
					Key("<C-k>", func() { b.move(-1) }),
					Key("<Up>", func() { b.move(-1) }),
					Key("<S-Tab>", func() { b.move(-1) }),
					Key("<C-p>", func() { b.move(-1) }),
					Key("<C-d>", func() { b.page(1) }),
					Key("<C-u>", func() { b.page(-1) }),
					Key("<C-g>", b.first),
					Key("<C-G>", b.last),
				),
				HBox(
					Text("mail").FG(b.t.Bright).Bold(),
					SpaceW(1),
					Text("commands").FG(b.t.Subtle),
					Space(),
					Text("j/k").FG(b.t.Muted),
					SpaceW(2),
					Text("<esc>").FG(b.t.Muted),
				),
				SpaceH(1),
				b.list,
				If(&b.empty).Then(
					VBox.Fill(b.t.BG).PaddingTRBL(1, 1, 1, 1)(
						Text("no commands").FG(b.t.Subtle),
					),
				),
				ScreenEffect(
					SEDropShadow().Focus(&b.ref).Strength(0.3),
					SEVignette().Smooth().Dodge(&b.ref).Strength(
						In(Animate(0.3)).Out(Animate.Duration(500*time.Millisecond)(0.0)),
					),
				),
			),
		),
	)
}

func (b *OmniBox) refresh() {
	b.empty = b.list == nil || b.list.Filter().Len() == 0
}

func (b *OmniBox) exec() {
	if b.list == nil {
		return
	}
	cmd := b.list.Selected()
	if cmd == nil {
		return
	}
	action := cmd.Action
	b.Close()
	if action != nil {
		action()
	}
}

func (b *OmniBox) move(delta int) {
	if b.list == nil || b.list.Filter().Len() == 0 {
		return
	}
	before := b.list.Selected()
	if delta > 0 {
		b.list.SelectNext()
		if b.list.Selected() == before {
			for range b.list.Filter().Len() {
				b.list.SelectPrev()
			}
		}
		return
	}
	b.list.SelectPrev()
	if b.list.Selected() == before {
		for range b.list.Filter().Len() {
			b.list.SelectNext()
		}
	}
}

func (b *OmniBox) page(delta int) {
	if b.list == nil || b.list.Filter().Len() == 0 {
		return
	}
	if delta > 0 {
		b.list.PageDown()
		return
	}
	b.list.PageUp()
}

func (b *OmniBox) first() {
	if b.list == nil {
		return
	}
	for range b.list.Filter().Len() {
		b.list.SelectPrev()
	}
}

func (b *OmniBox) last() {
	if b.list == nil {
		return
	}
	for range b.list.Filter().Len() {
		b.list.SelectNext()
	}
}

func (b *OmniBox) actions() commandActions {
	model := b.cfg.Model
	return commandActions{
		ComposeNew:    model.ComposeNew,
		ResumeDraft:   model.ResumeDraft,
		ReplySelected: model.ReplySelected,
		RefreshMail:   model.RefreshMail,
		ToggleFolders: model.ToggleFolders,
		FocusFolders:  model.FocusFolders,
		FocusThreads:  model.FocusThreads,
		FocusPreview:  model.FocusPreview,
		SearchMail:    model.StartSearch,
		OpenSelected:  model.Enter,
		ArchiveSelected: func() {
			model.ThreadAction("archive", model.Archive)
		},
		DeleteSelected: func() {
			model.ThreadAction("delete", model.Delete)
		},
		ToggleStar: func() {
			model.ThreadAction("star", model.ToggleStar)
		},
		ToggleRead: func() {
			model.ThreadAction("read", model.ToggleRead)
		},
		UndoLast:         model.UndoLast,
		ShowKeyboardHelp: model.ShowKeyboardHelp,
		Quit:             func() { b.app.Stop() },
	}
}

func commandSearchText(cmd *command) string {
	return cmd.Label + " " + cmd.Description + " " + cmd.Key + " " + cmd.Section
}

func maxVisibleRows(height int) int {
	h := height * 6 / 10
	if h < 10 {
		h = 10
	}
	if h > height-4 {
		h = height - 4
	}
	if h < 6 {
		h = 6
	}

	rows := (h - 6) / 5
	if rows < 1 {
		return 1
	}
	return rows
}
