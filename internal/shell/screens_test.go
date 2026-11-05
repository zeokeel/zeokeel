package shell

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/pixeltest"
)

// TestScreensBaseline renders every screen headlessly and compares it with
// the DSH baseline screenshot, writing per-screen renders and red diffs.
// The ratios are the 1:1 loop's working numbers: they should fall every
// iteration as layout and tokens converge.
func TestScreensBaseline(t *testing.T) {
	// maskedBudget/rawBudget are the maximum allowed diff ratios. They are
	// derived from the measured 1:1 loop with ~1.5pp of headroom: a moved
	// pane, a lost token or a clipped control moves far more pixels, while
	// text antialiasing drift stays below the budget.
	cases := []struct {
		name         string
		screen       Screen
		base         string
		maskedBudget float64
		rawBudget    float64
	}{
		{"home", Home, "../../baseline/screenshots/00-home.png", 0.020, 0.035},
		{"session", Session, "../../baseline/screenshots/61-session-chat.png", 0.085, 0.110},
		{"plugins", Plugins, "../../baseline/screenshots/59-plugins-page.png", 0.045, 0.065},
		{"settings", Settings, "../../baseline/screenshots/55-settings-general.png", 0.042, 0.050},
		{"automation", Automation, "../../baseline/screenshots/60-automation.png", 0.022, 0.040},
	}
	filter := os.Getenv("SCREEN")
	dir := os.Getenv("ARTIFACTS")
	if dir == "" {
		dir = "../../artifacts"
	}
	if err := os.MkdirAll(filepath.Join(dir, "screens"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "diff"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if filter != "" && c.name != filter {
			continue
		}
		app := &App{Screen: c.screen}
		got := ui.Render(app.View, 1600, 1000, 1)
		if got == nil {
			t.Fatalf("%s: nil render", c.name)
		}
		if err := pixeltest.SavePNG(filepath.Join(dir, "screens", c.name+".png"), got); err != nil {
			t.Fatalf("%s: save render: %v", c.name, err)
		}
		want, err := pixeltest.LoadPNG(c.base)
		if err != nil {
			t.Fatalf("%s: load baseline: %v", c.name, err)
		}
		raw := pixeltest.Compare(got, want, got.Bounds(), 12)
		// Excluded: only the wordmark text/logo in the brand row and the
		// glyph interiors of the dynamic sidebar rows. The collapse control
		// at x=240..268 y=22..50 stays compared, so losing it fails the gate;
		// the row fills and separators outside the text also stay compared.
		res := pixeltest.CompareExcluding(got, want, 12,
			image.Rect(0, 0, 236, 64),      // ZeoKeel logo + wordmark (intentional)
			image.Rect(236, 0, 280, 18),    // brand row above the collapse control
			image.Rect(236, 52, 280, 64),   // brand row below the collapse control
			image.Rect(38, 250, 232, 334),  // dynamic row labels/timestamps
			image.Rect(38, 350, 232, 386),  // dynamic workspace labels
			image.Rect(276, 210, 520, 410), // hover tooltip captured in the baseline
		)
		t.Logf("%-10s diff %.2f%% (masked %.2f%%, raw %.2f%%)", c.name, raw.Ratio*100, res.Ratio*100, raw.Ratio*100)
		if res.Ratio > c.maskedBudget {
			t.Errorf("%s: masked diff %.2f%% exceeds budget %.2f%%", c.name, res.Ratio*100, c.maskedBudget*100)
		}
		if raw.Ratio > c.rawBudget {
			t.Errorf("%s: raw diff %.2f%% exceeds budget %.2f%%", c.name, raw.Ratio*100, c.rawBudget*100)
		}
		if err := pixeltest.SavePNG(filepath.Join(dir, "diff", c.name+".png"), pixeltest.DiffImage(got, want, 12)); err != nil {
			t.Fatalf("%s: save diff: %v", c.name, err)
		}
	}
}
