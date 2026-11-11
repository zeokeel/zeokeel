package uikit

// The product's quiet code fence for Markdown views (chat replies, reasoning
// panels, the inspector's documents). mygo's markdown.DefaultCodeBlock draws a
// bordered card with the fence's raw info string as its header ("text" for a
// plain fence), which read like a stray card in the transcript. This override
// keeps the official CodeBlockView (highlighting, horizontal scroll, streaming,
// copy feedback) and only changes the surface:
//
//   - a filled raised surface with RadiusSM and no border, so a fence is a
//     bounded content block, not another card in a card wall;
//   - the language is shown only when the fence names a real language; generic
//     and empty info strings no longer print "text";
//   - the official copy action stays at the header's trailing edge.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/design"
)

// MarkdownCodeBlock is the product's code fence. Pass it as
// markdown.Options.CodeBlock (session replies, reasoning panels and the
// inspector documents all use it) to keep one fence anatomy everywhere.
func MarkdownCodeBlock(c *ui.Context, b *markdown.CodeBlockView) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Background(t.BgStrong).Radius(design.RadiusSM).
		AlignSelf(ui.Stretch).Clip().Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).
			Padding(design.Space1, design.Space2, design.Space0, design.Space2).Children(func() {
			if language := fenceLanguage(b.Language); language != "" {
				design.Apply(ui.Text(c, language), t.MonoText(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
			ui.Spacer(c).Grow(1)
			// The official copy button keeps its "copied" feedback, its disabled
			// state while a fence is still streaming, and its accessible name.
			b.CopyButton(c)
		})
		b.Body(c)
	})
}

// fenceLanguage returns the fence's language when it names one, and "" for the
// generic info strings a plain fence carries. Those were the ones printed as a
// meaningless "text" label by the default block.
func fenceLanguage(info string) string {
	language := strings.ToLower(strings.TrimSpace(info))
	if index := strings.IndexAny(language, " \t"); index >= 0 {
		language = language[:index]
	}
	switch language {
	case "", "text", "txt", "plain", "plaintext", "none", "output":
		return ""
	default:
		return language
	}
}
