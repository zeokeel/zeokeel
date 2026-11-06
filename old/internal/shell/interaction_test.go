package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSidebarNavigation is the M0-7 interaction skeleton: drive the live view
// with ui.NewTester (no window) and assert screen state changes. The static
// shell is gone, so this uses the controller-backed fixture.
func TestSidebarNavigation(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}
	if h.app.Screen != Plugins {
		t.Fatalf("screen = %v, want Plugins", h.app.Screen)
	}
	if err := tt.Click("Automation tasks"); err != nil {
		t.Fatalf("click Automation tasks: %v", err)
	}
	if h.app.Screen != Automation {
		t.Fatalf("screen = %v, want Automation", h.app.Screen)
	}
	// A sidebar session row opens the session screen. The live fixture's
	// initial session is titled "live".
	if err := tt.Click("live"); err != nil {
		t.Fatalf("click session row: %v", err)
	}
	if h.app.Screen != Session {
		t.Fatalf("screen = %v, want Session", h.app.Screen)
	}
}
