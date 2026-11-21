package agent

// Lazy tool discovery: the request carries the core surface, tool_search lists
// the rest and activation reaches the live run (and the following runs of the
// same session).

import (
	"context"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// discoveryStubTool is a registry tool with a controllable description.
type discoveryStubTool struct {
	name        string
	description string
}

func (t *discoveryStubTool) Name() string           { return t.name }
func (t *discoveryStubTool) Description() string    { return t.description }
func (t *discoveryStubTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *discoveryStubTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}

// fakeActivator records activations and reports them as activated.
type fakeActivator struct {
	sessionID string
	activated []string
}

func (f *fakeActivator) ActivateTools(sessionID string, names ...string) ([]string, []string) {
	f.sessionID = sessionID
	f.activated = append(f.activated, names...)
	return names, nil
}

// ActiveTools completes the rig.ToolActivator contract.
func (f *fakeActivator) ActiveTools(string) []string { return append([]string(nil), f.activated...) }

func newDiscoveryService(t *testing.T) (*service, *rigtools.Registry, string) {
	t.Helper()
	registry := rigtools.NewRegistry(
		&discoveryStubTool{name: "read_file", description: "Read one file."},
		&discoveryStubTool{name: "bash", description: "Run a command."},
		&discoveryStubTool{name: "workflow", description: "Run a JavaScript workflow."},
		&discoveryStubTool{name: "lsp_definition", description: "Go to a symbol definition."},
		&discoveryStubTool{name: "custom_tool", description: "A host-registered tool."},
		&discoveryStubTool{name: "enter_worktree", description: "Create a git worktree."},
	)
	settings := newFakeSettings(nil)
	svc := newService(Config{}, registry, contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	if err := svc.registerDiscoveryTool(svc.newDiscoveryTool()); err != nil {
		t.Fatalf("register tool_search: %v", err)
	}
	sessionID, err := svc.NewSession("discovery")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, registry, sessionID
}

// TestDiscoveryInitialActiveKeepsTheCoreSurface pins the starting set: frequent
// coding tools are active, the long tail is not.
func TestDiscoveryInitialActiveKeepsTheCoreSurface(t *testing.T) {
	svc, _, sessionID := newDiscoveryService(t)
	active := svc.initialActiveTools(sessionID)
	set := make(map[string]bool, len(active))
	for _, name := range active {
		set[name] = true
	}
	for _, want := range []string{"read_file", "bash", "tool_search", "workflow"} {
		if !set[want] {
			t.Errorf("core tool %q is not initially active", want)
		}
	}
	for _, hidden := range []string{"lsp_definition", "enter_worktree"} {
		if set[hidden] {
			t.Errorf("long-tail tool %q is active without discovery", hidden)
		}
	}
	// A tool the discovery list does not know (custom host/MCP registration)
	// stays active: discovery must never silently hide an unknown tool.
	if !set["custom_tool"] {
		t.Error("unknown custom tool was not active")
	}
}

// TestDiscoveryListsOnlyHiddenTools pins the list: active tools are absent, the
// hidden ones carry a one-line summary.
func TestDiscoveryListsOnlyHiddenTools(t *testing.T) {
	svc, _, sessionID := newDiscoveryService(t)
	items := svc.discoverableToolsFor(sessionID)
	names := make(map[string]string, len(items))
	for _, item := range items {
		names[item.Name] = item.Summary
	}
	if _, ok := names["read_file"]; ok {
		t.Fatal("active tool listed as discoverable")
	}
	if _, ok := names["tool_search"]; ok {
		t.Fatal("tool_search listed as discoverable")
	}
	if names["lsp_definition"] != "Go to a symbol definition" {
		t.Fatalf("lsp_definition summary = %q, want the first sentence", names["lsp_definition"])
	}
	if names["enter_worktree"] == "" {
		t.Fatal("enter_worktree missing from the discoverable list")
	}
}

// TestDiscoveryActivationReachesTheRun pins activation: the live runner is asked,
// the names are remembered for the session, and they disappear from the list.
func TestDiscoveryActivationReachesTheRun(t *testing.T) {
	svc, _, sessionID := newDiscoveryService(t)
	activator := &fakeActivator{}
	svc.bindSessionRunner(sessionID, activator)

	if runner := svc.sessionRunner(sessionID); runner == nil {
		t.Fatal("sessionRunner is nil right after bind")
	}
	activated, unknown := svc.activateSessionToolsFor(sessionID, []string{"lsp_definition"})
	if len(activated) != 1 || activated[0] != "lsp_definition" || len(unknown) != 0 {
		t.Fatalf("activation = %v/%v, want lsp_definition", activated, unknown)
	}
	if activator.sessionID != sessionID {
		t.Fatalf("activation session = %q, want %q", activator.sessionID, sessionID)
	}
	if active := svc.initialActiveTools(sessionID); !discoveryHasName(active, "lsp_definition") {
		t.Fatalf("activation not remembered: %v", active)
	}
	for _, item := range svc.discoverableToolsFor(sessionID) {
		if item.Name == "lsp_definition" {
			t.Fatal("activated tool still listed as discoverable")
		}
	}
}

// TestDiscoveryWithoutLiveRunReportsUnknown pins the outside-a-run case: no
// runner means nothing can be activated (the model gets a readable unknown list).
func TestDiscoveryWithoutLiveRunReportsUnknown(t *testing.T) {
	svc, _, sessionID := newDiscoveryService(t)
	activated, unknown := svc.activateSessionToolsFor(sessionID, []string{"lsp_definition"})
	if len(activated) != 0 || len(unknown) != 1 {
		t.Fatalf("activation outside a run = %v/%v, want unknown", activated, unknown)
	}
}

// TestDiscoveryPromptGuidanceFollowsTheActiveSurface pins that the prompt does
// not describe tools the request does not carry.
func TestDiscoveryPromptGuidanceFollowsTheActiveSurface(t *testing.T) {
	svc, registry, sessionID := newDiscoveryService(t)
	active := svc.activeToolSet(sessionID, registry.Tools())
	tools := activeRunTools(registry.Tools(), active)
	for _, tool := range tools {
		if tool.Name() == "enter_worktree" || tool.Name() == "lsp_definition" {
			t.Fatalf("hidden tool %q kept in the prompt surface", tool.Name())
		}
	}
	names := toolNameSet(tools)
	if names["enter_worktree"] {
		t.Fatal("prompt tool set claims enter_worktree is visible")
	}
	if !names["read_file"] {
		t.Fatal("prompt tool set lost read_file")
	}
}

func discoveryHasName(names []string, want string) bool {
	for _, name := range names {
		if strings.TrimSpace(name) == want {
			return true
		}
	}
	return false
}
