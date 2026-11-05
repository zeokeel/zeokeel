// Package pixeltest compares rendered frames against the baseline, the
// evidence gate described in docs/PLAN.md §5.9.
package pixeltest

import (
	"image"
	"image/draw"
)

// Result is the outcome of comparing two frames.
type Result struct {
	Total   int     // pixels compared
	Changed int     // pixels whose channel delta exceeded the tolerance
	Ratio   float64 // Changed / Total
}

// Compare counts pixels in roi that differ beyond tolerance (0-255 per
// channel). An empty roi compares the whole overlapping area.
func Compare(got, want image.Image, roi image.Rectangle, tolerance uint8) Result {
	b := got.Bounds().Intersect(want.Bounds())
	if roi.Empty() {
		roi = b
	} else {
		roi = roi.Intersect(b)
	}
	var res Result
	if roi.Empty() {
		return res
	}
	g := toRGBA(got)
	w := toRGBA(want)
	for y := roi.Min.Y; y < roi.Max.Y; y++ {
		for x := roi.Min.X; x < roi.Max.X; x++ {
			res.Total++
			gc := g.RGBAAt(x, y)
			wc := w.RGBAAt(x, y)
			if delta(gc.R, wc.R) > tolerance || delta(gc.G, wc.G) > tolerance ||
				delta(gc.B, wc.B) > tolerance || delta(gc.A, wc.A) > tolerance {
				res.Changed++
			}
		}
	}
	if res.Total > 0 {
		res.Ratio = float64(res.Changed) / float64(res.Total)
	}
	return res
}

func delta(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(b)
	draw.Draw(r, b, img, b.Min, draw.Src)
	return r
}

// CompareExcluding is Compare without the given rectangles, for regions the
// design intentionally changes (for example the ZeoKeel brand row replacing
// DSH's wordmark).
func CompareExcluding(got, want image.Image, tolerance uint8, exclude ...image.Rectangle) Result {
	res := Compare(got, want, got.Bounds(), tolerance)
	for _, r := range exclude {
		sub := Compare(got, want, r, tolerance)
		res.Total -= sub.Total
		res.Changed -= sub.Changed
	}
	if res.Total < 0 {
		res.Total = 0
	}
	if res.Changed < 0 {
		res.Changed = 0
	}
	if res.Total > 0 {
		res.Ratio = float64(res.Changed) / float64(res.Total)
	}
	return res
}
