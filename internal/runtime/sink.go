package runtime

import (
	"context"
	"sync"

	"github.com/zeokeel/zeokeel/internal/state"
)

// eventSink serializes event delivery for one turn. The turn goroutine and any
// tool goroutine publishing an approval request both send through it, so the
// output channel is closed exactly once and never while a send is in flight.
// A send that loses a race with the end of the turn is dropped instead of
// panicking.
type eventSink struct {
	mu     sync.Mutex
	ch     chan<- state.Event
	closed bool
}

func newEventSink(ch chan<- state.Event) *eventSink {
	return &eventSink{ch: ch}
}

// send publishes ev, returning false when ctx ends or the turn is over. It may
// block while the consumer drains the channel, exactly like the previous
// direct channel send.
func (s *eventSink) send(ctx context.Context, ev state.Event) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// trySend publishes ev only when a receiver is ready. Terminal events emitted
// after the turn has decided to stop must not block a cancelled consumer.
func (s *eventSink) trySend(ev state.Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- ev:
	default:
	}
}

// close closes the event channel and makes later sends no-ops. Only the run
// goroutine calls close.
func (s *eventSink) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

// turnContextKey carries per-turn runtime state into tool invocations. eino
// invokes tools with a context derived from the runner's context, so this is
// how a tool knows which turn's event sink to publish an approval request on.
type turnContextKey struct{}

type turnContext struct {
	sink   *eventSink
	record *turnRecord
	// userRequest is the user text that started this turn. The automatic
	// reviewer uses it as authorization evidence (never as an instruction).
	userRequest string
	// reviewContext supplies the reviewer's environment per review: the
	// current working directory and the project constraints. Nil leaves both
	// empty.
	reviewContext func() (workDir, constraints string)

	// failedTools records the calls whose Invoke returned an error. The
	// adapter delivers the error to the model as a tool result (so one bad
	// command does not end the turn); the ToolEnd event still needs the
	// error state for the UI's red card and the tool_calls table.
	mu          sync.Mutex
	failedTools map[string]bool
}

// markToolFailure records callID as a failed invocation for the current turn.
func (tc *turnContext) markToolFailure(callID string) {
	if tc == nil || callID == "" {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.failedTools == nil {
		tc.failedTools = make(map[string]bool)
	}
	tc.failedTools[callID] = true
}

// takeToolFailure reports whether callID failed and forgets the record.
func (tc *turnContext) takeToolFailure(callID string) bool {
	if tc == nil || callID == "" {
		return false
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	failed := tc.failedTools[callID]
	delete(tc.failedTools, callID)
	return failed
}

func withTurnContext(ctx context.Context, tc *turnContext) context.Context {
	return context.WithValue(ctx, turnContextKey{}, tc)
}

func turnContextFromContext(ctx context.Context) *turnContext {
	if ctx == nil {
		return nil
	}
	tc, _ := ctx.Value(turnContextKey{}).(*turnContext)
	return tc
}
