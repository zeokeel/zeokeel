package agent

// Durable per-session prompt inbox (DSH inbox parity).
//
// A human prompt submitted while the session is running used to be rejected by
// Send (ErrSessionRunning) and the desktop UI kept its own in-memory queue for
// the "queued send" affordance — invisible to headless products, lost on a
// restart, and racing the engine-side goal continuation. The backend now owns
// that queue:
//
//   - contracts.PromptQueue is implemented by the chat service (type-asserted by
//     hosts, like the other optional chat halves): EnqueuePrompt appends one
//     prompt, QueuedPrompts reads the inbox oldest-first, RemoveQueuedPrompt
//     deletes one entry idempotently;
//   - the queue is durable through a `queue/change` session event carrying the
//     complete snapshot (sparse: one event per mutation), and restore replays
//     the latest one;
//   - settle order is DSH's: after a run ends the oldest queued prompt starts
//     first (durable dequeue, then sendWithOptions origin=user); only an EMPTY
//     inbox lets the goal continuation driver admit a round;
//   - a user Stop keeps the inbox intact (Cancel never clears it) and a deleted
//     session drops it (the durable session/events are deleted with it).
//
// EnqueuePrompt also starts the first prompt immediately when the session is
// idle, so a host needs one code path for "submit a prompt".

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// queueEventType is the durable session event type of a prompt-inbox snapshot.
const queueEventType = "queue/change"

// maxQueuedPromptEntries bounds one session's inbox: it only holds prompts a
// human sent during a running turn, so an unbounded queue would be a bug (or a
// runaway automation loop). Excess prompts are rejected with a readable error.
const maxQueuedPromptEntries = 200

var _ contracts.PromptQueue = (*service)(nil)

// queuedPromptPayload is the durable shape of one inbox entry.
type queuedPromptPayload struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

// queuedPromptsPayload is the durable `queue/change` payload: the complete
// inbox snapshot, so replay never folds add/remove deltas.
type queuedPromptsPayload struct {
	Prompts []queuedPromptPayload `json:"prompts"`
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
	entry := contracts.QueuedPrompt{
		ID:        s.newQueuedPromptID(),
		SessionID: sessionID,
		Text:      prompt,
		CreatedAt: time.Now(),
	}
	s.queueMu.Lock()
	if len(s.queues[sessionID]) >= maxQueuedPromptEntries {
		s.queueMu.Unlock()
		return contracts.QueuedPrompt{}, fmt.Errorf("agent: session %q inbox is full (%d prompts)", sessionID, maxQueuedPromptEntries)
	}
	s.queues[sessionID] = append(s.queues[sessionID], entry)
	s.persistQueuedPromptsLocked(sessionID)
	s.queueMu.Unlock()
	s.notify(sessionID)
	// An idle session starts the prompt at once; a running one keeps it queued
	// and settleRun drains it after the current run.
	s.drainQueuedPrompt(sessionID)
	return entry, nil
}

// QueuedPrompts implements contracts.PromptQueue: the session inbox in send
// order. The returned slice is a copy the caller may retain.
func (s *service) QueuedPrompts(sessionID string) []contracts.QueuedPrompt {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queue := s.queues[sessionID]
	return append([]contracts.QueuedPrompt(nil), queue...)
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
	s.queueMu.Lock()
	queue := s.queues[sessionID]
	removed := false
	for index, entry := range queue {
		if entry.ID == id {
			next := make([]contracts.QueuedPrompt, 0, len(queue)-1)
			next = append(next, queue[:index]...)
			next = append(next, queue[index+1:]...)
			if len(next) == 0 {
				delete(s.queues, sessionID)
			} else {
				s.queues[sessionID] = next
			}
			removed = true
			break
		}
	}
	if removed {
		s.persistQueuedPromptsLocked(sessionID)
	}
	s.queueMu.Unlock()
	if removed {
		if s.queuedPromptCount(sessionID) == 0 {
			// Removing the last waiting prompt also clears a Stop-pending marker.
			s.clearPendingIdleDrain(sessionID)
		}
		s.notify(sessionID)
	}
	return nil
}

// queuedPromptCount reports how many prompts wait in the session inbox.
func (s *service) queuedPromptCount(sessionID string) int {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return len(s.queues[sessionID])
}

// newQueuedPromptID returns a durable inbox entry id (unique per process and
// stable across restarts, unlike a slice index).
func (s *service) newQueuedPromptID() string {
	return fmt.Sprintf("prompt-%d-%d", time.Now().UnixNano(), s.nextQueuedSeq.Add(1))
}

// persistQueuedPromptsLocked appends the durable inbox snapshot. The caller
// holds queueMu; the append serializes with the in-memory mutation so a durable
// dequeue can never be reordered behind a later enqueue.
func (s *service) persistQueuedPromptsLocked(sessionID string) {
	queue := s.queues[sessionID]
	prompts := make([]queuedPromptPayload, 0, len(queue))
	for _, entry := range queue {
		prompts = append(prompts, queuedPromptPayload{ID: entry.ID, Text: entry.Text, CreatedAt: entry.CreatedAt})
	}
	s.appendEvent(context.Background(), sessionID, queueEventType, map[string]any{"prompts": prompts})
}

// drainQueuedPrompt starts the oldest queued prompt when the session is idle. It
// dequeues durably FIRST and only then starts the run, so a crash cannot replay a
// prompt that already ran (the opposite failure — losing the prompt after the
// durable dequeue but before the send — is the accepted DSH trade-off). It
// returns true only when a run actually started.
func (s *service) drainQueuedPrompt(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.sessionDeleted(sessionID) {
		return false
	}
	s.queueMu.Lock()
	if !s.sessionIdle(sessionID) {
		s.queueMu.Unlock()
		return false
	}
	queue := s.queues[sessionID]
	if len(queue) == 0 {
		s.queueMu.Unlock()
		return false
	}
	entry := queue[0]
	if len(queue) == 1 {
		delete(s.queues, sessionID)
	} else {
		s.queues[sessionID] = append([]contracts.QueuedPrompt(nil), queue[1:]...)
	}
	s.persistQueuedPromptsLocked(sessionID)
	s.queueMu.Unlock()

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
func (s *service) requeueQueuedPrompt(sessionID string, entry contracts.QueuedPrompt) {
	s.queueMu.Lock()
	if s.sessionDeleted(sessionID) {
		s.queueMu.Unlock()
		return
	}
	queue := s.queues[sessionID]
	next := make([]contracts.QueuedPrompt, 0, len(queue)+1)
	next = append(next, entry)
	next = append(next, queue...)
	s.queues[sessionID] = next
	s.persistQueuedPromptsLocked(sessionID)
	s.queueMu.Unlock()
}

// markPendingIdleDrain records that a session's cancelled run left queued
// prompts behind: they wait for the next human gesture (no background timer, so
// Stop remains an explicit stop).
func (s *service) markPendingIdleDrain(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.queueMu.Lock()
	if s.pendingIdleDrain == nil {
		s.pendingIdleDrain = make(map[string]bool)
	}
	s.pendingIdleDrain[sessionID] = true
	s.queueMu.Unlock()
}

// hasPendingIdleDrain reports whether a session's inbox waits for a human
// gesture after a Stop.
func (s *service) hasPendingIdleDrain(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return s.pendingIdleDrain[sessionID]
}

// clearPendingIdleDrain drops the marker once the inbox resumed.
func (s *service) clearPendingIdleDrain(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.queueMu.Lock()
	delete(s.pendingIdleDrain, sessionID)
	s.queueMu.Unlock()
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

// clearQueuedPrompts drops a session's inbox in memory (DeleteSession). The
// durable events are deleted with the session row, and the deleted-session
// tombstone keeps any late drain from re-creating it, so no queue event is
// appended here.
func (s *service) clearQueuedPrompts(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.queueMu.Lock()
	delete(s.queues, sessionID)
	delete(s.pendingIdleDrain, sessionID)
	s.queueMu.Unlock()
}

// restoreQueuedPrompts installs one replayed durable inbox snapshot.
func (s *service) restoreQueuedPrompts(sessionID string, prompts []contracts.QueuedPrompt) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(prompts) == 0 {
		return
	}
	s.queueMu.Lock()
	s.queues[sessionID] = append([]contracts.QueuedPrompt(nil), prompts...)
	s.queueMu.Unlock()
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

// decodeQueuedPrompts parses one durable inbox snapshot, dropping malformed
// entries (a corrupt event must not fail the restore).
func decodeQueuedPrompts(payload []byte) ([]contracts.QueuedPrompt, bool) {
	if len(payload) == 0 {
		return nil, false
	}
	var decoded queuedPromptsPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, false
	}
	prompts := make([]contracts.QueuedPrompt, 0, len(decoded.Prompts))
	seen := make(map[string]bool, len(decoded.Prompts))
	for _, entry := range decoded.Prompts {
		id := strings.TrimSpace(entry.ID)
		text := strings.TrimSpace(entry.Text)
		if id == "" || text == "" || seen[id] {
			continue
		}
		seen[id] = true
		prompts = append(prompts, contracts.QueuedPrompt{
			ID:        id,
			Text:      text,
			CreatedAt: entry.CreatedAt,
		})
		if len(prompts) >= maxQueuedPromptEntries {
			break
		}
	}
	return prompts, true
}
