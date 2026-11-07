package uiroot

// The host plan-review bridge: the agent's plan_review tool parks a plan for
// display, exit_plan_mode applies the user's (or model's) decision, and the UI
// module installs a handler to render each state change.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Plan review states (the values contracts.PlanReview.State carries).
const (
	PlanIdle      = "idle"
	PlanReviewing = "reviewing"
	PlanApproved  = "approved"
	PlanCancelled = "cancelled"
)

// planService is the Root's contracts.PlanReviewer implementation.
type planService struct {
	mu      sync.Mutex
	state   string
	content string
	handler func(contracts.PlanReview)
}

// newPlanService returns a plan bridge in the idle state.
func newPlanService() *planService { return &planService{state: PlanIdle} }

// SetPlanHandler installs the display handler (nil clears it).
func (s *planService) SetPlanHandler(fn func(contracts.PlanReview)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// Review parks a plan for display and returns the reviewing state.
func (s *planService) Review(ctx context.Context, content string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("plan: content is required")
	}
	s.mu.Lock()
	s.state = PlanReviewing
	s.content = content
	handler := s.handler
	snapshot := contracts.PlanReview{Content: s.content, State: s.state}
	s.mu.Unlock()
	if handler != nil {
		handler(snapshot)
	}
	return PlanReviewing, nil
}

// Exit applies one decision: approve, cancel or status. Approve/cancel require
// a plan under review; status reports the current state without changing it.
// Every actual state change calls the handler.
func (s *planService) Exit(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	s.mu.Lock()
	state := s.state
	if action == "status" {
		s.mu.Unlock()
		return state, nil
	}
	var next string
	switch action {
	case "approve", "cancel":
		if state != PlanReviewing {
			s.mu.Unlock()
			return state, fmt.Errorf("plan: no plan is under review (state: %s)", state)
		}
		if action == "approve" {
			next = PlanApproved
		} else {
			next = PlanCancelled
		}
		s.state = next
		handler := s.handler
		snapshot := contracts.PlanReview{Content: s.content, State: next}
		s.mu.Unlock()
		if handler != nil {
			handler(snapshot)
		}
		return next, nil
	default:
		s.mu.Unlock()
		return state, fmt.Errorf("plan: unknown plan action %q (want approve, cancel or status)", action)
	}
}

// State returns the current plan-review state.
func (s *planService) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// PlanReviewer returns the host plan-review bridge
// (contracts.KeyPlanReviewer on the desktop product).
func (r *Root) PlanReviewer() *planService { return r.plan }
