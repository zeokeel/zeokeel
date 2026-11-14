package hosttools

// The DSH goal tools bridge: the product-owned get_goal/create_goal/update_goal
// tools (goaltools.go) call this revision-checked adapter, which maps them onto
// ZeoKeel's per-session durable contracts.GoalService. Rig v0.16.2 (the
// published dependency) has no ctx-carrying GoalRevision option, so the tools
// live in this product package instead of being the Rig host-tool set's; the
// model-visible names/schema/result JSON stay DSH-compatible. The adapter adds
// the DSH rules the service store does not know about:
//
//   - every call resolves the session from the agent session stamped into the
//     tool context (one process-wide tool instance serves every conversation);
//   - create/edit/pause/resume require a direct human turn (the run origin is
//     user or unstamped); goal-round/automation/subagent runs are rejected, so
//     the model can never create or resume a goal on its own;
//   - a blocked action reported from a goal-round run is only accepted after at
//     least MinGoalBlockedRounds starter rounds (the condition must have
//     persisted), and always requires a concrete reason.
//
// The service is resolved lazily at call time: an assembly may load the
// host-tools module before the goal backend, and a missing backend then produces
// a readable model-facing error instead of failing activation.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	righosttools "github.com/zeokeel/zeokeel-rig/tools/hosttools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// GoalStore is the ctx-carrying revision store the product DSH goal tools need.
// GoalRevisionStore implements it; the interface is product-owned (Rig v0.16.2's
// hosttools.GoalRevisionStore is context-less and no longer used).
type RevisionGoalStore interface {
	// CurrentGoal returns the ctx session's current goal.
	CurrentGoal(ctx context.Context) (righosttools.GoalSnapshot, bool)
	// CreateGoal creates a goal for the ctx session.
	CreateGoal(ctx context.Context, objective string, maxGoalRounds int) (righosttools.GoalSnapshot, error)
	// UpdateGoal applies one CAS update to the ctx session's goal.
	UpdateGoal(ctx context.Context, ref righosttools.GoalRef, update righosttools.GoalUpdate) (righosttools.GoalSnapshot, error)
}

// GoalRevisionStore adapts contracts.GoalService to the product DSH goal tools.
type GoalRevisionStore struct {
	// resolve returns the goal service (nil when no backend is assembled).
	resolve func() contracts.GoalService
	// defaultMaxGoalRounds is applied when a create request passes 0.
	defaultMaxGoalRounds int
	// minBlockedRounds is the minimum started-round count before a goal-round
	// turn may report blocked (0 uses contracts.MinGoalBlockedRounds).
	minBlockedRounds int
}

var _ RevisionGoalStore = (*GoalRevisionStore)(nil)

// NewGoalRevisionStore returns an adapter over a fixed goal service.
func NewGoalRevisionStore(goals contracts.GoalService) *GoalRevisionStore {
	return NewLazyGoalRevisionStore(func() contracts.GoalService { return goals })
}

// NewLazyGoalRevisionStore returns an adapter that resolves the goal service on
// every call. resolve may be nil (the tools then report an unavailable backend).
func NewLazyGoalRevisionStore(resolve func() contracts.GoalService) *GoalRevisionStore {
	return &GoalRevisionStore{resolve: resolve}
}

// CurrentGoal implements GoalStore: the current goal of
// the session carried by ctx.
func (s *GoalRevisionStore) CurrentGoal(ctx context.Context) (righosttools.GoalSnapshot, bool) {
	goals := s.goals()
	sessionID := strings.TrimSpace(contracts.AgentSessionFromContext(ctx))
	if goals == nil || sessionID == "" {
		return righosttools.GoalSnapshot{}, false
	}
	goal, ok := goals.Goal(sessionID)
	if !ok {
		return righosttools.GoalSnapshot{}, false
	}
	return rigGoalSnapshot(goal), true
}

// CreateGoal implements RevisionGoalStore. It needs a direct human
// turn: an automatic continuation, automation run or delegated child can never
// create a goal.
func (s *GoalRevisionStore) CreateGoal(ctx context.Context, objective string, maxGoalRounds int) (righosttools.GoalSnapshot, error) {
	goals, sessionID, err := s.goalTurn(ctx)
	if err != nil {
		return righosttools.GoalSnapshot{}, err
	}
	if err := requireDirectHumanTurn(ctx, "create_goal"); err != nil {
		return righosttools.GoalSnapshot{}, err
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return righosttools.GoalSnapshot{}, errors.New("create_goal: objective is required and must be non-empty after trimming")
	}
	if maxGoalRounds < 0 {
		return righosttools.GoalSnapshot{}, errors.New("create_goal: max_goal_rounds must be a positive integer")
	}
	rounds := maxGoalRounds
	if rounds <= 0 {
		rounds = s.defaultRounds()
	}
	goal, err := goals.CreateGoal(ctx, sessionID, objective, rounds)
	if err != nil {
		return righosttools.GoalSnapshot{}, err
	}
	return rigGoalSnapshot(goal), nil
}

// UpdateGoal implements RevisionGoalStore. The ref is the exact
// DSH compare-and-set identity; a stale pair is rejected by the goal service
// without overwriting newer state.
func (s *GoalRevisionStore) UpdateGoal(ctx context.Context, ref righosttools.GoalRef, update righosttools.GoalUpdate) (righosttools.GoalSnapshot, error) {
	goals, sessionID, err := s.goalTurn(ctx)
	if err != nil {
		return righosttools.GoalSnapshot{}, err
	}
	origin := contracts.AgentRunOriginFromContext(ctx)
	action := contracts.GoalAction(strings.ToLower(strings.TrimSpace(string(update.Action))))
	switch action {
	case contracts.GoalActionEdit, contracts.GoalActionPause, contracts.GoalActionResume:
		if err := requireDirectHumanTurn(ctx, fmt.Sprintf("update_goal action=%s", action)); err != nil {
			return righosttools.GoalSnapshot{}, err
		}
	case contracts.GoalActionComplete:
		// A goal-round run completes its own goal; that is the DSH path that
		// ends automatic continuation.
	case contracts.GoalActionBlocked:
		current, ok := goals.Goal(sessionID)
		if !ok {
			return righosttools.GoalSnapshot{}, fmt.Errorf("%w: session %q", contracts.ErrGoalNotFound, sessionID)
		}
		if origin == contracts.AgentRunOriginGoal && current.RoundsStarted < s.minRounds() {
			return righosttools.GoalSnapshot{}, fmt.Errorf(
				"update_goal: cannot report blocked from goal round %d; the blocking condition must persist for at least %d goal rounds before blocking (keep working or report the concrete blocking condition to the user)",
				current.RoundsStarted, s.minRounds())
		}
		if strings.TrimSpace(update.BlockedReason) == "" {
			return righosttools.GoalSnapshot{}, errors.New("update_goal: blocked requires a non-empty blocked_reason")
		}
	default:
		return righosttools.GoalSnapshot{}, fmt.Errorf("update_goal: unknown action %q (want edit, pause, resume, complete or blocked)", update.Action)
	}
	goal, err := goals.UpdateGoal(ctx, sessionID,
		contracts.GoalRef{ID: ref.ID, Revision: ref.Revision},
		contracts.GoalUpdate{
			Action:        action,
			Objective:     update.Objective,
			MaxGoalRounds: update.MaxGoalRounds,
			BlockedReason: update.BlockedReason,
		})
	if err != nil {
		return righosttools.GoalSnapshot{}, err
	}
	return rigGoalSnapshot(goal), nil
}

// goals resolves the goal service (nil when no backend is assembled).
func (s *GoalRevisionStore) goals() contracts.GoalService {
	if s == nil || s.resolve == nil {
		return nil
	}
	service := s.resolve()
	if service == nil {
		return nil
	}
	return service
}

// goalTurn resolves the service and the ctx session, returning the readable
// model-facing error when either is unavailable.
func (s *GoalRevisionStore) goalTurn(ctx context.Context) (contracts.GoalService, string, error) {
	goals := s.goals()
	if goals == nil {
		return nil, "", errors.New("goal tools are unavailable: no goal backend is assembled")
	}
	sessionID := strings.TrimSpace(contracts.AgentSessionFromContext(ctx))
	if sessionID == "" {
		return nil, "", errors.New("goal tools require a session-scoped agent run (no session is attached to this tool call)")
	}
	return goals, sessionID, nil
}

// defaultRounds is the round cap applied when create_goal passes 0.
func (s *GoalRevisionStore) defaultRounds() int {
	if s != nil && s.defaultMaxGoalRounds > 0 {
		return s.defaultMaxGoalRounds
	}
	return righosttools.DefaultMaxGoalRounds
}

// minRounds is the goal-round count required before a model-reported blocked
// action is accepted.
func (s *GoalRevisionStore) minRounds() int {
	if s != nil && s.minBlockedRounds > 0 {
		return s.minBlockedRounds
	}
	return contracts.MinGoalBlockedRounds
}

// requireDirectHumanTurn enforces DSH's "direct human turn" rule for the goal
// actions that must never be autonomous. An unstamped context is the direct
// host call (UI/command) path and is allowed.
func requireDirectHumanTurn(ctx context.Context, operation string) error {
	origin := strings.TrimSpace(contracts.AgentRunOriginFromContext(ctx))
	if contracts.IsDirectHumanAgentRun(origin) {
		return nil
	}
	return fmt.Errorf("%w: %s needs a direct human turn, but this run was started by %q; ask the user to perform it", contracts.ErrGoalDirectHumanRequired, operation, origin)
}

// rigGoalSnapshot renders the Rig-facing DSH view of one goal.
func rigGoalSnapshot(goal contracts.Goal) righosttools.GoalSnapshot {
	snapshot := righosttools.GoalSnapshot{
		ID:            goal.ID,
		Revision:      goal.Revision,
		Objective:     goal.Objective,
		Phase:         righosttools.GoalPhase(goal.Phase),
		RoundsStarted: goal.RoundsStarted,
		MaxGoalRounds: goal.MaxGoalRounds,
		Activation:    righosttools.GoalActivation(goal.Activation),
	}
	if snapshot.Activation == "" {
		snapshot.Activation = righosttools.GoalActivationDisarmed
	}
	if goal.BlockedReason != nil {
		snapshot.BlockedReason = &righosttools.GoalBlockedReason{
			Code:    goal.BlockedReason.Code,
			Message: goal.BlockedReason.Message,
		}
	}
	return snapshot
}
