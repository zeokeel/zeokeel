package agent

// Durable per-session prompt inbox (DSH inbox parity), backed by the rig
// session/inbox package.
//
// A human prompt submitted while the session is running used to be rejected by
// Send (ErrSessionRunning) and the desktop UI kept its own in-memory queue for
// the "queued send" affordance — invisible to headless products, lost on a
// restart, and racing the engine-side goal continuation. The backend owns that
// queue, and since the SDK ships the same inbox this module no longer keeps a
// private copy of it:
//
//   - contracts.PromptQueue is implemented by the chat service (type-asserted by
//     hosts, like the other optional chat halves): EnqueuePrompt appends one
//     prompt, QueuedPrompts reads the inbox oldest-first, RemoveQueuedPrompt
//     deletes one entry idempotently;
//   - queue durability, replay (the latest `queue/change` snapshot wins, never
//     folded deltas) and entry ids belong to inbox.Inbox. The durable payload
//     shape is unchanged — {"prompts":[{"id","text","createdAt"}]} — so a queue
//     written by an older build replays into this one and vice versa;
//   - the service keeps what the SDK package deliberately does not know: whether
//     the session exists and is idle, the immediate start of a prompt on an idle
//     session, host notification, and the DSH settle order (after a run ends the
//     oldest queued prompt starts first; only an EMPTY inbox lets the goal
//     continuation driver admit a round);
//   - a user Stop keeps the inbox intact (Cancel never clears it; the inbox
//     pending marker parks it until the next human gesture) and a deleted
//     session drops it.
//
// EnqueuePrompt also starts the first prompt immediately when the session is
// idle, so a host needs one code path for "submit a prompt".

import (
	"context"
	"errors"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/inbox"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// queueEventType is the durable session event type of a prompt-inbox snapshot.
// It aliases inbox.EventType so the durable shape stays one contract.
const queueEventType = inbox.EventType

var _ contracts.PromptQueue = (*service)(nil)

// inboxEventLog adapts the product store to the slice of a session log the
// durable inbox needs. Replay is bounded by the same window restore uses
// everywhere else (restoreMetadataEventWindow): an inbox snapshot is recent by
// nature, so the product does not scan a whole session log for it. A caller
// asking explicitly for a sequence range is passed through unchanged.
type inboxEventLog struct {
	store contracts.Store
}

// Events returns the session events with from <= seq <= to (to <= 0 means until
// the end). With from <= 0 the newest restore window is returned; the inbox
// only replays the highest seq, so a bounded scan is sufficient.
func (l inboxEventLog) Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error) {
	if l.store == nil {
		return nil, nil
	}
	if from > 0 {
		return l.store.Events(ctx, sessionID, from, to)
	}
	events, err := l.store.Last(ctx, sessionID, restoreMetadataEventWindow)
	if err != nil {
		return nil, err
	}
	if to <= 0 {
		return events, nil
	}
	bounded := make([]rig.SessionEvent, 0, len(events))
	for _, event := range events {
		if event.Seq <= to {
			bounded = append(bounded, event)
		}
	}
	return bounded, nil
}

// AppendEvent records one event (an inbox snapshot).
func (l inboxEventLog) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	if l.store == nil {
		return errors.New("agent: prompt inbox has no session log")
	}
	return l.store.AppendEvent(ctx, event)
}

// queuedPrompt maps one inbox entry onto the host contract type.
func queuedPrompt(sessionID string, entry inbox.Entry) contracts.QueuedPrompt {
	return contracts.QueuedPrompt{
		ID:        entry.ID,
		SessionID: sessionID,
		Text:      entry.Text,
		CreatedAt: entry.Created,
	}
}

// EnqueuePrompt implements contracts.PromptQueue: it validates the session and
// the prompt, appends the durable inbox entry, then starts it immediately when
// the session is idle (DSH inbox semantics).
func (s *service) EnqueuePrompt(ctx context.Context, sessionID, prompt string) (contracts.QueuedPrompt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return contracts.QueuedPrompt{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	prompt = strings.TrimSpace(prompt)
	if sessionID == "" {
		return contracts.QueuedPrompt{}, fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if prompt == "" {
		return contracts.QueuedPrompt{}, ErrEmptyPrompt
	}
	s.mu.Lock()
	_, exists := s.sessions[sessionID]
	s.mu.Unlock()
	if !exists || s.sessionDeleted(sessionID) {
		return contracts.QueuedPrompt{}, fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	// A stopped session with waiting prompts drains its oldest one before this
	// new prompt joins the tail: human input order is preserved and the queue
	// never stays stuck until a later run.
	s.resumePendingIdleDrain(sessionID)
	entry, err := s.inbox.Enqueue(ctx, sessionID, prompt)
	if err != nil {
		if errors.Is(err, inbox.ErrInboxFull) {
			// Keep the previous host-facing wording for a full inbox.
			return contracts.QueuedPrompt{}, fmt.Errorf("agent: session %q inbox is full (%d prompts)", sessionID, inbox.MaxEntries)
		}
		return contracts.QueuedPrompt{}, err
	}
	s.notify(sessionID)
	// An idle session starts the prompt at once; a running one keeps it queued
	// and settleRun drains it after the current run.
	s.drainQueuedPrompt(sessionID)
	return queuedPrompt(sessionID, entry), nil
}

// QueuedPrompts implements contracts.PromptQueue: the session inbox in send
// order. The returned slice is a copy the caller may retain.
func (s *service) QueuedPrompts(sessionID string) []contracts.QueuedPrompt {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	entries, err := s.inbox.List(context.Background(), sessionID)
	if err != nil {
		s.logger.Printf("[agent] session %s: read prompt inbox: %v", sessionID, err)
		return nil
	}
	if len(entries) == 0 {
		return nil
	}
	prompts := make([]contracts.QueuedPrompt, 0, len(entries))
	for _, entry := range entries {
		prompts = append(prompts, queuedPrompt(sessionID, entry))
	}
	return prompts
}

// RemoveQueuedPrompt implements contracts.PromptQueue: it removes one entry by
// ID and is idempotent (an unknown session or ID is a no-op).
func (s *service) RemoveQueuedPrompt(ctx context.Context, sessionID, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	id = strings.TrimSpace(id)
	if sessionID == "" || id == "" {
		return nil
	}
	// A removal is the only event worth notifying, so remember whether the id
	// was queued (Remove itself is an idempotent no-op for an unknown id).
	removed := false
	entries, err := s.inbox.List(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.ID == id {
			removed = true
			break
		}
	}
	// Removing the last waiting prompt also clears a Stop-pending marker; the
	// inbox owns that rule.
	if err := s.inbox.Remove(ctx, sessionID, id); err != nil {
		return err
	}
	if removed {
		s.notify(sessionID)
	}
	return nil
}

// queuedPromptCount reports how many prompts wait in the session inbox. A
// durable read failure is logged and reported as empty (the previous in-memory
// queue could not fail); it never blocks a drain attempt.
func (s *service) queuedPromptCount(sessionID string) int {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0
	}
	count, err := s.inbox.Count(context.Background(), sessionID)
	if err != nil {
		s.logger.Printf("[agent] session %s: count prompt inbox: %v", sessionID, err)
		return 0
	}
	return count
}

// drainQueuedPrompt starts the oldest queued prompt when the session is idle. The
// inbox dequeues durably FIRST and only then starts the run, so a crash cannot
// replay a prompt that already ran (the opposite failure — losing the prompt
// after the durable dequeue but before the send — is the accepted DSH
// trade-off). It returns true only when a run actually started.
func (s *service) drainQueuedPrompt(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.sessionDeleted(sessionID) {
		return false
	}
	if !s.sessionIdle(sessionID) {
		return false
	}
	entry, ok, err := s.inbox.Take(context.Background(), sessionID)
	if err != nil {
		s.logger.Printf("[agent] session %s: dequeue queued prompt: %v", sessionID, err)
		return false
	}
	if !ok {
		return false
	}
	if err := s.sendWithOptions(sessionID, entry.Text, nil, runOptions{origin: contracts.AgentRunOriginUser}); err != nil {
		s.logger.Printf("[agent] session %s: send queued prompt %s: %v", sessionID, entry.ID, err)
		// Put the prompt back at the front so a transient failure (a model not
		// configured yet, a concurrent manual send) never drops human input.
		s.requeueQueuedPrompt(sessionID, entry)
		s.notify(sessionID)
		return false
	}
	// The stopped-inbox marker only needs to survive until the first drained
	// prompt actually starts; the remaining prompts drain on their own settle.
	s.clearPendingIdleDrain(sessionID)
	return true
}

// requeueQueuedPrompt puts one prompt back at the front of the inbox (a drain
// that could not start its run). A deleted session drops it: the durable
// session/events are gone and the tombstone must win.
func (s *service) requeueQueuedPrompt(sessionID string, entry inbox.Entry) {
	if s.sessionDeleted(sessionID) {
		return
	}
	if err := s.inbox.Requeue(context.Background(), sessionID, entry); err != nil {
		s.logger.Printf("[agent] session %s: requeue prompt %s: %v", sessionID, entry.ID, err)
	}
}

// markPendingIdleDrain records that a session's cancelled run left queued
// prompts behind: they wait for the next human gesture (no background timer, so
// Stop remains an explicit stop).
func (s *service) markPendingIdleDrain(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.inbox.MarkPending(sessionID)
}

// hasPendingIdleDrain reports whether a session's inbox waits for a human
// gesture after a Stop.
func (s *service) hasPendingIdleDrain(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	return s.inbox.Pending(sessionID)
}

// clearPendingIdleDrain drops the marker once the inbox resumed.
func (s *service) clearPendingIdleDrain(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.inbox.ClearPending(sessionID)
}

// resumePendingIdleDrain drains the oldest queued prompt of a stopped session
// when a human gesture resumes it. It returns true only when a run started; a
// failed start keeps the marker so the next gesture retries.
func (s *service) resumePendingIdleDrain(sessionID string) bool {
	if !s.hasPendingIdleDrain(sessionID) {
		return false
	}
	if s.queuedPromptCount(sessionID) == 0 {
		s.clearPendingIdleDrain(sessionID)
		return false
	}
	if s.sessionDeleted(sessionID) || !s.sessionIdle(sessionID) {
		return false
	}
	if s.drainQueuedPrompt(sessionID) {
		s.clearPendingIdleDrain(sessionID)
		return true
	}
	return false
}

// clearQueuedPrompts drops a session's inbox (DeleteSession). The durable events
// are deleted with the session row, and the inbox tombstone keeps any late drain
// from re-creating it, so no queue event is appended here.
func (s *service) clearQueuedPrompts(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.inbox.Drop(sessionID)
}

// settleRun applies the DSH settle order after one run: the human inbox drains
// first, and the goal continuation driver only runs when the inbox is empty.
// A user Stop (cancelled run) leaves the inbox untouched for the user.
func (s *service) settleRun(sessionID, origin string, runErr error, runCtx context.Context) {
	if s.sessionDeleted(sessionID) {
		return
	}
	cancelled := runCtx != nil && runCtx.Err() != nil
	if !cancelled {
		// A failed-but-not-stopped run still releases the inbox: the user typed
		// those prompts while the turn was running and expects them to run.
		if s.drainQueuedPrompt(sessionID) {
			return
		}
		if s.queuedPromptCount(sessionID) > 0 {
			// The drain failed (prompt requeued): never let a goal round jump
			// ahead of queued human input.
			return
		}
	} else if s.queuedPromptCount(sessionID) > 0 {
		// Stop keeps the inbox (and never restarts the goal): the oldest queued
		// prompt waits for the next human gesture on this session.
		s.markPendingIdleDrain(sessionID)
		s.notify(sessionID)
	}
	s.continueGoalAfterRun(sessionID, origin, runErr, runCtx)
}
