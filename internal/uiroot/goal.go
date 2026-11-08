package uiroot

// The host goal bridge: the agent's goal tool calls Set/Get/Clear, the session
// header installs a handler to display the active goal, and Clear backs the
// header's "Clear goal" action.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Goal statuses: active goals drive auto-continue; paused goals stay visible
// but are not advanced.
const (
	goalStatusActive = "active"
	goalStatusPaused = "paused"
)

// goalService is the Root's contracts.GoalTracker implementation.
type goalService struct {
	mu      sync.Mutex
	handler func(contracts.Goal, bool)
	goal    *contracts.Goal
	nextID  atomic.Uint64
}

// newGoalService returns an empty goal tracker.
func newGoalService() *goalService { return &goalService{} }

var _ contracts.GoalPauser = (*goalService)(nil)

// SetHandler installs the change handler (nil clears it).
func (s *goalService) SetHandler(fn func(contracts.Goal, bool)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// Set creates or replaces the goal with a new ID and notifies the handler.
func (s *goalService) Set(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("goal: text is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	goal := contracts.Goal{
		ID:     fmt.Sprintf("goal-%d", s.nextID.Add(1)),
		Text:   text,
		Status: goalStatusActive,
	}
	s.mu.Lock()
	s.goal = &goal
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		// A panicking display handler is contained; the goal is committed.
		runHandlerSafely("goal", func() { handler(goal, true) })
	}
	return goal.ID, nil
}

// Get returns the current goal; ok is false when none is set.
func (s *goalService) Get() (id, text, status string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goal == nil {
		return "", "", "", false
	}
	return s.goal.ID, s.goal.Text, s.goal.Status, true
}

// Pause marks the active goal paused and notifies the handler. It reports
// whether a goal existed; pausing an already paused goal is a no-op.
func (s *goalService) Pause() bool {
	s.mu.Lock()
	if s.goal == nil {
		s.mu.Unlock()
		return false
	}
	if s.goal.Status != goalStatusActive {
		s.mu.Unlock()
		return true
	}
	goal := *s.goal
	goal.Status = goalStatusPaused
	s.goal = &goal
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		runHandlerSafely("goal", func() { handler(goal, true) })
	}
	return true
}

// Resume marks a paused goal active and notifies the handler. It reports
// whether a goal existed; resuming an active goal is a no-op.
func (s *goalService) Resume() bool {
	s.mu.Lock()
	if s.goal == nil {
		s.mu.Unlock()
		return false
	}
	if s.goal.Status != goalStatusPaused {
		s.mu.Unlock()
		return true
	}
	goal := *s.goal
	goal.Status = goalStatusActive
	s.goal = &goal
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		runHandlerSafely("goal", func() { handler(goal, true) })
	}
	return true
}

// Clear removes the goal and notifies the handler with ok=false. It reports
// whether a goal existed.
func (s *goalService) Clear() bool {
	s.mu.Lock()
	existed := s.goal != nil
	s.goal = nil
	handler := s.handler
	s.mu.Unlock()
	if existed && handler != nil {
		runHandlerSafely("goal", func() { handler(contracts.Goal{}, false) })
	}
	return existed
}

// GoalTracker returns the host goal bridge (contracts.KeyGoalTracker on the
// desktop product).
func (r *Root) GoalTracker() *goalService { return r.goal }
