package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Manual (on-demand) compaction is the backend of the shell's /compact
// command. Automatic compaction runs inside a turn when the prior conversation
// exceeds ContextBudget; CompactNow lets the user compact the same conversation
// immediately while the agent is idle, like DSH's /compact command. The command
// lifecycle itself stays outside the model history: the replacement is applied
// to the session's durable conversation (through CompactionPersister) and
// mirrored to the recovery checkpoint, so reopening the session shows the
// compacted surface instead of the dropped history.

// Sentinel errors returned by CompactNow. Callers classify failures with
// errors.Is and render a stable message for each.
var (
	// ErrCompactionBusy reports a turn that is currently driving the shared
	// topology, so a manual compaction must not interleave with it.
	ErrCompactionBusy = errors.New("runtime: manual compaction unavailable: a turn is running")
	// ErrNoCompactor reports an agent built without Config.Compactor.
	ErrNoCompactor = errors.New("runtime: manual compaction unavailable: no compactor configured")
	// ErrNoCompactionPersistence reports a caller that cannot durably replace
	// the conversation (no session store behind the agent).
	ErrNoCompactionPersistence = errors.New("runtime: manual compaction unavailable: no conversation persistence")
	// ErrNoCompactableHistory reports an empty prior conversation or a
	// compactor that left it unchanged.
	ErrNoCompactableHistory = errors.New("runtime: manual compaction: no compactable history")
	// ErrCompactionFailed reports a compactor that produced no conversation at
	// all (an implementation failure, not an expected domain rejection).
	ErrCompactionFailed = errors.New("runtime: manual compaction did not produce a usable conversation")
	// ErrCompactionNotSaved reports a compactor result that could not be
	// persisted to the conversation store or mirrored to the checkpoint store.
	ErrCompactionNotSaved = errors.New("runtime: manual compaction finished but could not be saved")
)

// CompactionPersister durably replaces a session's stored conversation with the
// compacted surface. The session controller implements it over the messages
// table (delete the replaced rows, insert the summary, record the replay event),
// which is what makes /compact survive a reopen and keeps the next turn's
// History in step; a bare Agent used without a session store passes nil and
// gets ErrNoCompactionPersistence.
type CompactionPersister interface {
	// PersistCompaction replaces the stored conversation that produced prior
	// with compacted and returns how many stored history items were replaced
	// (the count the UI reports). prior and compacted are copies in
	// chronological order; the implementation must not retain them.
	PersistCompaction(ctx context.Context, sessionID string, prior, compacted []Message) (replacedItems int, err error)
}

// CompactNow compacts sessionID's prior conversation on demand, even when the
// conversation is still within ContextBudget.
//
// It mirrors the automatic compaction path: the prior conversation is read
// through the agent's History/checkpoint, compacted with a.ContextBudget(), and
// the result is handed to persist so the session's durable conversation is
// replaced (the next Run is seeded from it, and reopening the session shows the
// compacted surface). The recovery checkpoint is updated in the same step. A
// queued manual compaction (a turn in flight) returns ErrCompactionBusy instead
// of interleaving with eino's shared topology.
//
// replacedItems comes from the persister (the number of stored history items it
// deleted); estimatedTokensSaved is the estimateTokens difference, clamped at
// zero.
func (a *Agent) CompactNow(ctx context.Context, sessionID string, persist CompactionPersister) (replacedItems, estimatedTokensSaved int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0, 0, errors.New("runtime: manual compaction: empty session id")
	}
	// The eino topology stores per-run state on the agent, so the turn
	// goroutine holds runMu while a turn runs. TryLock keeps a manual
	// compaction from queueing behind (or racing) an active turn; the UI
	// reports the busy result as "try again when idle".
	if !a.runMu.TryLock() {
		return 0, 0, ErrCompactionBusy
	}
	defer a.runMu.Unlock()

	if a.compactor == nil {
		return 0, 0, ErrNoCompactor
	}
	if persist == nil {
		return 0, 0, ErrNoCompactionPersistence
	}

	prior, err := a.loadPriorConversation(ctx, sessionID)
	if err != nil {
		return 0, 0, fmt.Errorf("runtime: manual compaction: load conversation for session %q: %w", sessionID, err)
	}
	if len(prior) == 0 {
		return 0, 0, ErrNoCompactableHistory
	}

	compacted, err := a.compactor.Compact(ctx, sessionID, cloneMessages(prior), a.ContextBudget())
	if err != nil {
		return 0, 0, fmt.Errorf("runtime: manual compaction for session %q: %w", sessionID, err)
	}
	if len(compacted) == 0 {
		return 0, 0, fmt.Errorf("%w: compactor returned an empty conversation for session %q", ErrCompactionFailed, sessionID)
	}

	saved := estimateTokens(prior) - estimateTokens(compacted)
	if saved < 0 {
		saved = 0
	}
	if len(compacted) >= len(prior) && saved == 0 {
		// The compactor kept the conversation as it was: report it as
		// "nothing to compact" instead of rewriting the store for no gain.
		return 0, 0, ErrNoCompactableHistory
	}

	replaced, persistErr := persist.PersistCompaction(ctx, sessionID, cloneMessages(prior), cloneMessages(compacted))
	if persistErr != nil {
		return 0, 0, fmt.Errorf("%w: session %q: %v", ErrCompactionNotSaved, sessionID, persistErr)
	}
	if replaced <= 0 {
		return 0, 0, ErrNoCompactableHistory
	}

	// Mirror the new conversation surface into the recovery checkpoint. The
	// existing turn id/phase are preserved: a completed checkpoint still names
	// the turn that produced the visible conversation, and a barrier
	// checkpoint keeps its crash-recovery meaning.
	if a.checkpoints != nil {
		checkpoint := &Checkpoint{
			History:   cloneMessages(compacted),
			UpdatedAt: time.Now(),
			Phase:     CheckpointPhaseCompleted,
		}
		if existing, loadErr := a.checkpoints.Load(ctx, sessionID); loadErr == nil && existing != nil {
			checkpoint.TurnID = existing.TurnID
			if phase := strings.TrimSpace(existing.Phase); phase != "" {
				checkpoint.Phase = phase
			}
		}
		if saveErr := a.checkpoints.Save(ctx, sessionID, checkpoint); saveErr != nil {
			return replaced, saved, fmt.Errorf("%w: session %q: %v", ErrCompactionNotSaved, sessionID, saveErr)
		}
	}
	return replaced, saved, nil
}
