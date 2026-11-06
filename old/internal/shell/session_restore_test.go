package shell

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestDraftPersistedAndRestoredAcrossRestart pins PD-10: a typed draft is
// written to draft.<sessionID>, and a fresh App over the same controller
// restores it (with the last session) on its first frame.
func TestDraftPersistedAndRestoredAcrossRestart(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type("half-written message")
	tt.Frame()

	// The debounce may hold the first write; the pending-write frame or a
	// later frame must land it in the store.
	deadline := time.Now().Add(2 * time.Second)
	sessionID := h.ctrl.CurrentSessionID()
	for time.Now().Before(deadline) {
		if got, err := h.store.GetSetting(draftSettingKey(sessionID)); err == nil && got == "half-written message" {
			break
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
	if got, err := h.store.GetSetting(draftSettingKey(sessionID)); err != nil || got != "half-written message" {
		t.Fatalf("draft.%s = %q, %v", sessionID, got, err)
	}

	// A fresh App over the same controller simulates the next launch.
	restarted := &App{Ctrl: h.ctrl, Language: LangEN}
	rtt := ui.NewTester(restarted.View, 1600, 1000)
	rtt.Frame()
	if restarted.Screen != Session {
		t.Fatalf("restored screen = %v, want Session", restarted.Screen)
	}
	if got := h.ctrl.CurrentSessionID(); got != sessionID {
		t.Fatalf("restored session = %q, want %q", got, sessionID)
	}
	if restarted.draft != "half-written message" {
		t.Fatalf("restored draft = %q, want the saved text", restarted.draft)
	}
}

// TestLastSessionRestoredReadOnly pins the read-only half: restoring the last
// session opens it without submitting anything.
func TestLastSessionRestoredReadOnly(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	ctx := context.Background()
	secondID, err := h.ctrl.CreateSessionInWorkspace(ctx, h.ctrl.CurrentWorkspaceID(), "second")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	h.app.Screen = Session
	h.app.openSession(secondID)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if got := h.ctrl.CurrentSessionID(); got != secondID {
		t.Fatalf("open session = %q, want %q", got, secondID)
	}
	if got, err := h.store.GetSetting(settingLastSession); err != nil || got != secondID {
		t.Fatalf("%s = %q, %v; want %q", settingLastSession, got, err, secondID)
	}

	restarted := &App{Ctrl: h.ctrl, Language: LangEN}
	rtt := ui.NewTester(restarted.View, 1600, 1000)
	rtt.Frame()
	if restarted.Screen != Session {
		t.Fatalf("restored screen = %v, want Session", restarted.Screen)
	}
	if got := h.ctrl.CurrentSessionID(); got != secondID {
		t.Fatalf("restored session = %q, want %q", got, secondID)
	}
	if snap := h.ctrl.Snapshot(); snap != nil && snap.Phase == "running" {
		t.Fatal("restore started a turn; it must only open the session")
	}
	if sessions, err := h.ctrl.ListSessions(ctx); err != nil || len(sessions) != 2 {
		t.Fatalf("restore created sessions: %v, %v", sessions, err)
	}
	_ = rtt
}

// TestMissingLastSessionFallsBackHome pins the deleted-session case: a stale
// ui.last_session does not block the Home start.
func TestMissingLastSessionFallsBackHome(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := h.store.SetSetting(settingLastSession, "deleted-session-id"); err != nil {
		t.Fatalf("seed last session: %v", err)
	}
	restarted := &App{Ctrl: h.ctrl, Language: LangEN}
	tt := ui.NewTester(restarted.View, 1600, 1000)
	tt.Frame()
	if restarted.Screen != Home {
		t.Fatalf("stale last session left screen %v, want Home", restarted.Screen)
	}
	if got, _ := h.store.GetSetting(settingLastSession); got != "" {
		t.Fatalf("stale %s not cleared: %q", settingLastSession, got)
	}
}
