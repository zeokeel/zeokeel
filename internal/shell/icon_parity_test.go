package shell

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/pixeltest"
)

// TestStaticHomeIconParity compares the sidebar Plugins glyph and the
// workspace-header Add control with the DSH baseline. The Plugins nav used a
// self-intersecting plug shape instead of DSH's jigsaw, and the static Add
// control drew a bare plus where the live path and the baseline show a
// folder-with-plus.
func TestStaticHomeIconParity(t *testing.T) {
	app := &App{Screen: Home}
	got := ui.Render(app.View, 1600, 1000, 1)
	want, err := pixeltest.LoadPNG("../../baseline/screenshots/00-home.png")
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	for _, tc := range []struct {
		name   string
		rect   image.Rectangle
		budget float64
	}{
		{"plugins jigsaw", image.Rect(20, 128, 40, 148), 0.18},
		{"add workspace folder-plus", image.Rect(236, 206, 264, 232), 0.20},
	} {
		res := pixeltest.Compare(got, want, tc.rect, 24)
		t.Logf("%s: crop diff %.2f%%", tc.name, res.Ratio*100)
		if res.Ratio > tc.budget {
			t.Errorf("%s: crop diff %.1f%% exceeds budget %.1f%%", tc.name, res.Ratio*100, tc.budget*100)
		}
	}
}
