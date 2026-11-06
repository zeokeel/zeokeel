package shell

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
)

// TestSessionSearchScrimIsThemeAware covers the session-search modal's
// backdrop: it used to share the Settings modal's hardcoded 0.24 black in both
// themes. The backdrop is the theme's Scrim composited over the page
// background, so it must follow the theme in both appearances and actually
// dim the page behind the modal. (The old DSH dark sample (10,10,11) was a
// token pin, retired with the brand theme.)
func TestSessionSearchScrimIsThemeAware(t *testing.T) {
	render := func(dark bool) *ui.Tester {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		h.app.paletteOpen = true
		tt := ui.NewTester(func(c *ui.Context) {
			if dark {
				c.SetTheme(design.Dark().Theme())
			} else {
				c.SetTheme(design.Light().Theme())
			}
			h.app.View(c)
		}, 1600, 1000)
		tt.Frame()
		return tt
	}

	for _, tc := range []struct {
		name   string
		dark   bool
		tokens *design.Tokens
	}{
		{"light", false, design.Light()},
		{"dark", true, design.Dark()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tt := render(tc.dark)
			// (1500,900) is outside the 580px modal panel, on the page
			// background behind it.
			want := rgbaOf(tc.tokens.Scrim().Over(tc.tokens.BgBase))
			got := tt.Image().RGBAAt(1500, 900)
			if got != want {
				t.Fatalf("%s session-search scrim at (1500,900) = %v, want %v", tc.name, got, want)
			}
			// The scrim is a dimming overlay, not the page color: the
			// composited backdrop must be darker than the plain background.
			if luminance(ui.Color{R: got.R, G: got.G, B: got.B}) >= luminance(tc.tokens.BgBase) {
				t.Fatalf("%s scrim sample %v does not dim BgBase %v", tc.name, got, tc.tokens.BgBase)
			}
		})
	}

	// Theme-aware behaviour: the light and dark backdrops are not one
	// hardcoded sample reused in both appearances.
	light := render(false).Image().RGBAAt(1500, 900)
	dark := render(true).Image().RGBAAt(1500, 900)
	if light == dark {
		t.Fatalf("light and dark session-search scrims are identical: %v", color.RGBA(light))
	}
}
