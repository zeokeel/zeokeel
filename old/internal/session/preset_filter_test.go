package session_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/presets"
)

// writePresetFile writes one user preset JSON into dataDir/presets.
func writePresetFile(t *testing.T, dataDir, name, body string) {
	t.Helper()
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReloadPresetsRejectsUnknownToolAndKeepsPrevious covers the fail-closed
// load path: with KnownTools installed, a user preset naming a misspelled tool
// fails ErrUnknownTool and the previous registry stays active.
func TestReloadPresetsRejectsUnknownToolAndKeepsPrevious(t *testing.T) {
	dataDir := t.TempDir()
	writeReviewerPreset(t, dataDir)
	h := newPresetHarness(t, dataDir)
	h.ctrl.SetPresetKnownTools([]string{"read_file", "grep", "bash", "write_file", "edit_file"})
	if _, err := h.ctrl.ReloadPresets(); err != nil {
		t.Fatalf("first validated ReloadPresets: %v", err)
	}
	previous, ok := h.ctrl.Presets().Get("reviewer")
	if !ok {
		t.Fatal("reviewer preset missing after the valid reload")
	}

	// Break the allowlist with a typo; the reload must fail closed.
	writePresetFile(t, dataDir, "reviewer.json", `{
		"id": "reviewer",
		"name": "Reviewer",
		"tools": ["read_file", "reed_file"]
	}`)
	if _, err := h.ctrl.ReloadPresets(); !errors.Is(err, presets.ErrUnknownTool) {
		t.Fatalf("ReloadPresets with a misspelled tool = %v, want presets.ErrUnknownTool", err)
	}
	current, ok := h.ctrl.Presets().Get("reviewer")
	if !ok {
		t.Fatal("previous registry was dropped after the failed reload")
	}
	if len(current.Tools) != len(previous.Tools) || current.Tools[0] != previous.Tools[0] {
		t.Fatalf("failed reload replaced the registry: got tools %v, want the previous %v", current.Tools, previous.Tools)
	}
	if err := h.ctrl.LastPresetLoadError(); !errors.Is(err, presets.ErrUnknownTool) {
		t.Fatalf("LastPresetLoadError = %v, want presets.ErrUnknownTool", err)
	}
}

// TestPresetKnownToolsInstalledAtNewLoadsValidated covers the New-time path:
// Config.PresetKnownTools validates the user files during the initial load, so
// a broken preset never becomes active.
func TestPresetKnownToolsInstalledAtNewLoadsValidated(t *testing.T) {
	dataDir := t.TempDir()
	writePresetFile(t, dataDir, "reviewer.json", `{
		"id": "reviewer",
		"name": "Reviewer",
		"tools": ["reed_file"]
	}`)
	h := newPresetHarnessWithKnownTools(t, dataDir, []string{"read_file", "grep"})
	if _, ok := h.ctrl.Presets().Get("reviewer"); ok {
		t.Fatal("invalid user preset became active during New; load must be fail-closed")
	}
	if err := h.ctrl.LastPresetLoadError(); !errors.Is(err, presets.ErrUnknownTool) {
		t.Fatalf("LastPresetLoadError = %v, want presets.ErrUnknownTool", err)
	}
}
