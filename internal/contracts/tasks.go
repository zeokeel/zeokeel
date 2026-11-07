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

// BoardTask is one shared task-board item (the old shell's todo list).
type BoardTask struct {
	ID          string
	Subject     string
	Description string
	Status      string
	Owner       string
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

// KeyTaskBoard is provided by the toolkit module (rig team.TaskManager).
var KeyTaskBoard = cordis.NewServiceKey[TaskBoard]("zeokeel/taskboard")
