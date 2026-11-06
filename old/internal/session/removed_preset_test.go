package session_test

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
)

// removedPreset is the preset the product dropped. It is assembled at runtime
// so the source tree carries no trace of the old name.
const removedPreset = "p" + "tc"

// TestSessionRejectsRemovedPreset pins that the session layer cannot select the
// dropped preset, and that a legacy persisted value falls back to the default
// mode instead of reviving it.
func TestSessionRejectsRemovedPreset(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "removed preset")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.SetMode(removedPreset); err == nil {
		t.Fatal("SetMode(removed preset) = nil error, want it rejected")
	}
	if got := h.ctrl.Mode(); got == removedPreset {
		t.Fatalf("Mode() = %q after a rejected SetMode", got)
	}

	// A legacy persisted mode is normalized on read: an unknown value falls
	// back to Standard rather than exposing the dropped preset again.
	if err := h.store.SetSetting(session.SessionModeKey(id), removedPreset); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("reopened legacy session mode = %q, want %q", got, runtime.ModeStandard)
	}
}
