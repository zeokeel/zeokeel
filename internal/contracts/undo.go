package contracts

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ErrUndoUnavailable reports "this host has no snapshot tracker for the run's
// workspace" (read-only build, no data directory, snapshot backend failure). The
// agent hook treats it as an expected condition, not an error to log.
var ErrUndoUnavailable = errors.New("undo: no snapshot tracker for this workspace")

// UndoService snapshots the run's workspace before a turn so the model's `undo`
// tool can revert that turn. The toolkit owns the trackers (one per workspace
// build); the agent only says "a turn is starting in this root".
//
// BeginTurn is best effort: a host without git and without a snapshot directory
// must not fail the run — undo simply reports that nothing can be reverted.
type UndoService interface {
	// BeginTurn takes a snapshot for the turn about to run in root (empty root =
	// the current workspace) and returns the turn number on success. It must
	// respect ctx: a cancelled run must not keep copying files.
	BeginTurn(ctx context.Context, root string) (int, error)
}

// KeyUndo is the toolkit-provided undo snapshot service (optional: a stripped
// assembly simply has no undo tool).
var KeyUndo = cordis.NewServiceKey[UndoService]("zeokeel/undo")
