package uiroot

// The host interaction bridge: agent host tools ask a question, the desktop UI
// module installs a handler that renders it, and Answer/Cancel resume the
// blocked Ask. The service is transport-only: it never touches mygo directly,
// so the UI module decides when to request a frame.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ErrInteractiveUnavailable reports an Ask with no UI handler installed
// (headless products and UI boot failures).
var ErrInteractiveUnavailable = errors.New("交互式界面不可用")

// ErrUserCancelled is the error a dismissed question delivers to the waiting
// Ask.
var ErrUserCancelled = errors.New("用户取消")

// interactionPending is one question parked on Ask.
type interactionPending struct {
	answers chan string
	cancels chan error
}

// interactionService is the Root's contracts.UserInteraction implementation.
// It is safe for concurrent use; handler calls and pending-map changes never
// hold the mutex while calling user code.
type interactionService struct {
	mu      sync.Mutex
	handler func(contracts.UserQuestion)
	pending map[string]*interactionPending
	nextID  atomic.Uint64
}

// newInteractionService returns an empty bridge.
func newInteractionService() *interactionService {
	return &interactionService{pending: make(map[string]*interactionPending)}
}

// SetHandler installs the question handler (nil clears it).
func (s *interactionService) SetHandler(fn func(contracts.UserQuestion)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// Ask creates a pending question, invokes the installed handler and blocks
// until Answer/Cancel resolves it or ctx ends. The handler runs outside the
// service mutex so it may call Answer directly (tests) or post to the UI thread.
func (s *interactionService) Ask(ctx context.Context, text string, options []string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	s.mu.Lock()
	handler := s.handler
	if handler == nil {
		s.mu.Unlock()
		return "", ErrInteractiveUnavailable
	}
	if s.pending == nil {
		s.pending = make(map[string]*interactionPending)
	}
	id := fmt.Sprintf("question-%d", s.nextID.Add(1))
	pending := &interactionPending{
		answers: make(chan string, 1),
		cancels: make(chan error, 1),
	}
	s.pending[id] = pending
	s.mu.Unlock()

	handler(contracts.UserQuestion{
		ID:      id,
		Text:    text,
		Options: append([]string(nil), options...),
	})

	select {
	case answer := <-pending.answers:
		return answer, nil
	case err := <-pending.cancels:
		return "", err
	case <-ctx.Done():
		s.remove(id)
		return "", ctx.Err()
	}
}

// Answer resolves the pending question with the chosen answer. Unknown or
// already-resolved IDs are a no-op.
func (s *interactionService) Answer(id, answer string) {
	s.resolve(id, func(pending *interactionPending) {
		pending.answers <- answer
	})
}

// Cancel dismisses the pending question with ErrUserCancelled. Unknown or
// already-resolved IDs are a no-op.
func (s *interactionService) Cancel(id string) {
	s.resolve(id, func(pending *interactionPending) {
		pending.cancels <- ErrUserCancelled
	})
}

// resolve removes id and delivers exactly one result; concurrent Answer/Cancel
// calls race on the map entry and only the winner delivers.
func (s *interactionService) resolve(id string, deliver func(*interactionPending)) {
	s.mu.Lock()
	pending, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if ok {
		deliver(pending)
	}
}

// remove drops a cancelled pending question if it is still registered.
func (s *interactionService) remove(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// UserInteraction returns the host interaction bridge
// (contracts.KeyUserInteraction on the desktop product).
func (r *Root) UserInteraction() *interactionService { return r.interaction }
