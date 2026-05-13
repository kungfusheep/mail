package preview

import (
	"strings"
	"testing"

	"github.com/kungfusheep/glyph"
)

func TestParseHTMLBuildsStructuredDocument(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<h1>Receipt</h1>
			<p>Hello <strong>Alex</strong>, see <a href="https://example.test">details</a>.</p>
			<ul><li>First item</li><li><em>Second</em> item</li></ul>
			<blockquote><p>older reply</p></blockquote>
			<p><code>ABC123</code></p>
			<img src="https://example.test/a.png" alt="chart">
			<hr>
		</body></html>
	`, "")

	if len(doc.Blocks) != 8 {
		t.Fatalf("blocks = %d, want 8: %#v", len(doc.Blocks), doc.Blocks)
	}
	if doc.Blocks[0].Kind != BlockHeading || doc.Blocks[0].Level != 1 {
		t.Fatalf("first block = %#v, want h1 heading", doc.Blocks[0])
	}
	if doc.Blocks[1].Kind != BlockParagraph {
		t.Fatalf("second block kind = %v, want paragraph", doc.Blocks[1].Kind)
	}
	if got, want := doc.Blocks[1].PlainText(), "Hello Alex, see details."; got != want {
		t.Fatalf("paragraph text = %q, want %q", got, want)
	}
	if !hasInline(doc.Blocks[1], "Alex", InlineStrong, "") {
		t.Fatalf("paragraph did not preserve strong inline: %#v", doc.Blocks[1].Inlines)
	}
	if !hasInline(doc.Blocks[1], "details", 0, "https://example.test") {
		t.Fatalf("paragraph did not preserve link inline: %#v", doc.Blocks[1].Inlines)
	}
	if doc.Blocks[2].Kind != BlockListItem || doc.Blocks[3].Kind != BlockListItem {
		t.Fatalf("list blocks = %v/%v, want list items", doc.Blocks[2].Kind, doc.Blocks[3].Kind)
	}
	if doc.Blocks[4].Kind != BlockQuote {
		t.Fatalf("quote block kind = %v, want quote", doc.Blocks[4].Kind)
	}
	if doc.Blocks[5].Inlines[0].Style&InlineCode == 0 {
		t.Fatalf("code inline = %#v, want code style", doc.Blocks[5].Inlines[0])
	}
	if doc.Blocks[6].Kind != BlockImage || !strings.Contains(doc.Blocks[6].PlainText(), "chart") {
		t.Fatalf("image block = %#v, want image placeholder with alt text", doc.Blocks[6])
	}
	if doc.Blocks[7].Kind != BlockDivider {
		t.Fatalf("last block kind = %v, want divider", doc.Blocks[7].Kind)
	}
}

func TestParseTextKeepsEmailSegments(t *testing.T) {
	doc := ParseText("Main body\n\nOn Tue, Bob wrote:\n> old reply\n-- \nAlex")

	if len(doc.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3: %#v", len(doc.Blocks), doc.Blocks)
	}
	if doc.Blocks[0].Kind != BlockParagraph {
		t.Fatalf("first block kind = %v, want paragraph", doc.Blocks[0].Kind)
	}
	if doc.Blocks[1].Kind != BlockQuote {
		t.Fatalf("second block kind = %v, want quote", doc.Blocks[1].Kind)
	}
	if doc.Blocks[2].Kind != BlockSignature {
		t.Fatalf("third block kind = %v, want signature", doc.Blocks[2].Kind)
	}

	segments := doc.Segments()
	if len(segments) != 3 || segments[0].Kind != SegmentMain || segments[1].Kind != SegmentQuote || segments[2].Kind != SegmentSignature {
		t.Fatalf("segments = %#v, want main then quote then signature", segments)
	}
}

func TestGlyphSpansStylesBlocksAndInlines(t *testing.T) {
	doc := Document{Blocks: []Block{
		{
			Kind: BlockParagraph,
			Inlines: []Inline{
				{Text: "Hello "},
				{Text: "Alex", Style: InlineStrong},
				{Text: "link", Href: "https://example.test"},
			},
		},
		{Kind: BlockQuote, Inlines: []Inline{{Text: "old reply"}}},
		{Kind: BlockSignature, Inlines: []Inline{{Text: "Alex"}}},
	}}

	spans := doc.GlyphSpans()
	if !spanWithTextHas(spans, "Alex", glyph.AttrBold) {
		t.Fatalf("spans = %#v, want bold Alex", spans)
	}
	if !spanWithTextHas(spans, "link", glyph.AttrUnderline) {
		t.Fatalf("spans = %#v, want underlined link", spans)
	}
	if !spanWithTextHas(spans, "old reply", glyph.AttrDim|glyph.AttrItalic) {
		t.Fatalf("spans = %#v, want dim italic quote", spans)
	}
	if !spanWithTextHas(spans, "Alex", glyph.AttrDim) {
		t.Fatalf("spans = %#v, want dim signature", spans)
	}
}

func TestGlyphLinesPreservesBlockLayout(t *testing.T) {
	doc := Document{Blocks: []Block{
		{Kind: BlockHeading, Inlines: []Inline{{Text: "Receipt"}}},
		{Kind: BlockParagraph, Inlines: []Inline{{Text: "Hello"}, {Text: "Alex", Style: InlineStrong}}},
		{Kind: BlockQuote, Inlines: []Inline{{Text: "old reply"}}},
	}}

	lines := doc.GlyphLines()
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5 including blank separators: %#v", len(lines), lines)
	}
	if !spanWithTextHas(lines[0], "Receipt", glyph.AttrBold) {
		t.Fatalf("heading line = %#v, want bold receipt", lines[0])
	}
	if len(lines[1]) != 0 || len(lines[3]) != 0 {
		t.Fatalf("separator lines = %#v / %#v, want blank separators", lines[1], lines[3])
	}
	if !spanWithTextHas(lines[2], "Alex", glyph.AttrBold) {
		t.Fatalf("paragraph line = %#v, want bold Alex", lines[2])
	}
	if !spanWithTextHas(lines[4], "old reply", glyph.AttrDim|glyph.AttrItalic) {
		t.Fatalf("quote line = %#v, want dim italic quote", lines[4])
	}
}

func TestParseHTMLKeepsTableEmailBlocks(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<span class="st-Preheader" style="display: none; max-height: 0;">hidden teaser text</span>
			<table>
				<tbody>
					<tr>
						<td class="st-Spacer"><div>&nbsp;</div></td>
						<td>
							<span style="font-weight: bold;">Benefits to you</span><br>
							Managing disputes is time consuming, and it can be difficult to know what evidence to submit. Smart Disputes helps:
						</td>
						<td class="st-Spacer"><div>&nbsp;</div></td>
					</tr>
				</tbody>
			</table>
			<table class="st-List st-List--unordered">
				<tbody>
					<tr>
						<td class="st-Spacer"><div>&nbsp;</div></td>
						<td width="28">
							<table><tbody><tr><td><div>&nbsp;</div></td></tr></tbody></table>
						</td>
						<td>
							<span style="font-weight: bold;">Save time:</span> Smart Disputes responds to disputes for you.
						</td>
						<td class="st-Spacer"><div>&nbsp;</div></td>
					</tr>
				</tbody>
			</table>
		</body></html>
	`, "")

	got := doc.PlainText()
	if strings.Contains(got, "hidden teaser text") {
		t.Fatalf("plain text = %q, want hidden preheader omitted", got)
	}
	if !strings.Contains(got, "Benefits to you\nManaging disputes") {
		t.Fatalf("plain text = %q, want br-preserved heading/body break", got)
	}
	if strings.Index(got, "Benefits to you") >= strings.Index(got, "Save time:") {
		t.Fatalf("plain text = %q, want table sections in order", got)
	}
	if len(doc.Blocks) < 2 {
		t.Fatalf("blocks = %#v, want table content split into sections", doc.Blocks)
	}
	if !hasInline(doc.Blocks[0], "Benefits to you", InlineStrong, "") {
		t.Fatalf("first block inlines = %#v, want bold benefits heading", doc.Blocks[0].Inlines)
	}

	spans := doc.GlyphSpans()
	if !spanWithTextHas(spans, "Benefits to you", glyph.AttrBold) {
		t.Fatalf("spans = %#v, want bold benefits heading", spans)
	}
	if !spansContainText(spans, "\n") {
		t.Fatalf("spans = %#v, want preserved line breaks", spans)
	}
}

func TestParseHTMLSkipsMalformedEmailCSS(t *testing.T) {
	doc := ParseHTML(`
		<html>
			<head>
				<table><tr><td>
					<style type="text/css">
						@media screen and (max-width:490px){.cta-buttons-component__item{width:100% !important}}
						@font-face{font-family:Bank Sans;src:url("https://example.test/font.woff") format("woff")}
					</style>
				</td></tr></table>
			</head>
			<body>
				<style type="text/css">body{background:#000!important}</style>
				<div class="preheader" style="display: none !important;">hidden teaser text</div>
				<table>
					<tr>
						<td><h1>Thinking ahead pays off</h1></td>
					</tr>
					<tr>
						<td><p>Hi there,</p><p>Planning early means more control.</p></td>
					</tr>
				</table>
			</body>
		</html>
	`, "")

	got := doc.PlainText()
	for _, unwanted := range []string{"@media", "@font-face", "!important", "hidden teaser text"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("plain text = %q, want %q omitted", got, unwanted)
		}
	}
	for _, want := range []string{"Thinking ahead pays off", "Hi there,", "Planning early means more control."} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain text = %q, want %q included", got, want)
		}
	}
}

func TestParseHTMLSkipsDecorativeEmailChrome(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<table>
				<tr><td>Trouble viewing this email? <a href="https://example.test/view">View in browser</a></td></tr>
				<tr><td><img src="https://example.test/hero.jpg"></td></tr>
				<tr><td><img alt="Image alt text here" src="https://example.test/button.png"></td></tr>
				<tr><td><img alt="logo" src="https://example.test/logo.png"></td></tr>
				<tr><td><h1>Thinking ahead pays off</h1></td></tr>
				<tr><td><p>Planning early means more control.</p></td></tr>
				<tr><td><a href="https://example.test/invest">What investors need to know</a></td></tr>
				<tr><td><img alt="Performance chart" src="https://example.test/chart.png"></td></tr>
			</table>
		</body></html>
	`, "")

	got := doc.PlainText()
	for _, unwanted := range []string{
		"Trouble viewing this email",
		"View in browser",
		"hero.jpg",
		"Image alt text here",
		"logo",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("plain text = %q, want %q omitted", got, unwanted)
		}
	}
	for _, want := range []string{
		"Thinking ahead pays off",
		"Planning early means more control.",
		"What investors need to know",
		"[image: Performance chart]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain text = %q, want %q included", got, want)
		}
	}
}

func TestParseHTMLPreservesBlocksNestedInsideInlineWrappers(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<table>
				<tr>
					<td>
						<span>
							<p>Hi there,</p>
							<p>If you're determined to reach your financial goals, now's the time to start planting the seeds for future growth.</p>
							<p>As the new tax year kicks off, serious investors are already making plans.</p>
						</span>
					</td>
				</tr>
			</table>
		</body></html>
	`, "")

	if len(doc.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3 nested paragraphs: %#v", len(doc.Blocks), doc.Blocks)
	}
	got := doc.PlainText()
	for _, want := range []string{
		"Hi there,\nIf you're determined",
		"future growth.\nAs the new tax year",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain text = %q, want nested block break %q", got, want)
		}
	}

	spans := doc.GlyphSpans()
	if !spansContainText(spans, "\n\n") {
		t.Fatalf("spans = %#v, want blank line between nested paragraphs", spans)
	}
}

func TestParseHTMLMergesTableBulletRows(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<table>
				<tr>
					<td width="24">•</td>
					<td>get refunds sooner - if you're due one, it can be processed earlier</td>
				</tr>
				<tr>
					<td width="24">•</td>
					<td>know what you owe - this will help you budget ahead</td>
				</tr>
			</table>
		</body></html>
	`, "")

	if len(doc.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 merged bullet rows: %#v", len(doc.Blocks), doc.Blocks)
	}
	for _, block := range doc.Blocks {
		if block.Kind != BlockListItem {
			t.Fatalf("block = %#v, want list item", block)
		}
		if strings.TrimSpace(block.PlainText()) == "•" {
			t.Fatalf("block = %#v, want bullet merged with content", block)
		}
	}

	got := doc.PlainText()
	if strings.Contains(got, "•\n") {
		t.Fatalf("plain text = %q, want no standalone bullet lines", got)
	}
	for _, want := range []string{
		"get refunds sooner",
		"know what you owe",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain text = %q, want %q", got, want)
		}
	}

	spans := doc.GlyphSpans()
	if !spansContainText(spans, "• ") {
		t.Fatalf("spans = %#v, want glyph list marker", spans)
	}
}

func TestParseHTMLMergesTableNumberedRows(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<table>
				<tr>
					<td width="24">1.</td>
					<td>Gather your sign-in details and other information.</td>
				</tr>
				<tr>
					<td width="24">2.</td>
					<td>Check your saved information is correct.</td>
				</tr>
			</table>
		</body></html>
	`, "")

	if len(doc.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 merged numbered rows: %#v", len(doc.Blocks), doc.Blocks)
	}
	if doc.Blocks[0].Kind != BlockListItem || doc.Blocks[0].Marker != "1." {
		t.Fatalf("first block = %#v, want numbered list item", doc.Blocks[0])
	}
	if doc.Blocks[1].Kind != BlockListItem || doc.Blocks[1].Marker != "2." {
		t.Fatalf("second block = %#v, want numbered list item", doc.Blocks[1])
	}

	spans := doc.GlyphSpans()
	if !spansContainText(spans, "1. ") || !spansContainText(spans, "2. ") {
		t.Fatalf("spans = %#v, want numbered glyph markers", spans)
	}
}

func TestParseHTMLKeepsDataTableRows(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<table>
				<thead>
					<tr>
						<th>Qty</th>
						<th>Product</th>
						<th>Unit price</th>
						<th>Total</th>
						<th>Saved</th>
					</tr>
				</thead>
				<tbody>
					<tr>
						<td>1</td>
						<td>Cheese snacks 4 pack</td>
						<td>GBP 1.75</td>
						<td>GBP 1.25</td>
						<td>-GBP 0.50</td>
					</tr>
					<tr>
						<td>1</td>
						<td>Soft cheese 200g</td>
						<td>GBP 0.95</td>
						<td>GBP 0.95</td>
						<td></td>
					</tr>
				</tbody>
			</table>
		</body></html>
	`, "")

	if len(doc.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 table block: %#v", len(doc.Blocks), doc.Blocks)
	}
	if doc.Blocks[0].Kind != BlockTable || doc.Blocks[0].Table == nil {
		t.Fatalf("block = %#v, want table", doc.Blocks[0])
	}

	got := doc.PlainText()
	for _, want := range []string{
		"Qty | Product",
		"---+",
		"1   | Cheese snacks 4 pack",
		"1   | Soft cheese 200g",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain text = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "Qty\nProduct\nUnit price") {
		t.Fatalf("plain text = %q, want row-oriented table", got)
	}

	spans := doc.GlyphSpans()
	if !spanContainingTextHas(spans, "Qty | Product", glyph.AttrBold) {
		t.Fatalf("spans = %#v, want bold table header", spans)
	}
}

func TestParseHTMLKeepsReceiptTableSectionAndOfferRows(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<table>
				<tr>
					<th>Qty</th>
					<th>Product</th>
					<th>Unit price</th>
					<th>Total</th>
					<th>Saved</th>
				</tr>
				<tr><td colspan="5"><strong>Fridge</strong></td></tr>
				<tr><td colspan="5"><strong>Fridge</strong></td></tr>
				<tr>
					<td>1</td>
					<td>
						Cheese snacks 4 pack
						<table><tr><td>mobile duplicate noise</td></tr></table>
					</td>
					<td>GBP 1.75</td>
					<td>GBP 1.25</td>
					<td>-GBP 0.50</td>
				</tr>
				<tr><td colspan="5"><strong>Was GBP 1.75, now GBP 1.25</strong></td></tr>
				<tr><td colspan="5"><strong>Was GBP 1.75, now GBP 1.25</strong></td></tr>
				<tr>
					<td>1</td>
					<td>Soft cheese 200g</td>
					<td>GBP 0.95</td>
					<td>GBP 0.95</td>
					<td></td>
				</tr>
			</table>
		</body></html>
	`, "")

	if len(doc.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 table block: %#v", len(doc.Blocks), doc.Blocks)
	}
	got := doc.PlainText()
	if strings.Count(got, "Fridge") != 1 {
		t.Fatalf("plain text = %q, want duplicate section row collapsed", got)
	}
	if strings.Count(got, "Was GBP 1.75, now GBP 1.25") != 1 {
		t.Fatalf("plain text = %q, want duplicate offer row collapsed", got)
	}
	if strings.Contains(got, "Fridge |") || strings.Contains(got, "Was GBP 1.75, now GBP 1.25 |") {
		t.Fatalf("plain text = %q, want full-width rows rendered outside grid columns", got)
	}
	if strings.Contains(got, "mobile duplicate noise") {
		t.Fatalf("plain text = %q, want nested layout table omitted from cell", got)
	}
	for _, want := range []string{
		"Fridge",
		"1   | Cheese snacks 4 pack",
		"Was GBP 1.75, now GBP 1.25",
		"1   | Soft cheese 200g",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain text = %q, want %q", got, want)
		}
	}
}

func TestParseHTMLDeduplicatesResponsiveSectionRows(t *testing.T) {
	doc := ParseHTML(`
		<html><body>
			<table>
				<tr><th>Qty</th><th>Product</th><th>Total</th></tr>
				<tr><td colspan="3"><strong>Fridge</strong></td></tr>
				<tr><td><strong>Fridge</strong></td></tr>
				<tr><td>1</td><td>Milk</td><td>GBP 1.65</td></tr>
				<tr><td colspan="3"><strong>Freezer</strong></td></tr>
				<tr><td><strong>Freezer</strong></td></tr>
				<tr><td>1</td><td>Peas</td><td>GBP 1.00</td></tr>
				<tr><td colspan="3"><strong>Cupboard</strong></td></tr>
				<tr><td><strong>Cupboard</strong></td></tr>
				<tr><td>1</td><td>Pasta</td><td>GBP 0.95</td></tr>
			</table>
		</body></html>
	`, "")

	got := doc.PlainText()
	for _, section := range []string{"Fridge", "Freezer", "Cupboard"} {
		if count := strings.Count(got, section); count != 1 {
			t.Fatalf("plain text = %q, want %s once, got %d", got, section, count)
		}
	}
	for _, want := range []string{
		"1   | Milk",
		"1   | Peas",
		"1   | Pasta",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain text = %q, want %q", got, want)
		}
	}
}

func hasInline(block Block, text string, style InlineStyle, href string) bool {
	for _, in := range block.Inlines {
		if in.Text == text && in.Style&style == style && in.Href == href {
			return true
		}
	}
	return false
}

func spansContainText(spans []glyph.Span, text string) bool {
	for _, span := range spans {
		if span.Text == text {
			return true
		}
	}
	return false
}

func spanWithTextHas(spans []glyph.Span, text string, attr glyph.Attribute) bool {
	for _, span := range spans {
		if span.Text == text && span.Style.Attr&attr == attr {
			return true
		}
	}
	return false
}

func spanContainingTextHas(spans []glyph.Span, text string, attr glyph.Attribute) bool {
	for _, span := range spans {
		if strings.Contains(span.Text, text) && span.Style.Attr&attr == attr {
			return true
		}
	}
	return false
}
