package hosttools

// A small in-memory contracts.GoalService for the host-tools tests: enough of
// the DSH semantics (per session, CAS revisions, round cap blocking, activation)
// to drive the real Rig get_goal/create_goal/update_goal tools through the
// toolkit registry without a database.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeGoalState struct {
	goal  contracts.Goal
	armed bool
}

type fakeGoalService struct {
	mu       sync.Mutex
	sessions map[string]*fakeGoalState
	nextID   int
}

func newFakeGoalService() *fakeGoalService {
	return &fakeGoalService{sessions: make(map[string]*fakeGoalState)}
}

// Goal implements contracts.GoalService.
func (f *fakeGoalService) Goal(sessionID string) (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.sessions[sessionID]
	if !ok || state.goal.ID == "" {
		return contracts.Goal{}, false
	}
	goal := state.goal
	if state.armed && goal.Phase == contracts.GoalPhaseActive {
		goal.Activation = contracts.GoalActivationArmed
	} else {
		goal.Activation = contracts.GoalActivationDisarmed
	}
	return goal, true
}

// CreateGoal implements contracts.GoalService.
func (f *fakeGoalService) CreateGoal(_ context.Context, sessionID, objective string, maxGoalRounds int) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return contracts.Goal{}, errors.New("objective is required")
	}
	if state, ok := f.sessions[sessionID]; ok && state.goal.ID != "" && state.goal.Phase != contracts.GoalPhaseComplete {
		return contracts.Goal{}, fmt.Errorf("%w: %q", contracts.ErrGoalConflict, state.goal.ID)
	}
	if maxGoalRounds <= 0 {
		maxGoalRounds = contracts.DefaultMaxGoalRounds
	}
	f.nextID++
	goal := contracts.Goal{
		SessionID: sessionID, ID: fmt.Sprintf("fake-goal-%d", f.nextID), Revision: 1,
		Objective: objective, Phase: contracts.GoalPhaseActive, MaxGoalRounds: maxGoalRounds,
		Activation: contracts.GoalActivationArmed,
	}
	f.sessions[sessionID] = &fakeGoalState{goal: goal, armed: true}
	return goal, nil
}

// UpdateGoal implements contracts.GoalService.
func (f *fakeGoalService) UpdateGoal(_ context.Context, sessionID string, ref contracts.GoalRef, update contracts.GoalUpdate) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.sessions[sessionID]
	if !ok || state.goal.ID == "" {
		return contracts.Goal{}, contracts.ErrGoalNotFound
	}
	if ref.ID != state.goal.ID || ref.Revision != state.goal.Revision {
		return contracts.Goal{}, fmt.Errorf("%w: want %q revision %d", contracts.ErrGoalStaleRef, state.goal.ID, state.goal.Revision)
	}
	next := state.goal
	switch update.Action {
	case contracts.GoalActionEdit:
		if objective := strings.TrimSpace(update.Objective); objective != "" {
			next.Objective = objective
		}
		if update.MaxGoalRounds > 0 {
			next.MaxGoalRounds = update.MaxGoalRounds
		}
	case contracts.GoalActionPause:
		if next.Phase != contracts.GoalPhaseActive {
			return contracts.Goal{}, contracts.ErrGoalInvalidTransition
		}
		next.Phase = contracts.GoalPhasePaused
		state.armed = false
	case contracts.GoalActionResume:
		if next.Phase != contracts.GoalPhasePaused && next.Phase != contracts.GoalPhaseBlocked {
			return contracts.Goal{}, contracts.ErrGoalInvalidTransition
		}
		next.Phase = contracts.GoalPhaseActive
		next.BlockedReason = nil
		state.armed = true
	case contracts.GoalActionComplete:
		next.Phase = contracts.GoalPhaseComplete
		state.armed = false
	case contracts.GoalActionBlocked:
		if strings.TrimSpace(update.BlockedReason) == "" {
			return contracts.Goal{}, errors.New("blocked reason is required")
		}
		next.Phase = contracts.GoalPhaseBlocked
		next.BlockedReason = &contracts.GoalBlockedReason{Code: contracts.GoalBlockedCodeModelReported, Message: update.BlockedReason}
		state.armed = false
	default:
		return contracts.Goal{}, contracts.ErrGoalInvalidTransition
	}
	next.Revision++
	state.goal = next
	goal := next
	if state.armed && goal.Phase == contracts.GoalPhaseActive {
		goal.Activation = contracts.GoalActivationArmed
	} else {
		goal.Activation = contracts.GoalActivationDisarmed
	}
	return goal, nil
}

// AdmitRound implements contracts.GoalService.
func (f *fakeGoalService) AdmitRound(_ context.Context, sessionID string) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.sessions[sessionID]
	if !ok || state.goal.ID == "" {
		return contracts.Goal{}, contracts.ErrGoalNotFound
	}
	if state.goal.Phase != contracts.GoalPhaseActive {
		return contracts.Goal{}, contracts.ErrGoalInvalidTransition
	}
	if state.goal.RoundsStarted >= state.goal.MaxGoalRounds {
		return contracts.Goal{}, contracts.ErrGoalRoundLimit
	}
	state.goal.RoundsStarted++
	state.goal.Revision++
	return state.goal, nil
}

// Disarm implements contracts.GoalService.
func (f *fakeGoalService) Disarm(sessionID string) (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.sessions[sessionID]
	if !ok || state.goal.ID == "" {
		return contracts.Goal{}, false
	}
	state.armed = false
	goal := state.goal
	goal.Activation = contracts.GoalActivationDisarmed
	return goal, true
}

// ClearGoal implements contracts.GoalService (tombstone: remove the session's goal).
func (f *fakeGoalService) ClearGoal(_ context.Context, sessionID string, ref contracts.GoalRef) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.sessions[sessionID]
	if !ok || state.goal.ID == "" {
		return false, nil
	}
	if ref.ID != state.goal.ID || ref.Revision != state.goal.Revision {
		return false, contracts.ErrGoalStaleRef
	}
	delete(f.sessions, sessionID)
	return true, nil
}

// Subscribe implements contracts.GoalService.
func (f *fakeGoalService) Subscribe(func(string)) (func(), error) { return func() {}, nil }

// setRounds seeds the round count of a session's goal (test helper for the
// blocked threshold).
func (f *fakeGoalService) setRounds(sessionID string, rounds int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state, ok := f.sessions[sessionID]; ok {
		state.goal.RoundsStarted = rounds
	}
}
