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
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// GoalPhase is the DSH persistent lifecycle phase of a session goal.
type GoalPhase string

const (
	// GoalPhaseActive is the working phase: the continuation driver may advance
	// the goal.
	GoalPhaseActive GoalPhase = "active"
	// GoalPhasePaused is a user pause: visible, never advanced automatically.
	GoalPhasePaused GoalPhase = "paused"
	// GoalPhaseBlocked records that progress stopped for a concrete reason
	// (model-reported or the round limit).
	GoalPhaseBlocked GoalPhase = "blocked"
	// GoalPhaseComplete is the terminal phase.
	GoalPhaseComplete GoalPhase = "complete"
)

// GoalActivation is the DSH live continuation state. It is an observation of
// this process, not persisted state.
type GoalActivation string

const (
	// GoalActivationArmed means the continuation driver may advance the goal.
	GoalActivationArmed GoalActivation = "armed"
	// GoalActivationDisarmed means automatic continuation is off until a human
	// resume (or a new goal) re-arms it.
	GoalActivationDisarmed GoalActivation = "disarmed"
)

// GoalBlockedReason is the machine-routable and human-readable explanation
// attached to a blocked goal.
type GoalBlockedReason struct {
	// Code is a stable lower-kebab-case category.
	Code string `json:"code"`
	// Message is the concrete explanation.
	Message string `json:"message"`
}

// Stable blocked-reason codes.
const (
	// GoalBlockedCodeRoundLimit is written when automatic continuation reached
	// the goal's round cap.
	GoalBlockedCodeRoundLimit = "round-limit"
	// GoalBlockedCodeModelReported is written when the model reported the
	// blocking condition itself.
	GoalBlockedCodeModelReported = "model-reported"
)

const (
	// DefaultMaxGoalRounds is the DSH default automatic continuation budget for
	// one goal instance (create_goal may override it).
	DefaultMaxGoalRounds = 256
	// MinGoalBlockedRounds is the minimum number of started goal rounds before a
	// model-reported blocked action from a goal-round turn is accepted (DSH
	// requires the condition to persist across at least three rounds).
	MinGoalBlockedRounds = 3
	// GoalRoundLimitMessage is the blocked_reason message written when the round
	// cap is reached.
	GoalRoundLimitMessage = "the goal reached its automatic continuation round limit"
)

// Goal is the current goal of one chat session.
type Goal struct {
	// SessionID is the conversation that owns this goal.
	SessionID string `json:"sessionId,omitempty"`
	// ID identifies one goal instance; update_goal compare-and-set uses it.
	ID string `json:"id"`
	// Revision is the monotonic compare-and-set token of the persisted snapshot.
	Revision int `json:"revision"`
	// Objective is the concrete completion objective.
	Objective string `json:"objective,omitempty"`
	// Phase is the lifecycle phase.
	Phase GoalPhase `json:"phase"`
	// RoundsStarted counts automatic continuation rounds that started.
	RoundsStarted int `json:"roundsStarted"`
	// MaxGoalRounds is this goal's automatic continuation budget.
	MaxGoalRounds int `json:"maxGoalRounds"`
	// BlockedReason is set while Phase is GoalPhaseBlocked.
	BlockedReason *GoalBlockedReason `json:"blockedReason,omitempty"`
	// Activation reports whether the engine-side driver may continue the goal
	// in this process.
	Activation GoalActivation `json:"activation,omitempty"`
	// UpdatedAt is when the persisted snapshot last changed.
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// GoalRef is the exact compare-and-set identity update_goal requires.
type GoalRef struct {
	// ID must equal the current goal's ID.
	ID string
	// Revision must equal the current goal's Revision.
	Revision int
}

// GoalAction is one validated goal lifecycle mutation.
type GoalAction string

const (
	// GoalActionEdit replaces the objective and/or the round cap.
	GoalActionEdit GoalAction = "edit"
	// GoalActionPause moves an active goal to paused.
	GoalActionPause GoalAction = "pause"
	// GoalActionResume moves a paused/blocked goal back to active (human path).
	GoalActionResume GoalAction = "resume"
	// GoalActionComplete moves a non-terminal goal to complete.
	GoalActionComplete GoalAction = "complete"
	// GoalActionBlocked records a concrete blocking condition.
	GoalActionBlocked GoalAction = "blocked"
)

// GoalUpdate is one validated goal mutation. Zero values mean "omitted":
// Objective and MaxGoalRounds are only meaningful with GoalActionEdit, and
// BlockedReason is required with GoalActionBlocked.
type GoalUpdate struct {
	// Action is the mutation to apply.
	Action GoalAction
	// Objective replaces the goal text (edit only).
	Objective string
	// MaxGoalRounds replaces the round cap (edit only; 0 = keep).
	MaxGoalRounds int
	// BlockedReason is the concrete blocking condition (blocked only).
	BlockedReason string
}

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

// Goal errors. Callers classify with errors.Is; the goal backend wraps these
// sentinels with session/goal context.
var (
	// ErrGoalNotFound means the session has no goal (or no goal service is
	// wired into this product).
	ErrGoalNotFound = errors.New("goal: no goal for this session")
	// ErrGoalStaleRef means the caller's GoalRef no longer matches the current
	// id/revision (another change landed first); nothing was overwritten.
	ErrGoalStaleRef = errors.New("goal: stale goal reference")
	// ErrGoalInvalidTransition means the phase does not allow the action.
	ErrGoalInvalidTransition = errors.New("goal: invalid goal transition")
	// ErrGoalRoundLimit means the automatic continuation budget is exhausted;
	// the goal was persisted as blocked with code round-limit.
	ErrGoalRoundLimit = errors.New("goal: goal round limit reached")
	// ErrGoalConflict means a non-complete goal already exists in the session.
	ErrGoalConflict = errors.New("goal: a goal already exists for this session")
	// ErrGoalDirectHumanRequired means the caller is not a direct human turn
	// (goal-round, automation or subagent run) and the action needs one.
	ErrGoalDirectHumanRequired = errors.New("goal: this action requires a direct human turn")
)

// GoalService is the per-session durable goal service provided by the goal
// backend module (contracts.KeyGoal). It is safe for concurrent use.
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
