// Package goal implements ZeoKeel's durable per-session goal backend (DSH
// Goal v2).
//
// The source of truth is the sessionstore event log: every accepted change
// appends one `goal/change` event carrying the complete goal snapshot (revision,
// objective, phase, rounds, cap, blocked reason). At startup and on the first
// access of a session the latest valid event is replayed, so a goal survives an
// application restart. No new database table is introduced. ClearGoal appends a
// revision-checked tombstone (the event history is preserved) that replay reads
// as "no goal".
//
// The durable session row is authoritative for existence: a goal commit first
// checks the session still exists, because AppendEvent would otherwise recreate
// a deleted session's row. Forget drops the process cache of a deleted session
// (and blocks replay for it) so a late driver/UI read cannot resurrect it.
//
// `Activation` (whether the engine-side continuation driver may advance the
// goal) is process-lifetime state and is deliberately NOT persisted: after a
// restart every goal is disarmed, and only a direct human create/resume can arm
// it again (DSH semantics). Pause/complete/blocked/user Stop disarm it too.
//
// The service is provided as contracts.KeyGoal and consumed by the agent
// module (continuation driver) and the host tools module (DSH
// get_goal/create_goal/update_goal). It is safe for concurrent use.
package goal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/goal"
	// goalEventType is the durable session event type of a goal snapshot.
	goalEventType = "goal/change"
)

// Config is the module's typed assembly config.
type Config struct {
	// DefaultMaxGoalRounds overrides the automatic continuation budget applied
	// when a create request passes 0. Zero (the default) uses
	// contracts.DefaultMaxGoalRounds (256, DSH's default).
	DefaultMaxGoalRounds int `json:"defaultMaxGoalRounds"`
}

// eventStore is the narrow view of the durable session log the goal backend
// needs. contracts.Store (KeyStore) satisfies it; tests use a tiny fake.
type eventStore interface {
	Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error)
	AppendEvent(ctx context.Context, event rig.SessionEvent) error
}

// sessionLookup is the optional durable session-existence check: appending a
// goal event to a deleted session would recreate its sessions row (AppendEvent
// calls ensureSessionTx), so every commit verifies the session first.
// contracts.Store satisfies it; a nil lookup (tests/embedders) skips the check.
type sessionLookup interface {
	GetSessionMeta(ctx context.Context, sessionID string) (contracts.SessionMeta, bool, error)
}

// goalSnapshot is the persisted payload of one goal/change event: a complete
// snapshot, so replay never needs to fold deltas.
type goalSnapshot struct {
	SessionID string `json:"sessionId"`
	// Cleared marks a ClearGoal tombstone: the goal was removed, the event
	// history stays intact, and replay reads "no goal". ID/Revision describe the
	// cleared instance (revision is the cleared revision + 1).
	Cleared       bool                         `json:"cleared,omitempty"`
	ID            string                       `json:"id"`
	Revision      int                          `json:"revision"`
	Objective     string                       `json:"objective"`
	Phase         contracts.GoalPhase          `json:"phase"`
	RoundsStarted int                          `json:"roundsStarted"`
	MaxGoalRounds int                          `json:"maxGoalRounds"`
	BlockedReason *contracts.GoalBlockedReason `json:"blockedReason,omitempty"`
	UpdatedAt     time.Time                    `json:"updatedAt"`
}

// goalState is the in-memory cached state of one session.
type goalState struct {
	// loaded records that the durable log was replayed for this session (the
	// goal fields are then authoritative).
	loaded bool
	goal   contracts.Goal
	// armed is the process-lifetime activation flag (never persisted).
	armed bool
}

// subscription is one Subscribe callback.
type subscription struct {
	fn func(sessionID string)
}

// service is the contracts.GoalService implementation.
type service struct {
	mu sync.Mutex
	// store is the durable session event log (nil keeps goals in memory only;
	// that mode is for tests/embedders, never the product).
	store eventStore
	// sessions is the optional durable session-existence lookup (contracts.Store
	// in the product). nil skips the deleted-session guard (tests/embedders).
	sessions sessionLookup
	logger   contracts.Logger
	// defaultMaxRounds is applied when a create request passes 0.
	defaultMaxRounds int

	goals map[string]*goalState
	// forgotten tombstones sessions whose in-memory goal cache was dropped after
	// the durable session was deleted: reads report "no goal" and replay is
	// blocked for the rest of the process, so a leftover event or a late caller
	// cannot resurrect a deleted conversation's goal.
	forgotten map[string]struct{}
	// nextGoalSeq names goal instances created by this process; the timestamp
	// prefix keeps ids unique across restarts (an old completed goal's id must
	// never be reused by a new instance while the old event history remains).
	nextGoalSeq atomic.Uint64
	subscribers []*subscription
}

var _ contracts.GoalService = (*service)(nil)

// newService builds the goal service; called by the module factory and tests.
// sessions is the optional durable session-existence lookup (nil = no check).
func newService(store eventStore, sessions sessionLookup, logger contracts.Logger, defaultMaxRounds int) *service {
	if logger == nil {
		logger = discardLogger{}
	}
	if defaultMaxRounds <= 0 {
		defaultMaxRounds = contracts.DefaultMaxGoalRounds
	}
	return &service{
		store:            store,
		sessions:         sessions,
		logger:           logger,
		defaultMaxRounds: defaultMaxRounds,
		goals:            make(map[string]*goalState),
		forgotten:        make(map[string]struct{}),
	}
}

// discardLogger drops goal log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// Goal implements contracts.GoalService.
func (s *service) Goal(sessionID string) (contracts.Goal, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.Goal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.ensureLoadedLocked(context.Background(), sessionID)
	if state == nil || state.goal.ID == "" {
		return contracts.Goal{}, false
	}
	return s.snapshotLocked(state), true
}

// CreateGoal implements contracts.GoalService.
func (s *service) CreateGoal(ctx context.Context, sessionID, objective string, maxGoalRounds int) (contracts.Goal, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.Goal{}, fmt.Errorf("%w: session id is required", contracts.ErrGoalNotFound)
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return contracts.Goal{}, errors.New("goal: objective is required and must be non-empty")
	}
	if maxGoalRounds < 0 {
		return contracts.Goal{}, errors.New("goal: maxGoalRounds must be a positive integer")
	}
	if err := ctx.Err(); err != nil {
		return contracts.Goal{}, err
	}
	rounds := maxGoalRounds
	if rounds <= 0 {
		rounds = s.defaultMaxRounds
	}

	s.mu.Lock()
	state := s.ensureLoadedLocked(ctx, sessionID)
	if state == nil {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: session %q was deleted", contracts.ErrGoalNotFound, sessionID)
	}
	if !state.loaded {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("goal: cannot read the durable goal state of session %q", sessionID)
	}
	if state.goal.ID != "" && state.goal.Phase != contracts.GoalPhaseComplete {
		existing := state.goal
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: session %q has goal %q in phase %q", contracts.ErrGoalConflict, sessionID, existing.ID, existing.Phase)
	}
	next := contracts.Goal{
		SessionID:     sessionID,
		ID:            s.newGoalID(),
		Revision:      1,
		Objective:     objective,
		Phase:         contracts.GoalPhaseActive,
		RoundsStarted: 0,
		MaxGoalRounds: rounds,
		UpdatedAt:     time.Now(),
	}
	if err := s.commitLocked(ctx, state, next, true); err != nil {
		s.mu.Unlock()
		return contracts.Goal{}, err
	}
	snapshot := s.snapshotLocked(state)
	s.mu.Unlock()
	s.notify(sessionID)
	return snapshot, nil
}

// UpdateGoal implements contracts.GoalService.
func (s *service) UpdateGoal(ctx context.Context, sessionID string, ref contracts.GoalRef, update contracts.GoalUpdate) (contracts.Goal, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.Goal{}, fmt.Errorf("%w: session id is required", contracts.ErrGoalNotFound)
	}
	if err := ctx.Err(); err != nil {
		return contracts.Goal{}, err
	}

	s.mu.Lock()
	state := s.ensureLoadedLocked(ctx, sessionID)
	if state == nil {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: session %q was deleted", contracts.ErrGoalNotFound, sessionID)
	}
	if !state.loaded {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("goal: cannot read the durable goal state of session %q", sessionID)
	}
	if state.goal.ID == "" {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: session %q", contracts.ErrGoalNotFound, sessionID)
	}
	current := state.goal
	if ref.ID != current.ID || ref.Revision != current.Revision {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: session %q wants %q revision %d; current is %q revision %d",
			contracts.ErrGoalStaleRef, sessionID, ref.ID, ref.Revision, current.ID, current.Revision)
	}
	next := current
	armed := state.armed
	switch update.Action {
	case contracts.GoalActionEdit:
		objective := strings.TrimSpace(update.Objective)
		if update.Objective != "" && objective == "" {
			s.mu.Unlock()
			return contracts.Goal{}, errors.New("goal: objective must be non-empty after trimming")
		}
		if update.MaxGoalRounds < 0 {
			s.mu.Unlock()
			return contracts.Goal{}, errors.New("goal: maxGoalRounds must be a positive integer")
		}
		if objective == "" && update.MaxGoalRounds == 0 {
			s.mu.Unlock()
			return contracts.Goal{}, errors.New("goal: edit requires objective and/or maxGoalRounds")
		}
		if update.BlockedReason != "" {
			s.mu.Unlock()
			return contracts.Goal{}, errors.New("goal: blockedReason is valid only with action blocked")
		}
		if objective != "" {
			next.Objective = objective
		}
		if update.MaxGoalRounds != 0 {
			next.MaxGoalRounds = update.MaxGoalRounds
		}
	case contracts.GoalActionPause:
		if current.Phase != contracts.GoalPhaseActive {
			s.mu.Unlock()
			return contracts.Goal{}, fmt.Errorf("%w: cannot pause goal %q from phase %q", contracts.ErrGoalInvalidTransition, current.ID, current.Phase)
		}
		next.Phase = contracts.GoalPhasePaused
		armed = false
	case contracts.GoalActionResume:
		switch current.Phase {
		case contracts.GoalPhaseActive:
			// Re-arm: a user Stop or a restart disarms the goal WITHOUT
			// changing its persisted phase, and the human "continue goal"
			// action arms it again. Activation is process state only, so this
			// is not a durable change (no event, no revision bump).
			if state.armed {
				s.mu.Unlock()
				return contracts.Goal{}, fmt.Errorf("%w: goal %q is already active and armed", contracts.ErrGoalInvalidTransition, current.ID)
			}
			state.armed = true
			snapshot := s.snapshotLocked(state)
			s.mu.Unlock()
			s.notify(sessionID)
			return snapshot, nil
		case contracts.GoalPhasePaused, contracts.GoalPhaseBlocked:
		default:
			s.mu.Unlock()
			return contracts.Goal{}, fmt.Errorf("%w: cannot resume goal %q from phase %q", contracts.ErrGoalInvalidTransition, current.ID, current.Phase)
		}
		if current.RoundsStarted >= current.MaxGoalRounds {
			s.mu.Unlock()
			return contracts.Goal{}, fmt.Errorf("%w: goal %q exhausted %d rounds; raise maxGoalRounds before resuming", contracts.ErrGoalInvalidTransition, current.ID, current.MaxGoalRounds)
		}
		next.Phase = contracts.GoalPhaseActive
		next.BlockedReason = nil
		armed = true
	case contracts.GoalActionComplete:
		switch current.Phase {
		case contracts.GoalPhaseActive, contracts.GoalPhasePaused, contracts.GoalPhaseBlocked:
		default:
			s.mu.Unlock()
			return contracts.Goal{}, fmt.Errorf("%w: cannot complete goal %q from phase %q", contracts.ErrGoalInvalidTransition, current.ID, current.Phase)
		}
		next.Phase = contracts.GoalPhaseComplete
		next.BlockedReason = nil
		armed = false
	case contracts.GoalActionBlocked:
		if current.Phase != contracts.GoalPhaseActive {
			s.mu.Unlock()
			return contracts.Goal{}, fmt.Errorf("%w: cannot block goal %q from phase %q", contracts.ErrGoalInvalidTransition, current.ID, current.Phase)
		}
		reason := strings.TrimSpace(update.BlockedReason)
		if reason == "" {
			s.mu.Unlock()
			return contracts.Goal{}, errors.New("goal: blocked requires a non-empty reason")
		}
		next.Phase = contracts.GoalPhaseBlocked
		next.BlockedReason = &contracts.GoalBlockedReason{Code: contracts.GoalBlockedCodeModelReported, Message: reason}
		armed = false
	default:
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: unknown action %q", contracts.ErrGoalInvalidTransition, update.Action)
	}
	next.Revision = current.Revision + 1
	next.UpdatedAt = time.Now()
	next.Activation = contracts.GoalActivationDisarmed
	if err := s.commitLocked(ctx, state, next, armed); err != nil {
		s.mu.Unlock()
		return contracts.Goal{}, err
	}
	snapshot := s.snapshotLocked(state)
	s.mu.Unlock()
	s.notify(sessionID)
	return snapshot, nil
}

// AdmitRound implements contracts.GoalService. At the cap it persists the
// goal as blocked (code round-limit) and returns ErrGoalRoundLimit: the round
// is never admitted, so the driver cannot exceed the budget.
func (s *service) AdmitRound(ctx context.Context, sessionID string) (contracts.Goal, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.Goal{}, fmt.Errorf("%w: session id is required", contracts.ErrGoalNotFound)
	}
	if err := ctx.Err(); err != nil {
		return contracts.Goal{}, err
	}

	s.mu.Lock()
	state := s.ensureLoadedLocked(ctx, sessionID)
	if state == nil {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: session %q was deleted", contracts.ErrGoalNotFound, sessionID)
	}
	if !state.loaded {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("goal: cannot read the durable goal state of session %q", sessionID)
	}
	if state.goal.ID == "" {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: session %q", contracts.ErrGoalNotFound, sessionID)
	}
	current := state.goal
	if current.Phase != contracts.GoalPhaseActive {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: goal %q is %q, not active", contracts.ErrGoalInvalidTransition, current.ID, current.Phase)
	}
	// Activation is checked IN the same critical section as the admission: a
	// user Stop/disarm that landed between the driver's Goal() read and this
	// call must not consume a round. A human resume (active+disarmed → resume
	// re-arms) is required before the driver may advance the goal again.
	if !state.armed {
		s.mu.Unlock()
		return contracts.Goal{}, fmt.Errorf("%w: goal %q is disarmed; resume it before admitting a round", contracts.ErrGoalInvalidTransition, current.ID)
	}
	if current.RoundsStarted >= current.MaxGoalRounds {
		next := current
		next.Phase = contracts.GoalPhaseBlocked
		next.BlockedReason = &contracts.GoalBlockedReason{
			Code:    contracts.GoalBlockedCodeRoundLimit,
			Message: fmt.Sprintf("%s (%d/%d)", contracts.GoalRoundLimitMessage, current.RoundsStarted, current.MaxGoalRounds),
		}
		next.Revision = current.Revision + 1
		next.UpdatedAt = time.Now()
		if err := s.commitLocked(ctx, state, next, false); err != nil {
			s.mu.Unlock()
			return contracts.Goal{}, err
		}
		s.mu.Unlock()
		s.notify(sessionID)
		return contracts.Goal{}, fmt.Errorf("%w: goal %q reached %d/%d", contracts.ErrGoalRoundLimit, current.ID, current.RoundsStarted, current.MaxGoalRounds)
	}
	next := current
	next.RoundsStarted = current.RoundsStarted + 1
	next.Revision = current.Revision + 1
	next.UpdatedAt = time.Now()
	if err := s.commitLocked(ctx, state, next, state.armed); err != nil {
		s.mu.Unlock()
		return contracts.Goal{}, err
	}
	snapshot := s.snapshotLocked(state)
	s.mu.Unlock()
	s.notify(sessionID)
	return snapshot, nil
}

// RollbackRound refunds one admitted continuation round whose run never
// started (BE-10, 2026-11 audit): AdmitRound persists RoundsStarted+1 BEFORE
// the driver starts the send, and a failed send used to leave the round
// consumed — the goal burned a round on a turn that never ran. The refund
// applies only when the goal is still active and still sits at exactly the
// admitted round; a goal that advanced, blocked or completed meanwhile keeps
// its durable truth (no-op). The refund is persisted as a new revision so a
// restart agrees, and the activation (armed/disarmed) is preserved.
func (s *service) RollbackRound(ctx context.Context, sessionID string, round int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: session id is required", contracts.ErrGoalNotFound)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	state := s.ensureLoadedLocked(ctx, sessionID)
	if state == nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: session %q was deleted", contracts.ErrGoalNotFound, sessionID)
	}
	if !state.loaded {
		s.mu.Unlock()
		return fmt.Errorf("goal: cannot read the durable goal state of session %q", sessionID)
	}
	current := state.goal
	if current.ID == "" {
		s.mu.Unlock()
		return fmt.Errorf("%w: session %q", contracts.ErrGoalNotFound, sessionID)
	}
	if round <= 0 || current.Phase != contracts.GoalPhaseActive || current.RoundsStarted != round {
		// Stale or moved-on: nothing to refund.
		s.mu.Unlock()
		return nil
	}
	next := current
	next.RoundsStarted = current.RoundsStarted - 1
	next.Revision = current.Revision + 1
	next.UpdatedAt = time.Now()
	if err := s.commitLocked(ctx, state, next, state.armed); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	s.notify(sessionID)
	return nil
}

// Disarm implements contracts.GoalService: it turns automatic continuation off
// in this process without touching the persisted phase/revision.
func (s *service) Disarm(sessionID string) (contracts.Goal, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.Goal{}, false
	}
	s.mu.Lock()
	state := s.ensureLoadedLocked(context.Background(), sessionID)
	if state == nil || !state.loaded || state.goal.ID == "" {
		s.mu.Unlock()
		return contracts.Goal{}, false
	}
	wasArmed := state.armed
	state.armed = false
	snapshot := s.snapshotLocked(state)
	s.mu.Unlock()
	if wasArmed {
		s.notify(sessionID)
	}
	return snapshot, true
}

// ClearGoal implements contracts.GoalService: a revision-checked tombstone that
// removes the current goal while preserving the durable event history. A stale
// ref changes nothing; a session without a goal reports (false, nil).
func (s *service) ClearGoal(ctx context.Context, sessionID string, ref contracts.GoalRef) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, fmt.Errorf("%w: session id is required", contracts.ErrGoalNotFound)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	s.mu.Lock()
	state := s.ensureLoadedLocked(ctx, sessionID)
	if state == nil {
		s.mu.Unlock()
		return false, fmt.Errorf("%w: session %q was deleted", contracts.ErrGoalNotFound, sessionID)
	}
	if !state.loaded {
		s.mu.Unlock()
		return false, fmt.Errorf("goal: cannot read the durable goal state of session %q", sessionID)
	}
	current := state.goal
	if current.ID == "" {
		s.mu.Unlock()
		return false, nil
	}
	if ref.ID != current.ID || ref.Revision != current.Revision {
		s.mu.Unlock()
		return false, fmt.Errorf("%w: session %q wants %q revision %d; current is %q revision %d",
			contracts.ErrGoalStaleRef, sessionID, ref.ID, ref.Revision, current.ID, current.Revision)
	}
	tombstone := goalSnapshot{
		SessionID: sessionID,
		ID:        current.ID,
		Revision:  current.Revision + 1,
		Cleared:   true,
		UpdatedAt: time.Now(),
	}
	if err := s.appendSnapshotLocked(ctx, tombstone); err != nil {
		s.mu.Unlock()
		return false, err
	}
	state.goal = contracts.Goal{}
	state.armed = false
	state.loaded = true
	s.mu.Unlock()
	s.notify(sessionID)
	return true, nil
}

// Forget drops the process cache and activation of a session whose durable
// session row was deleted (agent.DeleteSession calls it through an optional
// interface). It also tombstones the session for this process so a leftover goal
// event or a late driver read cannot resurrect the deleted conversation's goal.
// It is intentionally not part of contracts.GoalService: only the agent's
// delete path needs it.
func (s *service) Forget(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	delete(s.goals, sessionID)
	s.forgotten[sessionID] = struct{}{}
	s.mu.Unlock()
}

// CopyGoal copies one session's goal into another session (used by the agent's
// session fork). It is intentionally NOT part of contracts.GoalService: only the
// fork path needs it and the agent calls it through a type assertion.
//
// DSH semantics: the copy keeps the objective/phase/rounds/max/blocked reason,
// gets a fresh goal ID at revision 1 and is DISARMED (a forked session must not
// continue a goal without a human resume). The source goal (and its activation)
// is untouched. A session without a goal returns (zero, false, nil); a target
// with a non-complete goal is ErrGoalConflict; a missing target session is
// ErrGoalNotFound and nothing is written.
func (s *service) CopyGoal(ctx context.Context, fromSessionID, toSessionID string) (contracts.Goal, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fromSessionID = strings.TrimSpace(fromSessionID)
	toSessionID = strings.TrimSpace(toSessionID)
	if fromSessionID == "" || toSessionID == "" {
		return contracts.Goal{}, false, fmt.Errorf("%w: source and target session ids are required", contracts.ErrGoalNotFound)
	}
	if fromSessionID == toSessionID {
		return contracts.Goal{}, false, errors.New("goal: cannot copy a goal onto its own session")
	}
	if err := ctx.Err(); err != nil {
		return contracts.Goal{}, false, err
	}

	s.mu.Lock()
	sourceState := s.ensureLoadedLocked(ctx, fromSessionID)
	if sourceState == nil || !sourceState.loaded {
		s.mu.Unlock()
		return contracts.Goal{}, false, fmt.Errorf("goal: cannot read the durable goal state of session %q", fromSessionID)
	}
	if sourceState.goal.ID == "" {
		s.mu.Unlock()
		return contracts.Goal{}, false, nil
	}
	targetState := s.ensureLoadedLocked(ctx, toSessionID)
	if targetState == nil {
		s.mu.Unlock()
		return contracts.Goal{}, false, fmt.Errorf("%w: target session %q was deleted", contracts.ErrGoalNotFound, toSessionID)
	}
	if !targetState.loaded {
		s.mu.Unlock()
		return contracts.Goal{}, false, fmt.Errorf("goal: cannot read the durable goal state of session %q", toSessionID)
	}
	if targetState.goal.ID != "" && targetState.goal.Phase != contracts.GoalPhaseComplete {
		existing := targetState.goal
		s.mu.Unlock()
		return contracts.Goal{}, false, fmt.Errorf("%w: target session %q already has goal %q in phase %q",
			contracts.ErrGoalConflict, toSessionID, existing.ID, existing.Phase)
	}
	source := sourceState.goal
	next := contracts.Goal{
		SessionID:     toSessionID,
		ID:            s.newGoalID(),
		Revision:      1,
		Objective:     source.Objective,
		Phase:         source.Phase,
		RoundsStarted: source.RoundsStarted,
		MaxGoalRounds: source.MaxGoalRounds,
		UpdatedAt:     time.Now(),
	}
	if source.BlockedReason != nil {
		reason := *source.BlockedReason
		next.BlockedReason = &reason
	}
	if err := s.commitLocked(ctx, targetState, next, false); err != nil {
		s.mu.Unlock()
		return contracts.Goal{}, false, err
	}
	snapshot := s.snapshotLocked(targetState)
	s.mu.Unlock()
	s.notify(toSessionID)
	return snapshot, true, nil
}

// Subscribe implements contracts.GoalService.
func (s *service) Subscribe(fn func(sessionID string)) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	listener := &subscription{fn: fn}
	s.mu.Lock()
	s.subscribers = append(s.subscribers, listener)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for index, candidate := range s.subscribers {
				if candidate == listener {
					s.subscribers = append(s.subscribers[:index], s.subscribers[index+1:]...)
					return
				}
			}
		})
	}, nil
}

// --- durable log ------------------------------------------------------------

// ensureLoadedLocked replays the durable goal log for sessionID once. The caller
// holds s.mu; a read failure is logged and leaves the cached state empty (the
// next access retries the replay). It returns nil when the session id is empty.
func (s *service) ensureLoadedLocked(ctx context.Context, sessionID string) *goalState {
	if sessionID == "" {
		return nil
	}
	if _, forgotten := s.forgotten[sessionID]; forgotten {
		// A deleted session's goal must never come back (not even from a
		// leftover event): every read reports "no goal" for the rest of the
		// process.
		return nil
	}
	state, ok := s.goals[sessionID]
	if ok && state.loaded {
		return state
	}
	if state == nil {
		state = &goalState{}
		s.goals[sessionID] = state
	}
	if s.store == nil {
		state.loaded = true
		return state
	}
	if ctx == nil {
		ctx = context.Background()
	}
	events, err := s.store.Events(ctx, sessionID, 0, 0)
	if err != nil {
		s.logger.Printf("[goal] replay %s: %v", sessionID, err)
		return state
	}
	for _, event := range events {
		if event.Type != goalEventType {
			continue
		}
		snapshot, ok := decodeGoalSnapshot(event.Payload)
		if !ok {
			s.logger.Printf("[goal] replay %s: skipping malformed %s event", sessionID, goalEventType)
			continue
		}
		if snapshot.Cleared {
			// ClearGoal tombstone: the history is preserved but the newest
			// event says there is no goal. A later CreateGoal appends a fresh
			// instance that wins the replay again.
			state.goal = contracts.Goal{}
			continue
		}
		state.goal = replayGoal(sessionID, snapshot)
	}
	state.loaded = true
	return state
}

// decodeGoalSnapshot decodes one persisted snapshot; malformed payloads are
// skipped rather than failing the replay (a corrupt event must not hide an
// older valid one).
func decodeGoalSnapshot(payload []byte) (goalSnapshot, bool) {
	if len(payload) == 0 {
		return goalSnapshot{}, false
	}
	var snapshot goalSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return goalSnapshot{}, false
	}
	if snapshot.Cleared {
		// A ClearGoal tombstone is valid even though it removes the goal.
		return snapshot, true
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		return goalSnapshot{}, false
	}
	return snapshot, true
}

// replayGoal converts a persisted snapshot back into a cached goal. Activation
// is never replayed: every goal loaded after a restart is disarmed.
func replayGoal(sessionID string, snapshot goalSnapshot) contracts.Goal {
	revision := snapshot.Revision
	if revision < 1 {
		revision = 1
	}
	maxRounds := snapshot.MaxGoalRounds
	if maxRounds < 1 {
		maxRounds = contracts.DefaultMaxGoalRounds
	}
	phase := snapshot.Phase
	if phase == "" {
		phase = contracts.GoalPhaseActive
	}
	goal := contracts.Goal{
		SessionID:     sessionID,
		ID:            strings.TrimSpace(snapshot.ID),
		Revision:      revision,
		Objective:     strings.TrimSpace(snapshot.Objective),
		Phase:         phase,
		RoundsStarted: snapshot.RoundsStarted,
		MaxGoalRounds: maxRounds,
		BlockedReason: snapshot.BlockedReason,
		UpdatedAt:     snapshot.UpdatedAt,
	}
	if goal.RoundsStarted < 0 {
		goal.RoundsStarted = 0
	}
	if goal.Phase != contracts.GoalPhaseBlocked {
		goal.BlockedReason = nil
	}
	return goal
}

// commitLocked persists next and, only after the durable append succeeds,
// publishes it into the cached state. The caller holds s.mu; a persist failure
// leaves both the cache and the revision untouched (the caller reports it).
func (s *service) commitLocked(ctx context.Context, state *goalState, next contracts.Goal, armed bool) error {
	if err := s.appendSnapshotLocked(ctx, goalSnapshot{
		SessionID:     next.SessionID,
		ID:            next.ID,
		Revision:      next.Revision,
		Objective:     next.Objective,
		Phase:         next.Phase,
		RoundsStarted: next.RoundsStarted,
		MaxGoalRounds: next.MaxGoalRounds,
		BlockedReason: next.BlockedReason,
		UpdatedAt:     next.UpdatedAt,
	}); err != nil {
		return err
	}
	state.goal = next
	state.loaded = true
	state.armed = armed
	return nil
}

// appendSnapshotLocked appends one goal/change event after verifying the durable
// session still exists. The existence check is essential: AppendEvent recreates
// a deleted session's row (ensureSessionTx), so a goal event for a deleted
// conversation would resurrect it. A missing session returns ErrGoalNotFound
// and nothing is written. The caller holds s.mu.
func (s *service) appendSnapshotLocked(ctx context.Context, snapshot goalSnapshot) error {
	if err := s.requireSessionLocked(ctx, snapshot.SessionID); err != nil {
		return err
	}
	if s.store == nil {
		return nil
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("goal: encode snapshot of session %q: %w", snapshot.SessionID, err)
	}
	eventTime := snapshot.UpdatedAt
	if eventTime.IsZero() {
		eventTime = time.Now()
	}
	if err := s.store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: snapshot.SessionID,
		Type:      goalEventType,
		Time:      eventTime,
		Payload:   payload,
	}); err != nil {
		return fmt.Errorf("goal: persist goal of session %q: %w", snapshot.SessionID, err)
	}
	return nil
}

// requireSessionLocked fails closed when the durable session no longer exists
// (deleted conversation). A nil lookup keeps the historical in-memory behavior
// for tests/embedders; a lookup error is returned so the caller does not write
// a goal event it cannot verify. The caller holds s.mu.
func (s *service) requireSessionLocked(ctx context.Context, sessionID string) error {
	if s.sessions == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, found, err := s.sessions.GetSessionMeta(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("goal: check session %q: %w", sessionID, err)
	}
	if !found {
		return fmt.Errorf("%w: session %q was deleted", contracts.ErrGoalNotFound, sessionID)
	}
	return nil
}

// snapshotLocked builds the caller-facing view of one cached goal: the
// persisted fields plus the process-lifetime activation observation.
func (s *service) snapshotLocked(state *goalState) contracts.Goal {
	goal := state.goal
	goal.Activation = contracts.GoalActivationDisarmed
	if state.armed && goal.Phase == contracts.GoalPhaseActive {
		goal.Activation = contracts.GoalActivationArmed
	}
	if goal.Phase == contracts.GoalPhaseBlocked && goal.BlockedReason != nil {
		reason := *goal.BlockedReason
		goal.BlockedReason = &reason
	} else if goal.Phase != contracts.GoalPhaseBlocked {
		goal.BlockedReason = nil
	}
	return goal
}

// newGoalID returns a goal instance id unique across restarts.
func (s *service) newGoalID() string {
	return fmt.Sprintf("goal-%d-%d", time.Now().UnixNano(), s.nextGoalSeq.Add(1))
}

// notify calls every subscriber outside the service lock; a panicking callback
// is contained so a UI bug cannot break goal mutations.
func (s *service) notify(sessionID string) {
	s.mu.Lock()
	subscribers := append([]*subscription(nil), s.subscribers...)
	s.mu.Unlock()
	for _, listener := range subscribers {
		if listener == nil || listener.fn == nil {
			continue
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					s.logger.Printf("[goal] subscriber panicked: %v", recovered)
				}
			}()
			listener.fn(sessionID)
		}()
	}
}

// --- module -----------------------------------------------------------------

// Module is the goal backend descriptor. It provides contracts.KeyGoal and
// requires the durable session store (contracts.KeyStore).
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		store, err := ctx.Require(contracts.KeyStore)
		if err != nil {
			return fmt.Errorf("%s: require session store: %w", ModuleID, err)
		}
		var logger contracts.Logger = discardLogger{}
		if service, ok := ctx.Get(contracts.KeyLogger); ok && service != nil {
			logger = service
		}
		goalService := newService(store, store, logger, cfg.DefaultMaxGoalRounds)
		if _, err := ctx.Provide(contracts.KeyGoal, contracts.GoalService(goalService)); err != nil {
			return fmt.Errorf("%s: provide goal service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyGoal)).
		AddRequires(module.Requires(contracts.KeyStore)).
		WithName("ZeoKeel goal backend")
}
