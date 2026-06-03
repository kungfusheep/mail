package composeview

import (
	"fmt"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/omnibox"
	"github.com/kungfusheep/riffkey"
)

func registerEditorOmnibox(app *App, box *omnibox.OmniBox, router *riffkey.Router, ed *compose.Editor, notify func(string), onChange func()) {
	if box == nil || router == nil || ed == nil {
		return
	}
	router.Handle("z=", func(_ riffkey.Match) {
		openSpellSuggestions(app, box, ed, notify, onChange)
	})
	for _, def := range compose.BlockMatchers() {
		def := def
		router.Handle("gm"+def.Key, func(_ riffkey.Match) {
			openBlockMap(app, box, ed, def, notify)
		})
	}
	for _, def := range compose.TextMatchers() {
		def := def
		router.Handle("gm"+def.Key, func(_ riffkey.Match) {
			openTextMap(app, box, ed, def, notify)
		})
	}
}

func editorOmniboxView(box *omnibox.OmniBox) Component {
	if box == nil {
		return Space()
	}
	return box.View()
}

func openSpellSuggestions(app *App, box *omnibox.OmniBox, ed *compose.Editor, notify func(string), onChange func()) {
	if !ed.HasSpellChecker() {
		notify("spell suggestions unavailable")
		return
	}
	word := ed.WordAtCursor()
	if word == "" {
		notify("no word under cursor")
		return
	}
	suggestions := ed.GetSuggestions(word)
	if len(suggestions) == 0 {
		notify("no suggestions for " + word)
		return
	}
	if len(suggestions) > 10 {
		suggestions = suggestions[:10]
	}
	items := make([]omnibox.Item, 0, len(suggestions))
	for i, suggestion := range suggestions {
		suggestion := suggestion
		items = append(items, omnibox.Item{
			Label:       fmt.Sprintf("%d. %s", i+1, suggestion),
			Description: "replace " + word,
			Key:         "z=",
			Section:     "compose",
			Action: func() {
				ed.ReplaceWordAtCursor(suggestion)
				ed.Refresh()
				if onChange != nil {
					onChange()
				}
				app.RequestRender()
			},
		})
	}
	box.OpenItems("suggestions for "+word, items, nil)
}

func openBlockMap(app *App, box *omnibox.OmniBox, ed *compose.Editor, def compose.BlockMatcherDef, notify func(string)) {
	entries := ed.AllBlocksMatching(def.Matcher)
	if len(entries) == 0 {
		notify("no " + def.Name + " blocks")
		return
	}
	original := ed.Cursor()
	items := make([]omnibox.Item, 0, len(entries))
	for _, entry := range entries {
		entry := entry
		jump := func() {
			ed.GotoBlock(entry.BlockIdx)
			ed.Refresh()
			app.RequestRender()
		}
		items = append(items, omnibox.Item{
			Label:   entry.Text,
			Key:     "gm" + def.Key,
			Section: "compose",
			Preview: jump,
			Action:  jump,
		})
	}
	box.OpenItems(def.Icon+" "+def.Name, items, func(committed bool) {
		if committed {
			return
		}
		ed.SetCursor(original)
		ed.Refresh()
		app.RequestRender()
	})
}

func openTextMap(app *App, box *omnibox.OmniBox, ed *compose.Editor, def compose.TextMatcherDef, notify func(string)) {
	entries := ed.AllTextMatching(def.Matcher)
	if len(entries) == 0 {
		notify("no " + def.Name + " matches")
		return
	}
	original := ed.Cursor()
	items := make([]omnibox.Item, 0, len(entries))
	for _, entry := range entries {
		entry := entry
		jump := func() {
			ed.SetCursor(compose.Pos{Block: entry.BlockIdx, Col: entry.Col})
			ed.Refresh()
			app.RequestRender()
		}
		items = append(items, omnibox.Item{
			Label:   entry.Text,
			Key:     "gm" + def.Key,
			Section: "compose",
			Preview: jump,
			Action:  jump,
		})
	}
	box.OpenItems(def.Icon+" "+def.Name, items, func(committed bool) {
		if committed {
			return
		}
		ed.SetCursor(original)
		ed.Refresh()
		app.RequestRender()
	})
}
