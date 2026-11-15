package contracts

// Tests for the todo read model contract: the status vocabulary, the service key
// the toolkit provides and the session dock consumes, and the read-only
// interface shape.

import (
	"testing"
)

// stubTodoService is a minimal contracts.TodoService, pinning the interface a
// future per-session store must keep.
type stubTodoService struct{ items []TodoItem }

func (s stubTodoService) Todos() []TodoItem { return s.items }

var _ TodoService = stubTodoService{}

func TestTodoStatusVocabulary(t *testing.T) {
	statuses := []string{TodoStatusPending, TodoStatusInProgress, TodoStatusCompleted}
	seen := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		if status == "" || seen[status] {
			t.Fatalf("todo statuses must be distinct and non-empty: %v", statuses)
		}
		seen[status] = true
	}
}

func TestTodoServiceKey(t *testing.T) {
	if got := KeyTodo.Name(); got != "zeokeel/todo" {
		t.Fatalf("KeyTodo.Name() = %q, want zeokeel/todo", got)
	}
	// The interface is a plain snapshot read: a per-session store later only has
	// to return its own list.
	var service TodoService = stubTodoService{items: []TodoItem{{ID: "t1", Content: "做", Status: TodoStatusPending}}}
	if got := service.Todos(); len(got) != 1 || got[0].Status != TodoStatusPending {
		t.Fatalf("Todos() = %+v", got)
	}
}
