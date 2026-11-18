package uikit_test

// Frame tests for the context-usage ring (uikit.UsageRing, the product's one
// registered self-drawn widget): the arc's real pixels on the ring band, the
// capacity warning at 85%, and the clamp of nonsense input. The ring is 16 DIP
// with a 2 DIP stroke, so its band sits at radius 7 from the centre: the samples
// below read the band's four compass points, never the hole in the middle.

import (
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// usageRingFrame renders one ring on the page surface and returns the tester with
// the ring's rect.
func usageRingFrame(t *testing.T, ratio float64) (*ui.Tester, ui.Rect) {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) {
		tokens := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(tokens.BgBase).
			Padding(design.Space5).Children(func() {
			uikit.UsageRing(c, uikit.UsageRingOptions{Ratio: ratio, Used: 42_000, Limit: 128_000})
		})
	}, 120, 120)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	ring, ok := tester.Find("上下文 " + itoa(int(math.Round(clamp01(ratio)*100))) + "%")
	if !ok {
		t.Fatalf("the ring has no percentage label; texts = %v", tester.Texts())
	}
	return tester, ring
}

// composited is what one ink paints on the page: the alpha tokens the design system
// uses are read as pixels only after they land on the surface.
func composited(tokens *design.Tokens, ink ui.Color) color.RGBA {
	over := ink.Over(tokens.BgBase)
	return color.RGBA{R: over.R, G: over.G, B: over.B, A: over.A}
}

func clamp01(ratio float64) float64 {
	switch {
	case math.IsNaN(ratio), ratio < 0:
		return 0
	case ratio > 1:
		return 1
	default:
		return ratio
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}

// ringBandInk samples the point of the ring band at one compass angle, in DIPs
// from the ring's centre (radius 7 for a 16 DIP box with a 2 DIP stroke).
func ringBandInk(tester *ui.Tester, ring ui.Rect, degrees float64) color.RGBA {
	cx, cy := ring.X+ring.W/2, ring.Y+ring.H/2
	radius := float64(ring.W)/2 - 1 // the band's middle: (16-2)/2
	x := int(math.Round(float64(cx) + radius*math.Cos(degrees*math.Pi/180)))
	y := int(math.Round(float64(cy) + radius*math.Sin(degrees*math.Pi/180)))
	return tester.Image().RGBAAt(x, y)
}

// TestUsageRingPaintsTheArc covers the arc's geometry: nothing used is the track
// alone, half used fills the clockwise half (12 o'clock → 6 o'clock), and a full
// window fills the whole band.
func TestUsageRingPaintsTheArc(t *testing.T) {
	tokens := design.Light()
	// The tokens are alpha inks: on the page they composite, so the expectation is
	// the composited colour, not the token's raw channels.
	track := composited(tokens, tokens.BorderL1)
	accent := composited(tokens, tokens.Accent)

	t.Run("zero is the track alone", func(t *testing.T) {
		tester, ring := usageRingFrame(t, 0)
		for _, angle := range []float64{-90, 0, 90, 180} {
			if ink := ringBandInk(tester, ring, angle); !closeColor(ink, track, 24) {
				t.Fatalf("angle %.0f° ink = %v, want the track %v", angle, ink, track)
			}
		}
	})

	t.Run("half fills the clockwise half", func(t *testing.T) {
		tester, ring := usageRingFrame(t, 0.5)
		if ink := ringBandInk(tester, ring, 0); !closeColor(ink, accent, 40) {
			t.Fatalf("3 o'clock ink = %v, want the accent fill %v", ink, accent)
		}
		if ink := ringBandInk(tester, ring, -90); !closeColor(ink, accent, 40) {
			t.Fatalf("12 o'clock (the arc's start) ink = %v, want the accent fill %v", ink, accent)
		}
		// The left of the ring is still the track: the arc stops at 6 o'clock.
		if ink := ringBandInk(tester, ring, 180); !closeColor(ink, track, 24) {
			t.Fatalf("9 o'clock ink = %v, want the track %v (the arc covers half)", ink, track)
		}
	})

	t.Run("full fills the band", func(t *testing.T) {
		// A full window is past the 85% mark, so its fill is the warning token: the
		// capacity warning, not the brand accent.
		testing_ := composited(tokens, tokens.Warning)
		tester, ring := usageRingFrame(t, 1)
		for _, angle := range []float64{-90, 0, 90, 135, 180} {
			if ink := ringBandInk(tester, ring, angle); !closeColor(ink, testing_, 40) {
				t.Fatalf("angle %.0f° ink = %v, want the warning fill %v", angle, ink, testing_)
			}
		}
	})
}

// TestUsageRingWarnsWhenNearlyFull covers the capacity warning: from 85% the fill
// is the warning token (never danger — the red budget stays for failures).
func TestUsageRingWarnsWhenNearlyFull(t *testing.T) {
	tokens := design.Light()
	warn := composited(tokens, tokens.Warning)
	tester, ring := usageRingFrame(t, 0.86)
	if ink := ringBandInk(tester, ring, 0); !closeColor(ink, warn, 60) {
		t.Fatalf("the nearly-full ring's ink = %v, want the warning fill %v", ink, warn)
	}
	// Just under the threshold it is still the brand accent.
	accent := composited(tokens, tokens.Accent)
	below, belowRing := usageRingFrame(t, 0.5)
	if ink := ringBandInk(below, belowRing, 0); !closeColor(ink, accent, 40) {
		t.Fatalf("a half-full ring's ink = %v, want the accent fill %v", ink, accent)
	}
}

// TestUsageRingClamps covers nonsense input: a negative share and a NaN read as
// nothing used (the track alone), and anything past the window reads as full.
func TestUsageRingClamps(t *testing.T) {
	tokens := design.Light()
	track := composited(tokens, tokens.BorderL1)

	for _, ratio := range []float64{-1, math.NaN()} {
		tester, ring := usageRingFrame(t, ratio)
		for _, angle := range []float64{-90, 0, 90, 180} {
			if ink := ringBandInk(tester, ring, angle); !closeColor(ink, track, 24) {
				t.Fatalf("ratio %v at %.0f° ink = %v, want the track %v", ratio, angle, ink, track)
			}
		}
	}
	// Any share past the window clamps to full, which is the warning fill.
	full := composited(tokens, tokens.Warning)
	tester, ring := usageRingFrame(t, 2)
	for _, angle := range []float64{-90, 0, 90, 180} {
		if ink := ringBandInk(tester, ring, angle); !closeColor(ink, full, 40) {
			t.Fatalf("ratio 2 at %.0f° ink = %v, want the full-window fill %v", angle, ink, full)
		}
	}
}

// TestUsageRingAccessibleCopy covers what assistive technology reads: the
// percentage as the meter's name and the exact token counts as its description.
func TestUsageRingAccessibleCopy(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		tokens := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(tokens.BgBase).Padding(design.Space5).
			Children(func() {
				uikit.UsageRing(c, uikit.UsageRingOptions{
					Ratio: 0.33, Used: 42_000, Limit: 128_000,
				})
			})
	}, 120, 120)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	info, ok := tester.AccessByRole(ui.RoleMeter, "上下文 33%")
	if !ok {
		t.Fatalf("the ring is not a named meter; texts = %v", tester.Texts())
	}
	if info.Description != "42K / 128K" {
		t.Fatalf("the ring's description = %q, want the exact counts", info.Description)
	}
	if info.Bounds.W != design.Space4 || info.Bounds.H != design.Space4 {
		t.Fatalf("the ring is %.1fx%.1f, want %v square", info.Bounds.W, info.Bounds.H, design.Space4)
	}
}
