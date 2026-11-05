package shell

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/state"
)

// newLiveAppAt returns the live controller-backed fixture with the requested
// top-level screen. Use it instead of the retired static `&App{Screen: ...}`
// fixtures: since pixel parity was retired, App.View draws only live screens.
func newLiveAppAt(t *testing.T, screen Screen) *liveApp {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = screen
	h.app.SetSnapshot(h.ctrl.Snapshot())
	return h
}

// newLiveAppWithSnapshot builds the live fixture and renders the given
// snapshot instead of the controller's current one. Tests that need a
// synthetic transcript/tool-call/usage state use this to keep the live
// renderer as the thing under test.
func newLiveAppWithSnapshot(t *testing.T, screen Screen, snap *state.Snapshot) *liveApp {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = screen
	if snap != nil {
		h.app.SetSnapshot(snap)
	}
	return h
}
