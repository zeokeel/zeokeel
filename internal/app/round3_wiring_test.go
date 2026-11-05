package app

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/globalhotkey"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/updater"
)

// Round-3 app wiring (TDD): update check, global hotkey, automation rollback.

// TestRunAutomationTaskRollsBackEmptySessionOnSubmitFailure covers the P3
// run-now rollback: when the freshly created automation session cannot start a
// turn, the empty session is deleted again and the failed run is recorded.
func TestRunAutomationTaskRollsBackEmptySessionOnSubmitFailure(t *testing.T) {
	a, st := newSettingsTestApp(t) // controller has no agent: Submit fails
	task, err := st.CreateAutomation(store.Automation{
		Title: "rollback", Prompt: "do it", Cron: "@daily", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	a.runAutomationTask(a.services, *task)

	run, err := st.LatestAutomationRun(task.ID)
	if err != nil {
		t.Fatalf("LatestAutomationRun: %v", err)
	}
	if run.Status != store.AutomationRunFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	if run.SessionID == "" {
		t.Fatal("failed run did not record the created session id")
	}
	if _, err := st.GetSession(run.SessionID); err == nil {
		t.Fatalf("empty automation session %s was not rolled back", run.SessionID)
	}
	sessions, err := st.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions after rollback = %d, want 0: %+v", len(sessions), sessions)
	}
}

// fakeHotkeyManager records registrations for the app hotkey wiring test.
type fakeHotkeyManager struct {
	registerErr  error
	registered   []string
	unregistered int
	closed       int
}

func (f *fakeHotkeyManager) Register(h globalhotkey.Hotkey, _ func()) (globalhotkey.Handle, error) {
	if f.registerErr != nil {
		return 0, f.registerErr
	}
	f.registered = append(f.registered, h.String())
	return globalhotkey.Handle(len(f.registered)), nil
}

func (f *fakeHotkeyManager) Unregister(globalhotkey.Handle) error {
	f.unregistered++
	return nil
}

func (f *fakeHotkeyManager) Close() error {
	f.closed++
	return nil
}

// TestGlobalHotkeySettingWiring covers the product decision: the global_hotkey
// setting registers the shortcut, an empty value unregisters it, and an
// unsupported platform degrades with a notice instead of blocking startup.
func TestGlobalHotkeySettingWiring(t *testing.T) {
	a, st := newSettingsTestApp(t)
	fake := &fakeHotkeyManager{}
	a.newHotkeyManager = func() (hotkeyManager, error) { return fake, nil }
	shown := 0
	a.showWindowFromHotkey = func() { shown++ }

	if err := st.SetSetting(globalHotkeySettingKey, "Ctrl+Alt+Z"); err != nil {
		t.Fatal(err)
	}
	if err := a.syncGlobalHotkey(); err != nil {
		t.Fatalf("syncGlobalHotkey: %v", err)
	}
	if len(fake.registered) != 1 || fake.registered[0] != "Ctrl+Alt+Z" {
		t.Fatalf("registered = %v, want [Ctrl+Alt+Z]", fake.registered)
	}
	if a.hotkeyHandle == nil {
		t.Fatal("hotkey handle was not recorded")
	}

	// An empty setting unregisters the current shortcut.
	if err := st.SetSetting(globalHotkeySettingKey, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.syncGlobalHotkey(); err != nil {
		t.Fatalf("syncGlobalHotkey(empty): %v", err)
	}
	if fake.unregistered != 1 {
		t.Fatalf("unregistered = %d, want 1", fake.unregistered)
	}

	// Invalid spec: report a notice, do not break the app.
	if err := st.SetSetting(globalHotkeySettingKey, "not-a-hotkey"); err != nil {
		t.Fatal(err)
	}
	if err := a.syncGlobalHotkey(); err != nil {
		t.Fatalf("syncGlobalHotkey(invalid): %v", err)
	}
	if a.hotkeyNotice == "" {
		t.Fatal("invalid hotkey did not record a user-facing notice")
	}
	// Shutdown unregisters and closes the manager.
	a.stopGlobalHotkeys()
	if fake.closed != 1 {
		t.Fatalf("hotkey manager closed = %d, want 1", fake.closed)
	}
}

// TestGlobalHotkeyUnsupportedPlatformDegrades covers Linux/macOS: the manager
// reports ErrUnsupported, startup keeps running and the notice explains why.
func TestGlobalHotkeyUnsupportedPlatformDegrades(t *testing.T) {
	a, st := newSettingsTestApp(t)
	a.newHotkeyManager = func() (hotkeyManager, error) {
		return nil, globalhotkey.ErrUnsupported
	}
	if err := st.SetSetting(globalHotkeySettingKey, "Ctrl+Alt+Z"); err != nil {
		t.Fatal(err)
	}
	if err := a.syncGlobalHotkey(); err != nil {
		t.Fatalf("unsupported platform must not fail startup: %v", err)
	}
	if a.hotkeyNotice == "" {
		t.Fatal("unsupported platform did not record a notice")
	}
}

// TestCheckForUpdateWiring covers settings update.feed_url/update.check_on_start
// and the fakeable updater seam.
func TestCheckForUpdateWiring(t *testing.T) {
	a, st := newSettingsTestApp(t)
	want := &updater.Release{Version: "9.9.9"}
	var gotCfg updater.Config
	a.updateCheck = func(_ context.Context, cfg updater.Config) (*updater.Release, bool, error) {
		gotCfg = cfg
		return want, true, nil
	}

	// No feed configured: auto-update is disabled, not an error path for the UI.
	if _, _, err := a.CheckForUpdate(context.Background()); !errors.Is(err, updater.ErrDisabled) {
		t.Fatalf("CheckForUpdate without feed = %v, want ErrDisabled", err)
	}
	if err := st.SetSetting(updateFeedURLKey, "https://updates.example/feed.json"); err != nil {
		t.Fatal(err)
	}
	rel, available, err := a.CheckForUpdate(context.Background())
	if err != nil || !available || rel == nil || rel.Version != "9.9.9" {
		t.Fatalf("CheckForUpdate = %+v, %v, %v; want the fake release", rel, available, err)
	}
	if gotCfg.FeedURL != "https://updates.example/feed.json" || gotCfg.DataDir == "" {
		t.Fatalf("updater config = %+v, want the persisted feed and the data dir", gotCfg)
	}
	// check_on_start defaults off and honors the persisted spelling.
	if a.checkUpdatesOnStart() {
		t.Fatal("update.check_on_start default should be off")
	}
	if err := st.SetSetting(updateCheckOnStartKey, "on"); err != nil {
		t.Fatal(err)
	}
	if !a.checkUpdatesOnStart() {
		t.Fatal("update.check_on_start=on was not honored")
	}
}
