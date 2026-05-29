package omnibox

import (
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/theme"
)

func TestMaxVisibleRows(t *testing.T) {
	tests := []struct {
		name   string
		height int
		want   int
	}{
		{name: "small screens still show one command", height: 8, want: 1},
		{name: "medium screens use sixty percent budget", height: 40, want: 3},
		{name: "tall screens grow the command budget", height: 80, want: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maxVisibleRows(tt.height)
			if got != tt.want {
				t.Fatalf("maxVisibleRows(%d) = %d, want %d", tt.height, got, tt.want)
			}
		})
	}
}

func TestThemePickerPreviewsRevertsAndCommits(t *testing.T) {
	model := mailbox.NewUI(mailbox.UIConfig{
		App:   NewApp(),
		State: mailbox.NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	applied := []string{}
	saved := []string{}
	box := New(Config{
		App:   model.App,
		Theme: theme.Dark(),
		Model: model,
		ApplyTheme: func(name string, palette theme.Theme) {
			model.ThemeName = name
			model.ApplyTheme(palette)
			applied = append(applied, name)
		},
		SaveTheme: func(name string) {
			saved = append(saved, name)
		},
	})
	box.View()

	box.OpenThemePicker()
	renderOmnibox(box)
	if last(applied) != "dark" {
		t.Fatalf("theme picker initial preview = %q, want dark", last(applied))
	}
	box.move(1)
	if last(applied) != "light" {
		t.Fatalf("theme picker moved preview = %q, want light", last(applied))
	}
	box.Close()
	if last(applied) != "dark" || model.ThemeName != "dark" {
		t.Fatalf("theme picker cancel left theme = %q/%q, want dark", last(applied), model.ThemeName)
	}
	if len(saved) != 0 {
		t.Fatalf("theme picker saved on preview/cancel = %#v, want none", saved)
	}

	box.OpenThemePicker()
	renderOmnibox(box)
	box.move(1)
	box.exec()
	if last(applied) != "light" || model.ThemeName != "light" {
		t.Fatalf("theme picker commit left theme = %q/%q, want light", last(applied), model.ThemeName)
	}
	if last(saved) != "light" {
		t.Fatalf("theme picker saved = %#v, want light", saved)
	}
}

func TestThemePickerPreviewsWhenFilterListMovesSelection(t *testing.T) {
	model := mailbox.NewUI(mailbox.UIConfig{
		App:   NewApp(),
		State: mailbox.NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	applied := []string{}
	box := New(Config{
		App:   model.App,
		Theme: theme.Dark(),
		Model: model,
		ApplyTheme: func(name string, palette theme.Theme) {
			model.ThemeName = name
			model.ApplyTheme(palette)
			applied = append(applied, name)
		},
	})

	box.OpenThemePicker()
	renderOmnibox(box)
	box.list.SelectNext()

	if last(applied) != "light" {
		t.Fatalf("filter list selection preview = %q, want light", last(applied))
	}
}

func TestThemePickerIncludesMFDThemes(t *testing.T) {
	model := mailbox.NewUI(mailbox.UIConfig{
		App:   NewApp(),
		State: mailbox.NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	box := New(Config{
		App:   model.App,
		Theme: theme.Dark(),
		Model: model,
	})

	box.OpenThemePicker()
	renderOmnibox(box)

	if len(box.items) != len(theme.All()) {
		t.Fatalf("theme command count = %d, want %d", len(box.items), len(theme.All()))
	}
	if !hasThemeCommand(box.items, "mfd-flir-fusion") {
		t.Fatal("theme picker missing mfd-flir-fusion")
	}
	if !hasThemeCommand(box.items, "mfd-paper") {
		t.Fatal("theme picker missing mfd-paper")
	}
}

func TestCommandRowsFollowAppliedThemeAfterReopen(t *testing.T) {
	model := mailbox.NewUI(mailbox.UIConfig{
		App:   NewApp(),
		State: mailbox.NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	box := New(Config{
		App:   model.App,
		Theme: theme.Dark(),
		Model: model,
		ApplyTheme: func(name string, palette theme.Theme) {
			model.ThemeName = name
			model.ApplyTheme(palette)
		},
	})
	view := box.View()
	tmpl := Build(view)

	box.Open()
	buf := NewBuffer(100, 40)
	tmpl.Execute(buf, 100, 40)
	box.Close()

	light := theme.Light()
	box.applyTheme("light", light)
	box.Open()
	box.BeforeRender()
	buf = NewBuffer(100, 40)
	tmpl.Execute(buf, 100, 40)

	x, y := findText(buf, "Compose New")
	if x < 0 {
		t.Fatalf("rendered omnibox missing command row:\n%s", buf.String())
	}
	if got := buf.Get(x, y).Style.BG; got != light.SelBG {
		t.Fatalf("selected command row bg = %v, want light selected bg %v\n%s", got, light.SelBG, buf.String())
	}
}

func TestOpenResetsSelectionToFirstCommand(t *testing.T) {
	model := mailbox.NewUI(mailbox.UIConfig{
		App:   NewApp(),
		State: mailbox.NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	box := New(Config{
		App:   model.App,
		Theme: theme.Dark(),
		Model: model,
	})

	box.Open()
	renderOmnibox(box)
	box.move(1)
	if got := box.list.SelectedIndex(); got == 0 {
		t.Fatal("test setup failed: selection did not move from first command")
	}

	box.Close()
	box.Open()
	box.BeforeRender()

	if got := box.list.SelectedIndex(); got != 0 {
		t.Fatalf("reopened command selection = %d, want 0", got)
	}
}

func TestQueryChangeResetsSelectionToFirstMatch(t *testing.T) {
	model := mailbox.NewUI(mailbox.UIConfig{
		App:   NewApp(),
		State: mailbox.NewState(nil, "test@example.com"),
		Theme: theme.Dark(),
	})
	box := New(Config{
		App:   model.App,
		Theme: theme.Dark(),
		Model: model,
	})

	box.Open()
	renderOmnibox(box)
	box.move(1)
	if got := box.list.SelectedIndex(); got == 0 {
		t.Fatal("test setup failed: selection did not move from first command")
	}

	box.list.SetQuery("theme")
	box.BeforeRender()

	if got, want := box.list.Selected(), firstFilteredCommand(box); got != want {
		t.Fatalf("filtered command selection = %v, want first match %v", got, want)
	}
}

func hasThemeCommand(items []command, key string) bool {
	for _, item := range items {
		if item.Key == key {
			return true
		}
	}
	return false
}

func renderOmnibox(box *OmniBox) {
	buf := NewBuffer(100, 40)
	Build(box.View()).Execute(buf, 100, 40)
}

func firstFilteredCommand(box *OmniBox) *command {
	if box.list == nil || box.list.Filter().Len() == 0 {
		return nil
	}
	return box.list.Filter().Items[0]
}

func findText(buf *Buffer, text string) (int, int) {
	for y := 0; y < buf.Height(); y++ {
		line := buf.GetLine(y)
		for x := 0; x+len(text) <= len(line); x++ {
			if line[x:x+len(text)] == text {
				return x, y
			}
		}
	}
	return -1, -1
}

func last(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1]
}
