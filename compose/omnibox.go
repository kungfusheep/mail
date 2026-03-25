package compose

import (
	"fmt"
	"strings"
	"github.com/kungfusheep/glyph"
	"unicode/utf8"
)

// OmniboxItem represents an item in the omnibox results
type OmniboxItem struct {
	Label       string
	Description string
	Icon        string // optional icon/prefix
	Display     string // pre-formatted display string for SelectionList
	Action      func()
}

// Omnibox is a modern command palette overlay
type Omnibox struct {
	Query     string        // exported for reactive binding
	items     []OmniboxItem // source items
	Filtered  []OmniboxItem // exported for reactive binding
	Selected  int           // exported for reactive binding
	Visible   bool          // exported for reactive binding
	maxHeight int

	// SelectionList reference for Up/Down navigation
	List *glyph.SelectionList

	// styling
	width          int
	placeholderTxt string
	Prompt         string // custom prompt like "/" or "?" - exported for reactive binding
}

// NewOmnibox creates a new omnibox with default styling
func NewOmnibox() *Omnibox {
	return &Omnibox{
		maxHeight:      12,
		width:          60,
		placeholderTxt: "Type to search...",
	}
}

// SetItems sets the available items
func (o *Omnibox) SetItems(items []OmniboxItem) {
	o.items = items
	o.filter()
}

// Show displays the omnibox
func (o *Omnibox) Show() {
	o.Visible = true
	o.Query = ""
	o.Selected = 0
	o.filter()
}

// Hide closes the omnibox
func (o *Omnibox) Hide() {
	o.Visible = false
	o.Query = ""
	o.Prompt = ""
}

// IsVisible returns whether the omnibox is showing
func (o *Omnibox) IsVisible() bool {
	return o.Visible
}

// GetQuery returns the current search query
func (o *Omnibox) GetQuery() string {
	return o.Query
}

// Input returns the current input text (alias for GetQuery, used for search)
func (o *Omnibox) Input() string {
	return o.Query
}

// SetPrompt sets a custom prompt (like "/" or "?" for search)
func (o *Omnibox) SetPrompt(p string) {
	o.Prompt = p
}

// SetQuery sets the search query and filters results
func (o *Omnibox) SetQuery(q string) {
	o.Query = q
	o.Selected = 0
	o.filter()
}

// InsertChar adds a character to the query
func (o *Omnibox) InsertChar(r rune) {
	o.Query += string(r)
	o.Selected = 0
	o.filter()
}

// Backspace removes the last character
func (o *Omnibox) Backspace() {
	if len(o.Query) > 0 {
		runes := []rune(o.Query)
		o.Query = string(runes[:len(runes)-1])
		o.Selected = 0
		o.filter()
	}
}

// SelectNext moves selection down
func (o *Omnibox) SelectNext() {
	if o.Selected < len(o.Filtered)-1 {
		o.Selected++
	}
}

// SelectPrev moves selection up
func (o *Omnibox) SelectPrev() {
	if o.Selected > 0 {
		o.Selected--
	}
}

// Execute runs the selected item's action
func (o *Omnibox) Execute() bool {
	if o.Selected >= 0 && o.Selected < len(o.Filtered) {
		item := o.Filtered[o.Selected]
		o.Hide()
		if item.Action != nil {
			item.Action()
		}
		return true
	}
	return false
}

// SelectedItem returns the currently selected item, or nil
func (o *Omnibox) SelectedItem() *OmniboxItem {
	if o.Selected >= 0 && o.Selected < len(o.Filtered) {
		return &o.Filtered[o.Selected]
	}
	return nil
}

// filter updates the filtered list based on query
func (o *Omnibox) filter() {
	if o.Query == "" {
		o.Filtered = make([]OmniboxItem, len(o.items))
		for i, item := range o.items {
			item.Display = fmt.Sprintf("%s %s  %s", item.Icon, item.Label, item.Description)
			o.Filtered[i] = item
		}
		return
	}

	q := strings.ToLower(o.Query)
	o.Filtered = nil

	for _, item := range o.items {
		label := strings.ToLower(item.Label)
		desc := strings.ToLower(item.Description)

		// fuzzy match: check if all query chars appear in order
		if fuzzyMatch(label, q) || fuzzyMatch(desc, q) {
			item.Display = fmt.Sprintf("%s %s  %s", item.Icon, item.Label, item.Description)
			o.Filtered = append(o.Filtered, item)
		}
	}
}


// fuzzyMatch checks if all chars in pattern appear in str in order
func fuzzyMatch(str, pattern string) bool {
	pi := 0
	for _, c := range str {
		if pi < len(pattern) && c == rune(pattern[pi]) {
			pi++
		}
	}
	return pi == len(pattern)
}

// Render returns the overlay view for the app
func (o *Omnibox) Render(screenWidth, screenHeight int, theme Theme) any {
	if !o.Visible {
		return nil
	}

	// derive colors from theme
	bgColor := theme.Background
	textColor := theme.Text
	mutedColor := theme.Dimmed.FG
	accentColor := theme.Accent.FG
	highlightBG := theme.Code.BG
	borderColor := theme.Divider.FG

	// calculate width
	width := o.width
	if width > screenWidth-4 {
		width = screenWidth - 4
	}

	// build input display
	inputText := o.Query
	inputStyle := glyph.Style{FG: textColor, BG: bgColor}
	promptStyle := glyph.Style{FG: accentColor, BG: bgColor}
	promptText := ""
	if o.Prompt != "" {
		promptText = o.Prompt
	}
	if inputText == "" && o.Prompt == "" {
		inputText = o.placeholderTxt
		inputStyle = glyph.Style{FG: mutedColor, BG: bgColor}
	}

	// pad input to fill width
	promptLen := len(promptText)
	inputPadding := width - 4 - promptLen - len(inputText)
	if inputPadding < 0 {
		inputPadding = 0
	}

	// input row: prompt + query + padding
	var inputRow glyph.HBoxC
	if promptText != "" {
		inputRow = glyph.HBox(
			glyph.Text(" "+promptText).Style(promptStyle),
			glyph.Text(inputText+strings.Repeat(" ", inputPadding)).Style(inputStyle),
		)
	} else {
		inputRow = glyph.HBox(
			glyph.Text("  ").FG(accentColor).BG(bgColor),
			glyph.Text(inputText+strings.Repeat(" ", inputPadding)).Style(inputStyle),
		)
	}

	// separator line
	sepLine := strings.Repeat("─", width-2)
	separator := glyph.Text(sepLine).FG(borderColor).BG(bgColor)

	// build result rows
	children := []any{inputRow, separator}

	maxResults := o.maxHeight
	if len(o.Filtered) < maxResults {
		maxResults = len(o.Filtered)
	}

	for i := 0; i < maxResults; i++ {
		item := o.Filtered[i]
		isSelected := i == o.Selected

		// row styling - always set background
		rowBG := bgColor
		rowStyle := glyph.Style{FG: textColor, BG: rowBG}
		descStyle := glyph.Style{FG: mutedColor, BG: rowBG}
		iconStyle := glyph.Style{FG: accentColor, BG: rowBG}

		if isSelected {
			rowBG = highlightBG
			rowStyle = glyph.Style{FG: textColor, BG: rowBG}
			descStyle = glyph.Style{FG: mutedColor, BG: rowBG}
			iconStyle = glyph.Style{FG: accentColor, BG: rowBG}
		}

		// icon
		icon := item.Icon
		if icon == "" {
			icon = " "
		}

		// calculate padding to fill the row (use rune counts for Unicode)
		labelLen := utf8.RuneCountInString(item.Label)
		descLen := utf8.RuneCountInString(item.Description)
		iconLen := utf8.RuneCountInString(icon)
		usedWidth := 2 + iconLen + 1 + labelLen // " " + icon + " " + label
		if descLen > 0 {
			usedWidth += 2 + descLen // "  " + description
		}
		padding := width - 2 - usedWidth // -2 for border
		if padding < 0 {
			padding = 0
		}

		// build the row
		rowChildren := []any{
			glyph.Text(" " + icon + " ").Style(iconStyle),
			glyph.Text(item.Label).Style(rowStyle),
		}

		if item.Description != "" {
			rowChildren = append(rowChildren,
				glyph.Text(strings.Repeat(" ", padding)+"  ").Style(rowStyle),
				glyph.Text(item.Description).Style(descStyle),
			)
		} else {
			rowChildren = append(rowChildren,
				glyph.Text(strings.Repeat(" ", padding)).Style(rowStyle),
			)
		}

		row := glyph.HBox(rowChildren...)
		children = append(children, row)
	}

	// empty state
	if len(o.Filtered) == 0 && o.Query != "" {
		emptyPadding := width - 2 - 12 // "  No results" = 12 chars
		if emptyPadding < 0 {
			emptyPadding = 0
		}
		emptyRow := glyph.Text("  No results" + strings.Repeat(" ", emptyPadding)).FG(mutedColor).BG(bgColor)
		children = append(children, emptyRow)
	}

	// main container with border
	container := glyph.VBox.Border(glyph.BorderRounded).BorderFG(borderColor).BorderBG(bgColor).Width(int16(width))(children...)

	return glyph.Overlay.Centered().Backdrop().BG(bgColor)(container)
}
