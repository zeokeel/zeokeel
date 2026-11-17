package toolkit

// Tests for the todo read model (contracts.KeyTodo) and its wiring: the toolkit
// keeps the rig todo store it hands to todo_write, so the session UI can show
// the agent's current list. The store is process-level in this build (one list
// for the whole product), which the contract documents instead of faking
// per-session scoping.

import (
	"context"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
)

// activateTodoToolkit activates the toolkit and returns the tool registry and
// the provided todo read model.
func activateTodoToolkit(t *testing.T) (*rigtool.Registry, contracts.TodoService) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: map[string]any{"root": "."},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(hosttools.KeyRegistry)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	todo, err := root.Require(contracts.KeyTodo)
	if err != nil {
		t.Fatalf("require KeyTodo: %v", err)
	}
	return tools, todo
}

// writeTodos invokes the model-facing todo_write tool through the registry.
func writeTodos(t *testing.T, tools *rigtool.Registry, arguments string) {
	t.Helper()
	result, err := tools.Invoke(context.Background(), rig.ToolCall{
		ID: "call-todo", Name: "todo_write", Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("todo_write: %v", err)
	}
	if result.IsError {
		t.Fatalf("todo_write result = %q", result.Content)
	}
}

// TestTodoServiceSnapshotsTheAgentList covers the wiring end to end: the module
// provides an empty todo service, todo_write replaces the list, and the read
// model exposes the normalized snapshot (status spellings, generated IDs, order).
func TestTodoServiceSnapshotsTheAgentList(t *testing.T) {
	tools, todo := activateTodoToolkit(t)
	if todo == nil {
		t.Fatal("todo service is nil")
	}
	if got := todo.Todos(); len(got) != 0 {
		t.Fatalf("fresh todo service = %+v, want no items", got)
	}

	writeTodos(t, tools, `{"todos":[
		{"content":" 修复构建 ","status":"in_progress"},
		{"content":"补测试","status":"todo"},
		{"content":"合并发布","status":"done"}
	]}`)
	got := todo.Todos()
	if len(got) != 3 {
		t.Fatalf("Todos() = %+v, want 3 items", got)
	}
	wantStatuses := []string{contracts.TodoStatusInProgress, contracts.TodoStatusPending, contracts.TodoStatusCompleted}
	for index, want := range wantStatuses {
		if got[index].Status != want {
			t.Fatalf("item %d status = %q, want %q", index, got[index].Status, want)
		}
		if got[index].ID == "" {
			t.Fatalf("item %d has no id: %+v", index, got[index])
		}
	}
	if got[0].Content != "修复构建" {
		t.Fatalf("item 0 content = %q, want the trimmed content", got[0].Content)
	}
	// The snapshot is a copy: mutating it must not reach the store.
	got[0].Content = "mutated"
	if again := todo.Todos(); again[0].Content != "修复构建" {
		t.Fatalf("Todos() exposes the store's slice: %+v", again)
	}

	// An empty array clears the list; the service stays provided (the UI hides
	// the dock on the empty snapshot).
	writeTodos(t, tools, `{"todos":[]}`)
	if got := todo.Todos(); len(got) != 0 {
		t.Fatalf("Todos() after clearing = %+v, want none", got)
	}
}
