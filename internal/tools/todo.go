package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// TodoStatus is one task's state in the agent's todo list.
type TodoStatus string

const (
	// TodoPending is a task that has not been started.
	TodoPending TodoStatus = "pending"
	// TodoInProgress is a task the agent is working on.
	TodoInProgress TodoStatus = "in_progress"
	// TodoCompleted is a finished task.
	TodoCompleted TodoStatus = "completed"
)

// Todo is one task in the agent's plan. IDs are assigned by the tool and are
// stable until clear; they are what the update and complete actions address.
type Todo struct {
	ID     int        `json:"id"`
	Task   string     `json:"task"`
	Status TodoStatus `json:"status"`
}

// TodoResult is the JSON document TodoTool returns for every invocation. The
// controller folds a successful end event whose result has Kind "todo" back
// into the session's todo list, so the UI and the model share one source of
// truth without any global state.
type TodoResult struct {
	Kind    string `json:"kind"`
	Action  string `json:"action"`
	Tasks   []Todo `json:"tasks"`
	Message string `json:"message,omitempty"`
}

const todoKind = "todo"

// todoDescription documents the tool and its JSON argument schema. The runtime
// exposes the description to the model; eino's tool info has no separate
// schema field here, so the schema is part of the prose.
const todoDescription = `Manage the agent's task list for this session.

Call it with a JSON object:
  {"action":"add","task":"Write the tests","status":"pending"}
  {"action":"list"}
  {"action":"update","id":2,"task":"New text","status":"in_progress"}
  {"action":"complete","id":1}
  {"action":"clear"}

"action" is required and is one of add, list, update, complete, clear.
"task" is required for add and optional for update.
"status" is optional on add/update and is one of pending, in_progress, completed.
"id" is required for update/complete. IDs are returned by every action.

The tool returns a JSON document {"kind":"todo","action":...,"tasks":[...],
"message":...}; tasks keeps insertion order. Use list before updating so the
IDs are current. The UI shows this list beside the transcript.`

// TodoStateStore persists one session's todo list across tool rebuilds. The
// app backs it with the settings store, so switching models, toggling a plugin
// or changing the workspace does not silently reset the model's plan.
type TodoStateStore interface {
	LoadTodos(sessionID string) ([]Todo, error)
	SaveTodos(sessionID string, tasks []Todo) error
}

// TodoTool is the per-agent todo list. All state lives in this instance,
// guarded by mu, so two agents (or two test runs) never share tasks. There are
// no package-level variables. When a TodoStateStore is supplied, the list is
// lazily hydrated on first use and written back after every mutation.
type TodoTool struct {
	mu        sync.Mutex
	sessions  map[string]*todoSessionState
	loaded    map[string]bool
	persister TodoStateStore
}

// todoSessionState is one session's task list. The tool is a single registry
// entry shared by every session, so state must be keyed by the session the
// runtime passes through the invocation context; otherwise a task added in
// session A leaks into session B.
type todoSessionState struct {
	next  int
	tasks []Todo
}

// NewTodoTool returns an empty todo tool. Task IDs start at 1.
func NewTodoTool() *TodoTool {
	return &TodoTool{sessions: make(map[string]*todoSessionState)}
}

// NewTodoToolWithStore returns a todo tool that hydrates and saves its
// per-session list through store. It is what production registers, so a
// registry rebuild does not lose a plan that the UI still shows.
func NewTodoToolWithStore(store TodoStateStore) *TodoTool {
	return &TodoTool{sessions: make(map[string]*todoSessionState), persister: store}
}

// todoSessionKey names the bucket used by direct invocations (background
// context, tests) that have no session id.
func todoSessionKey(ctx context.Context) string {
	if id := strings.TrimSpace(runtime.SessionIDFromContext(ctx)); id != "" {
		return id
	}
	return ""
}

// stateLocked returns (creating on first use) the list for sessionID. t.mu
// must be held. A configured persister is consulted once per session.
func (t *TodoTool) stateLocked(sessionID string) *todoSessionState {
	if t.sessions == nil {
		t.sessions = make(map[string]*todoSessionState)
	}
	st := t.sessions[sessionID]
	if st == nil {
		st = &todoSessionState{next: 1}
		if t.loaded == nil {
			t.loaded = make(map[string]bool)
		}
		if t.persister != nil && !t.loaded[sessionID] {
			t.loaded[sessionID] = true
			if tasks, err := t.persister.LoadTodos(sessionID); err == nil && len(tasks) > 0 {
				st.tasks = cloneTodos(tasks)
				for _, task := range st.tasks {
					if task.ID >= st.next {
						st.next = task.ID + 1
					}
				}
			}
		}
		t.sessions[sessionID] = st
	}
	if st.next < 1 {
		st.next = 1
	}
	return st
}

// InteractiveTools returns the tools the live session has dedicated UI for:
// todo (the task panel), ask_user (the question card) and present (the
// artifact card). Each call returns tools with fresh per-agent state. Callers
// that build a runtime.Registry should register these alongside Builtins.
func InteractiveTools() []runtime.Tool {
	return []runtime.Tool{NewTodoTool(), NewAskUserTool(), NewPresentTool()}
}

// Name implements runtime.Tool.
func (t *TodoTool) Name() string { return "todo" }

// Description implements runtime.Tool.
func (t *TodoTool) Description() string { return todoDescription }

// todoArgs is the decoded argument object. One struct covers every action so
// the model can omit fields that do not apply.
type todoArgs struct {
	Action string `json:"action"`
	ID     int    `json:"id,omitempty"`
	Task   string `json:"task,omitempty"`
	Status string `json:"status,omitempty"`
}

// Invoke implements runtime.Tool. It returns a TodoResult JSON document and
// never panics on bad arguments; a non-nil error is reported to the model as a
// tool failure.
func (t *TodoTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in todoArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("todo: invalid arguments: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		return "", errors.New(`todo: "action" is required (add, list, update, complete, clear)`)
	}

	sessionID := todoSessionKey(ctx)
	t.mu.Lock()
	st := t.stateLocked(sessionID)
	var message string
	switch action {
	case "add":
		task := strings.TrimSpace(in.Task)
		if task == "" {
			t.mu.Unlock()
			return "", errors.New(`todo: "task" is required for add`)
		}
		status, err := parseTodoStatus(in.Status, TodoPending)
		if err != nil {
			t.mu.Unlock()
			return "", err
		}
		todo := Todo{ID: st.next, Task: task, Status: status}
		st.next++
		st.tasks = append(st.tasks, todo)
		message = fmt.Sprintf("added task %d", todo.ID)
	case "list":
		message = fmt.Sprintf("%d task(s)", len(st.tasks))
	case "update":
		if in.ID <= 0 {
			t.mu.Unlock()
			return "", errors.New(`todo: "id" is required for update`)
		}
		task := strings.TrimSpace(in.Task)
		statusSet := strings.TrimSpace(in.Status) != ""
		if task == "" && !statusSet {
			t.mu.Unlock()
			return "", errors.New(`todo: update needs "task" or "status"`)
		}
		idx := st.index(in.ID)
		if idx < 0 {
			t.mu.Unlock()
			return "", fmt.Errorf("todo: no task with id %d", in.ID)
		}
		if task != "" {
			st.tasks[idx].Task = task
		}
		if statusSet {
			status, err := parseTodoStatus(in.Status, TodoPending)
			if err != nil {
				t.mu.Unlock()
				return "", err
			}
			st.tasks[idx].Status = status
		}
		message = fmt.Sprintf("updated task %d", in.ID)
	case "complete":
		if in.ID <= 0 {
			t.mu.Unlock()
			return "", errors.New(`todo: "id" is required for complete`)
		}
		idx := st.index(in.ID)
		if idx < 0 {
			t.mu.Unlock()
			return "", fmt.Errorf("todo: no task with id %d", in.ID)
		}
		st.tasks[idx].Status = TodoCompleted
		message = fmt.Sprintf("completed task %d", in.ID)
	case "clear":
		st.tasks = nil
		st.next = 1
		message = "cleared all tasks"
	default:
		t.mu.Unlock()
		return "", fmt.Errorf("todo: unknown action %q (want add, list, update, complete, clear)", in.Action)
	}
	tasks := cloneTodos(st.tasks)
	if t.persister != nil && action != "list" {
		// Best effort: losing the mirror must not fail the model's action;
		// the controller still holds the authoritative in-memory copy for
		// the current registry.
		_ = t.persister.SaveTodos(sessionID, tasks)
	}
	t.mu.Unlock()

	out, err := json.Marshal(TodoResult{
		Kind:    todoKind,
		Action:  action,
		Tasks:   tasks,
		Message: message,
	})
	if err != nil {
		return "", fmt.Errorf("todo: encode result: %w", err)
	}
	return string(out), nil
}

// Tasks returns a copy of the current list. The view uses it only through the
// controller; it exists so tests and future UI wiring can read the tool
// directly without racing Invoke.
func (t *TodoTool) Tasks() []Todo {
	return t.TasksFor("")
}

// TasksFor returns a copy of one session's list. The live shell reads through
// the controller; this is the regression-test seam for session isolation.
func (t *TodoTool) TasksFor(sessionID string) []Todo {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sessions[sessionID]
	if st == nil {
		return []Todo{}
	}
	return cloneTodos(st.tasks)
}

// index returns the position of the task with id, or -1.
func (st *todoSessionState) index(id int) int {
	for i := range st.tasks {
		if st.tasks[i].ID == id {
			return i
		}
	}
	return -1
}

// parseTodoStatus normalizes an optional status string. Empty means def.
func parseTodoStatus(s string, def TodoStatus) (TodoStatus, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return def, nil
	}
	switch TodoStatus(s) {
	case TodoPending, TodoInProgress, TodoCompleted:
		return TodoStatus(s), nil
	default:
		return "", fmt.Errorf("todo: unknown status %q (want pending, in_progress, completed)", s)
	}
}

// cloneTodos copies a task slice so callers cannot mutate tool state through a
// returned value.
func cloneTodos(tasks []Todo) []Todo {
	if len(tasks) == 0 {
		return []Todo{}
	}
	out := make([]Todo, len(tasks))
	copy(out, tasks)
	return out
}
