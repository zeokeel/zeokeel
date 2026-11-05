package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/presets"
)

// writePresetFile seeds one user preset file in the live app's data dir.
func writePresetFile(t *testing.T, h *liveApp, name, content string) {
	t.Helper()
	dir := filepath.Join(h.app.pluginDataDir(), "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create presets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write preset %s: %v", name, err)
	}
}

// TestPresetsPageListsCatalog pins the FP-06 settings page: built-ins and user
// presets in registry order with the override marker, tool counts and model.
func TestPresetsPageListsCatalog(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	writePresetFile(t, h, "reviewer.json", `{"id":"reviewer","name":"Reviewer","description":"Read-only review","tools":["read_file","grep"],"model":"fast-model","order":10}`)
	// A built-in override that omits name/description (merge-then-validate).
	writePresetFile(t, h, "minimal.json", `{"id":"minimal","tools":["read_file","bash"]}`)
	if _, err := h.ctrl.ReloadPresets(); err != nil {
		t.Fatalf("reload presets: %v", err)
	}
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Agent presets")
	for _, want := range []string{"Standard", "Minimal", "Creator", "Reviewer", "Read-only review"} {
		if !tt.HasText(want) {
			t.Fatalf("presets page missing %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("2 tools") {
		t.Fatalf("tool count missing: %q", tt.Texts())
	}
	if !tt.HasText("Model: fast-model") {
		t.Fatalf("preset model missing: %q", tt.Texts())
	}
	if !tt.HasText("overrides built-in") || !tt.HasText("user file") {
		t.Fatalf("override/user-file markers missing: %q", tt.Texts())
	}
	// The override keeps the built-in name and shows the merged allowlist.
	if !tt.HasText("Minimal") || !tt.HasText("all registered tools") {
		t.Fatalf("built-in standard row missing: %q", tt.Texts())
	}
}

// TestPresetsPageShowsBrokenFileWarningAndRecovers pins the fail-closed state:
// a malformed preset file is reported with its cause, the built-ins stay
// listed, and Reload clears the warning once the file is fixed.
func TestPresetsPageShowsBrokenFileWarningAndRecovers(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	writePresetFile(t, h, "broken.json", `{"id": }`)
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Agent presets")
	if !tt.HasText("Preset files ignored") {
		t.Fatalf("broken-file warning missing: %q", tt.Texts())
	}
	if !tt.HasText("Standard") {
		t.Fatalf("built-ins disappeared with a broken file: %q", tt.Texts())
	}
	// Fix the file, reload, and the warning goes away. One frame rebuilds the
	// page without the warning banner before the click is dispatched (the
	// banner changes every control's position).
	writePresetFile(t, h, "broken.json", `{"id":"fixed","name":"Fixed"}`)
	tt.Frame()
	if err := tt.Click("Reload presets"); err != nil {
		t.Fatalf("click Reload presets: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("Preset files ignored") {
		t.Fatalf("warning survived a successful reload: %q", tt.Texts())
	}
	if !tt.HasText("Fixed") {
		t.Fatalf("reloaded preset missing: %q", tt.Texts())
	}
}

// TestPresetsPageCreateExample pins the "Create example preset" action: it
// writes an editable template and the registry picks it up.
func TestPresetsPageCreateExample(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Agent presets")
	if err := tt.Click("Create example preset"); err != nil {
		t.Fatalf("click Create example preset: %v", err)
	}
	tt.Frame()
	path := filepath.Join(h.app.presetsDir(), presetExampleFile)
	data, err := os.ReadFile(path)
	if err != nil || !contains(string(data), `"id": "example-reviewer"`) {
		t.Fatalf("example preset = %q, %v", string(data), err)
	}
	if !tt.HasText("Reviewer") {
		t.Fatalf("created example preset not listed: %q", tt.Texts())
	}
	// A second click does not overwrite the user's edits.
	if err := os.WriteFile(path, []byte(`{"id":"reviewer","name":"Mine"}`), 0o644); err != nil {
		t.Fatalf("edit example: %v", err)
	}
	if _, err := h.ctrl.ReloadPresets(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := tt.Click("Create example preset"); err != nil {
		t.Fatalf("click Create example second time: %v", err)
	}
	if data, _ := os.ReadFile(path); !contains(string(data), `"name":"Mine"`) {
		t.Fatalf("second click overwrote the example: %q", string(data))
	}
}

// TestSessionPresetPickerSetsPreset pins the live header picker: it lists the
// registry, and choosing a preset persists the per-session selection.
func TestSessionPresetPickerSetsPreset(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	writePresetFile(t, h, "reviewer.json", `{"id":"reviewer","name":"Reviewer","description":"Read-only review"}`)
	if _, err := h.ctrl.ReloadPresets(); err != nil {
		t.Fatalf("reload presets: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Standard") {
		t.Fatalf("initial preset chip missing: %q", tt.Texts())
	}
	if err := tt.Click("Session preset, current: Standard"); err != nil {
		t.Fatalf("open preset picker: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Reviewer"); err != nil {
		t.Fatalf("choose Reviewer: %v", err)
	}
	tt.Frame()
	sessionID := h.ctrl.CurrentSessionID()
	if got := h.ctrl.SessionPresetID(sessionID); got != "reviewer" {
		t.Fatalf("session preset = %q, want reviewer", got)
	}
	if !tt.HasText("Reviewer") {
		t.Fatalf("chip did not show the chosen preset: %q", tt.Texts())
	}
}

// TestSessionPresetPickerFallsBackOnUnknown pins the stale-selection path: a
// session pointing at a deleted preset renders the fallback with a warning,
// and applying an unknown id reports ErrNotFound instead of silently ignoring
// it.
func TestSessionPresetPickerFallsBackOnUnknown(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	sessionID := h.ctrl.CurrentSessionID()
	if err := h.store.SetSetting("session.preset."+sessionID, "ghost"); err != nil {
		t.Fatalf("seed stale preset: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Standard") {
		t.Fatalf("fallback preset missing: %q", tt.Texts())
	}
	if !tt.HasText("Preset warning:") || !tt.HasText("ghost") {
		t.Fatalf("fallback warning missing: %q", tt.Texts())
	}
	if err := tt.Click("Session preset, current: Standard"); err != nil {
		t.Fatalf("open preset picker: %v", err)
	}
	tt.Frame()
	h.app.applySessionPreset(nil, "ghost")
	tt.Frame()
	if !tt.HasText("Preset not found") {
		t.Fatalf("unknown preset error missing: %q", tt.Texts())
	}
	if got := h.ctrl.SessionPresetID(sessionID); got != "ghost" && got != "" {
		t.Fatalf("session preset changed to %q", got)
	}
	_ = presets.PresetFallbackID
}

// contains is a tiny substring helper for the file assertions above.
func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestSessionPresetPickerShowsBuildWarnings pins the new controller seam: a
// preset whose build recorded warnings (for example an allowlist that matched
// no tool) shows them next to the picker instead of silently filtering tools.
func TestSessionPresetPickerShowsBuildWarnings(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	sessionID := h.ctrl.CurrentSessionID()
	h.ctrl.SetPresetWarnings(sessionID, []string{"preset reviewer matched no registered tools"})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Preset warning:") {
		t.Fatalf("build warning missing beside the picker: %q", tt.Texts())
	}
	if !tt.HasText("matched no registered tools") {
		t.Fatalf("build warning text missing: %q", tt.Texts())
	}
}

// TestPresetsPageReportsControllerLoadError pins the settings page seam: a
// reload failure recorded by the controller is shown even when the shell's own
// directory probe would no longer see the broken file.
func TestPresetsPageReportsControllerLoadError(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	writePresetFile(t, h, "broken.json", `{"id": }`)
	if _, err := h.ctrl.ReloadPresets(); err == nil {
		t.Fatal("reload with a broken file unexpectedly succeeded")
	}
	// Remove the broken file: the controller still remembers the failure until
	// the next successful reload, and the page must explain it.
	if err := os.Remove(filepath.Join(h.app.presetsDir(), "broken.json")); err != nil {
		t.Fatalf("remove broken preset: %v", err)
	}
	tt := ui.NewTester(h.app.SettingsView, 900, 840)
	tt.Frame()
	openSettingsPage(t, h, tt, "Agent presets")
	if !tt.HasText("Preset files ignored") {
		t.Fatalf("controller load error not shown: %q", tt.Texts())
	}
	if !tt.HasText("Preset files ignored") || !tt.HasText("Standard") {
		t.Fatalf("page lost its rows with a load error: %q", tt.Texts())
	}
}
