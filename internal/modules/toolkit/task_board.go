package toolkit

// Shared task-board service: the model-facing team_task_* tools and the UI's
// todo panel operate on the same rig team.TaskManager instance.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// taskBoardAdapter implements contracts.TaskBoard over one team.TaskManager.
// The manager is shared with the registered team_task_* tools; store keeps the
// board durable across restarts (nil when the host has no data directory).
type taskBoardAdapter struct {
	manager *team.TaskManager
	store   *taskBoardStore
	// loadOK records that the durable document existed and parsed (even if it
	// was a valid empty board); false means missing/corrupt/unreadable.
	loadOK bool

	// deleted tracks tasks removed through the board API. rig v0.13's
	// TaskManager has no Delete operation, so Delete tombstones the id; the
	// board List hides it and SetStatus/Delete treat it as unknown.
	mu      sync.Mutex
	deleted map[string]bool
	// removed marks a conversation board whose document was deleted: a bound
	// tool that was resolved before the removal must not recreate the document.
	removed bool
}

var (
	_ contracts.TaskBoard    = (*taskBoardAdapter)(nil)
	_ contracts.TaskAssigner = (*taskBoardAdapter)(nil)
)

// newTaskBoard wraps the toolkit's shared task manager.
func newTaskBoard(manager *team.TaskManager, store *taskBoardStore) *taskBoardAdapter {
	return &taskBoardAdapter{manager: manager, store: store, deleted: make(map[string]bool)}
}

// setDeleted seeds the tombstoned ids restored from the durable board.
func (b *taskBoardAdapter) setDeleted(deleted map[string]bool) {
	if b == nil || len(deleted) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range deleted {
		b.deleted[id] = true
	}
}

// retire marks the board removed and stops all later persistence. It is used by
// RemoveTaskBoardForSession: tools bound before the deletion keep this adapter
// and must not resurrect the document.
func (b *taskBoardAdapter) retire() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.removed = true
	b.mu.Unlock()
	if b.store != nil {
		b.store.retire()
	}
}

// persist snapshots the live board into the durable document and reports the
// write error (persistence is best effort for ordinary mutations; the migration
// commit depends on it).
func (b *taskBoardAdapter) persist() error {
	if b == nil || b.store == nil {
		return nil
	}
	b.mu.Lock()
	if b.removed {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()
	tasks, err := b.manager.List(context.Background(), rig.TeamTaskFilter{})
	if err != nil {
		return err
	}
	b.mu.Lock()
	deleted := make(map[string]bool, len(b.deleted))
	for id := range b.deleted {
		deleted[id] = true
	}
	b.mu.Unlock()
	return b.store.save(tasks, deleted)
}

// List implements contracts.TaskBoard.
func (b *taskBoardAdapter) List(ctx context.Context) ([]contracts.BoardTask, error) {
	tasks, err := b.manager.List(ctx, rig.TeamTaskFilter{})
	if err != nil {
		return nil, fmt.Errorf("toolkit: task board: %w", err)
	}
	b.mu.Lock()
	deleted := make(map[string]bool, len(b.deleted))
	for id := range b.deleted {
		deleted[id] = true
	}
	b.mu.Unlock()
	out := make([]contracts.BoardTask, 0, len(tasks))
	for _, task := range tasks {
		if deleted[task.ID] {
			continue
		}
		out = append(out, boardTaskFromRig(task))
	}
	return out, nil
}

// Create implements contracts.TaskBoard.
func (b *taskBoardAdapter) Create(ctx context.Context, subject, description string) (contracts.BoardTask, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return contracts.BoardTask{}, errors.New("toolkit: task board: subject is required")
	}
	task, err := b.manager.Create(ctx, rig.TeamTask{
		Subject:     subject,
		Description: strings.TrimSpace(description),
		Status:      contracts.TaskStatusPending,
	})
	if err != nil {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w", err)
	}
	b.persist()
	return boardTaskFromRig(task), nil
}

// SetStatus implements contracts.TaskBoard. rig requires an owner for an
// in_progress task; the board-only API has no owner parameter, so it assigns
// the stable pseudo-owner "taskboard" when the task has none.
func (b *taskBoardAdapter) SetStatus(ctx context.Context, id, status string) (contracts.BoardTask, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case contracts.TaskStatusPending, contracts.TaskStatusInProgress, contracts.TaskStatusCompleted:
	default:
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: invalid status %q", status)
	}
	if b.isDeleted(id) {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w: %q", team.ErrTaskNotFound, strings.TrimSpace(id))
	}
	task, err := b.manager.Get(ctx, id)
	if err != nil {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w", err)
	}
	update := rig.TeamTaskUpdate{Status: &status}
	if status == contracts.TaskStatusInProgress && strings.TrimSpace(task.Owner) == "" {
		owner := "taskboard"
		update.Owner = &owner
	}
	updated, err := b.manager.Update(ctx, id, update)
	if err != nil {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w", err)
	}
	b.persist()
	return boardTaskFromRig(updated), nil
}

// Assign implements contracts.TaskAssigner: it sets (or clears) one task's
// owner through the shared manager, so the model tools and the inspector see the
// same assignee. The rig manager rejects an in-progress task without an owner;
// clearing the owner of an in-progress task is therefore reported as an error
// rather than silently demoting the task.
func (b *taskBoardAdapter) Assign(ctx context.Context, id, owner string) (contracts.BoardTask, error) {
	id = strings.TrimSpace(id)
	owner = strings.TrimSpace(owner)
	if id == "" {
		return contracts.BoardTask{}, errors.New("toolkit: task board: task id is required")
	}
	if b.isDeleted(id) {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w: %q", team.ErrTaskNotFound, id)
	}
	task, err := b.manager.Get(ctx, id)
	if err != nil {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w", err)
	}
	update := rig.TeamTaskUpdate{Owner: &owner}
	if owner == "" && task.Status == contracts.TaskStatusInProgress {
		return contracts.BoardTask{}, fmt.Errorf(
			"toolkit: task board: in-progress tasks need an owner; complete or reopen the task first")
	}
	updated, err := b.manager.Update(ctx, id, update)
	if err != nil {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w", err)
	}
	b.persist()
	return boardTaskFromRig(updated), nil
}

// Delete implements contracts.TaskBoard. rig v0.13 has no Delete, so the task is
// tombstoned and hidden from the board; the model tools keep their own view.
func (b *taskBoardAdapter) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("toolkit: task board: task id is required")
	}
	if b.isDeleted(id) {
		return fmt.Errorf("toolkit: task board: %w: %q", team.ErrTaskNotFound, id)
	}
	if _, err := b.manager.Get(ctx, id); err != nil {
		return fmt.Errorf("toolkit: task board: %w", err)
	}
	b.mu.Lock()
	b.deleted[id] = true
	b.mu.Unlock()
	b.persist()
	return nil
}

// deletedIDs returns the tombstones of this board (for migration).
func (b *taskBoardAdapter) deletedIDs() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.deleted))
	for id := range b.deleted {
		out = append(out, id)
	}
	return out
}

// isDeleted reports whether the board API removed id.
func (b *taskBoardAdapter) isDeleted(id string) bool {
	id = strings.TrimSpace(id)
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.deleted[id]
}

// boardTaskFromRig converts one rig task into the product BoardTask.
func boardTaskFromRig(task rig.TeamTask) contracts.BoardTask {
	return contracts.BoardTask{
		ID:          task.ID,
		Subject:     task.Subject,
		Description: task.Description,
		Status:      task.Status,
		Owner:       task.Owner,
		BlockedBy:   append([]string(nil), task.BlockedBy...),
		WriteScopes: append([]string(nil), task.WriteScopes...),
		Created:     task.Created,
		Updated:     task.Updated,
	}
}
