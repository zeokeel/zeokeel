package rightpanel_test

// Frame tests for the task-board depth implementation: the chat-notification
// cache invalidation (T-03), the status groups with counts, the owner control
// and its assigner menu, the dependency/blocker line, the Enter-expanded task
// detail, the plain Refresh loading state and the delete confirmation that has
// no default action.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// TestTasksChatNotifyRefreshesOpenPane pins the T-03 fix: an agent writing the
// shared task board while the pane is open reaches the pane through the chat
// notification (the subscription now drops the task cache too), without the
// user pressing Refresh.
func TestTasksChatNotifyRefreshesOpenPane(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "已存在的任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
	})
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "已存在的任务")
	before := board.listCount()

	// The agent creates a task and the agent run notifies the chat subscribers
	// (the same path the backend's team change uses).
	board.add(contracts.BoardTask{ID: "t2", Subject: "运行中新任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC()})
	h.chat.notify("")
	tester.Frame()
	h.assertTexts(tester, "运行中新任务", "2 个任务")
	if got := board.listCount(); got <= before {
		t.Fatalf("List calls = %d after the chat notification, want > %d", got, before)
	}
}

// TestTasksStatusGroupsWithCounts pins the grouped order and the per-section
// counts: in progress first (action), then pending, then completed.
func TestTasksStatusGroupsWithCounts(t *testing.T) {
	base := time.Now().UTC()
	board := newFakeTaskBoard(
		contracts.BoardTask{ID: "c", Subject: "完成的任务", Status: contracts.TaskStatusCompleted, Created: base},
		contracts.BoardTask{ID: "p", Subject: "待处理的任务", Status: contracts.TaskStatusPending, Created: base},
		contracts.BoardTask{ID: "i", Subject: "进行中的任务", Status: contracts.TaskStatusInProgress, Created: base},
	)
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)

	h.assertTexts(tester, "进行中 · 1", "待处理 · 1", "已完成 · 1",
		"3 个任务 · 进行中 1 · 待处理 1 · 已完成 1")
	texts := tester.Texts()
	inProgress := textIndex(texts, "进行中的任务")
	pending := textIndex(texts, "待处理的任务")
	completed := textIndex(texts, "完成的任务")
	if inProgress < 0 || pending < 0 || completed < 0 || !(inProgress < pending && pending < completed) {
		t.Fatalf("status groups are not in progress → pending → completed order: %v", texts)
	}
}

// TestTasksOwnerDisplayAndAssign pins the owner semantics: the empty owner is
// 未认领, the board API's pseudo owner is the lead, and the owner control is the
// optional TaskAssigner menu.
func TestTasksOwnerDisplayAndAssign(t *testing.T) {
	base := time.Now().UTC()
	board := newFakeTaskBoard(
		contracts.BoardTask{ID: "u", Subject: "未认领的任务", Status: contracts.TaskStatusPending, Created: base},
		contracts.BoardTask{ID: "l", Subject: "主代理的任务", Status: contracts.TaskStatusInProgress,
			Owner: contracts.TaskOwnerUnassigned, Created: base},
	)
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "负责人：未认领", "负责人：主代理")

	// Assign the unclaimed task to the lead through the owner menu.
	h.click(tester, "负责人：未认领")
	h.assertTexts(tester, "主代理")
	h.click(tester, "主代理")
	calls := board.assignments()
	if len(calls) != 1 || calls[0].id != "u" || calls[0].owner != contracts.TaskOwnerUnassigned {
		t.Fatalf("Assign calls = %+v, want u -> %q", calls, contracts.TaskOwnerUnassigned)
	}
	h.assertTexts(tester, "负责人：主代理")
}

// TestTasksDependencyLine pins the dependency/blocker line the old contract
// dropped: a waiting dependency is a warning, a ready task says 可开始, and a
// completed dependency unlocks the dependent task.
func TestTasksDependencyLine(t *testing.T) {
	base := time.Now().UTC()
	board := newFakeTaskBoard(
		contracts.BoardTask{ID: "dep", Subject: "前置任务", Status: contracts.TaskStatusInProgress, Created: base},
		contracts.BoardTask{ID: "blocked", Subject: "被阻塞的任务", Status: contracts.TaskStatusPending,
			BlockedBy: []string{"dep"}, Created: base},
		contracts.BoardTask{ID: "ready", Subject: "可开始的任务", Status: contracts.TaskStatusPending, Created: base},
	)
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "等待 dep「前置任务」完成", "可开始")

	// The dependency completes: the line turns into the ready copy.
	board.mu.Lock()
	for index := range board.tasks {
		if board.tasks[index].ID == "dep" {
			board.tasks[index].Status = contracts.TaskStatusCompleted
		}
	}
	board.mu.Unlock()
	h.chat.notify("")
	tester.Frame()
	h.assertTexts(tester, "依赖已完成 · 可开始")
	h.assertNoTexts(tester, "等待 dep")
}

// TestTasksEnterExpandsDetail pins the keyboard path: the native list chooses a
// row and Enter expands its detail (description, dependencies, write scopes and
// the inline transitions).
func TestTasksEnterExpandsDetail(t *testing.T) {
	base := time.Now().UTC()
	board := newFakeTaskBoard(
		contracts.BoardTask{ID: "dep", Subject: "前置任务", Status: contracts.TaskStatusCompleted, Created: base},
		contracts.BoardTask{ID: "t1", Subject: "展开我的任务", Status: contracts.TaskStatusPending,
			Description: "任务细节描述", BlockedBy: []string{"dep"}, WriteScopes: []string{"docs/a.md"},
			Created: base.Add(time.Minute)},
	)
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	if err := tester.Click("展开我的任务"); err != nil {
		t.Fatalf("select task row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "任务细节描述", "dep「前置任务」· 已完成", "写入范围：docs/a.md",
		"开始任务", "删除")
}

// TestTasksRefreshLoadingState pins the explicit Refresh loading state: the
// cached rows stay visible with a labelled spinner until the off-thread reload
// posts its result.
func TestTasksRefreshLoadingState(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "旧任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
	})
	block := make(chan struct{})
	blockTasks := func(blocked bool) {
		board.mu.Lock()
		if blocked {
			board.listBlock = block
		} else {
			board.listBlock = nil
		}
		board.mu.Unlock()
	}
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "旧任务")

	blockTasks(true)
	h.click(tester, "刷新")
	h.assertTexts(tester, "刷新中…", "旧任务")
	board.mu.Lock()
	board.tasks = append(board.tasks, contracts.BoardTask{
		ID: "t2", Subject: "外部新任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
	})
	board.mu.Unlock()
	close(block)
	blockTasks(false)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !tester.HasText("外部新任务") {
		tester.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	h.assertTexts(tester, "外部新任务")
	if tester.HasText("刷新中…") {
		t.Fatalf("loading state did not clear; texts = %v", tester.Texts())
	}
}

// TestTasksDeleteConfirmationHasNoDefault pins the destructive confirmation: the
// safe action is focused first, so Enter cancels; only the explicit Delete
// button removes the task.
func TestTasksDeleteConfirmationHasNoDefault(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "临时任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
	})
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	openTaskMenu(t, tester, "临时任务")
	h.click(tester, "删除")
	h.assertTexts(tester, "删除任务", "取消", "删除")

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := board.deleted(); len(got) != 0 {
		t.Fatalf("Enter deleted a task: %v", got)
	}
	// The safe action took the Enter: reopen the confirmation for the delete.
	if !tester.HasText("删除任务") {
		openTaskMenu(t, tester, "临时任务")
		h.click(tester, "删除")
	}
	h.click(tester, "删除")
	if got := board.deleted(); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("Delete calls = %v, want [t1]", got)
	}
	h.assertTexts(tester, "还没有任务")
}

// TestPaneSelectorGroupsOrchestrationFirst pins the pane menu order: the team
// and its task board are the first group, before the working and record panes.
func TestPaneSelectorGroupsOrchestrationFirst(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "概览")
	if err := tester.Click("切换面板"); err != nil {
		t.Fatalf("open pane selector: %v", err)
	}
	tester.Frame()
	texts := tester.Texts()
	teamIndex := exactTextIndex(texts, "团队")
	tasksIndex := exactTextIndex(texts, "任务")
	toolsIndex := exactTextIndex(texts, "工具")
	logIndex := exactTextIndex(texts, "日志")
	if teamIndex < 0 || tasksIndex < 0 || toolsIndex < 0 || logIndex < 0 {
		t.Fatalf("pane selector is missing panes: %v", texts)
	}
	if !(teamIndex < toolsIndex && tasksIndex < toolsIndex && toolsIndex < logIndex) {
		t.Fatalf("pane groups are not orchestration → working → record: team %d, tasks %d, tools %d, log %d",
			teamIndex, tasksIndex, toolsIndex, logIndex)
	}
}
