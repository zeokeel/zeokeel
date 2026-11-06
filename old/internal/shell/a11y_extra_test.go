package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestPluginsTabReachability covers the plugin page's filters, actions and
// switches, which used to be unlabeled rows.
func TestPluginsTabReachability(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Plugins
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	assertTabReachable(t, tt, []string{
		"About plugins", "Refresh", "Add plugin", "Choose how to add a plugin",
		"View Subagent", "View Agent Teams", "Enable Subagent",
	})
}

// TestAutomationTabReachability covers the filter tabs and per-task actions.
func TestAutomationTabReachability(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Reachable task", Cron: "@hourly", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	h.app.Screen = Automation
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	// The filter tabs are one focus group: Tab enters at All and the arrows
	// move within the group.
	assertTabReachable(t, tt, []string{
		"New automation task", "All",
		"Search automation tasks", "Run now", "Edit", "Delete",
		"Disable Reachable task",
	})
	for i := 0; i < 40 && !tt.Focused("All"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("All") {
		t.Fatal("automation tab group never took focus")
	}
	tt.Key(0, ui.KeyRight)
	if !tt.Focused("Enabled") {
		t.Fatalf("Right did not move to Enabled; texts: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyRight)
	if !tt.Focused("Inactive") {
		t.Fatalf("Right did not move to Inactive: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyHome)
	if !tt.Focused("All") {
		t.Fatalf("Home did not return to All: %q", tt.Texts())
	}
}

// TestSidebarRowsTabReachability covers the workspace/session rows and their
// plus/menu actions, which were pointer-only.
func TestSidebarRowsTabReachability(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	assertTabReachable(t, tt, []string{
		"test", "live", "New session in test", "Workspace actions for test",
		"Session actions for live", "Add workspace",
	})
}

// TestRightSidebarTabReachability covers the Files tab's breadcrumb, directory
// opener, reload and file rows.
func TestRightSidebarTabReachability(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	writeTestFile(t, h.dir, "probe.txt", "content")
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	assertTabReachable(t, tt, []string{
		"Start", "Files", "Terminal", "Log", "New tab", "Split", "Fullscreen",
		"Collapse right sidebar", "Open directory in system app", "Reload",
		"⌂ test", "probe.txt",
	})
}

// TestHomeModeChipReflectsActiveMode checks the accessible name follows the
// active agent mode instead of always saying "Standard mode".
func TestHomeModeChipReflectsActiveMode(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	h.app.setAgentMode("deep")
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if tt.HasText("Standard mode") {
		t.Fatalf("mode chip still says Standard mode in deep mode: %q", tt.Texts())
	}
	if !tt.HasText("Deep") {
		t.Fatalf("mode chip does not name the active mode: %q", tt.Texts())
	}
}
