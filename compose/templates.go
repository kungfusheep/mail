package compose

import (
	"strings"
	"github.com/kungfusheep/glyph"
	"unicode/utf8"
)

// BlockTemplate defines how a block type renders
type BlockTemplate struct {
	Name        string
	Description string
	// Render takes the styled spans and returns rendered lines
	// width is the content width (72), blockType indicates specific type (h1, h2, etc.)
	Render func(spans []glyph.Span, style glyph.Style, width int, blockType BlockType) [][]glyph.Span
}

// TemplateCategory holds templates for a specific category
type TemplateCategory struct {
	templates map[string]*BlockTemplate
	fallback  string
}

// NewTemplateCategory creates a new category with a default fallback
func NewTemplateCategory(fallback string) *TemplateCategory {
	return &TemplateCategory{
		templates: make(map[string]*BlockTemplate),
		fallback:  fallback,
	}
}

// Register adds a template to the category
func (tc *TemplateCategory) Register(t *BlockTemplate) {
	tc.templates[t.Name] = t
}

// Get returns a template by name, or the fallback if not found
func (tc *TemplateCategory) Get(name string) *BlockTemplate {
	if t, ok := tc.templates[name]; ok {
		return t
	}
	return tc.templates[tc.fallback]
}

// Exists returns true if a template with the given name exists
func (tc *TemplateCategory) Exists(name string) bool {
	_, ok := tc.templates[name]
	return ok
}

// List returns all template names in this category
func (tc *TemplateCategory) List() []string {
	names := make([]string, 0, len(tc.templates))
	for name := range tc.templates {
		names = append(names, name)
	}
	return names
}

// StyleBundle represents a cohesive set of template choices
type StyleBundle struct {
	Name        string
	Description string
	Templates   map[string]string // category -> template name
}

// TemplateRegistry holds all available templates organized by category
type TemplateRegistry struct {
	categories map[string]*TemplateCategory
	bundles    map[string]*StyleBundle
}

// NewTemplateRegistry creates a registry with default templates
func NewTemplateRegistry() *TemplateRegistry {
	r := &TemplateRegistry{
		categories: make(map[string]*TemplateCategory),
		bundles:    make(map[string]*StyleBundle),
	}
	r.initCategories()
	r.initHeadingTemplates()
	r.initQuoteTemplates()
	r.initDividerTemplates()
	r.initListTemplates()
	r.initCodeTemplates()
	r.initCalloutTemplates()
	r.initFrontmatterTemplates()
	r.initTableTemplates()
	r.initDialogueTemplates()
	r.initBundles()
	return r
}

func (r *TemplateRegistry) initCategories() {
	r.categories["headings"] = NewTemplateCategory("clean")
	r.categories["quotes"] = NewTemplateCategory("bar")
	r.categories["dividers"] = NewTemplateCategory("line")
	r.categories["lists"] = NewTemplateCategory("bullet")
	r.categories["code"] = NewTemplateCategory("minimal")
	r.categories["callouts"] = NewTemplateCategory("minimal")
	r.categories["frontmatter"] = NewTemplateCategory("minimal")
	r.categories["tables"] = NewTemplateCategory("unicode")
	r.categories["dialogue"] = NewTemplateCategory("stageplay")
}

// GetCategory returns a template category
func (r *TemplateRegistry) GetCategory(name string) *TemplateCategory {
	return r.categories[name]
}

// GetTemplate returns a template from a category
func (r *TemplateRegistry) GetTemplate(category, name string) *BlockTemplate {
	if cat := r.categories[category]; cat != nil {
		return cat.Get(name)
	}
	return nil
}

// GetDefault returns the default template name for a category
func (r *TemplateRegistry) GetDefault(category string) string {
	if cat := r.categories[category]; cat != nil {
		return cat.fallback
	}
	return ""
}

// GetBundle returns a style bundle by name
func (r *TemplateRegistry) GetBundle(name string) *StyleBundle {
	return r.bundles[name]
}

// ListBundles returns all bundle names
func (r *TemplateRegistry) ListBundles() []string {
	names := make([]string, 0, len(r.bundles))
	for name := range r.bundles {
		names = append(names, name)
	}
	return names
}

// ═══════════════════════════════════════════════════════════════════════════════
// LEGACY COMPATIBILITY - these wrap the new category system
// ═══════════════════════════════════════════════════════════════════════════════

// GetHeadingTemplate returns a heading template by name (legacy)
func (r *TemplateRegistry) GetHeadingTemplate(name string) *BlockTemplate {
	return r.GetTemplate("headings", name)
}

// GetCalloutTemplate returns a callout template by name (legacy)
func (r *TemplateRegistry) GetCalloutTemplate(name string) *BlockTemplate {
	return r.GetTemplate("callouts", name)
}

// GetQuoteTemplate returns a quote template by name
func (r *TemplateRegistry) GetQuoteTemplate(name string) *BlockTemplate {
	return r.GetTemplate("quotes", name)
}

// GetDividerTemplate returns a divider template by name
func (r *TemplateRegistry) GetDividerTemplate(name string) *BlockTemplate {
	return r.GetTemplate("dividers", name)
}

// GetListTemplate returns a list template by name
func (r *TemplateRegistry) GetListTemplate(name string) *BlockTemplate {
	return r.GetTemplate("lists", name)
}

// GetCodeTemplate returns a code template by name
func (r *TemplateRegistry) GetCodeTemplate(name string) *BlockTemplate {
	return r.GetTemplate("code", name)
}

// GetFrontmatterTemplate returns a frontmatter template by name
func (r *TemplateRegistry) GetFrontmatterTemplate(name string) *BlockTemplate {
	return r.GetTemplate("frontmatter", name)
}

// GetDialogueTemplate returns a dialogue template by name
func (r *TemplateRegistry) GetDialogueTemplate(name string) *BlockTemplate {
	return r.GetTemplate("dialogue", name)
}

// HeadingTemplates returns all heading template names (legacy)
func (r *TemplateRegistry) HeadingTemplates() []string {
	if cat := r.categories["headings"]; cat != nil {
		return cat.List()
	}
	return nil
}

// CalloutTemplates returns all callout template names (legacy)
func (r *TemplateRegistry) CalloutTemplates() []string {
	if cat := r.categories["callouts"]; cat != nil {
		return cat.List()
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// HEADING TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initHeadingTemplates() {
	cat := r.categories["headings"]

	// clean - simple bold text
	cat.Register(&BlockTemplate{
		Name:        "clean",
		Description: "Simple bold text",
		Render:      renderHeadingClean,
	})

	// banner - ═══ TEXT ═══
	cat.Register(&BlockTemplate{
		Name:        "banner",
		Description: "═══ TEXT ═══",
		Render:      renderHeadingBanner,
	})

	// underlined - text with line underneath
	cat.Register(&BlockTemplate{
		Name:        "underlined",
		Description: "Text with line underneath",
		Render:      renderHeadingUnderlined,
	})

	// edge - ▌Text
	cat.Register(&BlockTemplate{
		Name:        "edge",
		Description: "▌Text",
		Render:      renderHeadingEdge,
	})

	// typewriter - # TEXT (markdown style)
	cat.Register(&BlockTemplate{
		Name:        "typewriter",
		Description: "# TEXT (markdown style)",
		Render:      renderHeadingTypewriter,
	})

	// academic - formal document style
	cat.Register(&BlockTemplate{
		Name:        "academic",
		Description: "Formal document styling",
		Render:      renderHeadingAcademic,
	})

	// minimal - just the text
	cat.Register(&BlockTemplate{
		Name:        "minimal",
		Description: "Just the text, no decoration",
		Render:      renderHeadingMinimal,
	})

	// boxed - text in a box
	cat.Register(&BlockTemplate{
		Name:        "boxed",
		Description: "Text enclosed in a box",
		Render:      renderHeadingBoxed,
	})

	// monograph - technical documentation style with gold bar
	cat.Register(&BlockTemplate{
		Name:        "monograph",
		Description: "Technical docs with gold bar and underlines",
		Render:      renderHeadingMonograph,
	})
}

func renderHeadingClean(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := make([]glyph.Span, len(spans))
	for i, s := range spans {
		result[i] = glyph.Span{Text: s.Text, Style: s.Style.Bold()}
	}
	return [][]glyph.Span{result}
}

func renderHeadingBanner(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	text = strings.ToUpper(text)

	textLen := utf8.RuneCountInString(text)
	available := width - textLen - 2
	if available < 6 {
		available = 6
	}
	leftPad := available / 2
	rightPad := available - leftPad

	leftBar := strings.Repeat("═", leftPad) + " "
	rightBar := " " + strings.Repeat("═", rightPad)

	line := []glyph.Span{
		{Text: leftBar, Style: style.Bold()},
		{Text: text, Style: style.Bold()},
		{Text: rightBar, Style: style.Bold()},
	}

	return [][]glyph.Span{line}
}

func renderHeadingUnderlined(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := make([]glyph.Span, len(spans))
	for i, s := range spans {
		result[i] = glyph.Span{Text: s.Text, Style: s.Style.Bold()}
	}

	text := spansToText(spans)
	textLen := utf8.RuneCountInString(text)
	underline := strings.Repeat("─", textLen)

	return [][]glyph.Span{
		result,
		{{Text: underline, Style: style}},
	}
}

func renderHeadingEdge(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "▌ ", Style: style},
	}
	for _, s := range spans {
		result = append(result, glyph.Span{Text: s.Text, Style: s.Style.Bold()})
	}
	return [][]glyph.Span{result}
}

func renderHeadingTypewriter(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "# ", Style: style},
	}
	for _, s := range spans {
		result = append(result, glyph.Span{Text: s.Text, Style: s.Style.Bold()})
	}
	return [][]glyph.Span{result}
}

func renderHeadingAcademic(spans []glyph.Span, style glyph.Style, width int, blockType BlockType) [][]glyph.Span {
	text := spansToText(spans)

	switch blockType {
	case BlockH1:
		// title: bold, lines above and below
		line := strings.Repeat("─", width)
		return [][]glyph.Span{
			{{Text: line, Style: style}},
			{{Text: strings.ToUpper(text), Style: style.Bold()}},
			{{Text: line, Style: style}},
		}

	case BlockH2:
		// section: bold
		return [][]glyph.Span{
			{{Text: text, Style: style.Bold()}},
		}

	case BlockH3:
		// subsection: bold italic
		return [][]glyph.Span{
			{{Text: text, Style: style.Bold().Italic()}},
		}

	case BlockH4:
		// paragraph heading: italic
		return [][]glyph.Span{
			{{Text: text, Style: style.Italic()}},
		}

	case BlockH5:
		// minor heading: regular with period
		if !strings.HasSuffix(text, ".") {
			text = text + "."
		}
		return [][]glyph.Span{
			{{Text: text, Style: style}},
		}

	case BlockH6:
		// run-in heading: italic, inline feel
		if !strings.HasSuffix(text, ".") {
			text = text + "."
		}
		return [][]glyph.Span{
			{{Text: text, Style: style.Italic()}},
		}

	default:
		return [][]glyph.Span{spans}
	}
}

func renderHeadingMinimal(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	return [][]glyph.Span{spans}
}

func renderHeadingBoxed(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	textLen := utf8.RuneCountInString(text)
	boxWidth := textLen + 4

	top := "┌" + strings.Repeat("─", boxWidth-2) + "┐"
	mid := "│ " + text + " │"
	bot := "└" + strings.Repeat("─", boxWidth-2) + "┘"

	return [][]glyph.Span{
		{{Text: top, Style: style}},
		{{Text: mid, Style: style.Bold()}},
		{{Text: bot, Style: style}},
	}
}

func renderHeadingMonograph(spans []glyph.Span, style glyph.Style, width int, blockType BlockType) [][]glyph.Span {
	rawText := spansToText(spans)
	textLen := utf8.RuneCountInString(rawText)

	// colors from CSS dark mode
	mainBg := glyph.Hex(0x111111)      // --bg: #111
	shadeBg := glyph.Hex(0x1a1a1a)     // --shade-bg: #1a1a1a (H2 band)
	textColor := glyph.Hex(0xe0e0e0)   // --fg: #e0e0e0
	borderColor := glyph.Hex(0xe0e0e0) // --border: #e0e0e0 (H2 left bar)
	borderLight := glyph.Hex(0x444444) // --border-light: #444 (H3 underline)

	// CSS highlight colors (inverted)
	highlightBg := glyph.Hex(0xe0e0e0) // --highlight-bg: #e0e0e0
	highlightFg := glyph.Hex(0x111111) // --highlight-fg: #111

	switch blockType {
	case BlockH1:
		// H1: Title box - inverted colors, centered, ALL CAPS
		text := strings.ToUpper(rawText)
		textLen = utf8.RuneCountInString(text)

		// center the text
		bandWidth := width
		padding := (bandWidth - textLen) / 2
		if padding < 2 {
			padding = 2
		}

		// top edge: ▄ in highlight color
		topEdge := []glyph.Span{
			{Text: strings.Repeat("▄", bandWidth), Style: glyph.Style{FG: highlightBg, BG: mainBg}},
		}

		// middle: centered text on highlight background
		leftPad := strings.Repeat(" ", padding)
		rightPad := strings.Repeat(" ", bandWidth-padding-textLen)
		middleLine := []glyph.Span{
			{Text: leftPad + text + rightPad, Style: glyph.Style{FG: highlightFg, BG: highlightBg}.Bold()},
		}

		// bottom edge: ▀ in highlight color
		bottomEdge := []glyph.Span{
			{Text: strings.Repeat("▀", bandWidth), Style: glyph.Style{FG: highlightBg, BG: mainBg}},
		}

		return [][]glyph.Span{topEdge, middleLine, bottomEdge}

	case BlockH2:
		// H2: gray band with thin white bar on left, full width
		text := strings.ToUpper(rawText)
		textLen = utf8.RuneCountInString(text)

		// band fills full width
		bandWidth := width - 1 // -1 for the bar

		// top edge: half-bar (lower portion) + full-width half-blocks
		// ╷ = light down (lower half of line, matches ▄ background)
		topEdge := []glyph.Span{
			{Text: "╷", Style: glyph.Style{FG: borderColor, BG: mainBg}},
			{Text: strings.Repeat("▄", bandWidth), Style: glyph.Style{FG: shadeBg, BG: mainBg}},
		}

		// middle: full bar + text + padding to fill width, all on gray
		middleLine := []glyph.Span{
			{Text: "│", Style: glyph.Style{FG: borderColor, BG: shadeBg}},
			{Text: " " + text, Style: glyph.Style{FG: textColor, BG: shadeBg}.Bold()},
		}
		usedWidth := 1 + textLen // space + text
		if usedWidth < bandWidth {
			middleLine = append(middleLine, glyph.Span{
				Text:  strings.Repeat(" ", bandWidth-usedWidth),
				Style: glyph.Style{FG: textColor, BG: shadeBg},
			})
		}

		// bottom edge: half-bar (upper portion) + full-width half-blocks
		// ╵ = light up (upper half of line, matches ▀ background)
		bottomEdge := []glyph.Span{
			{Text: "╵", Style: glyph.Style{FG: borderColor, BG: mainBg}},
			{Text: strings.Repeat("▀", bandWidth), Style: glyph.Style{FG: shadeBg, BG: mainBg}},
		}

		return [][]glyph.Span{topEdge, middleLine, bottomEdge}

	case BlockH3:
		// H3: ALL CAPS bold + underline
		text := strings.ToUpper(rawText)
		textLen = utf8.RuneCountInString(text)

		line1 := []glyph.Span{
			{Text: text, Style: glyph.Style{FG: textColor, BG: mainBg}.Bold()},
		}

		underlineWidth := width
		if underlineWidth < textLen {
			underlineWidth = textLen
		}

		return [][]glyph.Span{
			line1,
			{{Text: strings.Repeat("─", underlineWidth), Style: glyph.Style{FG: borderLight, BG: mainBg}}},
		}

	case BlockH4:
		// H4: Bold only
		return [][]glyph.Span{
			{{Text: rawText, Style: glyph.Style{FG: textColor, BG: mainBg}.Bold()}},
		}

	case BlockH5, BlockH6:
		// H5/H6: just regular text
		return [][]glyph.Span{
			{{Text: rawText, Style: glyph.Style{FG: textColor, BG: mainBg}}},
		}

	default:
		return [][]glyph.Span{spans}
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// QUOTE TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initQuoteTemplates() {
	cat := r.categories["quotes"]

	// bar - vertical bar on left
	cat.Register(&BlockTemplate{
		Name:        "bar",
		Description: "│ Quote text",
		Render:      renderQuoteBar,
	})

	// indent - simple indentation
	cat.Register(&BlockTemplate{
		Name:        "indent",
		Description: "Indented text",
		Render:      renderQuoteIndent,
	})

	// marks - quotation marks
	cat.Register(&BlockTemplate{
		Name:        "marks",
		Description: "❝ Quote text ❞",
		Render:      renderQuoteMarks,
	})

	// typewriter - > prefix
	cat.Register(&BlockTemplate{
		Name:        "typewriter",
		Description: "> Quote text",
		Render:      renderQuoteTypewriter,
	})

	// monograph - blue bar + muted text
	cat.Register(&BlockTemplate{
		Name:        "monograph",
		Description: "│ Blue bar, muted text",
		Render:      renderQuoteMonograph,
	})
}

func renderQuoteBar(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "│ ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderQuoteIndent(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "    ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderQuoteMarks(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "❝ ", Style: style},
	}
	result = append(result, spans...)
	result = append(result, glyph.Span{Text: " ❞", Style: style})
	return [][]glyph.Span{result}
}

func renderQuoteTypewriter(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "> ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderQuoteMonograph(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	// CSS: border-left: 3px solid #6699ff; color: #aaa;
	linkColor := glyph.Hex(0x6699ff)
	mutedColor := glyph.Hex(0xaaaaaa)
	mainBg := glyph.Hex(0x111111)

	// get full text and wrap it ourselves so bar appears on every line
	text := spansToText(spans)
	textWidth := width - 2 // account for "│ " prefix
	if textWidth < 20 {
		textWidth = 20
	}

	wrappedLines := wrapTextRunes(text, textWidth)
	if len(wrappedLines) == 0 {
		wrappedLines = []string{""}
	}

	barStyle := glyph.Style{FG: linkColor, BG: mainBg}
	textStyle := glyph.Style{FG: mutedColor, BG: mainBg}.Italic()

	var result [][]glyph.Span
	for _, line := range wrappedLines {
		result = append(result, []glyph.Span{
			{Text: "│ ", Style: barStyle},
			{Text: line, Style: textStyle},
		})
	}
	return result
}

// ═══════════════════════════════════════════════════════════════════════════════
// DIVIDER TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initDividerTemplates() {
	cat := r.categories["dividers"]

	// line - simple horizontal line
	cat.Register(&BlockTemplate{
		Name:        "line",
		Description: "───────────",
		Render:      renderDividerLine,
	})

	// double - double line
	cat.Register(&BlockTemplate{
		Name:        "double",
		Description: "═══════════",
		Render:      renderDividerDouble,
	})

	// dots - dotted line
	cat.Register(&BlockTemplate{
		Name:        "dots",
		Description: "···········",
		Render:      renderDividerDots,
	})

	// stars - star separator
	cat.Register(&BlockTemplate{
		Name:        "stars",
		Description: "✦   ✦   ✦",
		Render:      renderDividerStars,
	})

	// ornate - decorative divider
	cat.Register(&BlockTemplate{
		Name:        "ornate",
		Description: "───◆ ❖ ◆───",
		Render:      renderDividerOrnate,
	})

	// typewriter - classic style
	cat.Register(&BlockTemplate{
		Name:        "typewriter",
		Description: "* * * * *",
		Render:      renderDividerTypewriter,
	})
}

func renderDividerLine(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	return [][]glyph.Span{
		{{Text: strings.Repeat("─", width), Style: style}},
	}
}

func renderDividerDouble(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	return [][]glyph.Span{
		{{Text: strings.Repeat("═", width), Style: style}},
	}
}

func renderDividerDots(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	return [][]glyph.Span{
		{{Text: strings.Repeat("·", width), Style: style}},
	}
}

func renderDividerStars(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	stars := "✦   ✦   ✦"
	starsLen := utf8.RuneCountInString(stars)
	padding := (width - starsLen) / 2
	if padding < 0 {
		padding = 0
	}
	return [][]glyph.Span{
		{{Text: strings.Repeat(" ", padding) + stars, Style: style}},
	}
}

func renderDividerOrnate(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	center := "◆ ❖ ◆"
	centerLen := utf8.RuneCountInString(center)
	sideWidth := (width - centerLen) / 2
	if sideWidth < 0 {
		sideWidth = 0
	}
	side := strings.Repeat("─", sideWidth)
	return [][]glyph.Span{
		{{Text: side + center + side, Style: style}},
	}
}

func renderDividerTypewriter(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	return [][]glyph.Span{
		{{Text: strings.Repeat("* ", width/2), Style: style}},
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// LIST TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initListTemplates() {
	cat := r.categories["lists"]

	// bullet - standard bullet points
	cat.Register(&BlockTemplate{
		Name:        "bullet",
		Description: "• Item",
		Render:      renderListBullet,
	})

	// dash - simple dashes
	cat.Register(&BlockTemplate{
		Name:        "dash",
		Description: "- Item",
		Render:      renderListDash,
	})

	// arrow - arrow markers
	cat.Register(&BlockTemplate{
		Name:        "arrow",
		Description: "→ Item",
		Render:      renderListArrow,
	})

	// checkbox - checkbox style
	cat.Register(&BlockTemplate{
		Name:        "checkbox",
		Description: "☐ Item",
		Render:      renderListCheckbox,
	})
}

func renderListBullet(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "• ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderListDash(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "- ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderListArrow(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "→ ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderListCheckbox(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "☐ ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

// ═══════════════════════════════════════════════════════════════════════════════
// CODE TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initCodeTemplates() {
	cat := r.categories["code"]

	// minimal - just indented
	cat.Register(&BlockTemplate{
		Name:        "minimal",
		Description: "Indented code",
		Render:      renderCodeMinimal,
	})

	// boxed - code in a box
	cat.Register(&BlockTemplate{
		Name:        "boxed",
		Description: "Code in a box",
		Render:      renderCodeBoxed,
	})

	// terminal - terminal style with $
	cat.Register(&BlockTemplate{
		Name:        "terminal",
		Description: "$ command style",
		Render:      renderCodeTerminal,
	})

	// sidebar - code with gold vertical bar on left
	cat.Register(&BlockTemplate{
		Name:        "sidebar",
		Description: "│ code with gold bar",
		Render:      renderCodeSidebar,
	})
}

func renderCodeMinimal(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "  ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderCodeBoxed(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	textLen := utf8.RuneCountInString(text)
	boxWidth := textLen + 4
	if boxWidth < 10 {
		boxWidth = 10
	}

	padding := boxWidth - 2 - textLen
	if padding < 0 {
		padding = 0
	}

	top := "┌" + strings.Repeat("─", boxWidth-2) + "┐"
	mid := "│ " + text + strings.Repeat(" ", padding) + "│"
	bot := "└" + strings.Repeat("─", boxWidth-2) + "┘"

	return [][]glyph.Span{
		{{Text: top, Style: style}},
		{{Text: mid, Style: style}},
		{{Text: bot, Style: style}},
	}
}

func renderCodeTerminal(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "$ ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderCodeSidebar(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	// CSS: background: #1a1a1a; border-left: 3px solid #ccc;
	codeBg := glyph.Hex(0x1a1a1a)   // --code-bg
	barColor := glyph.Hex(0xcccccc) // --code-fg (left border)
	textColor := glyph.Hex(0xcccccc)

	// get text from spans
	text := spansToText(spans)
	textLen := utf8.RuneCountInString(text)

	// calculate padding to extend code background to width
	contentWidth := width - 2 // bar + space
	padding := 0
	if textLen < contentWidth {
		padding = contentWidth - textLen
	}

	result := []glyph.Span{
		{Text: "│", Style: glyph.Style{FG: barColor, BG: codeBg}},
		{Text: " " + text, Style: glyph.Style{FG: textColor, BG: codeBg}},
	}
	if padding > 0 {
		result = append(result, glyph.Span{
			Text:  strings.Repeat(" ", padding),
			Style: glyph.Style{FG: textColor, BG: codeBg},
		})
	}
	return [][]glyph.Span{result}
}

// ═══════════════════════════════════════════════════════════════════════════════
// CALLOUT TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initCalloutTemplates() {
	cat := r.categories["callouts"]

	// minimal - ▎ text
	cat.Register(&BlockTemplate{
		Name:        "minimal",
		Description: "▎ Text",
		Render:      renderCalloutMinimal,
	})

	// boxed - callout in a box
	cat.Register(&BlockTemplate{
		Name:        "boxed",
		Description: "Boxed with border",
		Render:      renderCalloutBoxed,
	})

	// bracket - [TYPE] text
	cat.Register(&BlockTemplate{
		Name:        "bracket",
		Description: "[TYPE] text",
		Render:      renderCalloutBracket,
	})
}

func renderCalloutMinimal(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "▎ ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderCalloutBoxed(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}

	lines := wrapTextRunes(text, innerWidth)

	maxLen := 0
	for _, line := range lines {
		lineLen := utf8.RuneCountInString(line)
		if lineLen > maxLen {
			maxLen = lineLen
		}
	}
	if maxLen < 6 {
		maxLen = 6
	}

	boxWidth := maxLen + 2

	var result [][]glyph.Span

	top := "┌" + strings.Repeat("─", boxWidth) + "┐"
	result = append(result, []glyph.Span{{Text: top, Style: style}})

	for _, line := range lines {
		lineLen := utf8.RuneCountInString(line)
		padding := boxWidth - lineLen - 1
		if padding < 0 {
			padding = 0
		}
		content := "│ " + line + strings.Repeat(" ", padding) + "│"
		result = append(result, []glyph.Span{{Text: content, Style: style}})
	}

	bottom := "└" + strings.Repeat("─", boxWidth) + "┘"
	result = append(result, []glyph.Span{{Text: bottom, Style: style}})

	return result
}

func renderCalloutBracket(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "[!] ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

// ═══════════════════════════════════════════════════════════════════════════════
// FRONTMATTER TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initFrontmatterTemplates() {
	cat := r.categories["frontmatter"]

	// minimal - simple key: value display
	cat.Register(&BlockTemplate{
		Name:        "minimal",
		Description: "Simple key: value",
		Render:      renderFrontmatterMinimal,
	})

	// table - tabular display
	cat.Register(&BlockTemplate{
		Name:        "table",
		Description: "Key │ Value table",
		Render:      renderFrontmatterTable,
	})

	// boxed - in a box
	cat.Register(&BlockTemplate{
		Name:        "boxed",
		Description: "Frontmatter in a box",
		Render:      renderFrontmatterBoxed,
	})

	// hidden - don't render
	cat.Register(&BlockTemplate{
		Name:        "hidden",
		Description: "Hide frontmatter",
		Render:      renderFrontmatterHidden,
	})
}

func renderFrontmatterMinimal(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	result := []glyph.Span{
		{Text: "  ", Style: style},
	}
	result = append(result, spans...)
	return [][]glyph.Span{result}
}

func renderFrontmatterTable(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	lines := strings.Split(text, "\n")

	maxKeyWidth := 0
	pairs := make([][2]string, 0)
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if w := utf8.RuneCountInString(key); w > maxKeyWidth {
				maxKeyWidth = w
			}
			pairs = append(pairs, [2]string{key, val})
		}
	}

	var result [][]glyph.Span
	for _, pair := range pairs {
		padding := maxKeyWidth - utf8.RuneCountInString(pair[0])
		line := pair[0] + strings.Repeat(" ", padding) + " │ " + pair[1]
		result = append(result, []glyph.Span{{Text: line, Style: style}})
	}
	return result
}

func renderFrontmatterBoxed(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	lines := strings.Split(text, "\n")

	maxWidth := 0
	for _, line := range lines {
		if w := utf8.RuneCountInString(line); w > maxWidth {
			maxWidth = w
		}
	}
	boxWidth := maxWidth + 2

	var result [][]glyph.Span
	top := "╭" + strings.Repeat("─", boxWidth) + "╮"
	result = append(result, []glyph.Span{{Text: top, Style: style}})

	for _, line := range lines {
		padding := maxWidth - utf8.RuneCountInString(line)
		content := "│ " + line + strings.Repeat(" ", padding) + " │"
		result = append(result, []glyph.Span{{Text: content, Style: style}})
	}

	bot := "╰" + strings.Repeat("─", boxWidth) + "╯"
	result = append(result, []glyph.Span{{Text: bot, Style: style}})

	return result
}

func renderFrontmatterHidden(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	return [][]glyph.Span{}
}

// ═══════════════════════════════════════════════════════════════════════════════
// TABLE TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initTableTemplates() {
	cat := r.categories["tables"]

	// unicode - beautiful unicode box drawing
	cat.Register(&BlockTemplate{
		Name:        "unicode",
		Description: "Unicode box-drawing table",
		Render:      renderTableUnicode,
	})

	// ascii - simple ASCII table
	cat.Register(&BlockTemplate{
		Name:        "ascii",
		Description: "ASCII table with pipes",
		Render:      renderTableAscii,
	})

	// minimal - no borders, just spacing
	cat.Register(&BlockTemplate{
		Name:        "minimal",
		Description: "Minimal spacing, no borders",
		Render:      renderTableMinimal,
	})
}

// parseTableCells parses a markdown table into rows of cells
func parseTableCells(text string) [][]string {
	lines := strings.Split(text, "\n")
	var rows [][]string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// skip separator lines (|---|---|)
		if strings.Contains(line, "---") || strings.Contains(line, "===") {
			continue
		}

		// parse cells
		line = strings.Trim(line, "|")
		parts := strings.Split(line, "|")
		var cells []string
		for _, p := range parts {
			cells = append(cells, strings.TrimSpace(p))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}

	return rows
}

// calculateColumnWidths returns the max width of each column
func calculateColumnWidths(rows [][]string) []int {
	if len(rows) == 0 {
		return nil
	}

	// find max columns
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	widths := make([]int, maxCols)
	for _, row := range rows {
		for i, cell := range row {
			w := utf8.RuneCountInString(cell)
			if w > widths[i] {
				widths[i] = w
			}
		}
	}

	return widths
}

func renderTableUnicode(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	rows := parseTableCells(text)
	if len(rows) == 0 {
		return [][]glyph.Span{spans}
	}

	widths := calculateColumnWidths(rows)
	var result [][]glyph.Span

	// top border: ┌─────┬─────┐
	top := "┌"
	for i, w := range widths {
		top += strings.Repeat("─", w+2)
		if i < len(widths)-1 {
			top += "┬"
		}
	}
	top += "┐"
	result = append(result, []glyph.Span{{Text: top, Style: style}})

	for rowIdx, row := range rows {
		// content row: │ cell │ cell │
		line := "│"
		for i, w := range widths {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			padding := w - utf8.RuneCountInString(cell)
			line += " " + cell + strings.Repeat(" ", padding) + " │"
		}
		result = append(result, []glyph.Span{{Text: line, Style: style}})

		// separator after header (first row): ├─────┼─────┤
		if rowIdx == 0 && len(rows) > 1 {
			sep := "├"
			for i, w := range widths {
				sep += strings.Repeat("─", w+2)
				if i < len(widths)-1 {
					sep += "┼"
				}
			}
			sep += "┤"
			result = append(result, []glyph.Span{{Text: sep, Style: style}})
		}
	}

	// bottom border: └─────┴─────┘
	bot := "└"
	for i, w := range widths {
		bot += strings.Repeat("─", w+2)
		if i < len(widths)-1 {
			bot += "┴"
		}
	}
	bot += "┘"
	result = append(result, []glyph.Span{{Text: bot, Style: style}})

	return result
}

func renderTableAscii(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	rows := parseTableCells(text)
	if len(rows) == 0 {
		return [][]glyph.Span{spans}
	}

	widths := calculateColumnWidths(rows)
	var result [][]glyph.Span

	// top border: +-------+-------+
	top := "+"
	for _, w := range widths {
		top += strings.Repeat("-", w+2) + "+"
	}
	result = append(result, []glyph.Span{{Text: top, Style: style}})

	for rowIdx, row := range rows {
		// content row: | cell | cell |
		line := "|"
		for i, w := range widths {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			padding := w - utf8.RuneCountInString(cell)
			line += " " + cell + strings.Repeat(" ", padding) + " |"
		}
		result = append(result, []glyph.Span{{Text: line, Style: style}})

		// separator after header
		if rowIdx == 0 && len(rows) > 1 {
			sep := "+"
			for _, w := range widths {
				sep += strings.Repeat("-", w+2) + "+"
			}
			result = append(result, []glyph.Span{{Text: sep, Style: style}})
		}
	}

	// bottom border
	bot := "+"
	for _, w := range widths {
		bot += strings.Repeat("-", w+2) + "+"
	}
	result = append(result, []glyph.Span{{Text: bot, Style: style}})

	return result
}

func renderTableMinimal(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	text := spansToText(spans)
	rows := parseTableCells(text)
	if len(rows) == 0 {
		return [][]glyph.Span{spans}
	}

	widths := calculateColumnWidths(rows)
	var result [][]glyph.Span

	for rowIdx, row := range rows {
		// content row with column spacing
		var line string
		for i, w := range widths {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			padding := w - utf8.RuneCountInString(cell)
			line += cell + strings.Repeat(" ", padding)
			if i < len(widths)-1 {
				line += "  " // gap between columns
			}
		}
		result = append(result, []glyph.Span{{Text: line, Style: style}})

		// underline after header
		if rowIdx == 0 && len(rows) > 1 {
			var sep string
			for i, w := range widths {
				sep += strings.Repeat("─", w)
				if i < len(widths)-1 {
					sep += "  "
				}
			}
			result = append(result, []glyph.Span{{Text: sep, Style: style}})
		}
	}

	return result
}

// ═══════════════════════════════════════════════════════════════════════════════
// DIALOGUE TEMPLATES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initDialogueTemplates() {
	cat := r.categories["dialogue"]

	// stageplay - two-column format (character left, dialogue right)
	cat.Register(&BlockTemplate{
		Name:        "stageplay",
		Description: "Character left, dialogue right",
		Render:      renderDialogueStageplay,
	})

	// screenplay - centered character, indented dialogue
	cat.Register(&BlockTemplate{
		Name:        "screenplay",
		Description: "Centered character, indented dialogue",
		Render:      renderDialogueScreenplay,
	})
}

// renderDialogueStageplay renders dialogue in two-column stage play format
// Character name on the left (~12 chars), dialogue flows on the right
func renderDialogueStageplay(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	// spans format: first span is character name (bold), rest is dialogue
	if len(spans) == 0 {
		return [][]glyph.Span{{}}
	}

	charWidth := 14 // fixed width for character column
	character := ""
	var dialogueSpans []glyph.Span

	if len(spans) > 0 {
		character = spans[0].Text
		if len(spans) > 1 {
			dialogueSpans = spans[1:]
		}
	}

	// build dialogue text
	dialogueText := ""
	for _, s := range dialogueSpans {
		dialogueText += s.Text
	}

	// wrap dialogue to fit in remaining width
	dialogueWidth := width - charWidth - 2 // 2 for gap
	if dialogueWidth < 20 {
		dialogueWidth = 20
	}

	dialogueLines := wrapTextRunes(dialogueText, dialogueWidth)
	if len(dialogueLines) == 0 {
		dialogueLines = []string{""}
	}

	var result [][]glyph.Span

	// first line: character + first dialogue line
	charPadding := charWidth - utf8.RuneCountInString(character)
	if charPadding < 0 {
		charPadding = 0
	}
	// truncate character name if too long
	charRunes := []rune(character)
	if len(charRunes) > charWidth {
		character = string(charRunes[:charWidth-1]) + "…"
		charPadding = 0
	}

	firstLine := []glyph.Span{
		{Text: strings.Repeat(" ", charPadding) + strings.ToUpper(character), Style: style.Bold()},
		{Text: "  ", Style: style},
		{Text: dialogueLines[0], Style: style},
	}
	result = append(result, firstLine)

	// subsequent lines: empty character column + dialogue
	emptyChar := strings.Repeat(" ", charWidth)
	for i := 1; i < len(dialogueLines); i++ {
		line := []glyph.Span{
			{Text: emptyChar, Style: style},
			{Text: "  ", Style: style},
			{Text: dialogueLines[i], Style: style},
		}
		result = append(result, line)
	}

	return result
}

// renderDialogueScreenplay renders dialogue in centered screenplay format
// Character name centered and caps, dialogue indented below
func renderDialogueScreenplay(spans []glyph.Span, style glyph.Style, width int, _ BlockType) [][]glyph.Span {
	if len(spans) == 0 {
		return [][]glyph.Span{{}}
	}

	character := ""
	var dialogueSpans []glyph.Span

	if len(spans) > 0 {
		character = spans[0].Text
		if len(spans) > 1 {
			dialogueSpans = spans[1:]
		}
	}

	// build dialogue text
	dialogueText := ""
	for _, s := range dialogueSpans {
		dialogueText += s.Text
	}

	// screenplay format: character centered, dialogue ~35 chars wide centered
	dialogueWidth := 35
	if dialogueWidth > width-10 {
		dialogueWidth = width - 10
	}

	dialogueLines := wrapTextRunes(dialogueText, dialogueWidth)
	if len(dialogueLines) == 0 {
		dialogueLines = []string{""}
	}

	var result [][]glyph.Span

	// character name - centered, uppercase
	charText := strings.ToUpper(character)
	charLen := utf8.RuneCountInString(charText)
	charPadding := (width - charLen) / 2
	if charPadding < 0 {
		charPadding = 0
	}

	charLine := []glyph.Span{
		{Text: strings.Repeat(" ", charPadding) + charText, Style: style.Bold()},
	}
	result = append(result, charLine)

	// dialogue lines - indented from left (screenplay standard ~25 chars in)
	dialogueIndent := (width - dialogueWidth) / 2
	if dialogueIndent < 5 {
		dialogueIndent = 5
	}
	indentStr := strings.Repeat(" ", dialogueIndent)

	for _, dLine := range dialogueLines {
		line := []glyph.Span{
			{Text: indentStr + dLine, Style: style},
		}
		result = append(result, line)
	}

	return result
}

// ═══════════════════════════════════════════════════════════════════════════════
// STYLE BUNDLES
// ═══════════════════════════════════════════════════════════════════════════════

func (r *TemplateRegistry) initBundles() {
	// typewriter - classic aesthetic
	r.bundles["typewriter"] = &StyleBundle{
		Name:        "typewriter",
		Description: "Classic typewriter aesthetic",
		Templates: map[string]string{
			"headings":    "typewriter",
			"quotes":      "typewriter",
			"dividers":    "typewriter",
			"lists":       "dash",
			"code":        "minimal",
			"callouts":    "bracket",
			"frontmatter": "minimal",
			"tables":      "ascii",
			"dialogue":    "stageplay",
		},
	}

	// minimal - clean, distraction-free
	r.bundles["minimal"] = &StyleBundle{
		Name:        "minimal",
		Description: "Clean, distraction-free styling",
		Templates: map[string]string{
			"headings":    "minimal",
			"quotes":      "indent",
			"dividers":    "dots",
			"lists":       "dash",
			"code":        "minimal",
			"callouts":    "minimal",
			"frontmatter": "hidden",
			"tables":      "minimal",
			"dialogue":    "stageplay",
		},
	}

	// academic - formal document styling
	r.bundles["academic"] = &StyleBundle{
		Name:        "academic",
		Description: "Formal academic document styling",
		Templates: map[string]string{
			"headings":    "academic",
			"quotes":      "indent",
			"dividers":    "line",
			"lists":       "bullet",
			"code":        "boxed",
			"callouts":    "boxed",
			"frontmatter": "table",
			"tables":      "unicode",
			"dialogue":    "stageplay",
		},
	}

	// creative - expressive, decorative
	r.bundles["creative"] = &StyleBundle{
		Name:        "creative",
		Description: "Expressive, decorative styling",
		Templates: map[string]string{
			"headings":    "boxed",
			"quotes":      "marks",
			"dividers":    "ornate",
			"lists":       "arrow",
			"code":        "minimal",
			"callouts":    "bracket",
			"frontmatter": "table",
			"tables":      "unicode",
			"dialogue":    "screenplay",
		},
	}

	// monograph - technical documentation style
	r.bundles["monograph"] = &StyleBundle{
		Name:        "monograph",
		Description: "Technical documentation style",
		Templates: map[string]string{
			"headings":    "monograph",
			"quotes":      "monograph",
			"dividers":    "line",
			"lists":       "dash",
			"code":        "sidebar",
			"callouts":    "minimal",
			"frontmatter": "hidden",
			"tables":      "minimal",
			"dialogue":    "stageplay",
		},
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// HELPERS
// ═══════════════════════════════════════════════════════════════════════════════

func spansToText(spans []glyph.Span) string {
	var sb strings.Builder
	for _, s := range spans {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

func wrapTextRunes(text string, width int) []string {
	runes := []rune(text)
	if width <= 0 || len(runes) <= width {
		return []string{text}
	}

	var lines []string
	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}

		wrapAt := width
		for i := width - 1; i >= 0; i-- {
			if runes[i] == ' ' {
				wrapAt = i
				break
			}
		}

		lines = append(lines, string(runes[:wrapAt]))
		runes = runes[wrapAt:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}

	return lines
}
