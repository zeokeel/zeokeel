package uiroot

// Regression tests (TDD RED first) for handler-panic containment in the host
// bridges: a panicking UI-module handler must not propagate across the bridge
// boundary nor corrupt the bridge state (pending questions, plan/goal state).

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// pendingQuestions reports the number of parked Ask entries (test helper).
func pendingQuestions(s *interactionService) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// callContained runs fn and reports whether it panicked.
func callContained(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

func TestInteractionAskContainsHandlerPanic(t *testing.T) {
	root := New(Config{})
	interaction := root.UserInteraction()
	interaction.SetHandler(func(contracts.UserQuestion) { panic("handler boom") })

	var err error
	recovered := callContained(func() { _, err = interaction.Ask(context.Background(), "boom?", nil) })
	if recovered != nil {
		t.Fatalf("Ask propagated the handler panic: %v", recovered)
	}
	if err == nil {
		t.Fatal("Ask returned no error after a contained handler panic")
	}
	if pending := pendingQuestions(interaction); pending != 0 {
		t.Fatalf("pending questions = %d after a contained panic, want 0", pending)
	}

	// The bridge keeps working afterwards: a healthy handler answers normally.
	interaction.SetHandler(func(question contracts.UserQuestion) {
		interaction.Answer(question.ID, "ok")
	})
	answer, err := interaction.Ask(context.Background(), "still works?", nil)
	if err != nil || answer != "ok" {
		t.Fatalf("Ask after contained panic = (%q, %v), want ok", answer, err)
	}
}

func TestArtifactPresentContainsHandlerPanic(t *testing.T) {
	root := New(Config{})
	presenter := root.ArtifactPresenter()
	presenter.SetArtifactHandler(func(contracts.Artifact) { panic("artifact handler boom") })

	var id string
	var err error
	recovered := callContained(func() { id, err = presenter.Present(context.Background(), "title", "body") })
	if recovered != nil {
		t.Fatalf("Present propagated the handler panic: %v", recovered)
	}
	if err != nil || id == "" {
		t.Fatalf("Present = (%q, %v), want a recorded artifact id", id, err)
	}
}

func TestPlanReviewContainsHandlerPanic(t *testing.T) {
	root := New(Config{})
	reviewer := root.PlanReviewer()
	reviewer.SetPlanHandler(func(contracts.PlanReview) { panic("plan handler boom") })

	var state string
	var err error
	recovered := callContained(func() { state, err = reviewer.Review(context.Background(), "plan body") })
	if recovered != nil {
		t.Fatalf("Review propagated the handler panic: %v", recovered)
	}
	if err != nil || state != PlanReviewing {
		t.Fatalf("Review = (%q, %v), want reviewing", state, err)
	}
	if got := reviewer.State(); got != PlanReviewing {
		t.Fatalf("State = %q after contained panic, want reviewing", got)
	}
}

func TestPlanExitContainsHandlerPanic(t *testing.T) {
	root := New(Config{})
	reviewer := root.PlanReviewer()
	if _, err := reviewer.Review(context.Background(), "plan body"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	reviewer.SetPlanHandler(func(contracts.PlanReview) { panic("plan handler boom") })

	var state string
	var err error
	recovered := callContained(func() { state, err = reviewer.Exit("approve") })
	if recovered != nil {
		t.Fatalf("Exit propagated the handler panic: %v", recovered)
	}
	if err != nil || state != PlanApproved {
		t.Fatalf("Exit = (%q, %v), want approved", state, err)
	}
	if got := reviewer.State(); got != PlanApproved {
		t.Fatalf("State = %q after contained panic, want approved", got)
	}
}

func TestGoalSetContainsHandlerPanic(t *testing.T) {
	root := New(Config{})
	tracker := root.GoalTracker()
	tracker.SetHandler(func(contracts.Goal, bool) { panic("goal handler boom") })

	var id string
	var err error
	recovered := callContained(func() { id, err = tracker.Set(context.Background(), "ship it") })
	if recovered != nil {
		t.Fatalf("Set propagated the handler panic: %v", recovered)
	}
	if err != nil || id == "" {
		t.Fatalf("Set = (%q, %v), want a goal id", id, err)
	}
	if gotID, text, status, ok := tracker.Get(); !ok || gotID != id || text != "ship it" || status != "active" {
		t.Fatalf("Get() = (%q, %q, %q, %v), want the recorded goal", gotID, text, status, ok)
	}
}

func TestGoalClearContainsHandlerPanic(t *testing.T) {
	root := New(Config{})
	tracker := root.GoalTracker()
	if _, err := tracker.Set(context.Background(), "goal"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	tracker.SetHandler(func(contracts.Goal, bool) { panic("goal handler boom") })

	existed := false
	recovered := callContained(func() { existed = tracker.Clear() })
	if recovered != nil {
		t.Fatalf("Clear propagated the handler panic: %v", recovered)
	}
	if !existed {
		t.Fatal("Clear reported no goal after a contained handler panic")
	}
	if _, _, _, ok := tracker.Get(); ok {
		t.Fatal("the goal was not cleared after a contained handler panic")
	}
}

func TestBridgesContainPanicsConcurrently(t *testing.T) {
	root := New(Config{})
	interaction := root.UserInteraction()
	interaction.SetHandler(func(contracts.UserQuestion) { panic("boom") })
	root.ArtifactPresenter().SetArtifactHandler(func(contracts.Artifact) { panic("boom") })
	root.PlanReviewer().SetPlanHandler(func(contracts.PlanReview) { panic("boom") })
	root.GoalTracker().SetHandler(func(contracts.Goal, bool) { panic("boom") })

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < 10; index++ {
				switch worker % 4 {
				case 0:
					_, _ = interaction.Ask(context.Background(), "q", nil)
				case 1:
					_, _ = root.ArtifactPresenter().Present(context.Background(), "t", "b")
				case 2:
					_, _ = root.PlanReviewer().Review(context.Background(), "plan")
					_, _ = root.PlanReviewer().Exit("cancel")
				default:
					_, _ = root.GoalTracker().Set(context.Background(), "goal")
					root.GoalTracker().Clear()
				}
			}
		}()
	}
	wg.Wait()

	if pending := pendingQuestions(interaction); pending != 0 {
		t.Fatalf("pending questions = %d after concurrent panics, want 0", pending)
	}
}
