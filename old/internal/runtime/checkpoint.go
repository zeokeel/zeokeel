package runtime

import (
	"context"
	"strings"
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

// Checkpoint phases written by the runtime. Only the completed phase replaces
// the conversation as the recovery source of truth; the barrier phases expose
// how far an interrupted turn got so a crash cannot lose an already-recorded
// model request or an about-to-run tool call.
const (
	// CheckpointPhaseCompleted is the Phase written by the runtime after a
	// successful (or step-capped) turn.
	CheckpointPhaseCompleted = "completed"
	// CheckpointPhaseRequested is written before a model request is handed to
	// the provider, so a crash mid-response does not lose the request.
	CheckpointPhaseRequested = "requested"
	// CheckpointPhaseToolPending is written before a top-level tool with
	// external side effects runs, so a crash in the tool body leaves a record
	// that the call was dispatched (its result is then unknown, never replayed
	// blindly).
	CheckpointPhaseToolPending = "tool_pending"
)

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
	budget := a.ContextBudget()
	if a.compactor == nil || budget <= 0 || len(prior) == 0 {
		return prior, nil
	}
	if estimateTokens(prior) <= budget {
		return prior, nil
	}
	return a.compactor.Compact(ctx, sessionID, cloneMessages(prior), budget)
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

// turnCheckpointKey carries the barrier-checkpoint inputs into tool and model
// call contexts. Only the turn goroutine sets it; the model adapter and tool
// wrapper read it from their derived contexts.
type turnCheckpointKey struct{}

// turnCheckpointContext is the immutable per-turn checkpoint input.
type turnCheckpointContext struct {
	store     CheckpointStore
	prior     []Message
	sessionID string
	turnID    string
}

// withTurnCheckpoint installs the barrier-checkpoint context on a turn. A nil
// store (no recovery configured) leaves the context unchanged.
func withTurnCheckpoint(ctx context.Context, cc *turnCheckpointContext) context.Context {
	if ctx == nil || cc == nil || cc.store == nil {
		return ctx
	}
	return context.WithValue(ctx, turnCheckpointKey{}, cc)
}

// saveBarrierCheckpoint persists a non-terminal checkpoint for the current turn
// with its conversation so far (prior + everything recorded before the barrier).
// It is a no-op outside an agent turn or when no CheckpointStore is configured.
// A Save failure is returned to the caller, which fails closed: the model
// request is not sent and the tool body does not run.
func saveBarrierCheckpoint(ctx context.Context, phase string) error {
	if ctx == nil {
		return nil
	}
	cc, _ := ctx.Value(turnCheckpointKey{}).(*turnCheckpointContext)
	if cc == nil || cc.store == nil {
		return nil
	}
	var turn []Message
	if tc := turnContextFromContext(ctx); tc != nil && tc.record != nil {
		turn = tc.record.messages()
	}
	cp := checkpointFor(cc.prior, turn, cc.turnID, phase, time.Now())
	return cc.store.Save(ctx, cc.sessionID, cp)
}

// checkpointBarrierTool reports whether name is a top-level tool whose body may
// produce an external side effect (filesystem, process, network or another
// agent) and therefore earns a pre-execution checkpoint. Pure read tools and
// purely in-memory bookkeeping tools (todo, goal, present, ...) are skipped so
// the barrier stays cheap.
func checkpointBarrierTool(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	switch {
	case strings.HasPrefix(name, "terminal_"),
		strings.HasPrefix(name, "job_"),
		strings.HasPrefix(name, "mcp__"):
		return true
	}
	switch name {
	case "bash", "pwsh", "write_file", "edit_file", "str_replace_editor",
		"enter_worktree", "exit_worktree",
		"web_fetch", "web_search",
		"subagent", "workflow", "ralph",
		"spawn_team", "send_message", "interrupt_agent", "close_team",
		"schedule_create", "schedule_update", "schedule_delete",
		"settings_set":
		return true
	default:
		return false
	}
}
