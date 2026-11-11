package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Task board statuses (the shared team task board behind the todo tools).
const (
	TaskStatusPending    = "pending"
	TaskStatusInProgress = "in_progress"
	TaskStatusCompleted  = "completed"
)

// TaskOwnerUnassigned is the stable pseudo owner the board-only API stamps on
// an in-progress task that has no real assignee (the rig manager requires an
// owner for work in progress). It is a backend detail: the inspector shows it
// as the lead agent instead of hiding the owner line.
const TaskOwnerUnassigned = "taskboard"

// BoardTask is one shared task-board item (the old shell's todo list).
type BoardTask struct {
	ID          string
	Subject     string
	Description string
	Status      string
	Owner       string
	// BlockedBy lists the task IDs this task waits for. The rig manager
	// validates the IDs and cycles; the board API only reads them.
	BlockedBy []string
	// WriteScopes lists the workspace paths this task expects to write.
	WriteScopes []string
	Created     time.Time
	Updated     time.Time
}

// TaskBoard is the product-side view of the shared team task board: the todo
// panel lists/drives tasks while the agent's team_task_* tools mutate the same
// manager instance.
type TaskBoard interface {
	List(ctx context.Context) ([]BoardTask, error)
	Create(ctx context.Context, subject, description string) (BoardTask, error)
	SetStatus(ctx context.Context, id, status string) (BoardTask, error)
	Delete(ctx context.Context, id string) error
}

// TaskAssigner is the optional owner half of the task board: the rigor board
// requires an owner for in-progress tasks, and the inspector uses this to
// assign/claim/reassign a task to the lead or one of the live teammates.
// Backends that do not implement it keep the read-only owner line.
type TaskAssigner interface {
	// Assign sets (or clears, when owner is "") one task's owner.
	Assign(ctx context.Context, id, owner string) (BoardTask, error)
}

// SessionTaskBoard is the optional per-session view of the shared task board.
// DSH's team board belongs to the Lead session that created it; a backend that
// scopes tasks by session implements this so the session UI can show only the
// active conversation's tasks. Backends that do not implement it keep the
// process-wide board returned by KeyTaskBoard.
//
// The read path is cache-first and may perform at most one bounded document
// read on a cache miss; it never migrates or writes. Unknown/non-empty session
// IDs return an empty scoped board when no document exists, while "" keeps the
// legacy/global board for hosts without a session context.
type SessionTaskBoard interface {
	TaskBoardForSession(sessionID string) TaskBoard
}

// TaskBoardCleaner is the optional cleanup hook of the per-session task board:
// deleting a conversation removes its board document and cached state.
type TaskBoardCleaner interface {
	RemoveTaskBoardForSession(sessionID string) error
}

// KeyTaskBoard is provided by the toolkit module (rig team.TaskManager).
var KeyTaskBoard = cordis.NewServiceKey[TaskBoard]("zeokeel/taskboard")
