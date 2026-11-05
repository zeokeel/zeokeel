package pixeltest

import (
	"image"
	"image/color"
	"testing"
)

func TestCompareEqual(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if got := Compare(img, img, image.Rectangle{}, 0); got.Changed != 0 || got.Total != 64 {
		t.Fatalf("equal images: %+v", got)
	}
}

func TestCompareOnePixel(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b.Set(3, 4, color.RGBA{R: 255, A: 255})
	got := Compare(a, b, image.Rectangle{}, 0)
	if got.Changed != 1 || got.Total != 64 {
		t.Fatalf("one pixel: %+v", got)
	}
	if got.Ratio <= 0 {
		t.Fatalf("ratio: %+v", got)
	}
}

func TestCompareROIAndTolerance(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b := image.NewRGBA(image.Rect(0, 0, 8, 8))
	a.Set(3, 4, color.RGBA{A: 255})
	b.Set(3, 4, color.RGBA{R: 10, A: 255})
	if got := Compare(a, b, image.Rect(3, 4, 4, 5), 16); got.Changed != 0 {
		t.Fatalf("tolerance: %+v", got)
	}
	if got := Compare(a, b, image.Rect(0, 0, 1, 1), 0); got.Total != 1 || got.Changed != 0 {
		t.Fatalf("roi: %+v", got)
	}
}
