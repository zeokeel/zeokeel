package uikit

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The product's Markdown prose theme for the main reading column (chat
// replies). Markdown's own defaults aim at documents: generous margins, large
// headings, a 1.5 line box. A transcript is read in passes between turns, so
// this theme tightens the rhythm while keeping the app's type hierarchy:
//
//   - the body keeps the chat font-size setting and a proportional 1.55 line
//     box (a fixed 22 DIP box was too tight at 16 and too airy at 12.5);
//   - headings stay below the screen title (H1 17–18, H2 ≤16, H3 ≤15) with
//     explicit, smaller top/bottom margins instead of document-scale gaps;
//   - inline code sits on a quiet surface tint with the mono face, and fences
//     use MarkdownCodeBlock (filled raised surface, no border, language only
//     when named);
//   - block quotes, rules and table lines use the light border tint, and the
//     table header uses the raised surface, so the column has one quiet
//     surface vocabulary instead of Markdown's document greys.
func ChatMarkdown(t *design.Tokens, size float32) *markdown.Options {
	if size <= 0 {
		size, _, _ = t.Metrics(design.TypeBody)
	}
	theme := markdown.DefaultTheme(t.Theme())
	theme.FontSize = size
	theme.LineHeight = 1.55
	theme.Text = t.LabelPrimary
	theme.Muted = t.LabelTertiary
	theme.Link = t.Accent
	theme.LinkWeight = 500
	theme.Strong = t.LabelPrimary
	theme.Code = t.LabelPrimary
	theme.CodeBackground = t.BgStrong
	theme.CodeSize = 0.9
	theme.BlockSpacing = 0.72
	theme.ItemSpacing = 0.28
	theme.ListIndent = 1.5
	theme.QuoteBar = t.BorderL2
	theme.QuoteBarWidth = 3
	theme.QuoteText = t.LabelSecondary
	theme.Rule = t.BorderL2
	theme.CodeBlockBackground = t.BgStrong
	theme.CodeBlockBorder = ui.Transparent
	theme.CodeBlockRadius = float32(design.RadiusSM)
	theme.CodeBlockPadding = 0.8
	theme.CodeLineHeight = 1.5
	theme.TableBorder = t.BorderL2
	theme.TableHeader = t.BgRaised
	theme.TableStripe = ui.Transparent
	theme.TableCellPadding = [2]float32{0.7, 0.45}
	theme.TableMaxColumn = 22
	// The transcript's headings follow the app type scale, not Markdown's
	// document sizes: H1 17–18, H2 ≤16, H3 ≤15 at weight 600, all below the
	// 20/600 screen title. Sizes are ems of the body size so the small/large
	// chat font settings keep the same relation.
	heading := func(target float32) float32 {
		if size <= 0 {
			return 1
		}
		return target / size
	}
	h1 := size + 3.5
	if h1 < 17 {
		h1 = 17
	}
	if h1 > 18 {
		h1 = 18
	}
	theme.Headings[0].Size = heading(h1)
	theme.Headings[1].Size = heading(min(size+2, 16))
	theme.Headings[2].Size = heading(min(size+1, 15))
	theme.Headings[3].Size = 1
	theme.Headings[4].Size = 0.95
	theme.Headings[5].Size = 0.9
	for index := range theme.Headings {
		theme.Headings[index].Weight = 600
	}
	theme.Headings[0].Top, theme.Headings[0].Bottom = 1.1, 0.4
	theme.Headings[1].Top, theme.Headings[1].Bottom = 1.0, 0.4
	theme.Headings[2].Top, theme.Headings[2].Bottom = 0.9, 0.3
	for index := 3; index < 6; index++ {
		theme.Headings[index].Top, theme.Headings[index].Bottom = 0.8, 0.25
	}
	theme.Headings[5].Color = theme.Muted
	return &markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
		CodeBlock:  MarkdownCodeBlock,
	}
}
