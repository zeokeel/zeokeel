package rightpanel

// The Tasks pane of the session inspector: the shared team task board (the
// agent's todo list) rendered with native controls. The board is an optional
// host service (contracts.KeyTaskBoard) looked up lazily with ctx.Get; without
// it the pane shows an explicit empty state instead of failing activation.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildTasksPane renders the task board: the add row, the status-ordered task
// rows with their actions and the last error. The board service is optional;
// without it the pane explains that the task board is not enabled.
func buildTasksPane(c *ui.Context, t *design.Tokens, board contracts.TaskBoard,
	runtime contracts.UIRuntime, st *state) {
	if board == nil {
		buildEmptyPane(c, t, icons.Clock(), uikit.T(st.locale, "任务板未启用", "Task board is not enabled"))
		return
	}
	if !st.tasksLoaded {
		loadBoardTasks(board, st)
	}

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Badge(c, fmt.Sprintf("%d", len(st.tasks))), t.Badge())
			ui.Spacer(c).Grow(1)
			refresh := ui.Button(c, uikit.T(st.locale, "刷新", "Refresh")).
				Tooltip(uikit.T(st.locale, "重新加载任务", "Reload tasks"))
			if refresh.Clicked() {
				st.tasksLoaded = false
				st.tasksErr = ""
				runtime.Invalidate()
			}
		})
		buildTaskAddRow(c, t, board, runtime, st)
		if st.tasksErr != "" {
			buildErrorLine(c, t, st.tasksErr)
		}
		tasks := sortedBoardTasks(st.tasks)
		if len(tasks) == 0 {
			buildEmptyPane(c, t, icons.Clock(), uikit.T(st.locale, "还没有任务", "No tasks yet"))
			return
		}
		for _, task := range tasks {
			buildTaskRow(c, t, board, task, runtime, st)
		}
	})
}

// loadBoardTasks refreshes the cached task list once (first frame and after a
// mutation); a failed load keeps the old rows and records the danger row.
func loadBoardTasks(board contracts.TaskBoard, st *state) {
	st.tasksLoaded = true
	tasks, err := board.List(context.Background())
	if err != nil {
		st.tasksErr = uikit.T(st.locale, "加载任务失败：", "Failed to load tasks: ") + err.Error()
		st.tasks = nil
		return
	}
	st.tasksErr = ""
	st.tasks = tasks
}

// buildTaskAddRow is the native add row: one subject input and a primary Add
// button that is disabled while the draft is empty.
func buildTaskAddRow(c *ui.Context, t *design.Tokens, board contracts.TaskBoard,
	runtime contracts.UIRuntime, st *state) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.TextInput(c, &st.taskDraft).
			Label(uikit.T(st.locale, "新任务", "New task")).
			Placeholder(uikit.T(st.locale, "输入任务标题", "Task subject")).
			FillWidth()
		subject := strings.TrimSpace(st.taskDraft)
		add := design.Apply(ui.PrimaryButton(c, uikit.T(st.locale, "添加", "Add")), t.PrimaryButton()).
			Disabled(subject == "")
		if add.Clicked() && subject != "" {
			if _, err := board.Create(context.Background(), subject, ""); err != nil {
				st.tasksErr = uikit.T(st.locale, "创建任务失败：", "Failed to create task: ") + err.Error()
			} else {
				st.tasksErr = ""
				st.taskDraft = ""
				st.tasksLoaded = false
			}
			runtime.Invalidate()
		}
	})
}

// buildTaskRow is one task card: subject, status badge, owner and the native
// Start/Complete/Delete actions (only the transitions that make sense are shown).
func buildTaskRow(c *ui.Context, t *design.Tokens, board contracts.TaskBoard,
	task contracts.BoardTask, runtime contracts.UIRuntime, st *state) {
	subject := firstNonEmpty(task.Subject, task.ID)
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, subject), t.Font(design.TypeS)).FontWeight(600).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Badge(c, taskStatusLabel(task.Status, st.locale)), t.Badge()).
				TextColor(taskStatusColor(t, task.Status))
		})
		// The backend stamps a synthetic "taskboard" owner on in-progress tasks
		// that have no real assignee; do not surface that implementation detail.
		if owner := strings.TrimSpace(task.Owner); owner != "" && owner != "taskboard" {
			design.Apply(ui.Text(c, uikit.T(st.locale, "负责人：", "Owner: ")+owner), t.TextMuted(design.TypeXXS)).
				MaxLines(1).Ellipsis("…")
		}
		ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
			status := normalizeTaskStatus(task.Status)
			if status == contracts.TaskStatusPending {
				if ui.Button(c, uikit.T(st.locale, "开始", "Start")).Clicked() {
					updateTaskStatus(board, runtime, st, task.ID, contracts.TaskStatusInProgress)
				}
			}
			if status != contracts.TaskStatusCompleted {
				if ui.Button(c, uikit.T(st.locale, "完成", "Complete")).Clicked() {
					updateTaskStatus(board, runtime, st, task.ID, contracts.TaskStatusCompleted)
				}
			}
			deleteButton := ui.Button(c, uikit.T(st.locale, "删除", "Delete")).
				Tooltip(uikit.T(st.locale, "删除任务", "Delete task"))
			if deleteButton.Clicked() {
				deleteBoardTask(board, runtime, st, task.ID)
			}
		})
	})
}

// updateTaskStatus drives one task transition and reloads the list.
func updateTaskStatus(board contracts.TaskBoard, runtime contracts.UIRuntime, st *state, id, status string) {
	if _, err := board.SetStatus(context.Background(), id, status); err != nil {
		st.tasksErr = uikit.T(st.locale, "更新任务失败：", "Failed to update task: ") + err.Error()
	} else {
		st.tasksErr = ""
		st.tasksLoaded = false
	}
	runtime.Invalidate()
}

// deleteBoardTask removes one task and reloads the list.
func deleteBoardTask(board contracts.TaskBoard, runtime contracts.UIRuntime, st *state, id string) {
	if err := board.Delete(context.Background(), id); err != nil {
		st.tasksErr = uikit.T(st.locale, "删除任务失败：", "Failed to delete task: ") + err.Error()
	} else {
		st.tasksErr = ""
		st.tasksLoaded = false
	}
	runtime.Invalidate()
}

// sortedBoardTasks returns a copy ordered pending -> in_progress -> completed,
// then by Created ascending (ID as the stable tiebreak).
func sortedBoardTasks(tasks []contracts.BoardTask) []contracts.BoardTask {
	out := append([]contracts.BoardTask(nil), tasks...)
	sort.SliceStable(out, func(left, right int) bool {
		leftRank, rightRank := taskStatusRank(out[left].Status), taskStatusRank(out[right].Status)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if !out[left].Created.Equal(out[right].Created) {
			return out[left].Created.Before(out[right].Created)
		}
		return out[left].ID < out[right].ID
	})
	return out
}

// taskStatusRank orders the contract statuses; unknown values sort last.
func taskStatusRank(status string) int {
	switch normalizeTaskStatus(status) {
	case contracts.TaskStatusPending:
		return 0
	case contracts.TaskStatusInProgress:
		return 1
	case contracts.TaskStatusCompleted:
		return 2
	default:
		return 3
	}
}

// normalizeTaskStatus folds common status spellings onto the contract values.
func normalizeTaskStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "todo", "open", "waiting":
		return contracts.TaskStatusPending
	case "in_progress", "in-progress", "running", "active", "doing", "started":
		return contracts.TaskStatusInProgress
	case "completed", "complete", "done", "finished":
		return contracts.TaskStatusCompleted
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

// taskStatusLabel maps a task status to its localized badge label.
func taskStatusLabel(status string, locale contracts.LocaleService) string {
	switch normalizeTaskStatus(status) {
	case contracts.TaskStatusPending:
		return uikit.T(locale, "待处理", "Pending")
	case contracts.TaskStatusInProgress:
		return uikit.T(locale, "进行中", "In progress")
	case contracts.TaskStatusCompleted:
		return uikit.T(locale, "已完成", "Completed")
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return uikit.T(locale, "未知", "Unknown")
	}
}

// taskStatusColor maps a task status to its brand color (tertiary/warning/success).
func taskStatusColor(t *design.Tokens, status string) ui.Color {
	switch normalizeTaskStatus(status) {
	case contracts.TaskStatusPending:
		return t.LabelTertiary
	case contracts.TaskStatusInProgress:
		return t.WarningText
	case contracts.TaskStatusCompleted:
		return t.Success
	default:
		return t.LabelSecondary
	}
}
