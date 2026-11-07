package brand

import (
	"bytes"
	_ "embed"
	"image/png"

	"github.com/egoist/mygo/ui"
)

// The embedded brand marks are rendered from the logo SVGs in the archived
// project's logo/ directory. The light mark keeps its white rounded container
// (part of the light identity); the dark mark has a black container and is
// embedded at 2x so 24-48 DIP uses stay crisp.
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

func brandBitmap(data []byte, name string) *ui.Bitmap {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic("brand: decode " + name + ": " + err.Error())
	}
	return ui.NewBitmap(img)
}

// Mark returns the brand mark bitmap for the given appearance.
func Mark(dark bool) *ui.Bitmap {
	if dark {
		return markDark
	}
	return mark
}
