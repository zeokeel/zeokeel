package brand

// Contrast contract tests for the brand palette (2026-11 audit, UX-07/UX-13):
//
//   - UX-07: the light theme's native focus ring must clear WCAG 1.4.11's 3:1
//     non-text contrast on every light surface. The ring is the palette's Focus
//     color composited over the surface (mygo draws it with straight alpha), so
//     the test derives the ratio from the LIVE palette and confirms it on the
//     rendered frame's pixels (ui.NewTester + Image().RGBAAt, the product's own
//     pixel-test technique).
//   - UX-13: LabelTertiary (Palette.TextTertiary) carries metadata on selected
//     rows too, so it must clear WCAG AA's 4.5:1 on the RowSelected tint over
//     every surface of BOTH themes — while the resting surfaces keep their own
//     AA and the Text > Secondary > Tertiary hierarchy survives.
//
// The computation helpers use sRGB linearization + straight-alpha
// over-compositing, the blending mygo documents (the same math the 2026-11
// audit's contrast probes used).

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func srgbChannel(v uint8) float64 {
	c := float64(v) / 255.0
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func luminance(r, g, b uint8) float64 {
	return 0.2126*srgbChannel(r) + 0.7152*srgbChannel(g) + 0.0722*srgbChannel(b)
}

func contrastRGB(fg, bg [3]uint8) float64 {
	l1, l2 := luminance(fg[0], fg[1], fg[2]), luminance(bg[0], bg[1], bg[2])
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// over composites a straight-alpha color onto an opaque base in sRGB space.
func over(fg ui.Color, bg [3]uint8) [3]uint8 {
	a := float64(fg.A) / 255.0
	var out [3]uint8
	out[0] = uint8(math.Round(float64(fg.R)*a + float64(bg[0])*(1-a)))
	out[1] = uint8(math.Round(float64(fg.G)*a + float64(bg[1])*(1-a)))
	out[2] = uint8(math.Round(float64(fg.B)*a + float64(bg[2])*(1-a)))
	return out
}

func rgbOf(c ui.Color) [3]uint8 { return [3]uint8{c.R, c.G, c.B} }

// paletteSurfaces names the four opaque surfaces a focus ring or a selected row
// can sit on, in the order the screens use them.
func paletteSurfaces(p Palette) map[string][3]uint8 {
	return map[string][3]uint8{
		"BgBase":    rgbOf(p.Background),
		"BgSidebar": rgbOf(p.Sidebar),
		"BgRaised":  rgbOf(p.Surface),
		"BgStrong":  rgbOf(p.SurfaceStrong),
	}
}

// TestFocusRingContrast derives the focus-ring ratio on every surface of both
// themes from the live palette (WCAG 1.4.11 needs ≥3:1 for the focus indicator).
func TestFocusRingContrast(t *testing.T) {
	for _, theme := range []struct {
		name    string
		palette Palette
	}{
		{"light", LightPalette()},
		{"dark", DarkPalette()},
	} {
		for name, bg := range paletteSurfaces(theme.palette) {
			ring := over(theme.palette.Focus, bg)
			ratio := contrastRGB(ring, bg)
			t.Logf("%s focus ring on %s: composited=#%02X%02X%02X ratio=%.2f (need ≥3.0)",
				theme.name, name, ring[0], ring[1], ring[2], ratio)
			if ratio < 3.0 {
				t.Errorf("%s focus ring on %s is %.2f:1 < 3:1 (UX-07, WCAG 1.4.11)",
					theme.name, name, ratio)
			}
		}
	}
	// The themes must wire the palette's Focus into the ring slot unchanged.
	if got := LightTheme().Focus; got != LightPalette().Focus {
		t.Fatalf("LightTheme.Focus = %+v, want palette.Focus %+v", got, LightPalette().Focus)
	}
	if got := DarkTheme().Focus; got != DarkPalette().Focus {
		t.Fatalf("DarkTheme.Focus = %+v, want palette.Focus %+v", got, DarkPalette().Focus)
	}
}

// TestFocusRingPixelContrast renders a keyboard-focused native button in the
// light theme and measures the ring pixel just outside its edge against the
// page background: the rendered ring, not only the palette math, must clear
// 3:1 (the product's own pixel-test technique: ui.NewTester + Image().RGBAAt,
// as the sidebar's style tests sample fills).
func TestFocusRingPixelContrast(t *testing.T) {
	p := LightPalette()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(LightTheme())
		ui.Column(c).FillWidth().FillHeight().Background(p.Background).
			Padding(24).Children(func() {
			ui.Button(c, "发送")
		})
	}, 320, 200)
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !tt.Focused("发送") {
		t.Fatalf("Tab did not focus the button; texts = %v", tt.Texts())
	}
	info, ok := tt.AccessByRole(ui.RoleButton, "发送")
	if !ok {
		t.Fatalf("focused button missing from the accessibility tree; texts = %v", tt.Texts())
	}
	rect := info.Bounds
	sample := func(x, y int) [3]uint8 {
		c := tt.Image().RGBAAt(x, y)
		return [3]uint8{c.R, c.G, c.B}
	}
	bg := sample(int(rect.X)-12, int(rect.Y+rect.H/2))
	// The ring: FocusRingWidth 2, FocusRingOffset 1 (theme defaults) — it sits
	// 1..3 DIP outside the element edge. Sample left of the button.
	var ring [3]uint8
	ringFound := false
	for dx := 1; dx <= 5; dx++ {
		px := sample(int(rect.X)-dx, int(rect.Y+rect.H/2))
		if px != bg && !ringFound {
			ring = px
			ringFound = true
		}
	}
	if !ringFound {
		t.Fatalf("no ring pixel found left of the focused button (bg=#%02X%02X%02X)", bg[0], bg[1], bg[2])
	}
	ratio := contrastRGB(ring, bg)
	t.Logf("rendered ring #%02X%02X%02X vs bg #%02X%02X%02X → %.2f:1 (need ≥3.0)",
		ring[0], ring[1], ring[2], bg[0], bg[1], bg[2], ratio)
	if ratio < 3.0 {
		t.Errorf("rendered light focus ring measures %.2f:1 < 3:1 (UX-07 pixel proof)", ratio)
	}
}

// TestTertiaryOnSelectedTintMeetsAA derives LabelTertiary's ratio on the
// RowSelected tint over every surface of both themes (AA normal text: ≥4.5:1).
func TestTertiaryOnSelectedTintMeetsAA(t *testing.T) {
	for _, theme := range []struct {
		name    string
		palette Palette
	}{
		{"light", LightPalette()},
		{"dark", DarkPalette()},
	} {
		p := theme.palette
		for name, bg := range paletteSurfaces(p) {
			selected := over(p.RowSelected, bg)
			ratio := contrastRGB(rgbOf(p.TextTertiary), selected)
			t.Logf("%s TextTertiary on RowSelected(%s): ratio=%.2f (need ≥4.5)", theme.name, name, ratio)
			if ratio < 4.5 {
				t.Errorf("%s TextTertiary on the selected tint over %s is %.2f:1 < 4.5:1 (UX-13)",
					theme.name, name, ratio)
			}
		}
	}
}

// TestTertiaryRestingSurfacesKeepAA is the resting-surface control: the palette
// comment promises AA on every surface WITHOUT the selection tint; the UX-13
// fix must not trade that away.
func TestTertiaryRestingSurfacesKeepAA(t *testing.T) {
	for _, theme := range []struct {
		name    string
		palette Palette
	}{
		{"light", LightPalette()},
		{"dark", DarkPalette()},
	} {
		p := theme.palette
		for name, bg := range paletteSurfaces(p) {
			ratio := contrastRGB(rgbOf(p.TextTertiary), bg)
			if ratio < 4.5 {
				t.Errorf("%s TextTertiary on resting %s is %.2f:1 < 4.5:1 (the resting control must keep passing)",
					theme.name, name, ratio)
			}
		}
	}
}

// TestTextHierarchySurvives pins the recorded hierarchy decision (palette
// comment, P0-2 review-round3): Text outranks Secondary outranks Tertiary in
// both themes, so a contrast fix may not invert the ink order.
func TestTextHierarchySurvives(t *testing.T) {
	light := LightPalette()
	if !(luminance(light.Text.R, light.Text.G, light.Text.B) <
		luminance(light.TextSecondary.R, light.TextSecondary.G, light.TextSecondary.B) &&
		luminance(light.TextSecondary.R, light.TextSecondary.G, light.TextSecondary.B) <
			luminance(light.TextTertiary.R, light.TextTertiary.G, light.TextTertiary.B)) {
		t.Error("light ink hierarchy Text > Secondary > Tertiary inverted")
	}
	dark := DarkPalette()
	if !(luminance(dark.Text.R, dark.Text.G, dark.Text.B) >
		luminance(dark.TextSecondary.R, dark.TextSecondary.G, dark.TextSecondary.B) &&
		luminance(dark.TextSecondary.R, dark.TextSecondary.G, dark.TextSecondary.B) >
			luminance(dark.TextTertiary.R, dark.TextTertiary.G, dark.TextTertiary.B)) {
		t.Error("dark ink hierarchy Text > Secondary > Tertiary inverted")
	}
}

// TestSelectedTintPixelContrast renders a selected row (RowSelected tint +
// TextTertiary metadata) on every surface of both themes and measures the
// rendered tint pixel: the composited surface the text actually sits on must
// give the palette's tertiary ink ≥4.5:1 (UX-13 pixel proof).
func TestSelectedTintPixelContrast(t *testing.T) {
	for _, theme := range []struct {
		name    string
		palette Palette
		install func(*ui.Context)
	}{
		{"light", LightPalette(), func(c *ui.Context) { c.SetTheme(LightTheme()) }},
		{"dark", DarkPalette(), func(c *ui.Context) { c.SetTheme(DarkTheme()) }},
	} {
		p := theme.palette
		surfaces := []struct {
			name string
			bg   ui.Color
		}{
			{"BgBase", p.Background},
			{"BgSidebar", p.Sidebar},
			{"BgRaised", p.Surface},
			{"BgStrong", p.SurfaceStrong},
		}
		tt := ui.NewTester(func(c *ui.Context) {
			theme.install(c)
			ui.Column(c).FillWidth().FillHeight().Background(p.Background).Children(func() {
				for _, s := range surfaces {
					ui.Column(c).FillWidth().Background(s.bg).Padding(8).Children(func() {
						ui.Row(c).FillWidth().Background(p.RowSelected).Padding(8).Children(func() {
							ui.Text(c, "12:34 · 68 tokens").TextColor(p.TextTertiary).FontSize(12)
						})
					})
				}
			})
		}, 400, 400)
		tt.Frame()
		if !tt.HasText("12:34 · 68 tokens") {
			t.Fatalf("%s: metadata text missing; texts = %v", theme.name, tt.Texts())
		}
		for _, s := range surfaces {
			// Each surface composites the translucent RowSelected tint to its own
			// opaque color, so scanning the frame for that exact color locates
			// this surface's selected row. x=10 sits in the row's left padding
			// (band padding 8 + row padding 8): tint, clear of the glyphs.
			expected := over(p.RowSelected, rgbOf(s.bg))
			var tint [3]uint8
			found := false
			for y := 0; y < 400 && !found; y++ {
				px := tt.Image().RGBAAt(10, y)
				got := [3]uint8{px.R, px.G, px.B}
				// The painter's integer blend rounds within one step of the
				// float64 composite on non-white surfaces; the ratio below is
				// computed from the pixel actually rendered.
				near := true
				for i := range got {
					d := int(got[i]) - int(expected[i])
					if d < -1 || d > 1 {
						near = false
					}
				}
				if near {
					tint = got
					found = true
				}
			}
			if !found {
				t.Fatalf("%s %s: rendered RowSelected tint #%02X%02X%02X not found in the frame",
					theme.name, s.name, expected[0], expected[1], expected[2])
			}
			ratio := contrastRGB(rgbOf(p.TextTertiary), tint)
			t.Logf("%s %s: rendered tint #%02X%02X%02X, TextTertiary ratio=%.2f (need ≥4.5)",
				theme.name, s.name, tint[0], tint[1], tint[2], ratio)
			if ratio < 4.5 {
				t.Errorf("%s TextTertiary on the rendered selected tint over %s is %.2f:1 < 4.5:1 (UX-13 pixel proof)",
					theme.name, s.name, ratio)
			}
		}
	}
}
