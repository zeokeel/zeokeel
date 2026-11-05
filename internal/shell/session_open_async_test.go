package shell

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
)

// TestSlowSessionOpenDoesNotBlockFrame pins AR-08 for user-initiated opens: a
// store that takes 250ms must not hold the frame, the previous content stays
// visible with an "Opening session…" note, and the result is applied when the
// background read finishes.
func TestSlowSessionOpenDoesNotBlockFrame(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	target, err := h.ctrl.CreateSession(context.Background(), "slow target")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	snap, err := h.ctrl.OpenSession(context.Background(), target)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	// Return to the first session so opening the target is a real switch.
	if _, err := h.ctrl.OpenSession(context.Background(), h.ctrl.CurrentSessionID()); err != nil {
		t.Fatalf("restore session: %v", err)
	}
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	sessionOpenLoader = func(ctx context.Context, id string) (*state.Snapshot, error) {
		time.Sleep(250 * time.Millisecond)
		return snap, nil
	}
	t.Cleanup(func() { sessionOpenLoader = nil })

	start := time.Now()
	h.app.openSessionAsync(nil, target)
	tt.Frame() // build while the loader sleeps
	elapsed := time.Since(start)
	if elapsed > 120*time.Millisecond {
		t.Fatalf("frame blocked on the slow session open: %s", elapsed)
	}
	if !tt.HasText("Opening session") {
		t.Fatalf("loading state missing while the store is slow: %q", tt.Texts())
	}

	// The result lands on a later build.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if h.app.shellUI().sessionOpeningID == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tt.Frame()
	if got := h.app.Snapshot(); got == nil || got.SessionID != target {
		t.Fatalf("opened snapshot = %+v, want session %q", got, target)
	}
	if h.app.Screen != Session {
		t.Fatalf("screen = %v, want Session", h.app.Screen)
	}
	if tt.HasText("Opening session") {
		t.Fatalf("loading state was not cleared: %q", tt.Texts())
	}
}

// TestFastSessionOpenAppliesInline pins the fast path: a normal local store is
// applied without any visible loading state or extra frame.
func TestFastSessionOpenAppliesInline(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	target, err := h.ctrl.CreateSession(context.Background(), "quick target")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	h.app.openSessionAsync(nil, target)
	tt.Frame()
	if h.app.shellUI().sessionOpeningID != "" {
		t.Fatalf("fast store left the loading state on: %q", h.app.shellUI().sessionOpeningID)
	}
	if got := h.app.Snapshot(); got == nil || got.SessionID != target {
		t.Fatalf("snapshot = %+v, want %q", got, target)
	}
}
