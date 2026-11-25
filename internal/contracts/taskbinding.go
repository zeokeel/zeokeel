package contracts

// Session ⇄ task binding (M5 enforcement).
//
// A task carries an execution policy: the tools its owner may run, recorded by
// TaskLeaseService.SetTools and read back by TaskLeaseService.Tools. This file
// owns the other half of that policy — which session is working on the task —
// so the agent's tool gate can refuse a call outside the binding instead of
// merely recording it.
//
// The binding is one settings row holding one task id (no second document, no
// new service): the agent already persists per-session rows this way, and a row
// that disappears with its conversation is exactly the lifetime a binding wants.
// An empty or missing row means "unbound", which is the same behaviour the
// product had before this file existed.

import (
	"context"
	"errors"
)

// SessionBoundTaskSettingKey is the settings row storing the task one session is
// bound to. The value is a single task id; a missing or empty row means the
// session is not bound to anything (`session.bindtask` with an empty taskId
// writes the empty row, which un-binds).
func SessionBoundTaskSettingKey(sessionID string) string {
	return "session." + sessionID + ".boundTask"
}

// MaxSessionBoundTaskLen bounds the stored task id. An id longer than this is a
// caller bug (a pasted transcript) rather than an id; the write is refused
// instead of being stored and never matching a task.
const MaxSessionBoundTaskLen = 120

// ErrSessionTaskBindingInvalid means a bind request was malformed: an empty
// session id, or a task id with whitespace/control characters or over
// MaxSessionBoundTaskLen. It is a *request* problem (the gateway answers
// -32600), never a reason to modify the stored binding.
var ErrSessionTaskBindingInvalid = errors.New("session task binding: invalid")

// SessionTaskBinder is the optional half of the chat service that binds a
// conversation to one board task. The binding only ever narrows: once bound,
// the agent's tool gate refuses tools outside that task's binding, and tools the
// task does not restrict (no binding at all) keep today's approval flow.
//
// It is optional because a product without the task board has nothing to bind
// to; the gateway reports the method as unavailable instead of pretending.
type SessionTaskBinder interface {
	// BindSessionTask points sessionID at taskID. An empty taskID clears the
	// binding. The task does not have to exist yet (dispatch and binding are two
	// calls) and a binding to a task the board no longer has is inert at the
	// gate, not an error at write time.
	BindSessionTask(ctx context.Context, sessionID, taskID string) error
	// SessionTask reports the task the session is bound to ("" when unbound).
	SessionTask(sessionID string) (string, error)
}
