package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// focusedDuringTab runs up to n Tab presses and records which of the wanted
// accessible names received the keyboard focus. It is the keyboard-reachability
// gate the accessibility issues call for: a control without
// Focusable/Role never appears.
func focusedDuringTab(tt *ui.Tester, wanted []string, n int) map[string]bool {
	seen := make(map[string]bool)
	for i := 0; i < n && len(seen) < len(wanted); i++ {
		tt.Key(0, ui.KeyTab)
		for _, name := range wanted {
			if tt.Focused(name) {
				seen[name] = true
			}
		}
	}
	return seen
}

func assertTabReachable(t *testing.T, tt *ui.Tester, wanted []string) {
	t.Helper()
	seen := focusedDuringTab(tt, wanted, 120)
	var missing []string
	for _, name := range wanted {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("Tab never focused %q; accessible texts: %q", missing, tt.Texts())
	}
}

// TestHomeTabReachability covers the custom rows and chips on Home.
func TestHomeTabReachability(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	perm := "Access mode, current: " + h.app.composerPermissionLabel(h.app.currentSettings().Permission)
	model := "Select model, current " + h.app.DefaultModelLabel()
	assertTabReachable(t, tt, []string{
		"Collapse sidebar",
		"Plugins",
		"Automation tasks",
		"Search sessions",
		"View options",
		"Add workspace",
		"Workspace, current: test",
		"Standard mode",
		"Add files or run commands",
		perm,
		model,
		"Open right sidebar",
	})
}

// TestSessionTabReachability covers the session header tabs and controls.
func TestSessionTabReachability(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	// Send is disabled (and correctly not tabbable) with an empty draft, so
	// type something first; TestEmptySendIsDisabled covers the empty case.
	h.app.draft = "hello"
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	assertTabReachable(t, tt, []string{
		"Chat",
		"More actions",
		"Session actions for live",
		"Open right sidebar",
		"Composer input",
		"Add files or run commands",
		"Send",
	})
	for i := 0; i < 80 && !tt.Focused("Chat"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("Chat") {
		t.Fatal("Chat tab never took focus")
	}
	tt.Key(0, ui.KeyRight)
	if !tt.Focused("Trajectory") {
		t.Fatalf("Right did not move to Trajectory: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyLeft)
	if !tt.Focused("Chat") {
		t.Fatalf("Left did not return to Chat: %q", tt.Texts())
	}
}

// TestSettingsTabReachability covers the settings selectors, theme cards,
// shortcuts action and switch.
func TestSettingsTabReachability(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if h.app.Screen != Settings {
		t.Fatalf("settings did not open")
	}
	assertTabReachable(t, tt, []string{
		"Close settings",
		"General",
		"Models",
		"Built-in plugins",
		"Agent presets",
		"About",
		"Light",
		"Dark",
		"System",
		"Workspace Write",
		"English",
		"Edit shortcuts",
		"Toggle show coding view",
	})
}
