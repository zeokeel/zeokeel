package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// TestSessionSearchScrimIsThemeAware covers the session-search modal's
// backdrop: it shared the Settings modal's hardcoded 0.24 black in both
// themes. The dark backdrop is 0.5, composited through the same ui color path
// the renderer uses.
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
			if got := tt.Image().RGBAAt(1500, 900); got != want {
				t.Fatalf("%s session-search scrim at (1500,900) = %v, want %v", tc.name, got, want)
			}
			if tc.dark {
				// The dark scrim's exact baseline sample is 0.5 black over
				// #151517: (10,10,11), not the old 0.24 value (16,16,17).
				if got := tt.Image().RGBAAt(1500, 900); got.R != 10 || got.G != 10 || got.B != 11 {
					t.Fatalf("dark scrim sample = %v, want (10,10,11)", got)
				}
			}
		})
	}
}
