package shell

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/updater"
)

// TestUpdatesCheckRunsAndShowsResult pins the Updates page's real check: the
// button calls the update seam, shows a checking state, then the found version
// or the up-to-date note.
func TestUpdatesCheckRunsAndShowsResult(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	calls := 0
	checkForUpdate = func(app *App, ctx context.Context) (*updater.Release, bool, error) {
		calls++
		return &updater.Release{Version: "1.4.2"}, true, nil
	}
	t.Cleanup(func() { checkForUpdate = nil })

	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Updates")
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("click Check: %v", err)
	}
	tt.Frame()
	waitForText(t, tt, "1.4.2")
	if calls != 1 {
		t.Fatalf("update seam calls = %d, want 1", calls)
	}

	// A no-update run reports the up-to-date state.
	checkForUpdate = func(*App, context.Context) (*updater.Release, bool, error) {
		return nil, false, nil
	}
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("click Check again: %v", err)
	}
	waitForText(t, tt, "up to date")
}

// TestUpdatesCheckErrors pins the error state: a disabled feed and a failing
// check are explained instead of a silent button.
func TestUpdatesCheckErrors(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	checkForUpdate = func(*App, context.Context) (*updater.Release, bool, error) {
		return nil, false, updater.ErrDisabled
	}
	t.Cleanup(func() { checkForUpdate = nil })
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Updates")
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("click Check: %v", err)
	}
	waitForText(t, tt, "No update feed")

	checkForUpdate = func(*App, context.Context) (*updater.Release, bool, error) {
		return nil, false, errors.New("network is unreachable")
	}
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("click Check after error: %v", err)
	}
	waitForText(t, tt, "network is unreachable")
}

// TestDesktopGlobalHotkeyRow pins the global-hotkey setting: a valid shortcut
// is validated with the platform parser and persisted, an invalid one is
// refused with an explanation, and unsupported platforms say so.
func TestDesktopGlobalHotkeyRow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Desktop")
	if !tt.HasText("Global shortcut") {
		t.Fatalf("global hotkey row missing: %q", tt.Texts())
	}
	h.app.shellUI().textDrafts[settingGlobalHotkey] = "Ctrl+Alt+K"
	tt.Frame()
	if got, err := h.store.GetSetting(settingGlobalHotkey); err != nil || got != "Ctrl+Alt+K" {
		t.Fatalf("%s = %q, %v; want saved shortcut", settingGlobalHotkey, got, err)
	}
	if err := tt.Click("Global shortcut"); err != nil {
		t.Fatalf("focus hotkey field: %v", err)
	}
	h.app.shellUI().textDrafts[settingGlobalHotkey] = "Ctrl+NotAKey"
	tt.Frame()
	if got, _ := h.store.GetSetting(settingGlobalHotkey); got != "Ctrl+Alt+K" {
		t.Fatalf("invalid shortcut was persisted: %q", got)
	}
	if !tt.HasText("Invalid shortcut") {
		t.Fatalf("invalid shortcut explanation missing: %q", tt.Texts())
	}
	// This test runs on Linux, where the platform layer cannot register a
	// system-wide shortcut yet; the page must say so instead of pretending.
	if !tt.HasText("not supported on this platform") {
		t.Fatalf("unsupported-platform note missing: %q", tt.Texts())
	}
}

// waitForText frames the tester until text appears (the update check runs in a
// goroutine, so the result arrives a frame or two later).
func waitForText(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	for i := 0; i < 40; i++ {
		tt.Frame()
		if tt.HasText(text) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("text %q did not appear; texts: %v", text, tt.Texts())
}
