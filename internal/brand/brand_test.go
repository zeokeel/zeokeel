package brand

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestHexConstantsMatchLogoAsset pins the palette constants to the brand
// files they claim to come from: every gradient stop named in palette.go must
// appear in logo/logo.svg, the dark container must be black in
// logo-dark.svg, and the wordmark inks must appear in docs/wordmark.md. If
// the mark is ever redrawn, this test fails until palette.go follows.
func TestHexConstantsMatchLogoAsset(t *testing.T) {
	logo := readAsset(t, "../../logo/logo.svg")
	for name, c := range map[string]string{
		"HexCyan":       HexCyan,
		"HexBlue":       HexBlue,
		"HexRoyal":      HexRoyal,
		"HexSky":        HexSky,
		"HexIndigo":     HexIndigo,
		"HexIce":        HexIce,
		"HexViolet":     HexViolet,
		"HexUltra":      HexUltra,
		"HexMist":       HexMist,
		"HexPeriwinkle": HexPeriwinkle,
		"HexFrost":      HexFrost,
		"HexGlow":       HexGlow,
		"HexWhite":      HexWhite,
	} {
		if !strings.Contains(strings.ToUpper(logo), strings.ToUpper(c)) {
			t.Errorf("%s = %s does not appear in logo/logo.svg", name, c)
		}
	}

	darkLogo := strings.ToUpper(readAsset(t, "../../logo/logo-dark.svg"))
	if !strings.Contains(darkLogo, `FILL="#000000"`) {
		t.Errorf("logo-dark.svg no longer carries the #000000 container: HexBlack needs a new source")
	}

	wordmark := strings.ToUpper(readAsset(t, "../../docs/wordmark.md"))
	for name, c := range map[string]string{"HexInk": HexInk, "HexInkDark": HexInkDark} {
		if !strings.Contains(wordmark, c) {
			t.Errorf("%s = %s does not appear in docs/wordmark.md", name, c)
		}
	}
}

// TestThemeAppliesBrand checks the mapping from palette to mygo theme slots,
// the brand metrics, and that the fields the brand does not name keep mygo's
// native defaults.
func TestThemeAppliesBrand(t *testing.T) {
	light, dark := LightTheme(), DarkTheme()
	if light.Dark || !dark.Dark {
		t.Fatalf("theme Dark flags = %v/%v, want false/true", light.Dark, dark.Dark)
	}

	for _, tc := range []struct {
		name  string
		theme *ui.Theme
		p     Palette
	}{
		{"light", light, LightPalette()},
		{"dark", dark, DarkPalette()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := []struct {
				field string
				got   ui.Color
				want  ui.Color
			}{
				{"Background", tc.theme.Background, tc.p.Background},
				{"Surface", tc.theme.Surface, tc.p.Surface},
				{"SurfaceHover", tc.theme.SurfaceHover, tc.p.Hover},
				{"SurfacePressed", tc.theme.SurfacePressed, tc.p.SurfacePressed},
				{"Border", tc.theme.Border, tc.p.Border},
				{"Text", tc.theme.Text, tc.p.Text},
				{"TextMuted", tc.theme.TextMuted, tc.p.TextSecondary},
				{"Accent", tc.theme.Accent, tc.p.Accent},
				{"AccentHover", tc.theme.AccentHover, tc.p.AccentHover},
				{"AccentPressed", tc.theme.AccentPressed, tc.p.AccentPressed},
				{"AccentText", tc.theme.AccentText, tc.p.AccentText},
				{"Danger", tc.theme.Danger, tc.p.Danger},
				{"Warning", tc.theme.Warning, tc.p.Warning},
				{"Success", tc.theme.Success, tc.p.Success},
				{"Selection", tc.theme.Selection, tc.p.Selection},
				{"Focus", tc.theme.Focus, tc.p.Focus},
				{"Scrollbar", tc.theme.Scrollbar, tc.p.Scrollbar},
			}
			for _, c := range checks {
				if c.got != c.want {
					t.Errorf("Theme.%s = %v, want palette %v", c.field, c.got, c.want)
				}
			}
			if tc.theme.Radius != Radius {
				t.Errorf("Radius = %v, want %v", tc.theme.Radius, Radius)
			}
			if tc.theme.Spacing != Spacing {
				t.Errorf("Spacing = %v, want %v", tc.theme.Spacing, Spacing)
			}
			if tc.theme.FontSize != FontSize {
				t.Errorf("FontSize = %v, want %v", tc.theme.FontSize, FontSize)
			}
			if tc.theme.Font != SystemFont {
				t.Errorf("Font = %q, want the brand system stack", tc.theme.Font)
			}
			// Left to mygo: tooltips/toasts derive the inverse pair from
			// Text/Background, and the scroll bars stay 6 DIPs wide.
			if tc.theme.Inverse != (ui.Color{}) || tc.theme.InverseText != (ui.Color{}) {
				t.Errorf("Inverse/InverseText = %v/%v, want unset (mygo falls back to Text/Background)",
					tc.theme.Inverse, tc.theme.InverseText)
			}
			if tc.theme.ScrollbarWidth != 0 {
				t.Errorf("ScrollbarWidth = %v, want mygo's 0 default", tc.theme.ScrollbarWidth)
			}
		})
	}
}

// TestThemesAreIndependent makes sure each call returns a fresh theme: a
// screen that probes a variant must not leak its edits into the next render.
func TestThemesAreIndependent(t *testing.T) {
	first := LightTheme()
	first.Accent = ui.Hex("#FF00FF")
	first.Radius = 1
	second := LightTheme()
	if second.Accent != LightPalette().Accent || second.Radius != Radius {
		t.Fatalf("a mutated theme leaked into the next LightTheme: %v/%v", second.Accent, second.Radius)
	}
}

// TestPaletteContrast checks the WCAG AA pairs the brand promises: body text
// and secondary text on the page background, accent text on the accent fill,
// and the warning text color on the page background.
func TestPaletteContrast(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Palette
	}{
		{"light", LightPalette()},
		{"dark", DarkPalette()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range []struct {
				name   string
				fg, bg ui.Color
			}{
				{"primary text", tc.p.Text, tc.p.Background},
				{"secondary text", tc.p.TextSecondary, tc.p.Background},
				{"warning text", tc.p.WarningText, tc.p.Background},
				{"accent text on accent fill", tc.p.AccentText, tc.p.Accent},
			} {
				if r := contrast(c.fg, c.bg); r < 4.5 {
					t.Errorf("%s: contrast %.2f:1 < 4.5:1", c.name, r)
				}
			}
		})
	}
}

// TestThemeFor checks the appearance selector agrees with the palette
// selector, which is what the app uses to resolve the system appearance.
func TestThemeFor(t *testing.T) {
	if ThemeFor(true).Accent != DarkPalette().Accent || !ThemeFor(true).Dark {
		t.Errorf("ThemeFor(true) is not the dark brand theme")
	}
	if ThemeFor(false).Accent != LightPalette().Accent || ThemeFor(false).Dark {
		t.Errorf("ThemeFor(false) is not the light brand theme")
	}
	if PaletteOf(true).Accent != DarkPalette().Accent || PaletteOf(false).Accent != LightPalette().Accent {
		t.Errorf("PaletteOf does not agree with the named palettes")
	}
}

// readAsset reads a brand asset relative to the package directory.
func readAsset(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read brand asset %s: %v", path, err)
	}
	return string(b)
}

// contrast returns the WCAG 2.x contrast ratio of two colors.
func contrast(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}
