package app

import (
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// newSettingsTestApp returns an app with a minimal live controller (store +
// broker, no agent) so setting reads go through the real persistence path.
func newSettingsTestApp(t *testing.T) (*app, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	broker := state.NewBroker()
	ctrl, err := session.New(session.Config{Store: st, Broker: broker})
	if err != nil {
		broker.Close()
		_ = st.Close()
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		broker.Close()
		_ = st.Close()
	})
	return &app{services: ctrl}, st
}

// TestAppSettingsDefaultsAndValues covers the PD-05 settings readers: defaults
// apply when a key is missing, and the shell's persisted spellings are honored.
func TestAppSettingsDefaultsAndValues(t *testing.T) {
	a, st := newSettingsTestApp(t)
	if !a.notificationsEnabled() {
		t.Error("notification.enabled default = false, want true")
	}
	if !a.notificationsSuppressFocused() {
		t.Error("notification.suppress_focused default = false, want true")
	}
	if a.closeBehaviorQuits() {
		t.Error("ui.close_behavior default = quit, want hide")
	}

	cases := map[string]string{
		"notification.enabled":          "false",
		"notification.suppress_focused": "off",
		"ui.close_behavior":             "quit",
	}
	for key, value := range cases {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%q): %v", key, err)
		}
	}
	if a.notificationsEnabled() {
		t.Error("notification.enabled=false was ignored")
	}
	if a.notificationsSuppressFocused() {
		t.Error("notification.suppress_focused=off was ignored")
	}
	if !a.closeBehaviorQuits() {
		t.Error("ui.close_behavior=quit was ignored")
	}

	// Boolean spellings the shell may save all parse; junk keeps the default.
	for _, value := range []string{"true", "1", "on", "yes"} {
		if err := st.SetSetting("notification.enabled", value); err != nil {
			t.Fatal(err)
		}
		if !a.notificationsEnabled() {
			t.Errorf("notification.enabled=%q parsed as false", value)
		}
	}
	if err := st.SetSetting("notification.enabled", "maybe"); err != nil {
		t.Fatal(err)
	}
	if !a.notificationsEnabled() {
		t.Error("invalid notification.enabled did not fall back to the default")
	}
}

// TestNotificationDispatcherSettings proves the two notification settings gate
// delivery: disabled mutes everything, and suppress_focused=false delivers even
// while the app is focused.
func TestNotificationDispatcherSettings(t *testing.T) {
	base := func() *notificationDispatcher {
		d := newNotificationDispatcher(newFakeNotifier(), func() bool { return true }, func() string { return "en" }, nil)
		d.useTurnFinishedHook()
		d.useApprovalHook()
		d.enabled = func() bool { return true }
		d.suppressFocused = func() bool { return true }
		return d
	}

	// Focused + default suppression: nothing shown.
	d := base()
	d.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if n := d.notifier.(*fakeNotifier).count(); n != 0 {
		t.Fatalf("focused app notified with suppress_focused default: %d", n)
	}

	// suppress_focused=false: delivered while focused.
	d = base()
	d.suppressFocused = func() bool { return false }
	d.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if n := d.notifier.(*fakeNotifier).count(); n != 1 {
		t.Fatalf("suppress_focused=false notifications = %d, want 1", n)
	}

	// notification.enabled=false: muted even when unfocused.
	d = base()
	d.active = func() bool { return false }
	d.enabled = func() bool { return false }
	d.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if n := d.notifier.(*fakeNotifier).count(); n != 0 {
		t.Fatalf("notification.enabled=false still notified: %d", n)
	}

	// Enabled + unfocused: delivered.
	d = base()
	d.active = func() bool { return false }
	d.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if n := d.notifier.(*fakeNotifier).count(); n != 1 {
		t.Fatalf("unfocused enabled notifications = %d, want 1", n)
	}
}

// TestObserveTurnFinishedFinishesAutomationRun covers the PD-06 wiring: a fired
// automation's run is completed/failed/stopped when its session turn ends.
func TestObserveTurnFinishedFinishesAutomationRun(t *testing.T) {
	a, st := newSettingsTestApp(t)
	task, err := st.CreateAutomation(store.Automation{Title: "wired", Prompt: "p", Cron: "@daily", Enabled: true})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	run, err := st.CreateAutomationRun(task.ID, "session-x")
	if err != nil {
		t.Fatalf("CreateAutomationRun: %v", err)
	}
	a.trackAutomationRun("session-x", run.ID)
	a.observeTurnFinished(session.TurnOutcome{SessionID: "session-x", TurnID: "t1", Failed: true, Error: "model exploded"})

	finished, err := st.LatestAutomationRun(task.ID)
	if err != nil {
		t.Fatalf("LatestAutomationRun: %v", err)
	}
	if finished.Status != store.AutomationRunFailed || finished.Error != "model exploded" {
		t.Fatalf("run = %+v, want failed with the turn error", finished)
	}
	// The tracking entry is consumed: a second outcome for the session is a
	// no-op instead of double-finishing.
	a.observeTurnFinished(session.TurnOutcome{SessionID: "session-x", TurnID: "t2", Stopped: true})
	if again, err := st.LatestAutomationRun(task.ID); err != nil || again.Status != store.AutomationRunFailed {
		t.Fatalf("second outcome rewrote the run: %+v, %v", again, err)
	}
}

// TestRunAutomationTaskRecordsFailure proves a fired automation that cannot
// start a turn still records a failed run instead of leaving it "running".
func TestRunAutomationTaskRecordsFailure(t *testing.T) {
	a, st := newSettingsTestApp(t) // controller has no agent: Submit fails
	task, err := st.CreateAutomation(store.Automation{
		Title: "no agent", Prompt: "do it", Cron: "@daily", Enabled: true, Permission: "read-only",
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	a.runAutomationTask(a.services, *task)

	run, err := st.LatestAutomationRun(task.ID)
	if err != nil {
		t.Fatalf("LatestAutomationRun: %v", err)
	}
	if run.Status != store.AutomationRunFailed || run.Error == "" {
		t.Fatalf("run = %+v, want failed with an error", run)
	}
	if run.SessionID == "" {
		t.Error("failed run did not record the session it created")
	}
	// Run-now rollback (P3): the empty session is deleted again, so a failed
	// fired task leaves no empty conversation behind.
	if _, serr := st.GetSession(run.SessionID); serr == nil {
		t.Fatalf("failed automation left empty session %s behind", run.SessionID)
	}
}
