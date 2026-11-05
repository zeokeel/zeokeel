package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ErrApprovalExpired reports that a tool call was waiting for a human
// decision when the turn's context ended. It is wrapped together with the
// context error, so errors.Is matches both ErrApprovalExpired and
// context.Canceled / context.DeadlineExceeded.
var ErrApprovalExpired = errors.New("runtime: approval expired")

// Gate decides which tool invocations need a human decision and blocks those
// invocations until the decision arrives.
//
// RequireApproval is consulted before a tool runs. When it returns true the
// runtime publishes a state.ApprovalRequest for the call and then calls Wait;
// the tool invocation is blocked until the decision is delivered or ctx ends.
// Returning false from Wait denies the call: the runtime reports a failed
// ToolEnd and delivers the denial to the model as the call's tool result,
// without invoking the tool, so the conversation can continue.
//
// Wait is channel-based rather than polled so an approval never burns CPU and
// a cancelled turn unblocks immediately. Implementations must be safe for
// concurrent use across sessions.
type Gate interface {
	RequireApproval(toolName string, args json.RawMessage) bool
	Wait(ctx context.Context, callID string) (allow bool, err error)
}

// ApprovalQueue is the in-process Gate used by the desktop app: the UI receives
// state.ApprovalRequest events, renders a prompt, and calls Approve or Deny
// with the event's CallID.
//
// Decisions may arrive before the matching Wait call (the approval event is
// published slightly before the tool reaches Wait); such early decisions are
// buffered per call id and consumed by the next Wait. A decision for a call id
// that never waits is retained, so callers should only resolve ids they saw in
// an ApprovalRequest.
type ApprovalQueue struct {
	require func(toolName string, args json.RawMessage) bool

	mu      sync.Mutex
	waiters map[string]chan bool
	early   map[string]bool
	rules   map[string]struct{}
}

var _ Gate = (*ApprovalQueue)(nil)

// NewApprovalQueue returns a queue whose approvals are requested for exactly
// the tool calls accepted by require. A nil require means every tool call needs
// approval.
func NewApprovalQueue(require func(toolName string, args json.RawMessage) bool) *ApprovalQueue {
	return &ApprovalQueue{
		require: require,
		waiters: make(map[string]chan bool),
		early:   make(map[string]bool),
		rules:   make(map[string]struct{}),
	}
}

// RequireApproval implements Gate. A tool allow-listed with AllowAlways never
// requires approval; otherwise the queue's predicate decides.
func (q *ApprovalQueue) RequireApproval(toolName string, args json.RawMessage) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	_, always := q.rules[toolName]
	require := q.require
	q.mu.Unlock()
	if always {
		return false
	}
	if require == nil {
		return true
	}
	return require(toolName, args)
}

// AllowAlways records that toolName may run without future approval prompts in
// this queue. The rule lives only in memory: it is not persisted by the runtime
// and disappears when the queue is recreated or ClearRules is called.
//
// AllowAlways is safe to call concurrently with RequireApproval and with tool
// turns; an in-flight call that already received a pending ApprovalRequest still
// waits for its explicit Approve/Deny decision.
func (q *ApprovalQueue) AllowAlways(toolName string) {
	if q == nil || toolName == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.rules == nil {
		q.rules = make(map[string]struct{})
	}
	q.rules[toolName] = struct{}{}
}

// ClearRules forgets every AllowAlways rule, restoring the predicate's normal
// approval behaviour. It does not resolve tool calls that are already waiting.
func (q *ApprovalQueue) ClearRules() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	clear(q.rules)
}

// Wait implements Gate. It blocks until Approve/Deny resolves callID or ctx
// ends. A ctx that ends first returns ErrApprovalExpired wrapping ctx.Err().
func (q *ApprovalQueue) Wait(ctx context.Context, callID string) (bool, error) {
	if q == nil {
		return false, errors.New("runtime: nil approval queue")
	}
	if ctx == nil {
		return false, errors.New("runtime: nil approval context")
	}

	q.mu.Lock()
	if allow, ok := q.early[callID]; ok {
		delete(q.early, callID)
		q.mu.Unlock()
		return allow, nil
	}
	waiter := make(chan bool, 1)
	q.waiters[callID] = waiter
	q.mu.Unlock()

	select {
	case allow := <-waiter:
		return allow, nil
	case <-ctx.Done():
		q.mu.Lock()
		// Only remove our own waiter: a decision racing with the cancellation
		// may already have delivered into waiter and removed the entry.
		if current, ok := q.waiters[callID]; ok && current == waiter {
			delete(q.waiters, callID)
		}
		q.mu.Unlock()
		return false, fmt.Errorf("%w: %w", ErrApprovalExpired, ctx.Err())
	}
}

// Approve allows callID. It reports whether a blocked Wait received the
// decision; false means the decision was buffered for a future Wait (or the
// queue is nil).
func (q *ApprovalQueue) Approve(callID string) bool {
	return q.resolve(callID, true)
}

// Deny rejects callID. It reports whether a blocked Wait received the
// decision; false means the decision was buffered for a future Wait (or the
// queue is nil).
func (q *ApprovalQueue) Deny(callID string) bool {
	return q.resolve(callID, false)
}

func (q *ApprovalQueue) resolve(callID string, allow bool) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if waiter, ok := q.waiters[callID]; ok {
		delete(q.waiters, callID)
		// waiter is buffered, so a Wait that lost a race with cancellation can
		// never block this goroutine.
		waiter <- allow
		return true
	}
	q.early[callID] = allow
	return false
}
