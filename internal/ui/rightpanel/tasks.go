package rightpanel

// The Tasks pane of the session inspector: the shared team task board (the
// lead's and the teammates' todo list). Phase 3d moved the board's rendering
// anatomy to uikit.TeamSurface (AGENT-SURFACES.md S9); this module keeps only
// the data (the optional contracts.KeyTaskBoard service, its lazy cache, the
// explicit off-thread refresh) and the actions.
//
// The pane is an optional host service looked up lazily with ctx.Get; without
// it the pane shows an explicit empty state instead of failing activation. The
// uikit surface groups the task list by status with counts, assigns the owner
// through the optional contracts.TaskAssigner, shows the dependency/blocker line
// and expands one task's detail below the list. The destructive confirmation is
// a native Modal with no default action.

import (
	"context"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildTasksPane renders the task board: the add row, the grouped native list
// with counts, the selected task's detail below it, the error/retry row and the
// delete confirmation (all in uikit.TeamSurface).
func buildTasksPane(c *ui.Context, _ *design.Tokens, board contracts.TaskBoard,
	runtime contracts.UIRuntime, st *state) {
	if board == nil {
		buildEmptyPane(c, st.tr("任务板未启用", "Task board is not enabled"))
		return
	}
	if !st.tasksLoaded {
		loadBoardTasks(board, st)
	}

	// The owner menu offers the live teammates; without the optional team
	// service only 主代理/未认领 are listed (and the owner stays read-only when
	// the board has no TaskAssigner at all).
	var members []contracts.TeamMember
	if team := teamServiceFromState(st); team != nil {
		members = team.Members()
	}
	_, canAssign := board.(contracts.TaskAssigner)

	uikit.TeamSurface(c, uikit.TeamSurfaceOptions{
		Locale:       st.locale,
		Pane:         uikit.TeamPaneTasks,
		Tasks:        st.tasks,
		TasksLoading: st.tasksLoading,
		Members:      members,
		CanAssign:    canAssign,

		TaskList:     &st.tasksList,
		TaskSel:      &st.tasksSel,
		ExpandedTask: &st.tasksExpanded,
		TaskDraft:    &st.taskDraft,
		Err:          &st.tasksErr,

		TaskDeleteOpen:    &st.taskDeleteOpen,
		TaskDeleteID:      &st.taskDeleteID,
		TaskDeleteSubject: &st.taskDeleteSubject,

		OnTaskCreate: func(subject string) {
			// The draft was non-empty when Add was pressed: the component reads
			// the trimmed subject, so no second empty check is needed here.
			if _, err := board.Create(context.Background(), subject, ""); err != nil {
				st.tasksErr = st.tr("创建任务失败：", "Failed to create task: ") + err.Error()
			} else {
				st.tasksErr = ""
				st.taskDraft = ""
				st.tasksLoaded = false
			}
		},
		OnTaskStatus: func(taskID, status string) {
			updateTaskStatus(board, runtime, st, taskID, status)
		},
		OnTaskDelete: func(taskID string) {
			deleteBoardTask(board, runtime, st, taskID)
		},
		OnRetryTasks: func() {
			st.tasksLoaded = false
			st.tasksErr = ""
			runtime.Invalidate()
		},
		OnAssign: func(taskID, owner string) {
			assignTaskOwner(board, runtime, st, taskID, owner)
		},
		OnOpenTask: func(taskID string) {
			// Enter on a task row, the row menu's "show details" and the detail's
			// Collapse all toggle the one expanded task.
			if st.tasksExpanded == taskID {
				st.tasksExpanded = ""
			} else {
				st.tasksExpanded = taskID
			}
			runtime.Invalidate()
		},
		Invalidate: runtime.Invalidate,
	})
}

// loadBoardTasks refreshes the cached task list once (first frame, after a
// mutation and after the chat notified a change); a failed load keeps the old
// rows and records the danger row.
func loadBoardTasks(board contracts.TaskBoard, st *state) {
	st.tasksLoaded = true
	tasks, err := board.List(context.Background())
	if err != nil {
		st.tasksErr = st.tr("加载任务失败：", "Failed to load tasks: ") + err.Error()
		st.tasks = nil
		return
	}
	st.tasksErr = ""
	st.tasks = tasks
}

// refreshTasks reloads the board off the UI thread (the explicit Refresh
// action): the cached rows stay visible with a labelled spinner until the new
// snapshot arrives.
func refreshTasks(board contracts.TaskBoard, runtime contracts.UIRuntime, st *state) {
	if board == nil || st.tasksLoading {
		return
	}
	st.tasksLoading = true
	go func() {
		tasks, err := board.List(context.Background())
		runtime.Post(func() {
			st.tasksLoading = false
			st.tasksLoaded = true
			if err != nil {
				st.tasksErr = st.tr("加载任务失败：", "Failed to load tasks: ") + err.Error()
			} else {
				st.tasksErr = ""
				st.tasks = tasks
			}
			runtime.Invalidate()
		})
	}()
	runtime.Invalidate()
}

// assignTaskOwner drives one owner change through the optional assigner.
func assignTaskOwner(board contracts.TaskBoard, runtime contracts.UIRuntime, st *state, id, owner string) {
	assigner, ok := board.(contracts.TaskAssigner)
	if !ok {
		return
	}
	if _, err := assigner.Assign(context.Background(), id, owner); err != nil {
		st.tasksErr = st.tr("更新负责人失败：", "Failed to update the owner: ") + err.Error()
	} else {
		st.tasksErr = ""
		st.tasksLoaded = false
	}
	if runtime != nil {
		runtime.Invalidate()
	}
}

// updateTaskStatus drives one task transition and reloads the list.
func updateTaskStatus(board contracts.TaskBoard, runtime contracts.UIRuntime, st *state, id, status string) {
	if _, err := board.SetStatus(context.Background(), id, status); err != nil {
		st.tasksErr = st.tr("更新任务失败：", "Failed to update task: ") + err.Error()
	} else {
		st.tasksErr = ""
		st.tasksLoaded = false
	}
	if runtime != nil {
		runtime.Invalidate()
	}
}

// deleteBoardTask removes one task and reloads the list.
func deleteBoardTask(board contracts.TaskBoard, runtime contracts.UIRuntime, st *state, id string) {
	if err := board.Delete(context.Background(), id); err != nil {
		st.tasksErr = st.tr("删除任务失败：", "Failed to delete task: ") + err.Error()
	} else {
		st.tasksErr = ""
		st.tasksLoaded = false
	}
	if runtime != nil {
		runtime.Invalidate()
	}
}
