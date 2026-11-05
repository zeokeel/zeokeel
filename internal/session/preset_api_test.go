package session_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/presets"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// presetHarness is a store-backed controller used by the preset and question
// controller-seam tests.
type presetHarness struct {
	ctrl *session.Controller
	st   *store.Store
}

func newPresetHarness(t *testing.T, dataDir string) *presetHarness {
	return newPresetHarnessWithKnownTools(t, dataDir, nil)
}

// newPresetHarnessWithKnownTools is newPresetHarness with preset tool
// validation enabled (Config.PresetKnownTools).
func newPresetHarnessWithKnownTools(t *testing.T, dataDir string, knownTools []string) *presetHarness {
	t.Helper()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	broker := state.NewBroker()
	ctrl, err := session.New(session.Config{
		Store:            st,
		Broker:           broker,
		PresetKnownTools: knownTools,
	})
	if err != nil {
		_ = st.Close()
		broker.Close()
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		broker.Close()
		_ = st.Close()
	})
	return &presetHarness{ctrl: ctrl, st: st}
}

// writeReviewerPreset writes one user preset file into dataDir/presets.
func writeReviewerPreset(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
		"id": "reviewer",
		"name": "Reviewer",
		"description": "Read-only code review persona",
		"tools": ["read_file", "grep"],
		"persona": "You are a careful code reviewer.",
		"prompt": "Cite files by path and line.",
		"model": "review-model"
	}`
	if err := os.WriteFile(filepath.Join(dir, "reviewer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestControllerPresetsLoadsUserAndBuiltins covers the Controller preset
// registry: built-ins are always present and user JSON files merge in.
func TestControllerPresetsLoadsUserAndBuiltins(t *testing.T) {
	dataDir := t.TempDir()
	writeReviewerPreset(t, dataDir)
	h := newPresetHarness(t, dataDir)
	reg := h.ctrl.Presets()
	if reg == nil {
		t.Fatal("Controller.Presets() = nil")
	}
	preset, ok := reg.Get("reviewer")
	if !ok {
		t.Fatal("user preset reviewer missing from Controller.Presets()")
	}
	if preset.Persona != "You are a careful code reviewer." || preset.Model != "review-model" {
		t.Fatalf("user preset = %+v, want persona/model from the JSON file", preset)
	}
	if _, ok := reg.Get(presets.BuiltinStandardID); !ok {
		t.Fatal("built-in standard preset missing")
	}
}

// TestSetSessionPresetPersistsAndRejectsUnknown covers the per-session preset
// setting and its ErrNotFound contract.
func TestSetSessionPresetPersistsAndRejectsUnknown(t *testing.T) {
	dataDir := t.TempDir()
	writeReviewerPreset(t, dataDir)
	h := newPresetHarness(t, dataDir)
	ctx := context.Background()
	sessionID, err := h.ctrl.CreateSession(ctx, "preset session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := h.ctrl.SessionPresetID(sessionID); got != "" {
		t.Fatalf("new session preset = %q, want empty", got)
	}
	if err := h.ctrl.SetSessionPreset(sessionID, "reviewer"); err != nil {
		t.Fatalf("SetSessionPreset(reviewer): %v", err)
	}
	if got := h.ctrl.SessionPresetID(sessionID); got != "reviewer" {
		t.Fatalf("SessionPresetID = %q, want reviewer", got)
	}
	raw, err := h.st.GetSetting("session.preset." + sessionID)
	if err != nil || raw != "reviewer" {
		t.Fatalf("persisted preset setting = %q, %v; want reviewer", raw, err)
	}
	if err := h.ctrl.SetSessionPreset(sessionID, "no-such-preset"); !errors.Is(err, presets.ErrNotFound) {
		t.Fatalf("SetSessionPreset(unknown) = %v, want presets.ErrNotFound", err)
	}
}

// TestReloadPresetsAppliesAndRequestsRebuild covers ReloadPresets: new user
// files are visible afterwards and the next turn rebuild hook runs.
func TestReloadPresetsAppliesAndRequestsRebuild(t *testing.T) {
	dataDir := t.TempDir()
	h := newPresetHarness(t, dataDir)
	reasons := make(chan string, 4)
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		reasons <- reason
		return nil
	})
	writeReviewerPreset(t, dataDir)
	reg, err := h.ctrl.ReloadPresets()
	if err != nil {
		t.Fatalf("ReloadPresets: %v", err)
	}
	if _, ok := reg.Get("reviewer"); !ok {
		t.Fatal("ReloadPresets did not pick up the new user preset")
	}
	select {
	case reason := <-reasons:
		if reason == "" {
			t.Fatal("rebuild reason is empty")
		}
	default:
		// Some builds may not have an app rebuild hook attached in tests; the
		// contract only requires the hook to run when wired.
		t.Fatal("ReloadPresets did not request an agent rebuild for the next turn")
	}
}

// TestDeleteSessionClearsPresetSettings covers the cleanup contract: deleting a
// session removes its per-session preset selection.
func TestDeleteSessionClearsPresetSettings(t *testing.T) {
	dataDir := t.TempDir()
	writeReviewerPreset(t, dataDir)
	h := newPresetHarness(t, dataDir)
	ctx := context.Background()
	sessionID, err := h.ctrl.CreateSession(ctx, "to delete")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.SetSessionPreset(sessionID, "reviewer"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	if err := h.ctrl.DeleteSession(ctx, sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := h.ctrl.SessionPresetID(sessionID); got != "" {
		t.Fatalf("SessionPresetID after delete = %q, want empty", got)
	}
	raw, err := h.st.GetSetting("session.preset." + sessionID)
	if err == nil && raw != "" {
		t.Fatalf("session.preset setting survived delete: %q", raw)
	}
}
