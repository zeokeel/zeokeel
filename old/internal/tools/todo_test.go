package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// decodeTodoResult decodes a todo tool result document.
func decodeTodoResult(t *testing.T, raw string) TodoResult {
	t.Helper()
	var out TodoResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("todo result is not JSON: %v\n%s", err, raw)
	}
	return out
}

func TestTodoToolAddListCompleteClear(t *testing.T) {
	tool := NewTodoTool()
	if got := tool.Name(); got != "todo" {
		t.Fatalf("Name = %q, want todo", got)
	}

	add := decodeTodoResult(t, mustInvoke(t, tool, map[string]any{"action": "add", "task": "write tests"}))
	if add.Kind != "todo" || add.Action != "add" {
		t.Fatalf("add result = %+v", add)
	}
	if len(add.Tasks) != 1 || add.Tasks[0].ID != 1 || add.Tasks[0].Task != "write tests" || add.Tasks[0].Status != TodoPending {
		t.Fatalf("tasks after add = %+v", add.Tasks)
	}

	decodeTodoResult(t, mustInvoke(t, tool, map[string]any{"action": "add", "task": "ship it", "status": "in_progress"}))
	list := decodeTodoResult(t, mustInvoke(t, tool, map[string]any{"action": "list"}))
	if len(list.Tasks) != 2 {
		t.Fatalf("list returned %d tasks, want 2", len(list.Tasks))
	}
	if list.Tasks[1].ID != 2 || list.Tasks[1].Status != TodoInProgress {
		t.Fatalf("second task = %+v", list.Tasks[1])
	}

	complete := decodeTodoResult(t, mustInvoke(t, tool, map[string]any{"action": "complete", "id": 1}))
	if complete.Tasks[0].Status != TodoCompleted {
		t.Fatalf("task 1 status = %q, want completed", complete.Tasks[0].Status)
	}

	update := decodeTodoResult(t, mustInvoke(t, tool, map[string]any{
		"action": "update", "id": 2, "task": "ship it twice", "status": "completed",
	}))
	if update.Tasks[1].Task != "ship it twice" || update.Tasks[1].Status != TodoCompleted {
		t.Fatalf("task 2 after update = %+v", update.Tasks[1])
	}

	clear := decodeTodoResult(t, mustInvoke(t, tool, map[string]any{"action": "clear"}))
	if len(clear.Tasks) != 0 {
		t.Fatalf("tasks after clear = %+v, want empty", clear.Tasks)
	}
	if got := tool.Tasks(); len(got) != 0 {
		t.Fatalf("Tasks() after clear = %+v, want empty", got)
	}

	// IDs restart after clear so a fresh plan reads 1..n again.
	again := decodeTodoResult(t, mustInvoke(t, tool, map[string]any{"action": "add", "task": "new plan"}))
	if again.Tasks[0].ID != 1 {
		t.Fatalf("first id after clear = %d, want 1", again.Tasks[0].ID)
	}
}

func TestTodoToolRejectsBadArguments(t *testing.T) {
	tool := NewTodoTool()
	cases := []map[string]any{
		{},
		{"action": "nope"},
		{"action": "add"},
		{"action": "add", "task": "x", "status": "sideways"},
		{"action": "update", "task": "x"},
		{"action": "update", "id": 9, "task": "x"},
		{"action": "complete"},
		{"action": "complete", "id": 9},
	}
	for _, args := range cases {
		if out, err := invokeTool(t, tool, args); err == nil {
			t.Fatalf("args %v: err = nil, out = %q", args, out)
		}
	}
	if out, err := tool.Invoke(context.Background(), json.RawMessage("{")); err == nil {
		t.Fatalf("invalid JSON: err = nil, out = %q", out)
	}
}

func TestTodoToolTasksAreIsolatedSnapshots(t *testing.T) {
	tool := NewTodoTool()
	decodeTodoResult(t, mustInvoke(t, tool, map[string]any{"action": "add", "task": "one"}))

	tasks := tool.Tasks()
	tasks[0].Task = "mutated"
	if got := tool.Tasks()[0].Task; got != "one" {
		t.Fatalf("internal task changed through Tasks(): %q", got)
	}

	raw := mustArgs(t, map[string]any{"action": "add", "task": "parallel"})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tool.Invoke(context.Background(), raw); err != nil {
				t.Errorf("parallel add: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := len(tool.Tasks()); got != 17 {
		t.Fatalf("tasks after parallel adds = %d, want 17", got)
	}
}

func TestInteractiveTools(t *testing.T) {
	toolset := InteractiveTools()
	names := make([]string, 0, len(toolset))
	for _, tool := range toolset {
		names = append(names, tool.Name())
	}
	want := []string{"todo", "ask_user", "present"}
	if len(names) != len(want) {
		t.Fatalf("InteractiveTools = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("InteractiveTools = %v, want %v", names, want)
		}
	}
}

// TestTodoToolIsolatesSessions proves one shared tool instance keeps a
// separate list per session instead of leaking session A's tasks into B.
func TestTodoToolIsolatesSessions(t *testing.T) {
	tool := NewTodoTool()
	ctxA := runtime.WithSessionID(context.Background(), "session-a")
	ctxB := runtime.WithSessionID(context.Background(), "session-b")

	raw, err := tool.Invoke(ctxA, mustArgs(t, map[string]any{"action": "add", "task": "A-task"}))
	if err != nil {
		t.Fatalf("add in A: %v", err)
	}
	if got := decodeTodoResult(t, raw); len(got.Tasks) != 1 || got.Tasks[0].Task != "A-task" {
		t.Fatalf("A tasks = %+v", got.Tasks)
	}

	raw, err = tool.Invoke(ctxB, mustArgs(t, map[string]any{"action": "list"}))
	if err != nil {
		t.Fatalf("list in B: %v", err)
	}
	if got := decodeTodoResult(t, raw); len(got.Tasks) != 0 {
		t.Fatalf("session B saw session A's tasks: %+v", got.Tasks)
	}
	if got := tool.TasksFor("session-b"); len(got) != 0 {
		t.Fatalf("TasksFor(B) = %+v, want empty", got)
	}
	if got := tool.TasksFor("session-a"); len(got) != 1 || got[0].Task != "A-task" {
		t.Fatalf("TasksFor(A) = %+v", got)
	}

	if _, err := tool.Invoke(ctxB, mustArgs(t, map[string]any{"action": "add", "task": "B-task"})); err != nil {
		t.Fatalf("add in B: %v", err)
	}
	if got := tool.TasksFor("session-a"); len(got) != 1 || got[0].Task != "A-task" {
		t.Fatalf("A changed after B add: %+v", got)
	}
	if got := tool.TasksFor("session-b"); len(got) != 1 || got[0].Task != "B-task" {
		t.Fatalf("B tasks = %+v", got)
	}
}
