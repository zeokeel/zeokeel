package contracts

// DSH-aligned goal contract (Goal v2).
//
// A goal is per-session and durable: the goal backend module stores one
// complete snapshot per change as a `goal/change` session event and replays the
// latest one at startup. `Revision` is a compare-and-set token: every accepted
// change bumps it, and a caller that passes a stale GoalRef is rejected instead
// of overwriting newer state (DSH update_goal semantics).
//
// `Activation` is a process-lifetime observation, not durable state: after a
// restart (and after pause/complete/blocked/user Stop) the goal is disarmed, so
// the engine-side continuation driver never resumes an old goal on its own. A
// direct human create/resume re-arms it.

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
	riggoal "github.com/zeokeel/zeokeel-rig/session/goal"
)

// The goal value types come from the SDK goal service; these aliases keep the
// contract names stable for the UI, agent and host tools.
type (
	GoalPhase         = riggoal.Phase
	GoalActivation    = riggoal.Activation
	GoalBlockedReason = riggoal.BlockedReason
	Goal              = riggoal.Goal
	GoalRef           = riggoal.Ref
	GoalAction        = riggoal.Action
	GoalUpdate        = riggoal.Update
)

const (
	GoalPhaseActive   = riggoal.PhaseActive
	GoalPhasePaused   = riggoal.PhasePaused
	GoalPhaseBlocked  = riggoal.PhaseBlocked
	GoalPhaseComplete = riggoal.PhaseComplete

	GoalActivationArmed    = riggoal.ActivationArmed
	GoalActivationDisarmed = riggoal.ActivationDisarmed

	GoalBlockedCodeRoundLimit    = riggoal.BlockedCodeRoundLimit
	GoalBlockedCodeModelReported = riggoal.BlockedCodeModelReported

	DefaultMaxGoalRounds  = riggoal.DefaultMaxGoalRounds
	MinGoalBlockedRounds  = riggoal.MinGoalBlockedRounds
	GoalRoundLimitMessage = riggoal.RoundLimitMessage

	GoalActionEdit     = riggoal.ActionEdit
	GoalActionPause    = riggoal.ActionPause
	GoalActionResume   = riggoal.ActionResume
	GoalActionComplete = riggoal.ActionComplete
	GoalActionBlocked  = riggoal.ActionBlocked
)

// GoalRoundInfo marks a user message that the engine-side goal driver injected
// as an automatic continuation round (nil for messages the human sent).
type GoalRoundInfo struct {
	// GoalID is the goal instance the round belongs to.
	GoalID string `json:"goalId,omitempty"`
	// Round is the 1-based continuation round number.
	Round int `json:"round"`
	// MaxGoalRounds is the goal's round cap at the time the round started.
	MaxGoalRounds int `json:"maxGoalRounds,omitempty"`
}

// Goal errors. Callers classify with errors.Is; the SDK sentinels carry the
// same identities.
var (
	ErrGoalNotFound            = riggoal.ErrGoalNotFound
	ErrGoalStaleRef            = riggoal.ErrGoalStaleRef
	ErrGoalInvalidTransition   = riggoal.ErrGoalInvalidTransition
	ErrGoalRoundLimit          = riggoal.ErrGoalRoundLimit
	ErrGoalConflict            = riggoal.ErrGoalConflict
	ErrGoalDirectHumanRequired = riggoal.ErrGoalDirectHumanRequired
)

type GoalService interface {
	// Goal returns the session's current goal; ok is false when none exists.
	Goal(sessionID string) (Goal, bool)
	// CreateGoal starts a new active, armed goal. maxGoalRounds <= 0 applies
	// DefaultMaxGoalRounds; a non-complete existing goal is ErrGoalConflict and
	// a complete one is replaced by a new instance. CreateGoal arms the goal.
	CreateGoal(ctx context.Context, sessionID, objective string, maxGoalRounds int) (Goal, error)
	// UpdateGoal applies update to the exact ref. A stale ref returns
	// ErrGoalStaleRef without changing anything. Every accepted change bumps
	// Revision and persists a full snapshot.
	UpdateGoal(ctx context.Context, sessionID string, ref GoalRef, update GoalUpdate) (Goal, error)
	// ClearGoal removes the current goal with a revision-checked tombstone: a
	// stale ref returns ErrGoalStaleRef and nothing is removed, and a session
	// without a goal reports (false, nil). The durable goal/change event carries
	// the tombstone, so a replay after restart also reads "no goal" and a later
	// CreateGoal starts a fresh instance (new id, revision 1).
	ClearGoal(ctx context.Context, sessionID string, ref GoalRef) (bool, error)
	// AdmitRound durably counts one automatic continuation round. At the cap it
	// persists phase blocked (code round-limit) and returns ErrGoalRoundLimit
	// instead of admitting the round.
	AdmitRound(ctx context.Context, sessionID string) (Goal, error)
	// Disarm turns automatic continuation off for this process without changing
	// the persisted phase (user Stop). ok is false when the session has no goal.
	Disarm(sessionID string) (Goal, bool)
	// Subscribe registers a change notification callback (invoked with the
	// session ID) and returns its unsubscriber.
	Subscribe(fn func(sessionID string)) (unsub func(), err error)
}

// KeyGoal is provided by the goal backend module and consumed by the agent
// (continuation driver), the session UI and the host tools (DSH
// get_goal/create_goal/update_goal).
var KeyGoal = cordis.NewServiceKey[GoalService]("zeokeel/goal")
