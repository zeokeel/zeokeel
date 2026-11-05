package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/pixeltest"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestLiveScreensRenderAndDifferFromStatic closes the regression-gate gap:
// the five pixel baselines only render App{Ctrl:nil}, so live-only UI was
// never captured. This renders every screen through the controller-backed app
// at both the baseline and minimum-window sizes, saves the artifacts, and
// asserts each live screen actually differs from the static one.
func TestLiveScreensRenderAndDifferFromStatic(t *testing.T) {
	dir := os.Getenv("ARTIFACTS")
	if dir == "" {
		dir = "../../artifacts"
	}
	out := filepath.Join(dir, "live")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Live task", Cron: "@hourly", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	screens := []struct {
		name   string
		screen Screen
	}{
		{"home", Home},
		{"session", Session},
		{"plugins", Plugins},
		{"settings", Settings},
		{"automation", Automation},
	}
	for _, tc := range screens {
		t.Run(tc.name, func(t *testing.T) {
			h.app.Screen = tc.screen
			for _, size := range [][2]int{{1600, 1000}, {900, 600}} {
				w, ht := size[0], size[1]
				live := ui.Render(h.app.View, w, ht, 1)
				if live == nil {
					t.Fatalf("%s live %dx%d: nil render", tc.name, w, ht)
				}
				if err := pixeltest.SavePNG(filepath.Join(out, tc.name+"-live-"+itoa(w)+"x"+itoa(ht)+".png"), live); err != nil {
					t.Fatalf("%s live save: %v", tc.name, err)
				}
				static := &App{Screen: tc.screen}
				base := ui.Render(static.View, w, ht, 1)
				if base == nil {
					t.Fatalf("%s static %dx%d: nil render", tc.name, w, ht)
				}
				if err := pixeltest.SavePNG(filepath.Join(out, tc.name+"-static-"+itoa(w)+"x"+itoa(ht)+".png"), base); err != nil {
					t.Fatalf("%s static save: %v", tc.name, err)
				}
				res := pixeltest.Compare(live, base, live.Bounds(), 12)
				if res.Ratio < 0.001 {
					t.Fatalf("%s %dx%d: live and static renders are identical (%.4f%%)", tc.name, w, ht, res.Ratio*100)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
