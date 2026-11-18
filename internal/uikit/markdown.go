package uikit

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The product's Markdown prose theme for the main reading column (chat
// replies). Markdown's own defaults aim at documents: generous margins, large
// headings, a 1.5 line box. A transcript is read in passes between turns, so
// this theme sets the product's rhythm (B-spec-v2 §3.6):
//
//   - body 14/22 with 12 DIP between blocks;
//   - headings below the screen title (H1 18/600, H2 16/600, H3 15/600);
//   - inline code in the mono face at 12/18 on the strong surface tint, a 4 DIP
//     chip rather than a full-line tint;
//   - fences drawn by MarkdownCodeBlock (a 28 DIP language header over a mono
//     13/20 body, one hairline between them, a 1 DIP frame and a 6 DIP radius);
//   - quotes with a 2 DIP structural rule and tertiary text, tables with a
//     strong header row and structural lines, rules structural.
//
// The wide blocks (fences, tables) keep the column's width: they scroll
// sideways rather than squeezing prose into a second column.
func ChatMarkdown(t *design.Tokens, size float32) *markdown.Options {
	if size <= 0 {
		size, _, _ = t.Metrics(design.TypeBody)
	}
	theme := markdown.DefaultTheme(t.Theme())
	theme.FontSize = size
	// 14/22 body: the line box follows the size so the small/large chat font
	// settings keep the same leading.
	theme.LineHeight = 22.0 / 14.0
	theme.Text = t.LabelPrimary
	theme.Muted = t.LabelTertiary
	theme.Link = t.Accent
	theme.LinkWeight = design.WeightMedium
	theme.Strong = t.LabelPrimary
	theme.StrongWeight = design.WeightSemibold
	// Inline code: mono 12/18 on the strong tint. Markdown draws a code span as
	// a tinted run of the mono face; the 12/18 step is the metadata size, one
	// step under the 13 DIP body of a fence.
	theme.Code = t.LabelPrimary
	theme.CodeBackground = t.BgStrong
	theme.CodeSize = 12.0 / 14.0
	// 12 DIP between blocks; a tight list keeps its own smaller rhythm.
	theme.BlockSpacing = 12.0 / 14.0
	theme.ItemSpacing = design.Space1 / 14.0
	theme.ListIndent = 1.5
	// A quote is structural: a 2 DIP BorderL2 rule and tertiary text.
	theme.QuoteBar = t.BorderL2
	theme.QuoteBarWidth = design.Space1 / 2
	theme.QuoteText = t.LabelTertiary
	theme.Rule = t.BorderL2
	// The fence: the mono body sits on the strong surface inside a 1 DIP
	// structural frame at the code radius. The padding is in ems of the body
	// size, so 12 DIP of room on every side.
	theme.CodeBlockBackground = t.BgStrong
	theme.CodeBlockBorder = t.BorderL2
	theme.CodeBlockRadius = design.RadiusSM
	theme.CodeBlockPadding = 12.0 / 14.0
	// The fence body's own 13/20 step is applied by MarkdownCodeBlock (the theme
	// has one code size, shared with inline code, which is one step smaller).
	theme.CodeLineHeight = 20.0 / 12.0
	// Tables: a strong header row over structural lines, 12 DIP of cell padding.
	// mygo draws a table's grid with one color (outer frame, row rules and the
	// lines between cells). The product wants a header rule and row rules only;
	// until the widget can draw them alone, the one token is BorderL2, the
	// lightest structural weight, so the vertical lines recede instead of
	// forming a grid (B-spec-v2 §3.6, VD-13 — the gap is recorded in the report).
	theme.TableBorder = t.BorderL2
	theme.TableHeader = t.BgStrong
	theme.TableStripe = ui.Transparent
	theme.TableCellPadding = [2]float32{12.0 / 14.0, design.Space2 / 14.0}
	theme.TableMaxColumn = 22
	// Headings follow the app type scale, not Markdown's document sizes: H1 18,
	// H2 16, H3 15 at weight 600, all below the 20/600 screen title. Sizes are
	// ems of the body size so the chat font setting keeps the same relation.
	heading := func(target float32) float32 {
		if size <= 0 {
			return 1
		}
		return target / size
	}
	// UD-02: the absolute clamp bounds are DIP measures of text, so they follow
	// the Tokens' TextScale like the body; at scale 1 the historical bounds hold.
	h1 := size + t.Scale(3.5)
	if h1 < t.Scale(17) {
		h1 = t.Scale(17)
	}
	if h1 > t.Scale(18) {
		h1 = t.Scale(18)
	}
	theme.Headings[0].Size = heading(h1)
	theme.Headings[1].Size = heading(min(size+t.Scale(2), t.Scale(16)))
	theme.Headings[2].Size = heading(min(size+t.Scale(1), t.Scale(15)))
	theme.Headings[3].Size = 1
	theme.Headings[4].Size = 0.95
	theme.Headings[5].Size = 0.9
	for index := range theme.Headings {
		theme.Headings[index].Weight = design.WeightSemibold
	}
	theme.Headings[0].Top, theme.Headings[0].Bottom = 1.1, 0.4
	theme.Headings[1].Top, theme.Headings[1].Bottom = 1.0, 0.4
	theme.Headings[2].Top, theme.Headings[2].Bottom = 0.9, 0.3
	for index := 3; index < 6; index++ {
		theme.Headings[index].Top, theme.Headings[index].Bottom = 0.8, 0.25
	}
	theme.Headings[5].Color = theme.Muted
	return withMarkdownImageFallback(&markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
		CodeBlock:  MarkdownCodeBlock,
	})
}

// MarkdownReply shows one finished reply on the product's reading measure: the
// prose blocks (paragraphs, lists, quotes) are bounded by design.ProseMeasure so
// a wide window does not produce 90-character lines, while a reply that carries
// a table keeps the full column, because mygo's table scrolls sideways inside
// whatever width it is given and a narrow measure would make it scroll at once.
//
// A reply left-aligned inside its column, not centered: the transcript's blocks
// (fences, tables, cards) share the column's left edge, and a centered prose
// block would start every paragraph at a different x from the code below it.
func MarkdownReply(c *ui.Context, src string, options *markdown.Options) ui.Element {
	return markdownProse(c, src, options, false)
}

// MarkdownReplyStream is MarkdownReply for a reply still being written: it keeps
// the same measure rule, so the text does not re-flow into a different width
// when the stream completes.
func MarkdownReplyStream(c *ui.Context, src string, options *markdown.Options) ui.Element {
	return markdownProse(c, src, options, true)
}

// markdownProse builds the view inside the reply's measure.
func markdownProse(c *ui.Context, src string, options *markdown.Options, streaming bool) ui.Element {
	column := ui.Column(c).FillWidth()
	if markdownMeasuresProse(src) {
		column = column.MaxWidth(design.ProseMeasure)
	}
	return column.Children(func() {
		if streaming {
			markdown.Stream(c, src, options)
			return
		}
		markdown.View(c, src, options)
	})
}

// markdownMeasuresProse reports whether src is prose-only, so the reading
// measure applies. A table is the one block that cannot shrink gracefully, and
// markdown.Measure would leave it scrolling in a 640 DIP box; fences are drawn
// shrink-to-content and scroll on their longest line, so they stay inside the
// measure without harm.
func markdownMeasuresProse(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			return false
		}
	}
	return true
}
