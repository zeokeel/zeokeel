package app

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// buildForPermission builds the production registry for one access mode.
func buildForPermission(t *testing.T, permission string) (*runtime.Registry, string) {
	t.Helper()
	st, dir := newToolStore(t)
	reg, closeFn, err := registerAllToolsWithOptions(st, dir, func(k, e, d string) string { return d },
		registryOptions{PermissionOverride: permission})
	if err != nil {
		t.Fatalf("registerAllToolsWithOptions(%s): %v", permission, err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	return reg, dir
}

func toolNameSet(reg *runtime.Registry) map[string]bool {
	names := make(map[string]bool)
	for _, tool := range reg.List() {
		names[tool.Name()] = true
	}
	return names
}

// TestReadOnlyDropsPersistentSideEffectTools is the RT-03 regression: a Read
// Only build must not expose tools that persist state which later turns or
// future sessions act on (automations, workspace memories, goals), while the
// read-only variants and ordinary planning tools stay available.
func TestReadOnlyDropsPersistentSideEffectTools(t *testing.T) {
	reg, _ := buildForPermission(t, session.PermissionAsk)
	readOnly := toolNameSet(reg)
	for _, gone := range []string{
		"memory",          // notes are injected into every later turn's prompt
		"goal",            // goal driver submits automatic continuation turns
		"schedule_create", // persistent automation running a future session
		"schedule_update",
		"schedule_delete",
	} {
		if readOnly[gone] {
			t.Errorf("Read Only registered persistent side-effect tool %q", gone)
		}
	}
	for _, kept := range []string{"schedule_list", "todo", "session_search", "ask_user", "present"} {
		if !readOnly[kept] {
			t.Errorf("Read Only lost non-mutating tool %q", kept)
		}
	}

	// Control: the default Workspace Write build keeps the tools, so the gate
	// is about Read Only, not about removing the features wholesale.
	reg, _ = buildForPermission(t, session.PermissionWorkspace)
	workspace := toolNameSet(reg)
	for _, want := range []string{"memory", "goal", "schedule_create", "schedule_update", "schedule_delete", "schedule_list"} {
		if !workspace[want] {
			t.Errorf("Workspace Write lost tool %q", want)
		}
	}
}

// TestReadOnlyPromptMatchesToolSet proves the prompt policy agrees with the
// converged Read Only registry: it must not tell the model to use a goal,
// memory or schedule tool the session does not have.
func TestReadOnlyPromptMatchesToolSet(t *testing.T) {
	st, dir := newToolStore(t)
	reg, closeFn, err := registerAllToolsWithOptions(st, dir, func(k, e, d string) string { return d },
		registryOptions{PermissionOverride: session.PermissionAsk})
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	out := composeInstruction(st, dir, reg, "", session.PermissionAsk, "")
	for _, unwanted := range []string{"track a multi-step objective", "Use the goal tool", "schedule_create"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(unwanted)) {
			t.Errorf("Read Only prompt advertises an omitted tool: %q\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "read-only") {
		t.Errorf("Read Only prompt does not state the read-only policy:\n%s", out)
	}
}
