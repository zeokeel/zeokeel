package shell

import (
	"bytes"
	_ "embed"
	"image/png"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
)

// The embedded brand marks are rendered from the logo SVGs in logo/. The light
// mark (logo/logo.svg, 48x48) keeps its white rounded container, which is part
// of the light identity; the dark mark (logo/logo-dark.svg, 96x96) has a black
// container and is embedded at 2x so the 24-48 DIP uses stay crisp. The dark
// theme must use the dark asset: the light mark's white container edge shows
// up as a white fringe on the dark hero and sidebar (design review P0-3).
//
//go:embed mark.png
var markPNG []byte

//go:embed mark-dark@2x.png
var markDarkPNG []byte

var (
	mark     *ui.Bitmap
	markDark *ui.Bitmap
)

func init() {
	mark = brandBitmap(markPNG, "mark.png")
	markDark = brandBitmap(markDarkPNG, "mark-dark@2x.png")
}

// brandBitmap decodes an embedded brand PNG into a bitmap. A corrupt asset is a
// build/asset error, so it panics at startup instead of degrading a screen.
func brandBitmap(data []byte, name string) *ui.Bitmap {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic("shell: decode brand " + name + ": " + err.Error())
	}
	return ui.NewBitmap(img)
}

// brandMark returns the brand mark for the frame's appearance: the dark asset
// in the dark theme, the light one in the light theme. Callers pass the same
// tokens the rest of the frame paints with, so the mark follows the theme
// switch (and the system appearance when the theme setting follows it).
func brandMark(t *design.Tokens) *ui.Bitmap {
	if t != nil && t.Dark {
		return markDark
	}
	return mark
}
