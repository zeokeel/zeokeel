package session_test

// A scripted per-session GoalService for the goal-dock frame tests. It keeps
// one goal per session (so a conversation-switch isolation test is real), the
// compare-and-set revision contract (stale refs are rejected), the phase
// transitions and the round cap. Every mutation is recorded so a test can assert
// the exact human GoalService call the UI made.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type fakeGoalCreate struct {
	sessionID     string
	objective     string
	maxGoalRounds int
}

type fakeGoalUpdate struct {
	sessionID string
	ref       contracts.GoalRef
	update    contracts.GoalUpdate
}

type fakeGoalService struct {
	mu    sync.Mutex
	goals map[string]contracts.Goal
	next  int
	subs  []func(string)

	createCalls []fakeGoalCreate
	updateCalls []fakeGoalUpdate
	clearCalls  []contracts.GoalRef

	createErr error
	updateErr error
	clearErr  error
}

var _ contracts.GoalService = (*fakeGoalService)(nil)

func newFakeGoalService() *fakeGoalService {
	return &fakeGoalService{goals: make(map[string]contracts.Goal)}
}

// seed installs an active, armed, default-capped goal.
func (f *fakeGoalService) seed(sessionID, objective string) contracts.Goal {
	return f.seedGoal(sessionID, objective, contracts.GoalPhaseActive,
		contracts.GoalActivationArmed, 0, contracts.DefaultMaxGoalRounds)
}

// seedGoal installs one exact goal snapshot and notifies the subscribers like a
// real goal change.
func (f *fakeGoalService) seedGoal(sessionID, objective string, phase contracts.GoalPhase,
	activation contracts.GoalActivation, rounds, maxGoalRounds int) contracts.Goal {
	f.mu.Lock()
	goal := f.putGoalLocked(sessionID, objective, phase, activation, rounds, maxGoalRounds)
	f.notifyLocked(sessionID)
	f.mu.Unlock()
	return goal
}

// seedBlocked installs a blocked goal with one concrete reason.
func (f *fakeGoalService) seedBlocked(sessionID, code, message string, rounds, maxGoalRounds int) contracts.Goal {
	f.mu.Lock()
	goal := f.putGoalLocked(sessionID, "ship it", contracts.GoalPhaseBlocked,
		contracts.GoalActivationDisarmed, rounds, maxGoalRounds)
	goal.BlockedReason = &contracts.GoalBlockedReason{Code: code, Message: message}
	f.goals[sessionID] = goal
	f.notifyLocked(sessionID)
	f.mu.Unlock()
	return goal
}

// putGoalLocked writes one snapshot; the caller holds the lock and notifies.
func (f *fakeGoalService) putGoalLocked(sessionID, objective string, phase contracts.GoalPhase,
	activation contracts.GoalActivation, rounds, maxGoalRounds int) contracts.Goal {
	f.next++
	goal := contracts.Goal{
		SessionID:     sessionID,
		ID:            fmt.Sprintf("goal-%d", f.next),
		Revision:      1,
		Objective:     objective,
		Phase:         phase,
		RoundsStarted: rounds,
		MaxGoalRounds: maxGoalRounds,
		Activation:    activation,
	}
	f.goals[sessionID] = goal
	return goal
}

// wantGoal returns the current fake snapshot (zero value when absent).
func (f *fakeGoalService) wantGoal(sessionID string) contracts.Goal {
	goal, _ := f.Goal(sessionID)
	return goal
}

func (f *fakeGoalService) Goal(sessionID string) (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	goal, ok := f.goals[sessionID]
	return goal, ok
}

func (f *fakeGoalService) CreateGoal(_ context.Context, sessionID, objective string, maxGoalRounds int) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, fakeGoalCreate{sessionID: sessionID, objective: objective, maxGoalRounds: maxGoalRounds})
	if f.createErr != nil {
		return contracts.Goal{}, f.createErr
	}
	if existing, ok := f.goals[sessionID]; ok && existing.Phase != contracts.GoalPhaseComplete {
		return contracts.Goal{}, contracts.ErrGoalConflict
	}
	if maxGoalRounds <= 0 {
		maxGoalRounds = contracts.DefaultMaxGoalRounds
	}
	f.next++
	goal := contracts.Goal{
		SessionID:     sessionID,
		ID:            fmt.Sprintf("goal-%d", f.next),
		Revision:      1,
		Objective:     objective,
		Phase:         contracts.GoalPhaseActive,
		MaxGoalRounds: maxGoalRounds,
		Activation:    contracts.GoalActivationArmed,
	}
	f.goals[sessionID] = goal
	f.notifyLocked(sessionID)
	return goal, nil
}

func (f *fakeGoalService) UpdateGoal(_ context.Context, sessionID string, ref contracts.GoalRef, update contracts.GoalUpdate) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls = append(f.updateCalls, fakeGoalUpdate{sessionID: sessionID, ref: ref, update: update})
	if f.updateErr != nil {
		return contracts.Goal{}, f.updateErr
	}
	current, ok := f.goals[sessionID]
	if !ok {
		return contracts.Goal{}, contracts.ErrGoalNotFound
	}
	if ref.ID != current.ID || ref.Revision != current.Revision {
		return contracts.Goal{}, contracts.ErrGoalStaleRef
	}
	next := current
	switch update.Action {
	case contracts.GoalActionEdit:
		if update.Objective != "" {
			next.Objective = update.Objective
		}
		if update.MaxGoalRounds > 0 {
			next.MaxGoalRounds = update.MaxGoalRounds
		}
		next.Revision++
	case contracts.GoalActionPause:
		if current.Phase != contracts.GoalPhaseActive {
			return contracts.Goal{}, contracts.ErrGoalInvalidTransition
		}
		next.Phase = contracts.GoalPhasePaused
		next.Activation = contracts.GoalActivationDisarmed
		next.Revision++
	case contracts.GoalActionResume:
		switch current.Phase {
		case contracts.GoalPhaseActive:
			if current.Activation == contracts.GoalActivationArmed {
				return contracts.Goal{}, contracts.ErrGoalInvalidTransition
			}
			next.Activation = contracts.GoalActivationArmed
		case contracts.GoalPhasePaused, contracts.GoalPhaseBlocked:
			if current.MaxGoalRounds > 0 && current.RoundsStarted >= current.MaxGoalRounds {
				return contracts.Goal{}, contracts.ErrGoalInvalidTransition
			}
			next.Phase = contracts.GoalPhaseActive
			next.BlockedReason = nil
			next.Activation = contracts.GoalActivationArmed
			next.Revision++
		default:
			return contracts.Goal{}, contracts.ErrGoalInvalidTransition
		}
	case contracts.GoalActionComplete:
		if current.Phase == contracts.GoalPhaseComplete {
			return contracts.Goal{}, contracts.ErrGoalInvalidTransition
		}
		next.Phase = contracts.GoalPhaseComplete
		next.BlockedReason = nil
		next.Activation = contracts.GoalActivationDisarmed
		next.Revision++
	case contracts.GoalActionBlocked:
		next.Phase = contracts.GoalPhaseBlocked
		next.BlockedReason = &contracts.GoalBlockedReason{
			Code: contracts.GoalBlockedCodeModelReported, Message: update.BlockedReason,
		}
		next.Activation = contracts.GoalActivationDisarmed
		next.Revision++
	default:
		return contracts.Goal{}, contracts.ErrGoalInvalidTransition
	}
	f.goals[sessionID] = next
	f.notifyLocked(sessionID)
	return next, nil
}

func (f *fakeGoalService) ClearGoal(_ context.Context, sessionID string, ref contracts.GoalRef) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearCalls = append(f.clearCalls, ref)
	if f.clearErr != nil {
		return false, f.clearErr
	}
	current, ok := f.goals[sessionID]
	if !ok {
		return false, nil
	}
	if ref.ID != current.ID || ref.Revision != current.Revision {
		return false, contracts.ErrGoalStaleRef
	}
	delete(f.goals, sessionID)
	f.notifyLocked(sessionID)
	return true, nil
}

func (f *fakeGoalService) AdmitRound(_ context.Context, sessionID string) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current, ok := f.goals[sessionID]
	if !ok {
		return contracts.Goal{}, contracts.ErrGoalNotFound
	}
	if current.MaxGoalRounds > 0 && current.RoundsStarted >= current.MaxGoalRounds {
		current.Phase = contracts.GoalPhaseBlocked
		current.BlockedReason = &contracts.GoalBlockedReason{
			Code: contracts.GoalBlockedCodeRoundLimit, Message: contracts.GoalRoundLimitMessage,
		}
		current.Revision++
		f.goals[sessionID] = current
		return contracts.Goal{}, contracts.ErrGoalRoundLimit
	}
	current.RoundsStarted++
	current.Revision++
	f.goals[sessionID] = current
	return current, nil
}

func (f *fakeGoalService) Disarm(sessionID string) (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current, ok := f.goals[sessionID]
	if !ok {
		return contracts.Goal{}, false
	}
	current.Activation = contracts.GoalActivationDisarmed
	f.goals[sessionID] = current
	return current, true
}

func (f *fakeGoalService) Subscribe(fn func(sessionID string)) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	f.mu.Lock()
	f.subs = append(f.subs, fn)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for index, candidate := range f.subs {
			if fmt.Sprintf("%p", candidate) == fmt.Sprintf("%p", fn) {
				f.subs = append(f.subs[:index], f.subs[index+1:]...)
				return
			}
		}
	}, nil
}

// notifyLocked calls the subscribers with the lock released.
func (f *fakeGoalService) notifyLocked(sessionID string) {
	subs := make([]func(string), len(f.subs))
	copy(subs, f.subs)
	f.mu.Unlock()
	for _, sub := range subs {
		if sub != nil {
			sub(sessionID)
		}
	}
	f.mu.Lock()
}

func (f *fakeGoalService) creates() []fakeGoalCreate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeGoalCreate(nil), f.createCalls...)
}

func (f *fakeGoalService) updates() []fakeGoalUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeGoalUpdate(nil), f.updateCalls...)
}

func (f *fakeGoalService) clears() []contracts.GoalRef {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.GoalRef(nil), f.clearCalls...)
}

// --- harness ----------------------------------------------------------------

// newGoalHarness activates the session module with a scripted per-session
// GoalService (no settings backend).
func newGoalHarness(t *testing.T, chat *fakeChat) (*harness, *fakeGoalService) {
	t.Helper()
	return newGoalHarnessWithSettings(t, chat, nil)
}

// newGoalHarnessWithSettings is newGoalHarness with an optional settings store.
func newGoalHarnessWithSettings(t *testing.T, chat *fakeChat, settings contracts.SettingsService) (*harness, *fakeGoalService) {
	t.Helper()
	return newGoalHarnessCore(t, chat, settings, newFakeGoalService())
}

// newGoalHarnessWithService is newGoalHarness around a pre-seeded service.
func newGoalHarnessWithService(t *testing.T, chat *fakeChat, goals *fakeGoalService) (*harness, *fakeGoalService) {
	t.Helper()
	return newGoalHarnessCore(t, chat, nil, goals)
}

// newGoalHarnessCore activates the session module with one GoalService.
func newGoalHarnessCore(t *testing.T, chat *fakeChat, settings contracts.SettingsService, goals *fakeGoalService) (*harness, *fakeGoalService) {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if settings != nil {
		provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	}
	provide[contracts.GoalService](t, ctx, contracts.KeyGoal, goals)
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}, goals
}

// goalChat is a fake chat with one active conversation, so the goal dock above
// the composer has a screen to belong to.
func goalChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-goal", Title: "ship it"}, true)
	return chat
}
