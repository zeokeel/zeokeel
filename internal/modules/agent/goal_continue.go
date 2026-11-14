package agent

// Engine-side goal continuation (DSH goal rounds).
//
// The driver lives in the run lifecycle, not in a UI module: after a run settles
// successfully it re-reads the session's goal and, when the goal is active and
// armed, admits one durable round and starts the next run with origin=goal and a
// prompt carrying the objective and the round budget. Failures, cancellations,
// a disarmed/paused/blocked/complete goal and the round cap all stop the chain;
// a user Stop disarms the goal (agent.Cancel), so a stopped run never restarts.
//
// The human prompt inbox (promptqueue.go) always wins: settleRun drains it
// first and the driver re-checks that the inbox is empty, so no timing window
// is needed to keep a queued user message ahead of a goal round.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// maxGoalContinuationObjectiveRunes bounds the objective echoed into the
// continuation prompt (the goal text itself is already user-sized).
const maxGoalContinuationObjectiveRunes = 4000

// runOptions carries the engine-side run provenance of one send.
type runOptions struct {
	// origin overrides the derived run origin (goal continuation sets
	// contracts.AgentRunOriginGoal).
	origin string
	// goalRound marks the injected user message as an automatic continuation
	// round (nil for human messages).
	goalRound *contracts.GoalRoundInfo
}

// runOrigin resolves who started one run: an explicit origin (goal
// continuation) wins; otherwise the session's persisted provenance decides.
// Automation sessions and delegated children never masquerade as a user turn.
func (s *service) runOrigin(sessionID, explicit string) string {
	if origin := strings.TrimSpace(explicit); origin != "" {
		return origin
	}
	switch strings.TrimSpace(sessionOrigin(s.settings, sessionID)) {
	case contracts.SessionOriginAutomation:
		return contracts.AgentRunOriginAutomation
	case contracts.SessionOriginSubagent:
		return contracts.AgentRunOriginSubagent
	default:
		return contracts.AgentRunOriginUser
	}
}

// cloneGoalRound copies a continuation marker so the read model never shares a
// pointer with the driver.
func cloneGoalRound(round *contracts.GoalRoundInfo) *contracts.GoalRoundInfo {
	if round == nil {
		return nil
	}
	copied := *round
	return &copied
}

// userMessageEventPayload renders the durable user_message event: the prompt,
// its attachment names, the run origin and (for goal rounds) the continuation
// marker the UI uses to tell an injected round from a human message.
func userMessageEventPayload(prompt string, attachments []contracts.Attachment, origin string, goalRound *contracts.GoalRoundInfo) map[string]any {
	payload := map[string]any{
		"text":        prompt,
		"attachments": attachmentNames(attachments),
		"origin":      origin,
	}
	if goalRound != nil {
		payload["goalRound"] = map[string]any{
			"goalId":        goalRound.GoalID,
			"round":         goalRound.Round,
			"maxGoalRounds": goalRound.MaxGoalRounds,
		}
	}
	return payload
}

// continueGoalAfterRun is the goal-round driver. It runs on the settling run's
// goroutine right after finishWithReason; only a successful, non-cancelled run
// may continue, and only when this session's own goal is active and armed.
func (s *service) continueGoalAfterRun(sessionID, origin string, runErr error, runCtx context.Context) {
	if s == nil || s.goalService == nil {
		return
	}
	// DSH: no continuation after a failure, cancellation or timeout. Only a
	// direct human turn or a previous goal round may chain a continuation.
	if runErr != nil || (runCtx != nil && runCtx.Err() != nil) {
		return
	}
	if origin != contracts.AgentRunOriginUser && origin != contracts.AgentRunOriginGoal {
		return
	}
	// Human input wins over a goal round: an inbox with a waiting prompt means
	// the session must run that prompt first (settleRun normally drained it
	// already; this guards a concurrent enqueue or a drained-but-failed send).
	if s.queuedPromptCount(sessionID) > 0 {
		return
	}
	if s.sessionDeleted(sessionID) || !s.sessionIdle(sessionID) {
		return
	}
	goal, ok := s.goalService.Goal(sessionID)
	if !ok || goal.Phase != contracts.GoalPhaseActive || goal.Activation != contracts.GoalActivationArmed {
		return
	}
	admitted, err := s.goalService.AdmitRound(context.Background(), sessionID)
	if err != nil {
		if errors.Is(err, contracts.ErrGoalRoundLimit) {
			// AdmitRound already persisted phase blocked(code=round-limit); the
			// UI shows the stop reason. Not retried.
			s.logger.Printf("[agent] session %s goal %q reached its round limit", sessionID, goal.ID)
			s.notify(sessionID)
			return
		}
		if !errors.Is(err, contracts.ErrGoalInvalidTransition) && !errors.Is(err, contracts.ErrGoalNotFound) {
			s.logger.Printf("[agent] session %s: admit goal round: %v", sessionID, err)
		}
		return
	}
	round := &contracts.GoalRoundInfo{
		GoalID:        admitted.ID,
		Round:         admitted.RoundsStarted,
		MaxGoalRounds: admitted.MaxGoalRounds,
	}
	prompt := goalContinuationPrompt(admitted)
	if err := s.sendWithOptions(sessionID, prompt, nil, runOptions{
		origin:    contracts.AgentRunOriginGoal,
		goalRound: round,
	}); err != nil {
		s.logger.Printf("[agent] session %s: start goal round %d: %v", sessionID, admitted.RoundsStarted, err)
		s.notify(sessionID)
	}
}

// sessionIdle reports whether a session exists and has no run or manual
// compaction in flight: only then may a queued prompt or a goal round start.
func (s *service) sessionIdle(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	return ok && !state.running && !state.compacting
}

// goalContinuationPrompt renders the DSH continuation turn: the objective, the
// round budget and the lifecycle rules the model must follow.
func goalContinuationPrompt(goal contracts.Goal) string {
	objective := strings.Join(strings.Fields(goal.Objective), " ")
	objective = truncateRunes(objective, maxGoalContinuationObjectiveRunes)
	cap := goal.MaxGoalRounds
	if cap < 1 {
		cap = contracts.DefaultMaxGoalRounds
	}
	var builder strings.Builder
	builder.WriteString("Continue working toward the active goal.\n\n")
	fmt.Fprintf(&builder, "Goal: %s\n", objective)
	fmt.Fprintf(&builder, "Round %d/%d\n\n", goal.RoundsStarted, cap)
	builder.WriteString("Make concrete progress on this goal. Call get_goal before update_goal and copy the exact goal_id and revision; a stale pair is rejected instead of overwriting newer state. ")
	builder.WriteString("When the objective is achieved, call update_goal with action=complete. ")
	builder.WriteString("If progress genuinely stops, call update_goal with action=blocked and a concrete blocked_reason that has persisted across the rounds. ")
	builder.WriteString("Do not pause, create or resume goals from this round; those need a direct human turn.")
	return builder.String()
}
