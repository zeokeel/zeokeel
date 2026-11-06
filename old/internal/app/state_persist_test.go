package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

func invokeByName(t *testing.T, reg *runtime.Registry, ctx context.Context, name string, args any) (string, error) {
	t.Helper()
	for _, tool := range reg.List() {
		if tool.Name() != name {
			continue
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		return tool.Invoke(ctx, raw)
	}
	t.Fatalf("tool %q is not registered", name)
	return "", nil
}

// TestTodoAndGoalSurviveToolRebuild is the regression for instance-local tool
// state: a rebuild (model switch, plugin toggle, workspace switch, permission
// chip) used to replace the todo/goal tools with empty ones while the UI still
// showed the old list and objective.
func TestTodoAndGoalSurviveToolRebuild(t *testing.T) {
	st, dir := newToolStore(t)
	get := func(key, env, def string) string { return def }

	ctx := runtime.WithSessionID(context.Background(), "persist-session")

	reg1, close1, err := registerAllTools(st, dir, get)
	if err != nil {
		t.Fatalf("register #1: %v", err)
	}
	if close1 != nil {
		t.Cleanup(func() { _ = close1() })
	}
	if _, err := invokeByName(t, reg1, ctx, "todo", map[string]any{"action": "add", "task": "ship it"}); err != nil {
		t.Fatalf("todo add: %v", err)
	}
	if _, err := invokeByName(t, reg1, ctx, "goal", map[string]any{"action": "set", "objective": "ship the release"}); err != nil {
		t.Fatalf("goal set: %v", err)
	}

	reg2, close2, err := registerAllTools(st, dir, get)
	if err != nil {
		t.Fatalf("register #2: %v", err)
	}
	if close2 != nil {
		t.Cleanup(func() { _ = close2() })
	}
	out, err := invokeByName(t, reg2, ctx, "todo", map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("todo list after rebuild: %v", err)
	}
	if !strings.Contains(out, "ship it") {
		t.Fatalf("todo list after rebuild = %s", out)
	}
	out, err = invokeByName(t, reg2, ctx, "goal", map[string]any{"action": "get"})
	if err != nil {
		t.Fatalf("goal get after rebuild: %v", err)
	}
	if !strings.Contains(out, "ship the release") {
		t.Fatalf("goal get after rebuild = %s", out)
	}
	// The rebuilt goal tool must also be able to complete the restored goal.
	if _, err := invokeByName(t, reg2, ctx, "goal", map[string]any{"action": "complete"}); err != nil {
		t.Fatalf("goal complete after rebuild: %v", err)
	}
}
