package uiroot

// Host plan-review bridge: idle -> reviewing -> approved/cancelled, with one
// handler call per state change.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestPlanReviewStateMachine(t *testing.T) {
	root := newTestRoot(t)
	service := root.PlanReviewer()
	if state := service.State(); state != PlanIdle {
		t.Fatalf("initial state = %q, want idle", state)
	}
	if _, err := service.Review(context.Background(), "  "); err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("Review(empty) = %v, want content error", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Review(cancelled, "# x"); err == nil {
		t.Fatal("Review(cancelled ctx) returned nil error")
	}

	reviews := make(chan contracts.PlanReview, 8)
	service.SetPlanHandler(func(review contracts.PlanReview) { reviews <- review })

	state, err := service.Review(context.Background(), "  # Release plan  ")
	if err != nil || state != PlanReviewing {
		t.Fatalf("Review = %q, %v; want reviewing", state, err)
	}
	if got := service.State(); got != PlanReviewing {
		t.Fatalf("State after Review = %q", got)
	}
	review := <-reviews
	if review.State != PlanReviewing || review.Content != "# Release plan" {
		t.Fatalf("review snapshot = %+v", review)
	}

	if state, err := service.Exit("status"); err != nil || state != PlanReviewing {
		t.Fatalf("status = %q, %v; want reviewing/nil", state, err)
	}
	if state, err := service.Exit("approve"); err != nil || state != PlanApproved {
		t.Fatalf("approve = %q, %v; want approved/nil", state, err)
	}
	review = <-reviews
	if review.State != PlanApproved || review.Content != "# Release plan" {
		t.Fatalf("approved snapshot = %+v", review)
	}
	if state := service.State(); state != PlanApproved {
		t.Fatalf("State after approve = %q", state)
	}
	if _, err := service.Exit("approve"); err == nil || !strings.Contains(err.Error(), "no plan is under review") {
		t.Fatalf("second approve = %v, want no-plan error", err)
	}

	// A new Review restarts the state machine; cancel is another terminal state.
	if _, err := service.Review(context.Background(), "# Second"); err != nil {
		t.Fatalf("second Review: %v", err)
	}
	<-reviews
	if state, err := service.Exit("cancel"); err != nil || state != PlanCancelled {
		t.Fatalf("cancel = %q, %v; want cancelled/nil", state, err)
	}
	<-reviews
	if state := service.State(); state != PlanCancelled {
		t.Fatalf("State after cancel = %q", state)
	}
	if _, err := service.Exit("bogus"); err == nil || !strings.Contains(err.Error(), "unknown plan action") {
		t.Fatalf("bogus action = %v, want unknown-action error", err)
	}
	if _, err := service.Exit("approve"); err == nil {
		t.Fatal("approve after cancel returned nil error")
	}
}

func TestPlanExitWithoutReview(t *testing.T) {
	root := newTestRoot(t)
	service := root.PlanReviewer()
	if state, err := service.Exit("status"); err != nil || state != PlanIdle {
		t.Fatalf("idle status = %q, %v", state, err)
	}
	for _, action := range []string{"approve", "cancel"} {
		if _, err := service.Exit(action); err == nil || !strings.Contains(err.Error(), "no plan is under review") {
			t.Fatalf("%s without review = %v", action, err)
		}
	}
	if state := service.State(); state != PlanIdle {
		t.Fatalf("state after rejected exits = %q, want idle", state)
	}
}

func TestPlanHandlerNilIsSafe(t *testing.T) {
	root := newTestRoot(t)
	service := root.PlanReviewer()
	service.SetPlanHandler(func(contracts.PlanReview) {})
	service.SetPlanHandler(nil)
	if _, err := service.Review(context.Background(), "# plan"); err != nil {
		t.Fatalf("Review after clearing handler: %v", err)
	}
	if _, err := service.Exit("approve"); err != nil {
		t.Fatalf("Exit after clearing handler: %v", err)
	}
}

func TestPlanConcurrentReadsAndWrites(t *testing.T) {
	root := newTestRoot(t)
	service := root.PlanReviewer()
	service.SetPlanHandler(func(contracts.PlanReview) {})
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, _ = service.Review(context.Background(), "# plan")
				_ = service.State()
				_, _ = service.Exit("status")
				_, _ = service.Exit("approve")
				_, _ = service.Exit("cancel")
			}
		}()
	}
	wg.Wait()
	state := service.State()
	switch state {
	case PlanIdle, PlanReviewing, PlanApproved, PlanCancelled:
	default:
		t.Fatalf("state after concurrency = %q", state)
	}
}
