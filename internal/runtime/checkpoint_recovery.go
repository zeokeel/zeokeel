package runtime

import (
	"context"
	"strings"
)

// Interrupted-turn recovery.
//
// The runtime writes non-terminal barrier checkpoints before a model request
// (CheckpointPhaseRequested) and before a top-level side-effecting tool runs
// (CheckpointPhaseToolPending). When the process dies mid-turn those phases are
// left behind: a later consumer must close the interrupted turn in the
// conversation before the next model request, without replaying any side
// effect. RecoverInterruptedTurn is that consumer's pure building block: it
// reads the latest checkpoint, repairs the conversation tail deterministically
// and returns the messages that close the turn. It never writes: the caller
// persists the repair and marks the checkpoint completed (RepairedCheckpoint
// produces that checkpoint).
//
// This mirrors DSH's interrupted-turn closers: an interrupted turn is closed by
// appending ordinary messages to the log, never by re-running a tool or
// duplicating the user's prompt.

// InterruptedToolResult is the model-visible result written for a tool call
// that was dispatched but whose outcome a crash destroyed. The wording is
// deliberately conservative: the call was not replayed, so its external side
// effect is unknown.
const InterruptedToolResult = "Interrupted before the tool call finished; its outcome is unknown. It was not replayed. Retry only if the operation is read-only or idempotent; verify external state first if it may have side effects."

// InterruptedTurnMarker is the assistant message appended to close an
// interrupted turn in the model-visible conversation. It gives the next request
// an assistant anchor after the abandoned tail and tells the model not to
// assume the interrupted work completed.
const InterruptedTurnMarker = "The previous turn was interrupted: ZeoKeel exited before it finished. Nothing was replayed. Treat the pending work as not done and verify external state before retrying."

// InterruptedTurnRepair is the deterministic repair for a session whose latest
// turn was left open by a process exit.
type InterruptedTurnRepair struct {
	// SessionID is the repaired session.
	SessionID string
	// TurnID is the interrupted turn's id from the checkpoint.
	TurnID string
	// Phase is the non-terminal checkpoint phase that triggered the repair
	// (requested or tool_pending).
	Phase string
	// History is the checkpoint conversation with the interrupted turn closed:
	// an error result for every dispatched tool call without a recorded result,
	// then one assistant marker. It is safe to seed the next request with.
	History []Message
	// Appended is the deterministic tail History adds to the checkpoint
	// conversation, in order (synthetic tool results, then the marker). It
	// contains no user message: recovery never duplicates the prompt.
	Appended []Message
}

// RepairedCheckpoint returns the completed-phase checkpoint that records the
// repair. Persisting it makes a second recovery of the same session a no-op,
// which is what makes recovery idempotent across restarts.
func (r *InterruptedTurnRepair) RepairedCheckpoint() *Checkpoint {
	if r == nil {
		return nil
	}
	history := make([]Message, len(r.History))
	copy(history, r.History)
	return &Checkpoint{
		History: history,
		TurnID:  r.TurnID,
		Phase:   CheckpointPhaseCompleted,
	}
}

// CheckpointLoader loads one session's latest checkpoint. Recovery only reads:
// the caller persists the repair (see InterruptedTurnRepair.RepairedCheckpoint),
// so a narrower interface than CheckpointStore keeps recovery call sites free of
// a write seam they do not need.
type CheckpointLoader interface {
	Load(ctx context.Context, sessionID string) (*Checkpoint, error)
}

// RecoverInterruptedTurn reads the session's latest checkpoint and, when it
// records a non-terminal turn (requested/tool_pending), builds the repair that
// closes it. It is read-only and deterministic: the same checkpoint yields the
// same repair, and a missing, completed or unreadable checkpoint yields
// (nil, nil). Cancellation is reported so a stopped caller stays stopped.
func RecoverInterruptedTurn(ctx context.Context, store CheckpointLoader, sessionID string) (*InterruptedTurnRepair, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	checkpoint, err := store.Load(ctx, sessionID)
	if err != nil {
		// A checkpoint is a recovery hint, not the source of truth: a corrupt
		// or unreadable one must never block the session. Cancellation is
		// still reported so a stopped caller stays stopped.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, nil
	}
	if checkpoint == nil {
		return nil, nil
	}
	phase := strings.TrimSpace(checkpoint.Phase)
	if phase != CheckpointPhaseRequested && phase != CheckpointPhaseToolPending {
		return nil, nil
	}
	history := cloneMessages(checkpoint.History)
	if len(history) == 0 {
		return nil, nil
	}

	repair := &InterruptedTurnRepair{
		SessionID: sessionID,
		TurnID:    checkpoint.TurnID,
		Phase:     phase,
	}
	// A tool call is answered when any tool result in the conversation carries
	// its id. The barrier checkpoint is written between the assistant's tool
	// call and its result, so an unanswered call sits at the tail; scanning the
	// whole conversation keeps a malformed checkpoint from gaining duplicate
	// results.
	answered := make(map[string]bool)
	for _, msg := range history {
		if msg.Role == RoleTool && msg.ToolCallID != "" {
			answered[msg.ToolCallID] = true
		}
	}

	repaired := make([]Message, 0, len(history)+2)
	for index := 0; index < len(history); index++ {
		msg := history[index]
		repaired = append(repaired, msg)
		if msg.Role != RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		// Copy the tool results that immediately follow the assistant message,
		// then append placeholders for the calls this crash left open. Results
		// must follow their assistant call for the next provider request to be
		// valid.
		next := index + 1
		for next < len(history) && history[next].Role == RoleTool {
			repaired = append(repaired, history[next])
			next++
		}
		for _, call := range msg.ToolCalls {
			if call.ID == "" || answered[call.ID] {
				continue
			}
			result := Message{
				Role:       RoleTool,
				Content:    InterruptedToolResult,
				ToolCallID: call.ID,
				ToolName:   call.Name,
			}
			repaired = append(repaired, result)
			repair.Appended = append(repair.Appended, result)
		}
		index = next - 1
	}
	// The assistant anchor closes the abandoned tail: a requested-phase
	// checkpoint ends on the user prompt, and the next turn must not append a
	// second user message directly after it.
	marker := Message{Role: RoleAssistant, Content: InterruptedTurnMarker}
	repaired = append(repaired, marker)
	repair.Appended = append(repair.Appended, marker)
	repair.History = repaired
	return repair, nil
}
