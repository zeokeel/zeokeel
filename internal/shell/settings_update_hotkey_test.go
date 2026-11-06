package shell

import (
	"context"
	"errors"
	"runtime"
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

// TestUpdateDownloadInstallFlow pins the download/install state machine: after
// a check finds a release the install action is enabled, clicking it calls the
// seam once and the page awaits a restart.
func TestUpdateDownloadInstallFlow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := h.store.SetSetting(settingUpdateFeed, "https://updates.example/manifest.json"); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	checkForUpdate = func(*App, context.Context) (*updater.Release, bool, error) {
		return &updater.Release{Version: "1.4.2"}, true, nil
	}
	installed := 0
	SetInstallUpdateHook(func(ctx context.Context, progress func(int64, int64)) (*UpdateInstallOutcome, error) {
		installed++
		progress(512, 1024)
		time.Sleep(150 * time.Millisecond)
		return &UpdateInstallOutcome{
			CurrentVersion: BuildVersion, ReleaseVersion: "1.4.2",
			StagedPath: "/tmp/staged", Staged: true,
		}, nil
	})
	t.Cleanup(func() { checkForUpdate = nil; SetInstallUpdateHook(nil) })

	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Updates")
	// Before a check the action is present but the status says why it waits.
	if !tt.HasText("Check for updates first.") {
		t.Fatalf("pre-check install hint missing: %q", tt.Texts())
	}
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("click Check: %v", err)
	}
	waitForText(t, tt, "1.4.2")
	if !tt.HasText("Download and install") {
		t.Fatalf("install action missing after a check: %q", tt.Texts())
	}
	if err := tt.Click("Download and install"); err != nil {
		t.Fatalf("click Download and install: %v", err)
	}
	// The app seam's progress callback is rendered while downloading.
	waitForText(t, tt, "50%")
	waitForText(t, tt, "Restart ZeoKeel")
	if installed != 1 {
		t.Fatalf("install seam calls = %d, want 1", installed)
	}
	if !tt.HasText("Restart now") {
		t.Fatalf("restart action missing after install: %q", tt.Texts())
	}
}

// TestUpdateInstallErrorIsShown pins the error state of the install action.
func TestUpdateInstallErrorIsShown(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := h.store.SetSetting(settingUpdateFeed, "https://updates.example/manifest.json"); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	checkForUpdate = func(*App, context.Context) (*updater.Release, bool, error) {
		return &updater.Release{Version: "2.0.0"}, true, nil
	}
	SetInstallUpdateHook(func(context.Context, func(int64, int64)) (*UpdateInstallOutcome, error) {
		return nil, errors.New("checksum mismatch")
	})
	t.Cleanup(func() { checkForUpdate = nil; SetInstallUpdateHook(nil) })
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Updates")
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("click Check: %v", err)
	}
	waitForText(t, tt, "2.0.0")
	if err := tt.Click("Download and install"); err != nil {
		t.Fatalf("click Download and install: %v", err)
	}
	waitForText(t, tt, "checksum mismatch")
}

// TestUpdateNoFeedDisablesInstall pins the no-feed state: the page explains the
// missing update.feed_url and the install action never runs.
func TestUpdateNoFeedDisablesInstall(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	checkForUpdate = func(*App, context.Context) (*updater.Release, bool, error) {
		return nil, false, updater.ErrDisabled
	}
	t.Cleanup(func() { checkForUpdate = nil })
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Updates")
	if !tt.HasText("Configure update.feed_url") {
		t.Fatalf("no-feed hint missing: %q", tt.Texts())
	}
	// A disabled button must not start the install seam.
	called := false
	SetInstallUpdateHook(func(context.Context, func(int64, int64)) (*UpdateInstallOutcome, error) {
		called = true
		return &UpdateInstallOutcome{Staged: true}, nil
	})
	t.Cleanup(func() { SetInstallUpdateHook(nil) })
	if err := tt.Click("Download and install"); err == nil {
		// The button exists; a click must not reach the seam.
	}
	if called {
		t.Fatal("install seam ran without a feed")
	}
	if h.app.shellUI().updateSnapshot().installing {
		t.Fatal("install state machine started without a feed")
	}
}

// TestSettingsPagesFitNarrowWindow pins the narrow-window layout: at 900x600
// every settings page header and its primary control stay inside the window
// horizontally (no clipped controls), including the pages this round touched.
func TestSettingsPagesFitNarrowWindow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.SettingsView, 900, 600)
	tt.Frame()
	pages := []struct {
		nav   string
		title string
		ctrl  string
	}{
		{"Updates", "Updates", "Check"},
		{"Agent presets", "Agent presets", "Reload presets"},
		{"Desktop", "Desktop", "Global shortcut"},
		{"Data & privacy", "Data & privacy", "Open"},
	}
	for _, page := range pages {
		if err := tt.Click(page.nav); err != nil {
			t.Fatalf("open %q: %v", page.nav, err)
		}
		tt.Frame()
		if !tt.HasText(page.title) {
			t.Fatalf("%s: page title missing: %q", page.nav, tt.Texts())
		}
		for _, label := range []string{page.title, page.ctrl} {
			rect, ok := tt.Find(label)
			if !ok {
				t.Fatalf("%s: %q not found: %q", page.nav, label, tt.Texts())
			}
			if rect.X < 0 || rect.X+rect.W > 900 {
				t.Fatalf("%s: %q clipped horizontally: %+v", page.nav, label, rect)
			}
			if rect.Y < 0 {
				t.Fatalf("%s: %q clipped vertically: %+v", page.nav, label, rect)
			}
		}
	}
}

// TestUpdateInstallFallbackWithoutHook pins the built-in fallback: when no app
// seam is installed the action still runs the updater engine (download +
// schedule) and surfaces its failure instead of doing nothing.
func TestUpdateInstallFallbackWithoutHook(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := h.store.SetSetting(settingUpdateFeed, "https://127.0.0.1:9/manifest.json"); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	checkForUpdate = func(*App, context.Context) (*updater.Release, bool, error) {
		return &updater.Release{
			Version: "3.0.0",
			Asset:   updater.Asset{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: "https://127.0.0.1:9/zeokeel"},
		}, true, nil
	}
	t.Cleanup(func() { checkForUpdate = nil; SetInstallUpdateHook(nil) })
	SetInstallUpdateHook(nil) // explicit fallback
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Updates")
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("click Check: %v", err)
	}
	waitForText(t, tt, "3.0.0")
	if err := tt.Click("Download and install"); err != nil {
		t.Fatalf("click Download and install: %v", err)
	}
	waitForText(t, tt, "Could not install the update")
}

// TestHotkeyErrorsAreLocalized pins the P3 polish: parser errors from the
// globalhotkey package are English; the Chinese UI must explain them in
// Chinese instead of leaking the inner error text.
func TestHotkeyErrorsAreLocalized(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "桌面")
	h.app.shellUI().textDrafts[settingGlobalHotkey] = "Ctrl+NotAKey"
	tt.Frame()
	if !tt.HasText("未知按键") {
		t.Fatalf("localized hotkey key error missing: %q", tt.Texts())
	}
	if tt.HasText("unknown key") || tt.HasText("Invalid shortcut") {
		t.Fatalf("English hotkey error leaked into the Chinese UI: %q", tt.Texts())
	}
	h.app.shellUI().textDrafts[settingGlobalHotkey] = "Ctrl"
	tt.Frame()
	if !tt.HasText("至少需要一个修饰键") {
		t.Fatalf("localized hotkey shape error missing: %q", tt.Texts())
	}
}
