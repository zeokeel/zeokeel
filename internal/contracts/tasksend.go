package contracts

// Held sends: the outbound half of the approval gate.
//
// A task with approval=before_send starts normally — the person approved the work,
// not the words — and the message it wants to send when it settles waits with the
// task. Nothing leaves the product until `tasks.send.approve` is called, and an
// approved message leaves exactly once.
//
// It is its own service key (provided by the task lease module) so the lease
// interface stays about leases: a consumer that only holds sends does not have to
// implement thirteen lease methods to do it.

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ErrHeldSendUnknown means there is no held message for that task (never held, or
// already approved and sent).
var ErrHeldSendUnknown = errors.New("held send: none for this task")

// HeldSend is one parked message in the lease document.
type HeldSend struct {
	TaskID string
	Text   string
}

// TaskSendHolder parks and releases the message a before_send task wants out.
type TaskSendHolder interface {
	// Hold parks the text for one task, replacing any earlier hold (the newest
	// message is the one that would go out).
	Hold(ctx context.Context, taskID, text string) error
	// Held returns the parked text (found is false when nothing is held).
	Held(ctx context.Context, taskID string) (text string, found bool, err error)
	// Approve returns the parked text and forgets it, so an approved message
	// leaves once. A second approval reports ErrHeldSendUnknown.
	ApproveSend(ctx context.Context, taskID string) (string, error)
}

// KeyTaskSendHolder is provided by the task lease module.
var KeyTaskSendHolder = cordis.NewServiceKey[TaskSendHolder]("zeokeel/tasksend")
