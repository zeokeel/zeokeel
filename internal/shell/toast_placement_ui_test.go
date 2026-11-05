package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// overlaps reports whether two rects share any pixels.
func overlaps(a, b ui.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// TestToastDoesNotOverlapComposer pins UX-14's main-window rule: at the wide
// and the minimum window size the toast never covers the composer card of the
// screen it reports on (Session's composer owns the bottom edge, Home's is
// centered).
func TestToastDoesNotOverlapComposer(t *testing.T) {
	for _, size := range []struct{ w, h int }{{1600, 1000}, {900, 600}} {
		for _, screen := range []Screen{Home, Session} {
			h := newLiveAppAt(t, screen)
			h.app.Language = LangEN
			tt := ui.NewTester(h.app.View, size.w, size.h)
			tt.Frame()
			composer, ok := tt.Find("Composer input")
			if !ok {
				t.Fatalf("%dx%d screen %v: composer input missing: %q",
					size.w, size.h, screen, tt.Texts())
			}
			h.app.toastDeferred(nil, "placement probe")
			tt.Frame()
			toast, ok := tt.Find("placement probe")
			if !ok {
				t.Fatalf("%dx%d screen %v: toast missing", size.w, size.h, screen)
			}
			if overlaps(toast, composer) {
				t.Fatalf("%dx%d screen %v: toast %+v covers composer %+v",
					size.w, size.h, screen, toast, composer)
			}
		}
	}
}

// TestSettingsToastSitsInTopBand pins UX-14's Settings window rule: the shell
// places that window's toasts in the top band (pass-through), so a saved note
// never covers the form row it just changed.
func TestSettingsToastSitsInTopBand(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	h.app.toastDeferred(nil, "settings probe")
	tt.Frame()
	toast, ok := tt.Find("settings probe")
	if !ok {
		t.Fatalf("settings toast missing: %q", tt.Texts())
	}
	if toast.Y > 120 {
		t.Fatalf("settings toast is not in the top band: %+v", toast)
	}
}

// TestToastDoesNotInterceptPopupPointer pins the pointer half of UX-14: a
// visible toast must not swallow a click on a composer control, and the
// popover it opens must render.
func TestToastDoesNotInterceptPopupPointer(t *testing.T) {
	h := newLiveAppAt(t, Session)
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	h.app.toastDeferred(nil, "pointer probe")
	tt.Frame()
	if _, ok := tt.Find("pointer probe"); !ok {
		t.Fatalf("toast missing: %q", tt.Texts())
	}
	if err := tt.Click("Access mode, current: Workspace Write"); err != nil {
		t.Fatalf("toast intercepted the access-mode click: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Read Only") {
		t.Fatalf("access mode popover did not open under a visible toast: %q", tt.Texts())
	}
	// Choosing a row still works with the toast on screen.
	if err := tt.Click("Read Only"); err != nil {
		t.Fatalf("toast intercepted the menu row click: %v", err)
	}
	tt.Frame()
	if h.app.liveMenus().permissionOpen {
		t.Fatalf("permission menu stayed open after choosing: %q", tt.Texts())
	}
}
