package runtime

import (
	"context"
	"time"
)

// Checkpoint is the durable state of one session at a turn boundary. The
// runtime saves it after each successful turn when Config.CheckpointStore is
// set, and uses it to seed a new Agent when Config.History is nil.
type Checkpoint struct {
	// History is the conversation as of the completed turn: all seeded prior
	// messages plus the completed turn's user/assistant/tool messages.
	History []Message `json:"history,omitempty"`
	// TurnID identifies the turn that produced this checkpoint. It is the
	// MsgID the turn used for its streamed assistant events.
	TurnID string `json:"turn_id,omitempty"`
	// UpdatedAt is when the checkpoint was saved.
	UpdatedAt time.Time `json:"updated_at"`
	// Phase records the lifecycle phase of the checkpointed turn. The runtime
	// writes CheckpointPhaseCompleted; apps may store other values for their
	// own checkpoints (for example an interrupted phase).
	Phase string `json:"phase,omitempty"`
}

// CheckpointPhaseCompleted is the Phase written by the runtime after a
// successful turn.
const CheckpointPhaseCompleted = "completed"

// CheckpointStore persists turn-boundary checkpoints for crash/cold-start
// recovery. Implementations are called concurrently across sessions and must be
// safe for concurrent use.
//
// Load returns (nil, nil) when the session has no checkpoint. The runtime treats
// any other Load error as an unusable checkpoint and starts the session from a
// fresh conversation, because a damaged recovery hint must not prevent a new
// turn. Save and Delete errors are reported to the app by the runtime/app
// integration point (a Save failure at the end of a turn surfaces as
// state.Error).
//
// Delete is exposed for session deletion/reset; the Agent never calls it on
// its own. Checkpoints hold the conversation they are given, so an app that
// also configures Config.History may prefer to make History authoritative and
// treat the checkpoint as a best-effort mirror. That is the production app's
// contract (see internal/app): buildAgent configures both, History is the
// source of truth for seeding a turn, and the checkpoint is written after each
// successful turn for cold-start/diagnostics but is not read while History is
// configured.
type CheckpointStore interface {
	Load(ctx context.Context, sessionID string) (*Checkpoint, error)
	Save(ctx context.Context, sessionID string, cp *Checkpoint) error
	Delete(ctx context.Context, sessionID string) error
}

// loadPriorConversation returns the conversation to seed the next turn with.
// History wins when configured; otherwise a checkpoint is consulted. This
// precedence is intentional: the production app configures both and treats
// History as the source of truth while mirroring completed turns to the
// checkpoint (TestCheckpointProductionMirrorOnly pins it). Checkpoint read
// failures (corruption, I/O) are deliberately tolerated.
func (a *Agent) loadPriorConversation(ctx context.Context, sessionID string) ([]Message, error) {
	if a.history != nil {
		prior, err := a.history.Messages(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		return cloneMessages(prior), nil
	}
	if a.checkpoints == nil {
		return nil, nil
	}

	cp, err := a.checkpoints.Load(ctx, sessionID)
	if err != nil {
		// A checkpoint is a recovery hint, not the source of truth: a corrupt
		// or unreadable one must not stop the user from talking to the agent.
		// Cancellation is still reported so a stopped turn stays stopped.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, nil
	}
	if cp == nil {
		return nil, nil
	}
	return cloneMessages(cp.History), nil
}

// compactPrior applies Config.Compactor when the estimated prompt size of the
// prior conversation exceeds Config.ContextBudget. A nil Compactor, a
// non-positive budget, an empty conversation, or a conversation within budget
// is a no-op.
func (a *Agent) compactPrior(ctx context.Context, sessionID string, prior []Message) ([]Message, error) {
	if a.compactor == nil || a.contextBudget <= 0 || len(prior) == 0 {
		return prior, nil
	}
	if estimateTokens(prior) <= a.contextBudget {
		return prior, nil
	}
	return a.compactor.Compact(ctx, sessionID, cloneMessages(prior), a.contextBudget)
}

// checkpointFor assembles the completed state of a turn: the prior conversation
// (after any compaction) followed by the turn's own messages.
func checkpointFor(prior []Message, turn []Message, turnID string, phase string, now time.Time) *Checkpoint {
	history := make([]Message, 0, len(prior)+len(turn))
	history = append(history, prior...)
	history = append(history, turn...)
	return &Checkpoint{
		History:   history,
		TurnID:    turnID,
		UpdatedAt: now,
		Phase:     phase,
	}
}
