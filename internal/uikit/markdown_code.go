package uikit

// The product's code fence for Markdown views (chat replies, reasoning panels,
// the inspector's documents). mygo's markdown.DefaultCodeBlock draws a bordered
// card whose header is the fence's raw info string ("text" for a plain fence),
// which read like a stray card in the transcript. This override keeps the
// official body (highlighting, horizontal scroll, streaming) and gives the block
// the product's anatomy (B-spec-v2 §3.6):
//
//   - a 28 DIP header: the language in the mono face at 11/500 tertiary on the
//     left, one icon-only copy action (16 DIP glyph, 24 DIP target) on the right;
//   - one 1 DIP BorderL1 rule between the header and the code — the block's only
//     internal line;
//   - a mono 13/20 body with 12 DIP of room on every side, on the strong
//     surface tint, inside one 1 DIP BorderL2 frame at the 6 DIP radius;
//   - short fences shrink to their content width (the longest code line plus the
//     body's padding) instead of painting a full-width strip for a one-line
//     command; long lines keep horizontal scrolling;
//   - a fence that names no real language shows no language label, instead of
//     the meaningless "text" the default printed.

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// fenceHeaderHeight is the fence header's height: 28 DIP, the compact control
// step, so the language row reads as a header and not as another line of code.
const fenceHeaderHeight float32 = design.ControlCompactHeight

// fenceCopiedHold is how long the copy action shows its success check after a
// copy, matching the product's other copy affordances.
const fenceCopiedHold = 1200 * time.Millisecond

// MarkdownCodeBlock is the product's code fence. Pass it as
// markdown.Options.CodeBlock (session replies, reasoning panels and the
// inspector documents all use it) to keep one fence anatomy everywhere.
func MarkdownCodeBlock(c *ui.Context, b *markdown.CodeBlockView) {
	t := design.Of(c)
	code := b.Code()
	root := ui.Column(c).Width(fenceWidth(c, t, code)).MaxWidthPercent(100).
		Background(t.BgStrong).Border(1, t.BorderL2).Radius(design.RadiusSM).
		Clip()
	// The copy action's feedback state lives on the block (keyed by its index in
	// the document), so it survives the frames the view rebuilds.
	copiedAt := ui.Local(root, "copiedAt", func() time.Time { return time.Time{} })
	copied := !copiedAt.IsZero() && c.Now().Sub(*copiedAt) < fenceCopiedHold
	root.Role(ui.RoleGroup).Label(fenceLabel(c, b.Language))
	root.Children(func() {
		design.Apply(ui.Row(c).FillWidth().AlignItems(ui.Center).
			Height(fenceHeaderHeight).PaddingX(design.Space3).Gap(design.Space2),
			func(el ui.Element) ui.Element {
				// The header's rule is the block's only internal line: one
				// BorderL1 hairline, never a second frame.
				return el.BorderWidth(design.Space0, design.Space0, 1, design.Space0).
					BorderColor(t.BorderL1)
			}).Children(func() {
			if language := fenceLanguage(b.Language); language != "" {
				design.Apply(ui.Text(c, language), t.MonoText(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
			ui.Spacer(c).Grow(1)
			// An icon-only copy action: the official text button ("复制") widened
			// the header for a control whose meaning is already a glyph. The
			// label stays for assistive technology, and the icon turns into a
			// check for a moment after the copy.
			icon, label := icons.Copy(), fenceCopyLabel(c, false)
			if copied {
				icon, label = icons.Check(), fenceCopyLabel(c, true)
			}
			IconButton(c, IconButtonOptions{
				Label:    label,
				Icon:     icon,
				Size:     design.IconSizeS,
				Variant:  IconButtonNative,
				Disabled: b.Open,
				InkColor: t.LabelTertiary,
				OnClick: func() {
					c.WriteClipboard(code)
					*copiedAt = c.Now()
					c.After(fenceCopiedHold)
				},
			})
		})
		// The code body is the panel's reading step: mono 13/20, one step under
		// prose (the theme's code size serves inline code spans, which are 12/18).
		design.Apply(b.Body(c), t.MonoText(design.TypeXS))
	})
}

// fenceLabel is the block's accessible name: the fence's language when it names
// one, the generic code label otherwise.
func fenceLabel(c *ui.Context, info string) string {
	if language := fenceLanguage(info); language != "" {
		return language
	}
	if englishLocale(c) {
		return "Code"
	}
	return "代码"
}

// fenceCopyLabel names the copy action for assistive technology. A Markdown view
// carries no assembly-configured locale, so the frame's own locale decides;
// the visible surface is an icon either way.
func fenceCopyLabel(c *ui.Context, copied bool) string {
	if englishLocale(c) {
		if copied {
			return "Copied"
		}
		return "Copy code"
	}
	if copied {
		return "已复制"
	}
	return "复制代码"
}

// englishLocale reports whether the window's locale is an English one.
func englishLocale(c *ui.Context) bool {
	return strings.HasPrefix(strings.ToLower(c.Locale()), "en")
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

// fenceWidth is a short fence's content width: the longest code line measured
// in the brand mono face at the body size, plus the body's room on both sides.
// The markdown body pads by 12 DIP of the body's own size, which this mirrors
// with Space3; the header's copy action needs a floor, hence Space8*4.
// MaxWidthPercent(100) keeps a long line inside the reading column, where the
// body scrolls sideways instead.
func fenceWidth(c *ui.Context, t *design.Tokens, code string) float32 {
	size, _, _ := t.Metrics(design.TypeXS)
	// Pick the longest line by rune count first and measure only that one: a
	// frame may build a dozen fences and MeasureText is the costly part.
	longest := ""
	for _, line := range strings.Split(strings.TrimRight(code, "\n"), "\n") {
		if len(line) > len(longest) {
			longest = line
		}
	}
	width := float32(design.Space8 * 4)
	if longest != "" {
		measured, _ := c.MeasureText(0, ui.Span{
			Text: longest, Font: design.MonoFontStack(), Size: size,
		})
		if measured += design.Space3 * 2; measured > width {
			width = measured
		}
	}
	return width
}
