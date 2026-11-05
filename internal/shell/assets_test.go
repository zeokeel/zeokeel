package shell

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// decodeBrandPNG decodes one embedded brand asset for pixel inspection.
func decodeBrandPNG(t *testing.T, data []byte, name string) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return img
}

// brandPixel returns the non-premultiplied RGBA of one asset pixel.
func brandPixel(img image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

// nearWhite reports whether a pixel would read as the light container's white
// against a dark page: opaque enough to matter and close to #FFFFFF.
func nearWhite(c color.NRGBA) bool {
	return c.A >= 200 && c.R >= 235 && c.G >= 235 && c.B >= 235
}

// TestBrandMarkAssets pins the embedded marks and the theme switch: the light
// asset stays the 48x48 logo/logo.svg render with its white container, the dark
// asset is the 96x96 (2x) logo/logo-dark.svg render, and brandMark picks the
// dark one in the dark theme.
func TestBrandMarkAssets(t *testing.T) {
	if mark == nil || markDark == nil {
		t.Fatal("embedded brand marks were not decoded")
	}
	if bytes.Equal(markPNG, markDarkPNG) {
		t.Fatal("light and dark marks are the same asset")
	}
	light := decodeBrandPNG(t, markPNG, "mark.png")
	dark := decodeBrandPNG(t, markDarkPNG, "mark-dark@2x.png")
	if got, want := light.Bounds().Size(), image.Pt(48, 48); got != want {
		t.Fatalf("light mark size = %v, want %v", got, want)
	}
	if got, want := dark.Bounds().Size(), image.Pt(96, 96); got != want {
		t.Fatalf("dark mark size = %v, want the 2x %v", got, want)
	}

	// The helper follows the frame's appearance tokens.
	if got := brandMark(design.Light()); got != mark {
		t.Fatal("brandMark(light theme) did not return the light mark")
	}
	if got := brandMark(design.Dark()); got != markDark {
		t.Fatal("brandMark(dark theme) did not return the dark mark")
	}
}

// TestDarkMarkHasNoWhiteContainer is the P0-3 regression guard: the dark mark
// is derived from logo/logo-dark.svg, so its rounded container is black and its
// antialiased edge cannot paint the white fringe the light asset shows on the
// dark hero and sidebar.
func TestDarkMarkHasNoWhiteContainer(t *testing.T) {
	dark := decodeBrandPNG(t, markDarkPNG, "mark-dark@2x.png")
	// Every pixel on the outer frame (where the container's edge lives) must be
	// transparent or dark: no white container pixel reaches the dark page.
	frame := 3
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			if x >= frame && x < 96-frame && y >= frame && y < 96-frame {
				continue
			}
			if c := brandPixel(dark, x, y); nearWhite(c) {
				t.Fatalf("dark mark frame pixel (%d,%d) = %v, want no white container", x, y, c)
			}
		}
	}
	// The straight edges are the black container, fully opaque.
	if c := brandPixel(dark, 48, 0); c.A < 200 || c.R > 24 || c.G > 24 || c.B > 24 {
		t.Fatalf("dark mark top edge = %v, want the opaque black container", c)
	}
	// The rounded corners stay transparent, so the mark does not paint a square
	// on the dark page.
	for _, p := range [][2]int{{0, 0}, {95, 0}, {0, 95}, {95, 95}} {
		if c := brandPixel(dark, p[0], p[1]); c.A != 0 {
			t.Fatalf("dark mark corner %v = %v, want transparent", p, c)
		}
	}

	// The light asset still owns its white container: that is the light
	// identity, and exactly why the dark theme must not reuse it.
	light := decodeBrandPNG(t, markPNG, "mark.png")
	foundWhite := false
	for y := 0; y < 48 && !foundWhite; y++ {
		for x := 0; x < 48; x++ {
			if x >= 3 && x < 45 && y >= 3 && y < 45 {
				continue
			}
			if nearWhite(brandPixel(light, x, y)) {
				foundWhite = true
				break
			}
		}
	}
	if !foundWhite {
		t.Fatal("light mark lost its white container pixels")
	}
}

// TestBrandMarkRendersWithoutWhiteCorners renders the mark headlessly on the
// dark page background the way Home and the sidebar composite it, and checks
// the corners: with the dark asset they are the page color; with the light
// asset (the P0-3 bug) they would be white.
func TestBrandMarkRendersWithoutWhiteCorners(t *testing.T) {
	theme := design.Dark()
	render := func(bitmap *ui.Bitmap) *image.RGBA {
		return ui.Render(func(c *ui.Context) {
			c.SetTheme(theme.Theme())
			ui.Column(c).Fill().Background(theme.BgBase).Children(func() {
				ui.Image(c, bitmap).Size(48, 48)
			})
		}, 48, 48, 1)
	}

	// The container's antialiased edge sits on this outer band, so the P0-3
	// fringe would show here: the composited frame must stay at page/black
	// luminosity in the dark render.
	brightest := func(img *image.RGBA) uint8 {
		var max uint8
		for y := 0; y < 48; y++ {
			for x := 0; x < 48; x++ {
				if x >= 6 && x < 42 && y >= 6 && y < 42 {
					continue
				}
				r, g, b, _ := img.At(x, y).RGBA()
				for _, v := range []uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)} {
					if v > max {
						max = v
					}
				}
			}
		}
		return max
	}
	for _, bitmap := range []*ui.Bitmap{brandMark(theme), markDark} {
		if got := brightest(render(bitmap)); got > 80 {
			t.Fatalf("dark render frame reaches %d/255: the dark mark painted a light container", got)
		}
	}

	// Contrast check: the same composition with the light bitmap does paint
	// the white container on the dark background, so the assertion above
	// detects exactly the P0-3 regression if brandMark ever returns the light
	// asset for the dark theme.
	if got := brightest(render(mark)); got < 150 {
		t.Fatalf("light mark on the dark page reached only %d/255: the render check cannot catch the P0-3 regression", got)
	}
}
