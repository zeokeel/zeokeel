package session

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Streaming events (one per token or reasoning chunk) arrive at tens per
// second. Persisting each one with its own SQLite transaction and its own
// transcript open+fsync is the dominant per-token cost on the streaming path
// (RT-10). The buffer below coalesces them into one transaction and one
// transcript append per flush, while keeping the persisted trace semantically
// identical: rows keep contiguous store-assigned sequence numbers in arrival
// order, and every non-streaming event (tool call, status, turn end, error,
// message) flushes the pending stream first so replay order never changes.
const (
	// eventBatchMaxBytes bounds one buffered stream before it is written
	// eagerly even though no boundary event arrived yet.
	eventBatchMaxBytes = 8 << 10
	// eventBatchMaxDelay bounds how long a buffered token may stay unpersisted
	// when the stream stalls or the turn is interrupted. The live UI does not
	// depend on it (state events are published in memory); it only bounds the
	// crash-recovery window.
	eventBatchMaxDelay = 200 * time.Millisecond
)

// controllerEventBatch holds the not-yet-persisted stream events of one
// controller, per session. Controller state itself is not changed (controller.go
// is owned by another worker), so the buffer is keyed by controller pointer.
type controllerEventBatch struct {
	mu      sync.Mutex
	c       *Controller
	pending map[string][]store.Event
	size    map[string]int
	timer   *time.Timer
}

// controllerEventBatches maps a Controller to its stream buffer. Entries live
// for the controller's lifetime, which is the app's lifetime in production.
var controllerEventBatches sync.Map // map[*Controller]*controllerEventBatch

// eventBatch returns c's stream buffer, creating it on first use.
func (c *Controller) eventBatch() *controllerEventBatch {
	if b, ok := controllerEventBatches.Load(c); ok {
		return b.(*controllerEventBatch)
	}
	created := &controllerEventBatch{
		c:       c,
		pending: make(map[string][]store.Event),
		size:    make(map[string]int),
	}
	actual, _ := controllerEventBatches.LoadOrStore(c, created)
	return actual.(*controllerEventBatch)
}

// isStreamEventType reports whether a persisted event type is one of the
// high-frequency streaming chunk types that are safe to batch. Every other
// type is a boundary event and is written immediately.
func isStreamEventType(typ string) bool {
	switch typ {
	case EventToken, EventReasoning:
		return true
	default:
		return false
	}
}

// bufferStoredEvent queues one stream event and flushes the session's batch
// when it grew past eventBatchMaxBytes, or schedules a delayed flush when the
// stream stalls.
func (c *Controller) bufferStoredEvent(sessionID string, rec store.Event) error {
	b := c.eventBatch()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending[sessionID] = append(b.pending[sessionID], rec)
	b.size[sessionID] += len(rec.Type) + len(rec.Payload)
	if b.size[sessionID] >= eventBatchMaxBytes {
		return b.flushSessionLocked(sessionID)
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(eventBatchMaxDelay, b.flushAll)
	}
	return nil
}

// flushStoredEvents writes any buffered stream events of sessionID. Boundary
// events call it before their own insert so persisted order matches arrival
// order.
func (c *Controller) flushStoredEvents(sessionID string) error {
	b := c.eventBatch()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.flushSessionLocked(sessionID)
}

// flushAll is the delayed-flush callback: it persists every session's buffered
// stream events. Errors are logged (the per-event path was also best effort:
// the controller ignores append failures) so a failed batch is visible in the
// app log instead of vanishing.
func (b *controllerEventBatch) flushAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timer = nil
	for sessionID := range b.pending {
		if err := b.flushSessionLocked(sessionID); err != nil {
			log.Printf("zeokeel: session: flush buffered stream events for %s: %v", sessionID, err)
		}
	}
}

// flushSessionLocked writes and removes sessionID's pending stream events.
// b.mu must be held.
func (b *controllerEventBatch) flushSessionLocked(sessionID string) error {
	recs := b.pending[sessionID]
	if len(recs) == 0 {
		return nil
	}
	delete(b.pending, sessionID)
	delete(b.size, sessionID)
	written, err := b.c.store.AppendEvents(sessionID, recs)
	if err != nil {
		return fmt.Errorf("append %d buffered events: %w", len(recs), err)
	}
	records := make([]any, len(written))
	for i := range written {
		records[i] = written[i]
	}
	// The transcript mirror is best effort on the single-event path as well.
	_ = store.AppendTranscriptRecords(b.c.store.Dir(), sessionID, records)
	return nil
}
