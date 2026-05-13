package preview

import (
	"strings"

	"github.com/kungfusheep/glyph"
	"golang.org/x/net/html"
)

type BlockKind int

const (
	BlockParagraph BlockKind = iota
	BlockHeading
	BlockQuote
	BlockListItem
	BlockCode
	BlockDivider
	BlockImage
	BlockSignature
	BlockFooter
	BlockForwarded
	BlockTable
)

type InlineStyle int

const (
	InlineStrong InlineStyle = 1 << iota
	InlineEmphasis
	InlineCode
)

type Document struct {
	Blocks []Block
}

type Block struct {
	Kind    BlockKind
	Level   int
	Marker  string
	Inlines []Inline
	Table   *Table
}

type Inline struct {
	Text  string
	Href  string
	Style InlineStyle
}

type Table struct {
	Rows []TableRow
}

type TableRow struct {
	Cells []TableCell
}

type TableCell struct {
	Inlines  []Inline
	IsHeader bool
	ColSpan  int
}

func ParseHTML(htmlBody, fallbackText string) Document {
	if strings.TrimSpace(htmlBody) == "" {
		return ParseText(fallbackText)
	}

	root, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return ParseText(fallbackText)
	}

	var doc Document
	body := findHTMLNode(root, "body")
	if body == nil {
		body = root
	}
	extractBlocks(body, &doc, false)

	if len(doc.Blocks) == 0 {
		return ParseText(fallbackText)
	}
	return doc
}

func ParseText(text string) Document {
	var doc Document
	for _, segment := range SegmentText(strings.TrimSpace(Sanitize(text))) {
		kind := blockKindFromSegment(segment.Kind)
		for _, part := range splitSegmentBlocks(segment.Text) {
			doc.appendBlock(Block{
				Kind:    kind,
				Inlines: []Inline{{Text: part}},
			})
		}
	}
	return doc
}

func (d Document) PlainText() string {
	parts := make([]string, 0, len(d.Blocks))
	for _, block := range d.Blocks {
		text := strings.TrimSpace(block.PlainText())
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (d Document) Segments() []Segment {
	segments := make([]Segment, 0, len(d.Blocks))
	for _, block := range d.Blocks {
		text := strings.TrimSpace(block.PlainText())
		if text == "" {
			continue
		}
		appendSegment(&segments, segmentKindFromBlock(block.Kind), text)
	}
	return segments
}

func (d Document) GlyphSpans() []glyph.Span {
	spans := make([]glyph.Span, 0, len(d.Blocks)*3)
	for _, block := range d.Blocks {
		if block.Empty() {
			continue
		}
		if len(spans) > 0 {
			spans = append(spans, glyph.Span{Text: "\n\n"})
		}
		spans = append(spans, block.GlyphSpans()...)
	}
	return spans
}

func (d Document) GlyphLines() [][]glyph.Span {
	lines := make([][]glyph.Span, 0, len(d.Blocks)*2)
	for _, block := range d.Blocks {
		if block.Empty() {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, nil)
		}
		lines = append(lines, splitGlyphLines(block.GlyphSpans())...)
	}
	return lines
}

func (b Block) Empty() bool {
	if b.Kind == BlockTable && b.Table != nil {
		return b.Table.Empty()
	}
	return strings.TrimSpace(b.PlainText()) == ""
}

func (b Block) PlainText() string {
	if b.Kind == BlockTable && b.Table != nil {
		return strings.Join(renderTableLines(*b.Table), "\n")
	}
	if len(b.Inlines) == 0 {
		return ""
	}
	var out strings.Builder
	for _, in := range b.Inlines {
		writeInlinePlainText(&out, in.Text)
	}
	return out.String()
}

func (b Block) GlyphSpans() []glyph.Span {
	if b.Kind == BlockTable && b.Table != nil {
		return b.Table.GlyphSpans()
	}

	style := blockStyle(b.Kind)
	spans := make([]glyph.Span, 0, len(b.Inlines)+1)
	switch b.Kind {
	case BlockHeading:
		style.Attr |= glyph.AttrBold
	case BlockQuote:
		spans = append(spans, glyph.Span{Text: "│ ", Style: style})
	case BlockListItem:
		marker := b.Marker
		if marker == "" {
			marker = "•"
		}
		spans = append(spans, glyph.Span{Text: marker + " ", Style: style})
	case BlockDivider:
		return []glyph.Span{{Text: "────────", Style: style}}
	case BlockImage:
		style.Attr |= glyph.AttrDim
	}
	for _, in := range b.Inlines {
		spans = appendInlineGlyphSpans(spans, style, in)
	}
	return spans
}

func (t Table) Empty() bool {
	for _, row := range t.Rows {
		for _, cell := range row.Cells {
			if strings.TrimSpace(cell.PlainText()) != "" {
				return false
			}
		}
	}
	return true
}

func (t Table) GlyphSpans() []glyph.Span {
	lines := renderTableLines(t)
	spans := make([]glyph.Span, 0, len(lines)*2)
	for i, line := range lines {
		if i > 0 {
			spans = append(spans, glyph.Span{Text: "\n"})
		}
		style := glyph.Style{}
		if i == 0 && t.HasHeader() {
			style.Attr |= glyph.AttrBold
		}
		if tableSeparatorLine(line) {
			style.Attr |= glyph.AttrDim
		}
		spans = append(spans, glyph.Span{Text: line, Style: style})
	}
	return spans
}

func (t Table) HasHeader() bool {
	if len(t.Rows) == 0 {
		return false
	}
	for _, cell := range t.Rows[0].Cells {
		if cell.IsHeader {
			return true
		}
	}
	return false
}

func (c TableCell) PlainText() string {
	var out strings.Builder
	for _, in := range c.Inlines {
		writeInlinePlainText(&out, in.Text)
	}
	return strings.TrimSpace(out.String())
}

func (d *Document) appendBlock(block Block) {
	if block.Empty() {
		return
	}
	if previewChromeText(block.PlainText()) {
		return
	}
	d.Blocks = append(d.Blocks, block)
}

func findHTMLNode(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findHTMLNode(c, tag); found != nil {
			return found
		}
	}
	return nil
}

func extractBlocks(n *html.Node, doc *Document, quoted bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			if text := cleanText(c.Data); text != "" {
				if cssBlobText(text) {
					continue
				}
				doc.appendBlock(Block{Kind: quoteAwareKind(BlockParagraph, quoted), Inlines: []Inline{{Text: text}}})
			}
			continue
		}
		if c.Type != html.ElementNode {
			extractBlocks(c, doc, quoted)
			continue
		}
		if hiddenHTMLNode(c) {
			continue
		}

		switch c.Data {
		case "script", "style", "head", "meta", "title", "template":
			continue
		case "h1", "h2", "h3", "h4", "h5", "h6":
			doc.appendBlock(Block{Kind: quoteAwareKind(BlockHeading, quoted), Level: headingLevel(c.Data), Inlines: extractInline(c, 0, "")})
		case "p":
			doc.appendBlock(Block{Kind: quoteAwareKind(BlockParagraph, quoted), Inlines: extractInline(c, 0, "")})
		case "blockquote":
			extractBlocks(c, doc, true)
		case "ul", "ol":
			extractList(c, doc, quoted)
		case "pre":
			doc.appendBlock(Block{Kind: quoteAwareKind(BlockCode, quoted), Inlines: []Inline{{Text: preText(c), Style: InlineCode}}})
		case "hr":
			doc.appendBlock(Block{Kind: BlockDivider, Inlines: []Inline{{Text: "────────"}}})
		case "img":
			if block := imageBlock(c); !block.Empty() {
				doc.appendBlock(block)
			}
		case "br":
			continue
		case "table":
			if table, ok := dataTable(c); ok {
				doc.appendBlock(Block{Kind: BlockTable, Table: &table})
				continue
			}
			extractBlocks(c, doc, quoted)
		case "tr":
			if block, ok := tableBulletBlock(c, quoted); ok {
				doc.appendBlock(block)
				continue
			}
			extractBlocks(c, doc, quoted)
		case "tbody", "thead", "tfoot", "body", "html":
			extractBlocks(c, doc, quoted)
		case "div", "section", "article", "main", "td", "th":
			if hasBlockChildren(c) {
				extractBlocks(c, doc, quoted)
				continue
			}
			doc.appendBlock(Block{Kind: quoteAwareKind(BlockParagraph, quoted), Inlines: extractInline(c, 0, "")})
		default:
			if hasBlockChildren(c) {
				extractBlocks(c, doc, quoted)
				continue
			}
			doc.appendBlock(Block{Kind: quoteAwareKind(BlockParagraph, quoted), Inlines: extractInline(c, 0, "")})
		}
	}
}

func extractList(n *html.Node, doc *Document, quoted bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.Data == "li" {
			doc.appendBlock(Block{Kind: quoteAwareKind(BlockListItem, quoted), Inlines: extractInline(c, 0, "")})
			continue
		}
		extractList(c, doc, quoted)
	}
}

func tableBulletBlock(row *html.Node, quoted bool) (Block, bool) {
	cells := tableRowTextCells(row)
	if len(cells) < 2 {
		return Block{}, false
	}
	marker, ok := listMarker(cells[0].PlainText())
	if !ok {
		return Block{}, false
	}

	var inlines []Inline
	for _, cell := range cells[1:] {
		if len(inlines) > 0 {
			inlines = append(inlines, Inline{Text: " "})
		}
		inlines = append(inlines, cell.Inlines...)
	}
	block := Block{Kind: quoteAwareKind(BlockListItem, quoted), Marker: marker, Inlines: inlines}
	return block, !block.Empty()
}

func dataTable(n *html.Node) (Table, bool) {
	var table Table
	collectTableRows(n, &table)
	if len(table.Rows) < 2 {
		return Table{}, false
	}
	if !tableLooksLikeData(table) {
		return Table{}, false
	}
	return table, true
}

func collectTableRows(n *html.Node, table *Table) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "thead", "tbody", "tfoot":
			collectTableRows(c, table)
		case "tr":
			row := tableRow(c)
			if len(row.Cells) > 0 {
				appendTableRow(table, row)
			}
		}
	}
}

func appendTableRow(table *Table, row TableRow) {
	if duplicateTableRow(table.Rows, row) {
		return
	}
	table.Rows = append(table.Rows, row)
}

func duplicateTableRow(rows []TableRow, row TableRow) bool {
	if len(rows) == 0 {
		return false
	}
	if sameTableRow(rows[len(rows)-1], row) {
		return true
	}
	if !tableSectionRow(row) {
		return false
	}

	key := tableSectionKey(row)
	for i := len(rows) - 1; i >= 0 && len(rows)-i <= 3; i-- {
		previous := rows[i]
		if !tableSectionRow(previous) {
			continue
		}
		if tableSectionKey(previous) == key {
			return true
		}
	}
	return false
}

func sameTableRow(a, b TableRow) bool {
	if len(a.Cells) != len(b.Cells) {
		return false
	}
	for i := range a.Cells {
		if a.Cells[i].IsHeader != b.Cells[i].IsHeader ||
			a.Cells[i].ColSpan != b.Cells[i].ColSpan ||
			a.Cells[i].PlainText() != b.Cells[i].PlainText() {
			return false
		}
	}
	return true
}

func tableSectionRow(row TableRow) bool {
	if len(row.Cells) != 1 {
		return false
	}
	text := row.Cells[0].PlainText()
	if strings.TrimSpace(text) == "" {
		return false
	}
	return len(strings.Fields(text)) <= 4
}

func tableSectionKey(row TableRow) string {
	if len(row.Cells) == 0 {
		return ""
	}
	return strings.ToLower(strings.Join(strings.Fields(row.Cells[0].PlainText()), " "))
}

func tableRow(row *html.Node) TableRow {
	var out TableRow
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
			continue
		}
		cell := TableCell{
			Inlines:  extractCellInline(c),
			IsHeader: c.Data == "th",
			ColSpan:  htmlIntAttr(c, "colspan", 1),
		}
		if strings.TrimSpace(cell.PlainText()) == "" {
			continue
		}
		out.Cells = append(out.Cells, cell)
	}
	return out
}

func extractCellInline(n *html.Node) []Inline {
	var out []Inline
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "table" {
			continue
		}
		switch c.Type {
		case html.TextNode:
			if text := cleanText(c.Data); text != "" && !cssBlobText(text) {
				out = append(out, Inline{Text: text})
			}
		case html.ElementNode:
			if hiddenHTMLNode(c) {
				continue
			}
			out = append(out, extractInline(c, 0, "")...)
		}
	}
	return mergeInlineSpaces(out)
}

func tableLooksLikeData(table Table) bool {
	var rowsWithMultipleCells int
	var headerCells int
	var markerRows int
	maxCells := 0

	for _, row := range table.Rows {
		if len(row.Cells) > maxCells {
			maxCells = len(row.Cells)
		}
		if len(row.Cells) > 1 && !tableFullWidthRow(row) {
			rowsWithMultipleCells++
		}
		if tableRowStartsWithMarker(row) {
			markerRows++
		}
		for _, cell := range row.Cells {
			if cell.IsHeader {
				headerCells++
			}
		}
	}

	if maxCells < 2 || rowsWithMultipleCells < 2 {
		return false
	}
	if markerRows > 0 && markerRows == rowsWithMultipleCells {
		return false
	}
	return headerCells > 0 || rowsWithMultipleCells >= 3
}

func tableFullWidthRow(row TableRow) bool {
	return len(row.Cells) == 1 && row.Cells[0].ColSpan > 1
}

func tableRowStartsWithMarker(row TableRow) bool {
	if len(row.Cells) == 0 {
		return false
	}
	_, ok := listMarker(row.Cells[0].PlainText())
	return ok
}

func tableRowTextCells(row *html.Node) []Block {
	var cells []Block
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
			continue
		}
		block := Block{Kind: BlockParagraph, Inlines: extractInline(c, 0, "")}
		if block.Empty() {
			continue
		}
		cells = append(cells, block)
	}
	return cells
}

func listMarker(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	switch trimmed {
	case "•", "·", "-", "–", "—":
		return trimmed, true
	}
	if strings.HasSuffix(trimmed, ".") {
		number := strings.TrimSuffix(trimmed, ".")
		if number == "" {
			return "", false
		}
		for _, r := range number {
			if r < '0' || r > '9' {
				return "", false
			}
		}
		return trimmed, true
	}
	return "", false
}

func extractInline(n *html.Node, style InlineStyle, href string) []Inline {
	var out []Inline
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if text := cleanText(c.Data); text != "" {
				if cssBlobText(text) {
					continue
				}
				out = append(out, Inline{Text: text, Href: href, Style: style})
			}
		case html.ElementNode:
			if hiddenHTMLNode(c) {
				continue
			}
			nextStyle := style
			nextHref := href
			switch c.Data {
			case "strong", "b":
				nextStyle |= InlineStrong
			case "em", "i":
				nextStyle |= InlineEmphasis
			case "span":
				if htmlStyleHas(c, "font-weight", "bold") || htmlStyleHas(c, "font-weight", "700") {
					nextStyle |= InlineStrong
				}
			case "code", "kbd", "samp":
				nextStyle |= InlineCode
			case "a":
				if h := htmlAttr(c, "href"); h != "" {
					nextHref = h
				}
			case "br":
				out = append(out, Inline{Text: "\n", Href: href, Style: style})
				continue
			case "img":
				if alt := imageText(c); alt != "" {
					out = append(out, Inline{Text: alt, Href: htmlAttr(c, "src"), Style: style | InlineEmphasis})
				}
				continue
			}
			out = append(out, extractInline(c, nextStyle, nextHref)...)
		}
	}
	return mergeInlineSpaces(out)
}

func hasBlockChildren(n *html.Node) bool {
	blockTags := map[string]bool{
		"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"blockquote": true, "ul": true, "ol": true, "pre": true, "hr": true,
		"table": true, "tbody": true, "thead": true, "tfoot": true, "tr": true, "td": true, "th": true,
		"div": true, "section": true, "article": true,
	}

	var walk func(*html.Node, int) bool
	walk = func(node *html.Node, depth int) bool {
		if depth > 5 {
			return false
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if blockTags[c.Data] {
				return true
			}
			if walk(c, depth+1) {
				return true
			}
		}
		return false
	}
	return walk(n, 0)
}

func blockKindFromSegment(kind SegmentKind) BlockKind {
	switch kind {
	case SegmentQuote:
		return BlockQuote
	case SegmentSignature:
		return BlockSignature
	case SegmentFooter:
		return BlockFooter
	case SegmentForwarded:
		return BlockForwarded
	default:
		return BlockParagraph
	}
}

func segmentKindFromBlock(kind BlockKind) SegmentKind {
	switch kind {
	case BlockQuote:
		return SegmentQuote
	case BlockSignature:
		return SegmentSignature
	case BlockFooter:
		return SegmentFooter
	case BlockForwarded:
		return SegmentForwarded
	default:
		return SegmentMain
	}
}

func blockStyle(kind BlockKind) glyph.Style {
	switch kind {
	case BlockQuote:
		return glyph.Style{Attr: glyph.AttrDim | glyph.AttrItalic}
	case BlockSignature, BlockFooter, BlockForwarded, BlockDivider:
		return glyph.Style{Attr: glyph.AttrDim}
	default:
		return glyph.Style{}
	}
}

func renderTableLines(table Table) []string {
	widths := tableColumnWidths(table)
	if len(widths) == 0 {
		return nil
	}
	shrinkTableColumns(widths, 92)

	lines := make([]string, 0, len(table.Rows)+1)
	for i, row := range table.Rows {
		lines = append(lines, renderTableRow(row, widths))
		if i == 0 && table.HasHeader() && !tableFullWidthRow(row) {
			lines = append(lines, renderTableSeparator(widths))
		}
	}
	return lines
}

func tableColumnWidths(table Table) []int {
	maxCols := 0
	for _, row := range table.Rows {
		if len(row.Cells) > maxCols {
			maxCols = len(row.Cells)
		}
	}
	if maxCols == 0 {
		return nil
	}

	widths := make([]int, maxCols)
	for _, row := range table.Rows {
		if tableFullWidthRow(row) {
			continue
		}
		for i, cell := range row.Cells {
			width := textWidth(cell.PlainText())
			if width > widths[i] {
				widths[i] = width
			}
		}
	}
	for i := range widths {
		if widths[i] < 3 {
			widths[i] = 3
		}
		if widths[i] > 32 {
			widths[i] = 32
		}
	}
	return widths
}

func shrinkTableColumns(widths []int, maxWidth int) {
	for tableWidth(widths) > maxWidth {
		widest := 0
		for i, width := range widths {
			if width > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 3 {
			return
		}
		widths[widest]--
	}
}

func tableWidth(widths []int) int {
	if len(widths) == 0 {
		return 0
	}
	total := 0
	for _, width := range widths {
		total += width
	}
	return total + (len(widths)-1)*3
}

func renderTableRow(row TableRow, widths []int) string {
	if tableFullWidthRow(row) {
		return row.Cells[0].PlainText()
	}

	parts := make([]string, len(widths))
	for i := range widths {
		text := ""
		if i < len(row.Cells) {
			text = row.Cells[i].PlainText()
		}
		parts[i] = padRight(truncateText(text, widths[i]), widths[i])
	}
	return strings.Join(parts, " | ")
}

func renderTableSeparator(widths []int) string {
	parts := make([]string, len(widths))
	for i, width := range widths {
		parts[i] = strings.Repeat("-", width)
	}
	return strings.Join(parts, "-+-")
}

func tableSeparatorLine(line string) bool {
	if line == "" {
		return false
	}
	for _, r := range line {
		if r != '-' && r != '+' {
			return false
		}
	}
	return true
}

func truncateText(text string, width int) string {
	if textWidth(text) <= width {
		return text
	}
	if width <= 1 {
		return strings.Repeat(".", width)
	}
	runes := []rune(text)
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}

func padRight(text string, width int) string {
	padding := width - textWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func textWidth(text string) int {
	return len([]rune(text))
}

func inlineGlyphStyle(base glyph.Style, in Inline) glyph.Style {
	style := base
	if in.Style&InlineStrong != 0 {
		style.Attr |= glyph.AttrBold
	}
	if in.Style&InlineEmphasis != 0 {
		style.Attr |= glyph.AttrItalic
	}
	if in.Style&InlineCode != 0 {
		style.Attr |= glyph.AttrDim
	}
	if in.Href != "" {
		style.Attr |= glyph.AttrUnderline
	}
	return style
}

func quoteAwareKind(kind BlockKind, quoted bool) BlockKind {
	if quoted {
		return BlockQuote
	}
	return kind
}

func splitSegmentBlocks(text string) []string {
	parts := strings.Split(strings.TrimSpace(text), "\n\n")
	if len(parts) == 1 {
		return []string{strings.TrimSpace(text)}
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func headingLevel(tag string) int {
	switch tag {
	case "h1":
		return 1
	case "h2":
		return 2
	default:
		return 3
	}
}

func imageBlock(n *html.Node) Block {
	return Block{Kind: BlockImage, Inlines: []Inline{{Text: imageText(n), Href: htmlAttr(n, "src"), Style: InlineEmphasis}}}
}

func imageText(n *html.Node) string {
	alt := strings.TrimSpace(htmlAttr(n, "alt"))
	if !meaningfulImageAlt(alt) {
		return ""
	}
	return "[image: " + alt + "]"
}

func preText(n *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			out.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(Sanitize(out.String()))
}

func htmlAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func htmlIntAttr(n *html.Node, key string, fallback int) int {
	value := htmlAttr(n, key)
	if value == "" {
		return fallback
	}
	out := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return fallback
		}
		out = out*10 + int(r-'0')
	}
	if out == 0 {
		return fallback
	}
	return out
}

func hiddenHTMLNode(n *html.Node) bool {
	class := htmlAttr(n, "class")
	style := strings.ToLower(htmlAttr(n, "style"))
	if strings.Contains(class, "Preheader") || strings.Contains(class, "preheader") ||
		strings.Contains(class, "tracking") || strings.Contains(class, "open-counter") {
		return true
	}
	return strings.Contains(style, "display: none") ||
		strings.Contains(style, "display:none") ||
		strings.Contains(style, "visibility: hidden") ||
		strings.Contains(style, "visibility:hidden") ||
		strings.Contains(style, "opacity: 0") ||
		strings.Contains(style, "opacity:0") ||
		strings.Contains(style, "max-height: 0") ||
		strings.Contains(style, "max-height:0") ||
		strings.Contains(style, "mso-hide: all") ||
		strings.Contains(style, "mso-hide:all")
}

func previewChromeText(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if normalized == "" {
		return true
	}
	chrome := []string{
		"trouble viewing this email? view in browser",
		"view in browser",
		"view online",
		"unsubscribe",
		"manage your preferences",
		"update contact preferences",
		"send me less",
	}
	for _, item := range chrome {
		if normalized == item {
			return true
		}
	}
	return false
}

func meaningfulImageAlt(alt string) bool {
	if alt == "" {
		return false
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(alt), " "))
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "http://") || strings.HasPrefix(normalized, "https://") {
		return false
	}
	if strings.Contains(normalized, "/") || strings.Contains(normalized, ".png") || strings.Contains(normalized, ".jpg") || strings.Contains(normalized, ".gif") {
		return false
	}
	generic := []string{
		"image",
		"image alt text here",
		"logo",
		"spacer",
		"tracking pixel",
		"pixel",
		"no",
	}
	for _, item := range generic {
		if normalized == item {
			return false
		}
	}
	if strings.Contains(normalized, "logo") {
		return false
	}
	return true
}

func cssBlobText(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "@media ") ||
		strings.Contains(lower, "@font-face") ||
		strings.Contains(lower, "!important") ||
		(strings.Contains(lower, "{") && strings.Contains(lower, "}") && strings.Contains(lower, "font-family:")) ||
		(strings.Contains(lower, "{") && strings.Contains(lower, "}") && strings.Contains(lower, "max-width:"))
}

func htmlStyleHas(n *html.Node, property, value string) bool {
	style := strings.ToLower(htmlAttr(n, "style"))
	property = strings.ToLower(property)
	value = strings.ToLower(value)
	for _, part := range strings.Split(style, ";") {
		key, val, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == property && strings.Contains(strings.TrimSpace(val), value) {
			return true
		}
	}
	return false
}

func cleanText(text string) string {
	text = Sanitize(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	return strings.Join(strings.Fields(text), " ")
}

func mergeInlineSpaces(in []Inline) []Inline {
	out := make([]Inline, 0, len(in))
	for _, next := range in {
		if next.Text == "" {
			continue
		}
		if len(out) == 0 {
			out = append(out, next)
			continue
		}
		prev := &out[len(out)-1]
		if strings.Contains(prev.Text, "\n") || strings.Contains(next.Text, "\n") {
			out = append(out, next)
			continue
		}
		if prev.Href == next.Href && prev.Style == next.Style {
			prev.Text = strings.TrimSpace(prev.Text + " " + next.Text)
			continue
		}
		out = append(out, next)
	}
	return out
}

func writeInlinePlainText(out *strings.Builder, text string) {
	parts := strings.Split(text, "\n")
	for i, part := range parts {
		if i > 0 {
			out.WriteByte('\n')
		}
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		writeInlineText(out, part)
	}
}

func appendInlineGlyphSpans(spans []glyph.Span, blockStyle glyph.Style, in Inline) []glyph.Span {
	style := inlineGlyphStyle(blockStyle, in)
	parts := strings.Split(in.Text, "\n")
	for i, part := range parts {
		if i > 0 {
			spans = append(spans, glyph.Span{Text: "\n", Style: style})
		}
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if needsInlineSpace(lastSpanText(spans), part) {
			spans = append(spans, glyph.Span{Text: " ", Style: blockStyle})
		}
		spans = append(spans, glyph.Span{Text: part, Style: style})
	}
	return spans
}

func writeInlineText(out *strings.Builder, text string) {
	if needsInlineSpace(lastBuilderText(out), text) {
		out.WriteByte(' ')
	}
	out.WriteString(text)
}

func lastBuilderText(out *strings.Builder) string {
	if out.Len() == 0 {
		return ""
	}
	return out.String()[out.Len()-1:]
}

func lastSpanText(spans []glyph.Span) string {
	if len(spans) == 0 {
		return ""
	}
	return spans[len(spans)-1].Text
}

func splitGlyphLines(spans []glyph.Span) [][]glyph.Span {
	lines := [][]glyph.Span{{}}
	for _, span := range spans {
		parts := strings.Split(span.Text, "\n")
		for i, part := range parts {
			if i > 0 {
				lines = append(lines, nil)
			}
			if part == "" {
				continue
			}
			next := span
			next.Text = part
			lines[len(lines)-1] = append(lines[len(lines)-1], next)
		}
	}
	return lines
}

func needsInlineSpace(prev, next string) bool {
	if prev == "" || next == "" {
		return false
	}
	if strings.HasSuffix(prev, " ") || strings.HasSuffix(prev, "\n") || strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\n") {
		return false
	}
	if strings.ContainsRune(",.;:!?)]}%", rune(next[0])) {
		return false
	}
	if strings.ContainsRune("([{", rune(prev[len(prev)-1])) {
		return false
	}
	return true
}
