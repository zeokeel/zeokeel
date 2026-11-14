package regression

// Scripted per-session GoalService and the session-UI starter used by the Goal
// v2 regression tests. The real backend has its own unit tests; this fake only
// lets the audit drive the UI through the contract (one goal per session, CAS
// revisions, phase transitions, round cap, subscribe) and inspect the calls.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

var _ contracts.GoalService = (*fakeGoalService)(nil)

type fakeGoalService struct {
	mu    sync.Mutex
	goals map[string]contracts.Goal
	next  int
	subs  []func(string)

	creates     int
	updates     []contracts.GoalUpdate
	clears      int
	createErr   error
	updateErr   error
	clearErr    error
	admitRounds int
}

func newRegressionGoalService() *fakeGoalService {
	return &fakeGoalService{goals: make(map[string]contracts.Goal)}
}

func (f *fakeGoalService) seed(sessionID string, phase contracts.GoalPhase,
	activation contracts.GoalActivation, rounds, maxGoalRounds int) contracts.Goal {
	f.mu.Lock()
	goal := f.putLocked(sessionID, phase, activation, rounds, maxGoalRounds)
	f.notifyLocked(sessionID)
	f.mu.Unlock()
	return goal
}

func (f *fakeGoalService) putLocked(sessionID string, phase contracts.GoalPhase,
	activation contracts.GoalActivation, rounds, maxGoalRounds int) contracts.Goal {
	f.next++
	if maxGoalRounds <= 0 {
		maxGoalRounds = contracts.DefaultMaxGoalRounds
	}
	goal := contracts.Goal{
		SessionID:     sessionID,
		ID:            fmt.Sprintf("goal-%d", f.next),
		Revision:      1,
		Objective:     "ship the audit",
		Phase:         phase,
		RoundsStarted: rounds,
		MaxGoalRounds: maxGoalRounds,
		Activation:    activation,
	}
	f.goals[sessionID] = goal
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
	f.creates++
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
		SessionID: sessionID, ID: fmt.Sprintf("goal-%d", f.next), Revision: 1,
		Objective: objective, Phase: contracts.GoalPhaseActive,
		MaxGoalRounds: maxGoalRounds, Activation: contracts.GoalActivationArmed,
	}
	f.goals[sessionID] = goal
	f.notifyLocked(sessionID)
	return goal, nil
}

func (f *fakeGoalService) UpdateGoal(_ context.Context, sessionID string, ref contracts.GoalRef, update contracts.GoalUpdate) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, update)
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
		if current.Phase == contracts.GoalPhaseActive {
			if current.Activation == contracts.GoalActivationArmed {
				return contracts.Goal{}, contracts.ErrGoalInvalidTransition
			}
			next.Activation = contracts.GoalActivationArmed
		} else if current.Phase == contracts.GoalPhasePaused || current.Phase == contracts.GoalPhaseBlocked {
			if current.MaxGoalRounds > 0 && current.RoundsStarted >= current.MaxGoalRounds {
				return contracts.Goal{}, contracts.ErrGoalInvalidTransition
			}
			next.Phase = contracts.GoalPhaseActive
			next.BlockedReason = nil
			next.Activation = contracts.GoalActivationArmed
			next.Revision++
		} else {
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
	f.clears++
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
	f.admitRounds++
	current, ok := f.goals[sessionID]
	if !ok {
		return contracts.Goal{}, contracts.ErrGoalNotFound
	}
	if current.RoundsStarted >= current.MaxGoalRounds {
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

// notifyLocked calls subscribers with the lock released.
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

func (f *fakeGoalService) goal(sessionID string) (contracts.Goal, bool) {
	return f.Goal(sessionID)
}

func (f *fakeGoalService) updateCalls() []contracts.GoalUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.GoalUpdate(nil), f.updates...)
}

func (f *fakeGoalService) clearCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clears
}

// goalHarnessUI is the session screen driven by the goal audit.
type goalHarnessUI struct {
	h      *harness
	chat   *fakeChat
	goals  *fakeGoalService
	tester *ui.Tester
}

// startGoalUI activates the session screen with a scripted per-session goal
// service, so the dock can be driven through the real UI.
func startGoalUI(t *testing.T, goals *fakeGoalService, sessions ...contracts.ChatSession) *goalHarnessUI {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	for index, chatSession := range sessions {
		chat.addSession(chatSession, index == 0)
	}
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyFileDrop, contracts.FileDropService(h.ui.FileDrop()))
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	provide(t, h.ctx, contracts.KeyGoal, contracts.GoalService(goals))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()
	return &goalHarnessUI{h: h, chat: chat, goals: goals, tester: tester}
}
