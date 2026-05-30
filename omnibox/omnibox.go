package omnibox

import (
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/theme"
)

type Config struct {
	App        *App
	Theme      theme.Theme
	Model      *mailbox.UI
	ApplyTheme func(name string, palette theme.Theme)
	SaveTheme  func(name string)
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

	mode            string
	title           string
	lastQuery       string
	themeBeforeName string
	themeBefore     theme.Theme
	themeCommitted  bool
}

func New(cfg Config) *OmniBox {
	box := &OmniBox{
		app:   cfg.App,
		t:     cfg.Theme,
		cfg:   cfg,
		empty: true,
		mode:  "commands",
		title: "commands",
	}
	box.items = buildCommands(box.actions())

	size := cfg.App.Size()
	box.Resize(size.Width, size.Height)
	box.refresh()

	return box
}

func (b *OmniBox) Ref() *NodeRef {
	return &b.ref
}

func (b *OmniBox) Open() {
	if b.open {
		return
	}
	b.mode = "commands"
	b.title = "commands"
	b.items = buildCommands(b.actions())
	b.lastQuery = ""
	if b.list != nil {
		b.list.Clear()
		b.list.Refresh()
		b.resetSelection()
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
	if b.mode == "themes" && !b.themeCommitted {
		b.applyTheme(b.themeBeforeName, b.themeBefore)
	}
	if b.list != nil {
		b.list.Clear()
		b.resetSelection()
	}
	b.lastQuery = ""
	b.refresh()
	b.open = false
	b.mode = "commands"
	b.title = "commands"
	b.themeCommitted = false
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
	model := b.cfg.Model
	b.list = FilterList(&b.items, commandSearchText).
		Placeholder("type a command").
		MaxVisible(b.maxRows).
		Marker("  ").
		Style(Style{BG: model.BG}).
		SelectedStyle(Style{FG: model.Bright, BG: model.SelBG}).
		OnSelect(func(cmd *command) {
			b.previewCommand(cmd)
		}).
		Render(func(cmd *command) Component {
			return VBox.PaddingVH(1, 2)(
				HBox(
					Text(&cmd.Label).FG(&model.Bright),
					Space(),
					Text(&cmd.Key).FG(&model.Subtle),
				),
				HBox(
					Text(&cmd.Section).FG(&model.Accent).Width(12),
					Text(&cmd.Description).FG(&model.Subtle),
				),
			)
		})
	b.refresh()

	return If(&b.open).Then(
		Overlay.Centered()(
			VBox.
				Width(86).
				FitContent().
				Fill(&model.BG).
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
					Text("mail").FG(&model.Bright).Bold(),
					SpaceW(1),
					Text(&b.title).FG(&model.Subtle),
					Space(),
					Text("j/k").FG(&model.Muted),
					SpaceW(2),
					Text("<esc>").FG(&model.Muted),
				),
				SpaceH(1),
				b.list,
				If(&b.empty).Then(
					VBox.Fill(&model.BG).PaddingTRBL(1, 1, 1, 1)(
						Text("no commands").FG(&model.Subtle),
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
	if b.list != nil {
		query := b.list.Query()
		if query != b.lastQuery {
			b.resetSelection()
			b.lastQuery = query
			b.previewSelected()
		}
		b.list.Style(Style{BG: b.cfg.Model.BG})
		b.list.SelectedStyle(Style{FG: b.cfg.Model.Bright, BG: b.cfg.Model.SelBG})
	}
}

func (b *OmniBox) resetSelection() {
	if b.list == nil {
		return
	}
	for range b.list.Filter().Len() {
		b.list.SelectPrev()
	}
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
	if b.mode == "themes" {
		b.themeCommitted = true
	}
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
		b.previewSelected()
		return
	}
	b.list.SelectPrev()
	if b.list.Selected() == before {
		for range b.list.Filter().Len() {
			b.list.SelectNext()
		}
	}
	b.previewSelected()
}

func (b *OmniBox) page(delta int) {
	if b.list == nil || b.list.Filter().Len() == 0 {
		return
	}
	if delta > 0 {
		b.list.PageDown()
		b.previewSelected()
		return
	}
	b.list.PageUp()
	b.previewSelected()
}

func (b *OmniBox) first() {
	if b.list == nil {
		return
	}
	for range b.list.Filter().Len() {
		b.list.SelectPrev()
	}
	b.previewSelected()
}

func (b *OmniBox) last() {
	if b.list == nil {
		return
	}
	for range b.list.Filter().Len() {
		b.list.SelectNext()
	}
	b.previewSelected()
}

func (b *OmniBox) actions() commandActions {
	model := b.cfg.Model
	moveTargets := []moveTarget{}
	for _, folder := range model.MoveTargetFolders() {
		moveTargets = append(moveTargets, moveTarget{
			ID:   folder.ID,
			Name: folder.Name,
		})
	}
	return commandActions{
		ComposeNew:       model.ComposeNew,
		ResumeDraft:      model.ResumeDraft,
		ReplySelected:    model.ReplySelected,
		ReplyAllSelected: model.ReplyAllSelected,
		ForwardSelected:  model.ForwardSelected,
		RefreshMail:      model.RefreshMail,
		ToggleFolders:    model.ToggleFolders,
		FocusFolders:     model.FocusFolders,
		FocusThreads:     model.FocusThreads,
		FocusPreview:     model.FocusPreview,
		SearchMail:       model.StartSearch,
		LoadMoreThreads: func() {
			model.LoadMoreThreads()
		},
		OpenSelected: model.Enter,
		ArchiveSelected: func() {
			model.ThreadAction("archive", model.Archive)
		},
		DeleteSelected: func() {
			model.ThreadAction("delete", model.Delete)
		},
		SpamSelected: func() {
			model.ThreadAction("spam", model.Spam)
		},
		SnoozeSelected: func() {
			model.ThreadAction("snooze", model.SnoozeTomorrow)
		},
		MoveTargets: moveTargets,
		MoveSelectedTo: func(folderID, folderName string) {
			model.MoveSelectedToFolder(folderID, folderName)
		},
		ToggleStar: func() {
			model.ThreadAction("star", model.ToggleStar)
		},
		ToggleRead: func() {
			model.ThreadAction("read", model.ToggleRead)
		},
		CopySender:       model.CopySenderAddress,
		UndoLast:         model.UndoLast,
		ShowKeyboardHelp: model.ShowKeyboardHelp,
		SwitchTheme:      b.OpenThemePicker,
		Quit:             func() { b.app.Stop() },
	}
}

func (b *OmniBox) OpenThemePicker() {
	model := b.cfg.Model
	b.mode = "themes"
	b.title = "themes"
	b.themeBeforeName = model.ThemeName
	b.themeBefore = model.Theme
	b.themeCommitted = false
	b.items = b.themeCommands()
	b.lastQuery = ""
	if b.list != nil {
		b.list.Clear()
		b.list.Refresh()
		b.resetSelection()
	}
	b.refresh()
	b.open = true
	b.previewSelected()
	b.app.HideCursor()
	b.app.RequestRender()
}

func (b *OmniBox) themeCommands() []command {
	themes := theme.All()
	commands := make([]command, 0, len(themes))
	for _, named := range themes {
		commands = append(commands, b.themeCommand(named))
	}
	return commands
}

func (b *OmniBox) themeCommand(named theme.NamedTheme) command {
	return command{
		Label:       named.Label,
		Description: "preview and apply the " + named.Name + " palette",
		Key:         named.Name,
		Section:     "theme",
		Preview: func() {
			b.applyTheme(named.Name, named.Palette)
		},
		Action: func() {
			b.applyTheme(named.Name, named.Palette)
			if b.cfg.SaveTheme != nil {
				b.cfg.SaveTheme(named.Name)
			}
		},
	}
}

func (b *OmniBox) previewSelected() {
	if b.mode != "themes" || b.list == nil {
		return
	}
	b.previewCommand(b.list.Selected())
}

func (b *OmniBox) previewCommand(cmd *command) {
	if b.mode != "themes" || cmd == nil || cmd.Preview == nil {
		return
	}
	cmd.Preview()
	b.app.RequestRender()
}

func (b *OmniBox) applyTheme(name string, palette theme.Theme) {
	if b.cfg.ApplyTheme != nil {
		b.cfg.ApplyTheme(name, palette)
		return
	}
	b.cfg.Model.ThemeName = name
	b.cfg.Model.ApplyTheme(palette)
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
