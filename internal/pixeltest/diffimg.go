package pixeltest

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

// DiffImage paints differing pixels red over a dimmed copy of got, for
// eyeballing what moved.
func DiffImage(got, want image.Image, tolerance uint8) *image.RGBA {
	b := got.Bounds().Intersect(want.Bounds())
	out := image.NewRGBA(b)
	g := toRGBA(got)
	w := toRGBA(want)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			gc := g.RGBAAt(x, y)
			wc := w.RGBAAt(x, y)
			if delta(gc.R, wc.R) > tolerance || delta(gc.G, wc.G) > tolerance || delta(gc.B, wc.B) > tolerance {
				out.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			} else {
				out.SetRGBA(x, y, color.RGBA{R: gc.R / 4, G: gc.G / 4, B: gc.B / 4, A: 255})
			}
		}
	}
	return out
}

// SavePNG writes img to path.
func SavePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// LoadPNG reads a PNG file.
func LoadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// Draw is re-exported so callers can compose overlays without importing
// image/draw themselves.
var Draw = draw.Draw
