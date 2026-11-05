package shell

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"

	"github.com/egoist/mygo/ui"
)

//go:embed mark.png
var markPNG []byte

var mark *ui.Bitmap

func init() {
	img, err := png.Decode(bytes.NewReader(markPNG))
	if err != nil {
		panic("shell: decode brand mark: " + err.Error())
	}
	_ = image.Rect
	mark = ui.NewBitmap(img)
}
