package uiroot

// Goal lifecycle tests: complete/abandon/edit status transitions and handler
// notifications (TDD: written before the implementation).

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newLifecycleGoal sets an active goal and returns the service plus an event
// channel fed by the handler.
func newLifecycleGoal(t *testing.T) (*goalService, chan contracts.Goal, func()) {
	t.Helper()
	root := newTestRoot(t)
	service := root.GoalTracker()
	events := make(chan contracts.Goal, 8)
	service.SetHandler(func(goal contracts.Goal, ok bool) {
		if ok {
			events <- goal
		}
	})
	if _, err := service.Set(context.Background(), "ship it"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	<-events // initial set
	return service, events, func() {}
}

func drainEvent(t *testing.T, events chan contracts.Goal) contracts.Goal {
	t.Helper()
	select {
	case goal := <-events:
		return goal
	default:
		t.Fatal("expected one goal notification")
		return contracts.Goal{}
	}
}

func TestGoalCompleteLifecycle(t *testing.T) {
	service, events, _ := newLifecycleGoal(t)
	if !service.Complete() {
		t.Fatal("Complete(active) = false")
	}
	event := drainEvent(t, events)
	if event.Status != contracts.GoalStatusCompleted || event.Text != "ship it" {
		t.Fatalf("complete event = %+v", event)
	}
	_, _, status, ok := service.Get()
	if !ok || status != contracts.GoalStatusCompleted {
		t.Fatalf("status after Complete = %q/%v", status, ok)
	}
	if service.Complete() {
		t.Fatal("Complete(completed) = true")
	}
	if service.Abandon() {
		t.Fatal("Abandon(completed) = true")
	}
	if service.Pause() {
		t.Fatal("Pause(completed) = true")
	}
	if service.Resume() {
		t.Fatal("Resume(completed) = true")
	}
}

func TestGoalAbandonLifecycle(t *testing.T) {
	service, events, _ := newLifecycleGoal(t)
	if !service.Abandon() {
		t.Fatal("Abandon(active) = false")
	}
	event := drainEvent(t, events)
	if event.Status != contracts.GoalStatusAbandoned {
		t.Fatalf("abandon event = %+v", event)
	}
	if service.Abandon() {
		t.Fatal("Abandon(abandoned) = true")
	}
	if service.Pause() {
		t.Fatal("Pause(abandoned) = true")
	}
}

func TestGoalCompletePausedGoal(t *testing.T) {
	service, events, _ := newLifecycleGoal(t)
	if !service.Pause() {
		t.Fatal("Pause(active) = false")
	}
	<-events
	if !service.Complete() {
		t.Fatal("Complete(paused) = false")
	}
	if event := drainEvent(t, events); event.Status != contracts.GoalStatusCompleted {
		t.Fatalf("paused complete event = %+v", event)
	}
}

func TestGoalEditTextKeepsIDAndReactivates(t *testing.T) {
	service, events, _ := newLifecycleGoal(t)
	originalID, _, _, _ := service.Get()
	if !service.Complete() {
		t.Fatal("Complete = false")
	}
	<-events

	id, err := service.Edit("  revised goal  ")
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if id != originalID {
		t.Fatalf("Edit id = %q, want the original %q", id, originalID)
	}
	event := drainEvent(t, events)
	if event.ID != originalID || event.Text != "revised goal" || event.Status != contracts.GoalStatusActive {
		t.Fatalf("edit event = %+v", event)
	}
	_, text, status, ok := service.Get()
	if !ok || text != "revised goal" || status != contracts.GoalStatusActive {
		t.Fatalf("goal after edit = %q/%q/%v", text, status, ok)
	}

	if _, err := service.Edit("   "); err == nil {
		t.Fatal("Edit(blank) succeeded")
	}
	if !service.Clear() {
		t.Fatal("Clear after edit = false")
	}
	if _, err := service.Edit("after clear"); err == nil {
		t.Fatal("Edit without a goal succeeded")
	}
}

func TestGoalLifecycleWithoutGoal(t *testing.T) {
	root := newTestRoot(t)
	service := root.GoalTracker()
	service.SetHandler(func(contracts.Goal, bool) {})
	if service.Complete() || service.Abandon() {
		t.Fatal("lifecycle transition without a goal reported success")
	}
	if _, err := service.Edit("no goal"); err == nil {
		t.Fatal("Edit without a goal succeeded")
	}
}

func TestGoalLifecycleConcurrent(t *testing.T) {
	root := newTestRoot(t)
	service := root.GoalTracker()
	service.SetHandler(func(contracts.Goal, bool) {})
	if _, err := service.Set(context.Background(), "concurrent"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < 10; index++ {
				switch worker % 3 {
				case 0:
					service.Complete()
				case 1:
					service.Abandon()
				default:
					_, _ = service.Edit("edited")
					service.Clear()
					_, _ = service.Set(context.Background(), "reset")
				}
			}
		}()
	}
	wg.Wait()
	_, _, status, ok := service.Get()
	if ok {
		switch status {
		case contracts.GoalStatusActive, contracts.GoalStatusPaused,
			contracts.GoalStatusCompleted, contracts.GoalStatusAbandoned:
		default:
			t.Fatalf("unknown status after concurrency: %q", status)
		}
	}
}
