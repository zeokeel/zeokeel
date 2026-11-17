package uikit

// The product's Markdown image fallback (2026-11 UI review B6). mygo's markdown
// shows an image that has not loaded as a bare grey chip of its alt text, which
// reads as a leftover placeholder rather than an honest "this is an image that
// did not load". The product's document/artifact previews do not set a LoadImage
// (the bytes are not fetched into the view), so every image takes that fallback.
// This override intercepts image-only paragraph blocks through Options.Block and
// draws an image glyph + the alt text + a quiet "图片未加载" note, so a failed or
// unfetched image is named as one instead of looking like stray text. When a
// caller does set LoadImage, the block is left to mygo (it loads and renders the
// real image, keeping its own pending placeholder).

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// withMarkdownImageFallback wires the image-block fallback into one Markdown
// options set. It returns the same options for chaining. The block callback is
// asked first for every block; it only takes image-only paragraphs and only when
// no LoadImage is set (a loader means mygo renders the real image).
func withMarkdownImageFallback(o *markdown.Options) *markdown.Options {
	o.Block = func(c *ui.Context, d *parser.Document, i int) bool {
		if o.LoadImage != nil {
			return false
		}
		b := d.Block(i)
		if b.Kind != parser.Paragraph {
			return false
		}
		in := d.Inlines(i)
		if !markdownImagesOnly(in) {
			return false
		}
		drawMarkdownImageFallback(c, in)
		return true
	}
	return o
}

// markdownImagesOnly reports whether a paragraph's inlines are images and the
// whitespace between them alone (the shape mygo renders as an image row). It
// mirrors the markdown view's own image-paragraph test so the fallback takes
// exactly the blocks the default would have drawn as images.
func markdownImagesOnly(in []parser.Inline) bool {
	n := 0
	for k := 0; k < len(in); k = in[k].Next {
		switch in[k].Kind {
		case parser.Image:
			n++
		case parser.SoftBreak, parser.LineBreak:
		case parser.Text:
			if strings.TrimSpace(in[k].Text) != "" {
				return false
			}
		default:
			return false
		}
	}
	return n > 0
}

// drawMarkdownImageFallback draws one image-only paragraph as a row of quiet
// image chips: the image glyph, the alt text and the "not loaded" note. Each chip
// keeps the image role and the alt as its accessible name, so assistive
// technology still reads it as an image with its description.
func drawMarkdownImageFallback(c *ui.Context, in []parser.Inline) {
	t := design.Of(c)
	note := markdownImageNote(c)
	ui.Row(c).Wrap().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		for k := 0; k < len(in); k = in[k].Next {
			n := &in[k]
			if n.Kind != parser.Image {
				continue
			}
			alt := parser.PlainText(in[k+1 : n.Next])
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).
				Padding(design.Space1, design.Space2).Radius(design.RadiusSM).
				Background(t.BgStrong).Role(ui.RoleImage).Label(alt).Children(func() {
				ui.Icon(c, icons.Image()).Size(design.Space3, design.Space3).
					TextColor(t.LabelTertiary)
				if alt != "" {
					design.Apply(ui.Text(c, alt), t.Font(design.TypeXS)).
						TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
				}
				design.Apply(ui.Text(c, note), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			})
		}
	})
}

// markdownImageNote localizes the fallback note from the frame's language tag
// (the block callback has the Context, not a locale service).
func markdownImageNote(c *ui.Context) string {
	if strings.HasPrefix(c.Locale(), "en") {
		return "Image not loaded"
	}
	return "图片未加载"
}
