package design

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
)

// TestBrandTokens checks the brand mapping: every semantic token comes from
// the internal/brand palette, and the light/dark accents are the two brand
// blues the palette names.
func TestBrandTokens(t *testing.T) {
	l, d := Light(), Dark()
	pLight, pDark := brand.LightPalette(), brand.DarkPalette()

	if l.Dark || !d.Dark {
		t.Fatalf("tokens Dark flags = %v/%v, want false/true", l.Dark, d.Dark)
	}
	checks := []struct {
		name      string
		got, want ui.Color
	}{
		{"light base", l.BgBase, pLight.Background},
		{"light sidebar", l.BgSidebar, pLight.Sidebar},
		{"light strong", l.BgStrong, pLight.SurfaceStrong},
		{"light hover", l.BgHover, pLight.Hover},
		{"light primary label", l.LabelPrimary, pLight.Text},
		{"light secondary label", l.LabelSecondary, pLight.TextSecondary},
		{"light border", l.BorderL2, pLight.Border},
		{"light accent", l.Accent, ui.Hex(brand.HexRoyal)},
		{"light on-primary", l.OnPrimary, pLight.OnPrimary},
		{"dark base", d.BgBase, pDark.Background},
		{"dark sidebar", d.BgSidebar, pDark.Sidebar},
		{"dark raised", d.BgRaised, pDark.Surface},
		{"dark hover", d.BgHover, pDark.Hover},
		{"dark primary label", d.LabelPrimary, pDark.Text},
		{"dark accent", d.Accent, ui.Hex(brand.HexSky)},
		{"dark on-primary", d.OnPrimary, pDark.OnPrimary},
		{"white", l.White, ui.Hex(brand.HexWhite)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if l.Accent == d.Accent {
		t.Errorf("light and dark accents are the same color; the palette maps them differently on purpose")
	}
	// Status colors are semantic and shared by neither theme's logo palette:
	// they must still come from the palettes, and warning text must be
	// readable on the page background.
	if l.WarningText != pLight.WarningText || d.WarningText != pDark.WarningText {
		t.Errorf("warning text does not come from the brand palette")
	}
}

// TestScrim checks the modal backdrop is the brand palette's scrim in both
// appearances.
func TestScrim(t *testing.T) {
	if got := Light().Scrim(); got != brand.LightPalette().Scrim {
		t.Errorf("light scrim = %v, want brand scrim", got)
	}
	if got := Dark().Scrim(); got != brand.DarkPalette().Scrim {
		t.Errorf("dark scrim = %v, want brand scrim", got)
	}
}

// TestThemeIsBrandTheme pins the contract screens rely on: Tokens.Theme is
// exactly the brand ui.Theme (colors, metrics and font), so installing it
// with c.SetTheme gives every stock mygo widget the brand appearance.
func TestThemeIsBrandTheme(t *testing.T) {
	for _, tc := range []struct {
		name string
		tok  *Tokens
		want *ui.Theme
	}{
		{"light", Light(), brand.LightTheme()},
		{"dark", Dark(), brand.DarkTheme()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tok.Theme()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokens.Theme() = %+v\nwant brand theme %+v", got, tc.want)
			}
			// The theme's color slots must agree with the tokens the screens
			// pass around, so a widget and a hand-styled element never
			// disagree on the brand.
			if got.Background != tc.tok.BgBase || got.Surface != tc.tok.BgRaised || got.Text != tc.tok.LabelPrimary {
				t.Errorf("theme slots drifted from tokens: %+v", got)
			}
			if got.Radius != RadiusMD {
				t.Errorf("theme radius = %v, want the control corner %v", got.Radius, RadiusMD)
			}
		})
	}
}

// TestOfFollowsContextTheme checks the helper screens use to pick tokens for
// the current appearance.
func TestOfFollowsContextTheme(t *testing.T) {
	ui.Render(func(c *ui.Context) {
		if Of(c).Dark {
			t.Error("Of(default context) is dark, want light")
		}
		c.SetTheme(brand.DarkTheme())
		if !Of(c).Dark {
			t.Error("Of(context with the dark theme) is light")
		}
	}, 40, 20, 1)
}

func TestTypeScale(t *testing.T) {
	cases := []struct {
		role   TypeRole
		size   float32
		line   float32
		weight int
	}{
		{TypeXL, 24, 32, 400},
		{TypeM, 16, 24, 400},
		{TypeBody, 14, 22, 400},
		{TypeXS, 13, 20, 400},
		{TypeXXS, 12, 18, 400},
	}
	for _, c := range cases {
		size, line, weight := TypeMetrics(c.role)
		if size != c.size || line != c.line || weight != c.weight {
			t.Errorf("role %d: got %v/%v/%d, want %v/%v/%d", c.role, size, line, weight, c.size, c.line, c.weight)
		}
	}
	// The body step is the brand base size, and the theme carries the same
	// number: a raw FontSize and a Tokens.Font(TypeBody) run at one size.
	if size, _, _ := TypeMetrics(TypeBody); size != brand.FontSize {
		t.Errorf("TypeBody = %v, want the brand base font size %v", size, brand.FontSize)
	}
}

func TestBrandGrid(t *testing.T) {
	if Space1 != brand.Spacing || Space2 != 2*brand.Spacing || Space4 != 4*brand.Spacing || Space6 != 6*brand.Spacing {
		t.Fatalf("spacing scale is not on the brand grid: %v %v %v %v", Space1, Space2, Space4, Space6)
	}
	if RadiusMD != brand.Radius {
		t.Errorf("RadiusMD = %v, want the brand control radius %v", RadiusMD, brand.Radius)
	}
	if RadiusXS >= RadiusSM || RadiusSM >= RadiusMD || RadiusMD >= RadiusLG || RadiusLG >= RadiusXL {
		t.Errorf("radius scale is not ascending: %v %v %v %v %v", RadiusXS, RadiusSM, RadiusMD, RadiusLG, RadiusXL)
	}
}
