package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSidebarNavigation is the M0-7 interaction skeleton: drive the view
// with ui.NewTester (no window) and assert screen state changes.
func TestSidebarNavigation(t *testing.T) {
	app := &App{}
	tt := ui.NewTester(app.View, 1600, 1000)

	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}
	if app.Screen != Plugins {
		t.Fatalf("screen = %v, want Plugins", app.Screen)
	}
	if err := tt.Click("Automation tasks"); err != nil {
		t.Fatalf("click Automation tasks: %v", err)
	}
	if app.Screen != Automation {
		t.Fatalf("screen = %v, want Automation", app.Screen)
	}
	// A sidebar session row opens the session screen.
	if err := tt.Click("Untitled"); err != nil {
		t.Fatalf("click session row: %v", err)
	}
	if app.Screen != Session {
		t.Fatalf("screen = %v, want Session", app.Screen)
	}
}
