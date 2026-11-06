package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/presets"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// sameNameSet reports whether names contains exactly the given tool names.
func sameNameSet(names []string, want ...string) bool {
	if len(names) != len(want) {
		return false
	}
	got := make(map[string]bool, len(names))
	for _, name := range names {
		got[name] = true
	}
	for _, name := range want {
		if !got[name] {
			return false
		}
	}
	return true
}

// namedTool is a minimal runtime.Tool for the registry-filter unit tests.
type namedTool struct{ name string }

func (t namedTool) Name() string                                            { return t.name }
func (t namedTool) Description() string                                     { return t.name }
func (t namedTool) Invoke(context.Context, json.RawMessage) (string, error) { return "", nil }

// registryWithTools builds a small sealed-capable registry from tool names.
func registryWithTools(names ...string) *runtime.Registry {
	reg := runtime.NewRegistry()
	for _, name := range names {
		_ = reg.Register(namedTool{name: name})
	}
	return reg
}

// presetRegistryWith writes one user preset (without KnownTools validation) and
// loads a registry that still contains it; this simulates the "bypass Load
// validation" path the filter must defend against.
func presetRegistryWith(t *testing.T, presetID string, tools []string) *presets.Registry {
	t.Helper()
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"id":    presetID,
		"name":  "Filtered",
		"tools": tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, presetID+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := presets.Load(presets.Options{UserDir: dataDir})
	if err != nil {
		t.Fatalf("presets.Load: %v", err)
	}
	return reg
}

// TestFilterRegistryByPresetFailsClosedOnZeroMatch covers the P1 fail-open
// defect: a non-empty allowlist that matches no registered tool must yield an
// EMPTY registry plus a warning, never the full tool set.
func TestFilterRegistryByPresetFailsClosedOnZeroMatch(t *testing.T) {
	registry := presetRegistryWith(t, "typo-preset", []string{"reed_file", "grep_typo"})
	reg := registryWithTools("read_file", "grep", "bash")
	filtered, warning := filterRegistryByPreset(reg, registry, "typo-preset")
	if filtered == nil {
		t.Fatal("filterRegistryByPreset returned a nil registry")
	}
	if got := len(filtered.List()); got != 0 {
		t.Fatalf("zero-match allowlist filtered to %d tools, want 0 (fail-closed): %v", got, filtered.List())
	}
	if !strings.Contains(warning, "typo-preset") || !strings.Contains(warning, "no registered tool") {
		t.Fatalf("warning = %q, want the preset id and the no-match explanation", warning)
	}
}

// TestFilterRegistryByPresetNormalSubsetAndEmptyAllowlist pins the two valid
// cases: a matching subset keeps only its tools, an empty allowlist keeps all.
func TestFilterRegistryByPresetNormalSubsetAndEmptyAllowlist(t *testing.T) {
	reg := registryWithTools("read_file", "grep", "bash")
	subsetRegistry := presetRegistryWith(t, "reader", []string{"read_file", "grep"})
	subset, warning := filterRegistryByPreset(reg, subsetRegistry, "reader")
	if warning != "" {
		t.Fatalf("subset filter warning = %q, want none", warning)
	}
	names := make([]string, 0, len(subset.List()))
	for _, tool := range subset.List() {
		names = append(names, tool.Name())
	}
	if len(names) != 2 || !sameNameSet(names, "read_file", "grep") {
		t.Fatalf("subset tools = %v, want read_file+grep only", names)
	}

	allRegistry, err := presets.Load(presets.Options{})
	if err != nil {
		t.Fatalf("presets.Load: %v", err)
	}
	all, allWarning := filterRegistryByPreset(reg, allRegistry, "standard")
	if allWarning != "" {
		t.Fatalf("empty-allowlist filter warning = %q, want none", allWarning)
	}
	if got := len(all.List()); got != 3 {
		t.Fatalf("empty allowlist filtered to %d tools, want every registered tool (3)", got)
	}
}

// TestBuildAgentPresetZeroMatchFailsClosedAndWarns covers the app build path when
// a registry with a broken preset bypasses Load validation: the built agent has
// no tools (fail-closed) and the session exposes a warning instead of silently
// running with every tool.
func TestBuildAgentPresetZeroMatchFailsClosedAndWarns(t *testing.T) {
	dataDir := t.TempDir()
	writeHeadlessStore(t, dataDir, "http://127.0.0.1:1/v1", nil)
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"typo-preset","name":"Typo","tools":["reed_file"]}`
	if err := os.WriteFile(filepath.Join(dir, "typo-preset.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	broker := state.NewBroker()
	ctrl, err := session.New(session.Config{Store: st, Broker: broker})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		broker.Close()
		_ = st.Close()
	})
	a := &app{services: ctrl}
	a.installSessionAgentFactory(ctrl)
	ctx := context.Background()
	sessionID, err := ctrl.CreateSession(ctx, "broken preset")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.SetSessionPreset(sessionID, "typo-preset"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	agent, err := a.buildSessionAgent(sessionID)
	if err != nil || agent == nil {
		t.Fatalf("buildSessionAgent = %+v, %v", agent, err)
	}
	if tools := agent.ToolNames(); len(tools) != 0 {
		t.Fatalf("zero-match preset built with %d tools (must fail closed): %v", len(tools), tools)
	}
	warnings := ctrl.PresetWarnings(sessionID)
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, " "), "typo-preset") {
		t.Fatalf("session warnings = %v, want a visible typo-preset warning", warnings)
	}
}

// TestBuildServicesRejectsBrokenUserPreset covers the app-level validation path:
// BuildServicesWith installs the built registry's tool names as KnownTools and
// reloads, so a misspelled allowlist fails closed and stays out of the catalog.
func TestBuildServicesRejectsBrokenUserPreset(t *testing.T) {
	dataDir := t.TempDir()
	writeHeadlessStore(t, dataDir, "http://127.0.0.1:1/v1", nil)
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"broken","name":"Broken","tools":["reed_file"]}`
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ctrl, cleanup, err := BuildServicesWith(dataDir, func(st *store.Store) (*AgentBundle, error) {
		return buildAgentWithOptions(st, dataDir, DefaultInstruction, registryOptions{})
	})
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		if cleanup != nil {
			_ = cleanup()
		}
	})
	if err := ctrl.LastPresetLoadError(); !errors.Is(err, presets.ErrUnknownTool) {
		t.Fatalf("LastPresetLoadError = %v, want presets.ErrUnknownTool", err)
	}
	if _, ok := ctrl.Presets().Get("broken"); ok {
		t.Fatal("broken user preset became active; the validated reload must fail closed")
	}
}
