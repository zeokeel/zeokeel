package uiroot

// Host goal bridge pause/resume half.

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestGoalPauseResume(t *testing.T) {
	root := newTestRoot(t)
	service := root.GoalTracker()
	if _, ok := any(service).(contracts.GoalPauser); !ok {
		t.Fatal("goalService does not implement contracts.GoalPauser")
	}
	events := make(chan goalEvent, 8)
	service.SetHandler(func(goal contracts.Goal, ok bool) { events <- goalEvent{goal: goal, ok: ok} })

	if service.Pause() {
		t.Fatal("Pause without a goal reported one")
	}
	if service.Resume() {
		t.Fatal("Resume without a goal reported one")
	}

	if _, err := service.Set(context.Background(), "ship it"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if event := <-events; !event.ok || event.goal.Status != "active" {
		t.Fatalf("set event = %+v", event)
	}
	if !service.Pause() {
		t.Fatal("Pause existing active goal = false")
	}
	if _, _, status, ok := service.Get(); !ok || status != "paused" {
		t.Fatalf("after Pause status = %q ok=%v", status, ok)
	}
	if event := <-events; !event.ok || event.goal.Status != "paused" || event.goal.Text != "ship it" {
		t.Fatalf("pause event = %+v", event)
	}
	// Pausing an already paused goal is a no-op that still reports the goal.
	if !service.Pause() {
		t.Fatal("second Pause = false")
	}
	select {
	case event := <-events:
		t.Fatalf("second Pause notified: %+v", event)
	default:
	}

	if !service.Resume() {
		t.Fatal("Resume paused goal = false")
	}
	if _, _, status, ok := service.Get(); !ok || status != "active" {
		t.Fatalf("after Resume status = %q ok=%v", status, ok)
	}
	if event := <-events; !event.ok || event.goal.Status != "active" {
		t.Fatalf("resume event = %+v", event)
	}
	if !service.Resume() {
		t.Fatal("second Resume = false")
	}
	select {
	case event := <-events:
		t.Fatalf("resume of an active goal notified: %+v", event)
	default:
	}

	// A new goal always starts active.
	service.Pause()
	<-events
	if _, err := service.Set(context.Background(), "new goal"); err != nil {
		t.Fatalf("Set replacement: %v", err)
	}
	event := <-events
	if !event.ok || event.goal.Status != "active" || event.goal.Text != "new goal" {
		t.Fatalf("replacement event = %+v", event)
	}
	if _, _, status, _ := service.Get(); status != "active" {
		t.Fatalf("replacement status = %q", status)
	}
}

func TestGoalPauseConcurrent(t *testing.T) {
	root := newTestRoot(t)
	service := root.GoalTracker()
	service.SetHandler(func(contracts.Goal, bool) {})
	if _, err := service.Set(context.Background(), "race"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				switch worker % 3 {
				case 0:
					service.Pause()
				case 1:
					service.Resume()
				default:
					_, _, _, _ = service.Get()
				}
			}
		}(worker)
	}
	wg.Wait()
	if _, _, status, ok := service.Get(); !ok || (status != "active" && status != "paused") {
		t.Fatalf("inconsistent goal status = %q ok=%v", status, ok)
	}
}
