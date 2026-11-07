package uiroot

// Host goal bridge: Set/Get/Clear with one handler call per transition.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type goalEvent struct {
	goal contracts.Goal
	ok   bool
}

func TestGoalSetGetClear(t *testing.T) {
	root := newTestRoot(t)
	service := root.GoalTracker()
	events := make(chan goalEvent, 8)
	service.SetHandler(func(goal contracts.Goal, ok bool) { events <- goalEvent{goal: goal, ok: ok} })

	if _, err := service.Set(context.Background(), "   "); err == nil {
		t.Fatal("Set(empty text) returned nil error")
	}
	if id, text, status, ok := service.Get(); ok || id != "" || text != "" || status != "" {
		t.Fatalf("empty Get = %q/%q/%q/%v", id, text, status, ok)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Set(cancelled, "goal"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set(cancelled ctx) = %v, want context.Canceled", err)
	}

	id, err := service.Set(context.Background(), "  Ship v0.5  ")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if id != "goal-1" {
		t.Fatalf("first id = %q, want goal-1", id)
	}
	gotID, gotText, gotStatus, ok := service.Get()
	if !ok || gotID != id || gotText != "Ship v0.5" || gotStatus != "active" {
		t.Fatalf("Get = %q/%q/%q/%v", gotID, gotText, gotStatus, ok)
	}
	event := <-events
	if !event.ok || event.goal.ID != id || event.goal.Text != "Ship v0.5" || event.goal.Status != "active" {
		t.Fatalf("set event = %+v", event)
	}

	// Replacing a goal allocates a new ID and notifies with the new text.
	second, err := service.Set(context.Background(), "Ship v0.6")
	if err != nil {
		t.Fatalf("Set second: %v", err)
	}
	if second == id || second != "goal-2" {
		t.Fatalf("second id = %q, want a fresh goal-2", second)
	}
	event = <-events
	if !event.ok || event.goal.ID != second || event.goal.Text != "Ship v0.6" {
		t.Fatalf("replace event = %+v", event)
	}

	if !service.Clear() {
		t.Fatal("Clear existing goal = false")
	}
	event = <-events
	if event.ok || event.goal.ID != "" || event.goal.Text != "" {
		t.Fatalf("clear event = %+v", event)
	}
	if _, _, _, ok := service.Get(); ok {
		t.Fatal("goal survived Clear")
	}
	if service.Clear() {
		t.Fatal("second Clear reported a goal")
	}
	select {
	case event := <-events:
		t.Fatalf("Clear without a goal notified: %+v", event)
	default:
	}
}

func TestGoalNilContextAndNoHandler(t *testing.T) {
	root := newTestRoot(t)
	service := root.GoalTracker()
	id, err := service.Set(nil, "nil context")
	if err != nil || id == "" {
		t.Fatalf("Set(nil ctx) = %q, %v", id, err)
	}
	service.SetHandler(func(contracts.Goal, bool) {})
	service.SetHandler(nil)
	if _, err := service.Set(context.Background(), "after clear handler"); err != nil {
		t.Fatalf("Set after clearing handler: %v", err)
	}
	if !service.Clear() {
		t.Fatal("Clear after clearing handler failed")
	}
}

func TestGoalConcurrent(t *testing.T) {
	root := newTestRoot(t)
	service := root.GoalTracker()
	service.SetHandler(func(contracts.Goal, bool) {})
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, _ = service.Set(context.Background(), "goal")
				_, _, _, _ = service.Get()
				service.Clear()
			}
		}(worker)
	}
	wg.Wait()
	// The final state is whichever operation won last; only consistency matters.
	id, text, status, ok := service.Get()
	if ok && (id == "" || text == "" || status != "active") {
		t.Fatalf("inconsistent goal = %q/%q/%q/%v", id, text, status, ok)
	}
}
