package compose

import (
	"strings"

	. "github.com/kungfusheep/glyph"
)

// bindingLabel converts a snake_case binding name to a Title Case display label.
func bindingLabel(name string) string {
	words := strings.Split(name, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// BuildHelpItems generates omnibox items from the app's keybindings and editor grammar
func BuildHelpItems(app *App, ed *Editor) []OmniboxItem {
	bindings := app.Router().Bindings()

	items := make([]OmniboxItem, 0, len(bindings))
	for _, b := range bindings {
		pattern := b.Pattern
		items = append(items, OmniboxItem{
			Icon:        "󰌌",
			Label:       bindingLabel(b.Name),
			Description: pattern,
			Action:      func() {},
		})
	}

	// grammar: edit operators (d/c/y + text object)
	for _, op := range EditOperators {
		items = append(items, OmniboxItem{
			Icon:        "󰒅",
			Label:       op.Name + " (operator)",
			Description: op.Key + " + text object",
			Action:      func() {},
		})
	}

	// grammar: style operators (gb/gi/gu/gs/gc + text object)
	for _, op := range StyleOperators {
		items = append(items, OmniboxItem{
			Icon:        "󰒅",
			Label:       op.Name + " (operator)",
			Description: op.Key + " + text object",
			Action:      func() {},
		})
	}

	// grammar: text objects (skip aliases — empty Name)
	for _, obj := range TextObjects {
		if obj.Name == "" {
			continue
		}
		items = append(items, OmniboxItem{
			Icon:        "󰊗",
			Label:       obj.Name,
			Description: obj.Key,
			Action:      func() {},
		})
	}

	return items
}
