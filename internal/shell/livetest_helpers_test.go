package shell

import (
	"os"
	"path/filepath"
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

// previewDir is where a brand-preview PNG goes. Ordinary test runs and CI
// write into the test's temp dir so they never dirty the work tree; run
// ZEOKEEL_UPDATE_PREVIEWS=1 go test -run 'TestRender.*NativePreview' to
// refresh the committed docs/design/preview snapshots on purpose.
func previewDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("ZEOKEEL_UPDATE_PREVIEWS") == "1" {
		dir := filepath.Join("..", "..", "docs", "design", "preview")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create preview dir: %v", err)
		}
		return dir
	}
	return t.TempDir()
}
