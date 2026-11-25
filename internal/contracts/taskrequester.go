package contracts

// Who asked for a task (M6 attribution).
//
// The board records the work and the lease records the executor, but neither says
// which human wanted it. That question is what makes cost attribution and the audit
// trail possible ("this bot spent a million tokens on Alice's request"), so it is
// stored with the same task-scoped rows the lease already keeps.
//
// Its own service key, like held sends: a consumer that only records attribution
// should not implement thirteen lease methods.

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ErrTaskRequesterInvalid means the write was malformed (empty task id or empty
// user id). It is a request problem, never a reason to modify a stored row.
var ErrTaskRequesterInvalid = errors.New("task requester: invalid")

// TaskRequester records which user asked for a task.
type TaskRequester interface {
	// SetRequestedBy records the requesting user for one task. Recording twice
	// replaces the row: the newest statement of who asked wins.
	SetRequestedBy(ctx context.Context, taskID, userID string) error
	// RequestedBy reports the user who asked (found is false when unknown).
	RequestedBy(ctx context.Context, taskID string) (userID string, found bool, err error)
}

// KeyTaskRequester is provided by the task lease module.
var KeyTaskRequester = cordis.NewServiceKey[TaskRequester]("zeokeel/taskrequester")
