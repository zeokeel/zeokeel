package toolkit

// Shared task-board service: the model-facing team_task_* tools and the UI's
// todo panel operate on the same rig team.TaskManager instance.
//
// The durable half of one board (JSON document, atomic writes, replay,
// tombstones, renumbering) is the Rig SDK taskstore.Store; this adapter maps
// the board API onto one manager plus its store.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/team"
	"github.com/zeokeel/zeokeel-rig/tools/team/taskstore"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// taskBoardAdapter implements contracts.TaskBoard over one team.TaskManager.
// The manager is shared with the registered team_task_* tools; store keeps the
// board durable across restarts (it has no path when the host has no data
// directory, which makes it memory-only).
type taskBoardAdapter struct {
	manager *team.TaskManager
	store   *taskstore.Store
	// loadOK records that the durable document existed and parsed (even if it
	// was a valid empty board); false means missing/corrupt/unreadable.
	loadOK bool
	// closeMu guards closeObserver, which the lease module installs at activation
	// while tool calls may already be running.
	closeMu sync.RWMutex
	// closeObserver is the audit sink for tasks that reach a terminal state
	// without a lease review (contracts.TaskCloseAuditor). Nil means this
	// assembly has no lease layer, so there is nowhere to record it.
	closeObserver contracts.TaskCloseAuditor
	// logf is the board service's logger (nil-safe: an adapter built outside the
	// service, e.g. a memory board in a unit test, has none).
	logf func(string, ...any)
}

var (
	_ contracts.TaskBoard               = (*taskBoardAdapter)(nil)
	_ contracts.TaskAssigner            = (*taskBoardAdapter)(nil)
	_ contracts.TaskBoardRestorer       = (*taskBoardAdapter)(nil)
	_ contracts.TaskCloseObserverSetter = (*taskBoardAdapter)(nil)
)

// newTaskBoard wraps one rig task manager and its SDK store.
func newTaskBoard(manager *team.TaskManager, store *taskstore.Store) *taskBoardAdapter {
	return &taskBoardAdapter{manager: manager, store: store}
}

// memoryTaskBoard returns an in-memory board that never persists: used for a
// deleted conversation (a late tool call must not resurrect the document) and
// for the retired legacy board after migration.
func memoryTaskBoard() *taskBoardAdapter {
	return newTaskBoard(team.NewTaskManager(), taskstore.New("", ""))
}

// setDeleted seeds the tombstoned ids restored from the durable board.
func (b *taskBoardAdapter) setDeleted(deleted []string) {
	if b == nil || b.store == nil {
		return
	}
	b.store.SetDeleted(deleted)
}

// deletedSet returns the tombstoned ids as a set for board filtering.
func (b *taskBoardAdapter) deletedSet() map[string]bool {
	if b == nil || b.store == nil {
		return nil
	}
	ids := b.store.Deleted()
	if len(ids) == 0 {
		return nil
	}
	deleted := make(map[string]bool, len(ids))
	for _, id := range ids {
		deleted[id] = true
	}
	return deleted
}

// persist snapshots the live board into the durable document and reports the
// write error (persistence is best effort for ordinary mutations; the migration
// commit depends on it). The SDK store is a no-op after it was retired.
func (b *taskBoardAdapter) persist() error {
	if b == nil || b.store == nil {
		return nil
	}
	return b.store.Save(context.Background(), b.manager)
}

// List implements contracts.TaskBoard.
func (b *taskBoardAdapter) List(ctx context.Context) ([]contracts.BoardTask, error) {
	tasks, err := b.manager.List(ctx, rig.TeamTaskFilter{})
	if err != nil {
		return nil, fmt.Errorf("toolkit: task board: %w", err)
	}
	deleted := b.deletedSet()
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
	// No close audit here on purpose: TaskLeaseService.Accept completes the board
	// through this same method right after recording its own review, and a hook
	// at this layer would append an "unaudited" row to every properly reviewed
	// task. The audited closes are the entry points that bypass the lease — the
	// gateway's task.status command and the model's team_task_update tool — and
	// they record themselves (see notifyDirectClose callers).
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

// Delete implements contracts.TaskBoard. rig has no Delete, so the task is
// tombstoned in the SDK store and hidden from the board; the model tools keep
// their own view.
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
	if b.store != nil {
		b.store.MarkDeleted(id)
	}
	b.persist()
	return nil
}

// RestoreTask implements contracts.TaskBoardRestorer: Delete hides a task behind
// a tombstone (rig has no Delete), so restoring is clearing that tombstone — the
// manager still holds the row with its id, status, owner and timestamps.
func (b *taskBoardAdapter) RestoreTask(ctx context.Context, id string) (contracts.BoardTask, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return contracts.BoardTask{}, errors.New("toolkit: task board: task id is required")
	}
	if !b.isDeleted(id) {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w: %q", team.ErrTaskNotFound, id)
	}
	if b.store != nil {
		b.store.ClearDeleted(id)
	}
	b.persist()
	task, err := b.manager.Get(ctx, id)
	if err != nil {
		return contracts.BoardTask{}, fmt.Errorf("toolkit: task board: %w", err)
	}
	return boardTaskFromRig(task), nil
}

// isDeleted reports whether the board API removed id.
func (b *taskBoardAdapter) isDeleted(id string) bool {
	if b == nil || b.store == nil {
		return false
	}
	id = strings.TrimSpace(id)
	for _, deleted := range b.store.Deleted() {
		if deleted == id {
			return true
		}
	}
	return false
}

// Entry points that can move a task into a terminal state without a lease
// review. They are named so the audit row and the UI say which one did it.
const (
	// taskCloseSourceStatus is the gateway's `task.status` command (the
	// complete/stop chip in the task panel).
	taskCloseSourceStatus = "tasks.status"
	// taskCloseSourceTool is the model-facing `team_task_update` tool
	// (action=complete, or action=edit with status=completed).
	taskCloseSourceTool = "team_task_update"
	// taskCloseActorUser is the identity behind the UI's commands: this product
	// has one human, and the gateway has no per-user identity to name.
	taskCloseActorUser = "user"
	// taskCloseActorAgent is the identity behind a model-initiated close.
	taskCloseActorAgent = "agent"
)

// SetTaskCloseObserver implements contracts.TaskCloseObserverSetter: the lease
// module installs its review-log writer here at activation. Nil is valid and
// means "this assembly has no review log to write to".
func (b *taskBoardAdapter) SetTaskCloseObserver(observer contracts.TaskCloseAuditor) {
	if b == nil {
		return
	}
	b.closeMu.Lock()
	b.closeObserver = observer
	b.closeMu.Unlock()
}

// notifyDirectClose records one unaudited close when this board has a sink and
// the task really is in a terminal state. A failure is logged, never returned:
// the task is already completed, and turning the audit write into a failed
// command would make the client retry a write that succeeded.
func (b *taskBoardAdapter) notifyDirectClose(ctx context.Context, task contracts.BoardTask, actor, source string) {
	if b == nil || task.Status != contracts.TaskStatusCompleted {
		return
	}
	b.closeMu.RLock()
	observer := b.closeObserver
	b.closeMu.RUnlock()
	if observer == nil {
		return
	}
	if err := observer.RecordDirectClose(ctx, task.ID, actor, source); err != nil {
		if b.logf != nil {
			b.logf("[toolkit] record unaudited close of task %s: %v", task.ID, err)
		}
	}
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

// persistingTaskTool wraps one model-facing task tool: after a successful
// invocation it writes the board document, so a task created/updated through the
// model survives the next restart exactly like one changed from the UI.
type persistingTaskTool struct {
	inner rig.Tool
	board *taskBoardAdapter
}

var _ rig.Tool = (*persistingTaskTool)(nil)

// Name implements rig.Tool.
func (t *persistingTaskTool) Name() string { return t.inner.Name() }

// Description implements rig.Tool.
func (t *persistingTaskTool) Description() string { return t.inner.Description() }

// Schema implements rig.Tool.
func (t *persistingTaskTool) Schema() map[string]any { return t.inner.Schema() }

// Invoke implements rig.Tool. Only the mutating task tools persist: a read-only
// list/get must not rewrite the document (that previously recreated a retired
// legacy document after migration).
func (t *persistingTaskTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	// The status before the call: a close is a transition, not a state, so a
	// no-op update on an already-completed task must not add an audit row.
	before := ""
	if t.inner.Name() == "team_task_update" {
		if id := taskUpdateID(call.Arguments); id != "" {
			if task, getErr := t.board.manager.Get(ctx, id); getErr == nil {
				before = task.Status
			}
		}
	}
	result, err := t.inner.Invoke(ctx, call)
	if err == nil && !result.IsError && taskToolMutates(t.inner.Name()) {
		t.board.persist()
		if t.inner.Name() == "team_task_update" {
			// The model can close a task with this tool (action=complete, or
			// action=edit with status=completed) — the second path that used to
			// leave no audit row. The action/status vocabulary is the SDK's, so
			// the board is re-read instead of the arguments being re-parsed: what
			// matters is the state the call produced, not the spelling it used.
			// Pre-completed tasks are skipped, so a no-op update does not look
			// like a fresh close.
			if id := taskUpdateID(call.Arguments); id != "" && before != contracts.TaskStatusCompleted {
				if task, getErr := t.board.manager.Get(ctx, id); getErr == nil {
					t.board.notifyDirectClose(ctx, boardTaskFromRig(task), taskCloseActorAgent, taskCloseSourceTool)
				}
			}
		}
	}
	return result, err
}

// taskUpdateID reads the task id out of one team_task_update call. An unreadable
// argument blob is "no id": the call either failed (no persist) or updated
// nothing, so there is no close to record.
func taskUpdateID(arguments string) string {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.ID)
}

// taskToolMutates reports whether a task tool changes the board.
func taskToolMutates(name string) bool {
	switch name {
	case "team_task_create", "team_task_update":
		return true
	default:
		return false
	}
}

// wrapPersistingTaskTools wraps the task-board tools of one manager.
func wrapPersistingTaskTools(tools []rig.Tool, board *taskBoardAdapter) []rig.Tool {
	if board == nil || board.store == nil {
		return tools
	}
	wrapped := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		wrapped = append(wrapped, &persistingTaskTool{inner: tool, board: board})
	}
	return wrapped
}
