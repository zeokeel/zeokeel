package agent

// Restore-only metadata hydration for the DSH long-task surfaces:
//
//   - the durable prompt inbox (queue/change snapshots) is replayed so a
//     restart keeps prompts that were waiting for a running session;
//   - the goal-round marker of restored user messages is recovered from the
//     durable user_message events (the Rig history has no such field), matched
//     in order and only when the texts agree, so a rewound/compacted history
//     never mislabels a human message;
//   - an interrupted run (the durable checkpoint is still requested/
//     tool_pending, e.g. the app crashed mid-turn) is surfaced as
//     ChatSession.Interrupted, a read-model hint for the UI.
//
// The event window is bounded: restore reads the newest events of each session
// (the same order of magnitude as the history it just loaded), not the whole
// log. Goal rounds and a crashed inbox are recent by nature.

import (
	"context"
	"encoding/json"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	obs "github.com/zeokeel/zeokeel-rig/session/observability"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// restoreMetadataEventWindow bounds the per-session event scan during restore.
const restoreMetadataEventWindow = 4000

// restoredGoalRound is one durable goal-round user_message event.
type restoredGoalRound struct {
	text  string
	round *contracts.GoalRoundInfo
}

// restoreSessionMetadata replays the durable prompt inbox and the goal-round
// markers onto one freshly restored session. It runs during restore, before the
// state is published into s.sessions, so it needs no session lock (the queue
// map has its own lock).
func (s *service) restoreSessionMetadata(ctx context.Context, sessionID string, state *sessionState) {
	if s.store == nil || state == nil {
		return
	}
	events, err := s.store.Last(ctx, sessionID, restoreMetadataEventWindow)
	if err != nil {
		s.logger.Printf("[agent] restore: recent events of %s: %v", sessionID, err)
		return
	}
	var (
		queued        []contracts.QueuedPrompt
		rounds        []restoredGoalRound
		turns         []restoredTurnTime
		observability []rig.SessionEvent
		hasQueueRow   bool
	)
	for _, event := range events {
		switch {
		case event.Type == queueEventType:
			prompts, ok := decodeQueuedPrompts(event.Payload)
			if !ok {
				continue
			}
			hasQueueRow = true
			queued = prompts
		case obs.ObservedEvent(event.Type), event.Type == compactArchiveEventType,
			event.Type == contextRecoveryEventType:
			observability = append(observability, event)
		case event.Type == restoreUserMessageEvent:
			var payload struct {
				Text      string                   `json:"text"`
				GoalRound *contracts.GoalRoundInfo `json:"goalRound"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			turns = append(turns, restoredTurnTime{text: payload.Text, userAt: event.Time})
			if payload.GoalRound != nil {
				rounds = append(rounds, restoredGoalRound{text: payload.Text, round: payload.GoalRound})
			}
		case event.Type == restoreRunEndEvent:
			// The run_end ends the turn its user_message opened; a run_end whose
			// user_message fell outside the bounded window has no turn to end.
			if len(turns) > 0 {
				turns[len(turns)-1].endAt = event.Time
			}
		}
	}
	if hasQueueRow && len(queued) > 0 {
		s.restoreQueuedPrompts(sessionID, queued)
	}
	s.foldRestoredContextEvents(sessionID, observability)
	hydrateGoalRoundsLocked(state, rounds)
	hydrateRestoredTimes(state, turns)
}

// hydrateGoalRoundsLocked applies the durable goal-round markers to the restored
// user messages in order: a marker is attached only when the next user message's
// text matches the event's text, so human messages in between are left alone and
// a history that no longer contains the round (compaction, regenerate, fork)
// safely skips it.
func hydrateGoalRoundsLocked(state *sessionState, rounds []restoredGoalRound) {
	if state == nil || len(rounds) == 0 {
		return
	}
	cursor := 0
	for index := range state.messages {
		message := &state.messages[index]
		if message.Role != contracts.ChatRoleUser || message.GoalRound != nil {
			continue
		}
		if cursor >= len(rounds) {
			return
		}
		if strings.TrimSpace(rounds[cursor].text) != strings.TrimSpace(message.Text) {
			continue
		}
		message.GoalRound = cloneGoalRound(rounds[cursor].round)
		cursor++
	}
}

// restoredInterrupted reports whether the durable checkpoint of a session shows
// a run that never finished (the checkpoint phase is still requested or
// tool_pending). A graceful Stop clears the checkpoint, so this only fires for a
// crash/kill. Failures are logged and treated as "not interrupted".
func (s *service) restoredInterrupted(ctx context.Context, sessionID string) bool {
	if s.store == nil {
		return false
	}
	checkpoints := s.store.CheckpointStore()
	if checkpoints == nil {
		return false
	}
	checkpoint, err := checkpoints.Load(ctx, sessionID)
	if err != nil {
		s.logger.Printf("[agent] restore: load checkpoint of %s: %v", sessionID, err)
		return false
	}
	return checkpoint != nil && checkpoint.Phase.Interrupted()
}
