package regression

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// revealSettings scrolls the settings page body (or navigation column) until
// the element showing label sits inside the dialog's visible area; call it
// before clicking or focusing a control on a long settings page.
func revealSettings(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	for range 30 {
		rect, ok := tester.Find(label)
		if !ok {
			return
		}
		cx, cy := rect.X+rect.W/2, rect.Y+rect.H/2
		if rect.W > 0 && rect.H > 0 && cx >= 200 && cx <= 1200 && cy >= 100 && cy <= 720 {
			return
		}
		sx, sy := float32(740), float32(500)
		if rect.X < 420 {
			sx = 330
		}
		delta := cy - 480
		if delta > 220 {
			delta = 220
		}
		if delta < -220 {
			delta = -220
		}
		if delta == 0 {
			delta = 120
		}
		tester.Scroll(sx, sy, 0, delta)
		tester.Frame()
	}
}

// openSettingsPage clicks one settings navigation item, scrolling the
// navigation (and page body) column when the grouped 19-page list puts the
// target outside the dialog's visible area. The headless tester has no wheel
// and only rejects clicks outside the window, so an item clipped by the scroll
// viewport would otherwise be clicked on the modal scrim.
func openSettingsPage(t *testing.T, tester *ui.Tester, width, height int, label string) {
	t.Helper()
	// The 高级 navigation group is collapsed by default; a test that wants one
	// of its low-frequency pages opens the group first.
	if _, ok := tester.Find(label); !ok {
		for _, advanced := range []string{"高级", "Advanced"} {
			found := false
			for _, text := range tester.Texts() {
				if text == advanced {
					found = true
					break
				}
			}
			if found {
				if err := tester.Click(advanced); err == nil {
					tester.Frame()
				}
				break
			}
		}
	}
	panelW, panelH := 820, 760
	if width-48 < panelW {
		panelW = width - 48
	}
	if height-48 < panelH {
		panelH = height - 48
	}
	panelX, panelY := (width-panelW)/2, (height-panelH)/2
	top, bottom := float32(panelY+50), float32(panelY+panelH-16)
	for range 30 {
		rect, ok := tester.Find(label)
		if !ok {
			break
		}
		cx, cy := rect.X+rect.W/2, rect.Y+rect.H/2
		if rect.W > 0 && rect.H > 0 &&
			cx >= float32(panelX+8) && cx <= float32(panelX+panelW-8) &&
			cy >= top && cy <= bottom {
			break
		}
		sx := float32(panelX + panelW/2)
		if rect.X < float32(panelX+200) {
			sx = float32(panelX + 100)
		}
		delta := cy - (top+bottom)/2
		if delta > 220 {
			delta = 220
		}
		if delta < -220 {
			delta = -220
		}
		if delta == 0 {
			delta = 120
		}
		tester.Scroll(sx, float32(panelY+panelH/2), 0, delta)
		tester.Frame()
	}
	if err := tester.Click(label); err != nil {
		t.Fatalf("open settings page %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}
