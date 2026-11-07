package rightpanel_test

// Tests for the Tasks inspector tab (the shared task board): the optional
// TaskBoard service, status-ordered rows with their actions, the add row, the
// refresh action, errors, English copy and the config label override. Written
// before the implementation (TDD): they drive the real host frame.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeTaskBoard is a scripted contracts.TaskBoard: it records mutations and
// returns the configured errors.
type fakeTaskBoard struct {
	mu sync.Mutex

	tasks []contracts.BoardTask
	next  int

	listCalls   int
	creates     []taskCreate
	statusCalls []taskStatusCall
	deletes     []string

	listErr   error
	createErr error
	statusErr error
	deleteErr error
}

type taskCreate struct {
	subject     string
	description string
}

type taskStatusCall struct {
	id     string
	status string
}

func newFakeTaskBoard(tasks ...contracts.BoardTask) *fakeTaskBoard {
	return &fakeTaskBoard{tasks: append([]contracts.BoardTask(nil), tasks...)}
}

func (f *fakeTaskBoard) List(context.Context) ([]contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]contracts.BoardTask(nil), f.tasks...), nil
}

func (f *fakeTaskBoard) Create(_ context.Context, subject, description string) (contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, taskCreate{subject: subject, description: description})
	if f.createErr != nil {
		return contracts.BoardTask{}, f.createErr
	}
	f.next++
	task := contracts.BoardTask{
		ID: fmt.Sprintf("task-%d", f.next), Subject: subject, Description: description,
		Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
	}
	f.tasks = append(f.tasks, task)
	return task, nil
}

func (f *fakeTaskBoard) SetStatus(_ context.Context, id, status string) (contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls = append(f.statusCalls, taskStatusCall{id: id, status: status})
	if f.statusErr != nil {
		return contracts.BoardTask{}, f.statusErr
	}
	for index := range f.tasks {
		if f.tasks[index].ID == id {
			f.tasks[index].Status = status
			return f.tasks[index], nil
		}
	}
	return contracts.BoardTask{}, fmt.Errorf("unknown task %q", id)
}

func (f *fakeTaskBoard) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, id)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	out := f.tasks[:0]
	for _, task := range f.tasks {
		if task.ID != id {
			out = append(out, task)
		}
	}
	f.tasks = out
	return nil
}

func (f *fakeTaskBoard) created() []taskCreate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]taskCreate(nil), f.creates...)
}

func (f *fakeTaskBoard) statuses() []taskStatusCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]taskStatusCall(nil), f.statusCalls...)
}

func (f *fakeTaskBoard) deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

func (f *fakeTaskBoard) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
}

func (f *fakeTaskBoard) add(task contracts.BoardTask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, task)
}

// newTasksHarness builds the product frame with an explicit locale and the
// optional task board backend.
func newTasksHarness(t *testing.T, language string, chat *fakeChat, cfg rightpanel.Config,
	board contracts.TaskBoard) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if board != nil {
		provide[contracts.TaskBoard](t, ctx, contracts.KeyTaskBoard, board)
	}
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) {
			uiRoot.Runtime().RenderSlot(c, contracts.SlotSessionPanelRight)
		},
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, chat: chat}
	h.fiber = activate(t, ctx, rightpanel.Module(), cfg)
	return h
}

// openTasks opens the Tasks tab (default Chinese label).
func openTasks(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	tester.Frame()
	h.click(tester, "任务")
}

// typeTask fills the add-task input.
func typeTask(t *testing.T, tester *ui.Tester, subject string) {
	t.Helper()
	rect, ok := tester.Find("新任务")
	if !ok {
		t.Fatalf("new-task input not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
	tester.Type(subject)
	tester.Frame()
}

// exactTextCount counts exactly equal text nodes (Tester.HasText is substring).
func exactTextCount(texts []string, want string) int {
	count := 0
	for _, text := range texts {
		if text == want {
			count++
		}
	}
	return count
}

// textIndex returns the index of the first exact text node equal to want.
func textIndex(texts []string, want string) int {
	for index, text := range texts {
		if text == want {
			return index
		}
	}
	return -1
}

// --- tests ------------------------------------------------------------------

func TestTasksTabEmptyStateWithoutService(t *testing.T) {
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, nil)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "任务")

	h.click(tester, "任务")
	h.assertTexts(tester, "任务板未启用")
	h.assertNoTexts(tester, "还没有任务")
}

func TestTasksListSortsAndRendersFields(t *testing.T) {
	base := time.Date(2026, 11, 8, 3, 0, 0, 0, time.UTC)
	board := newFakeTaskBoard(
		contracts.BoardTask{ID: "c", Subject: "已完成的", Status: contracts.TaskStatusCompleted, Created: base.Add(3 * time.Hour)},
		contracts.BoardTask{ID: "p2", Subject: "第二个待办", Status: contracts.TaskStatusPending, Owner: "alice", Created: base.Add(2 * time.Hour)},
		contracts.BoardTask{ID: "i", Subject: "正在做的", Status: contracts.TaskStatusInProgress, Created: base.Add(30 * time.Minute)},
		contracts.BoardTask{ID: "p1", Subject: "第一个待办", Status: contracts.TaskStatusPending, Created: base.Add(time.Hour)},
	)
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)

	h.assertTexts(tester, "第一个待办", "第二个待办", "正在做的", "已完成的",
		"待处理", "进行中", "已完成", "alice", "负责人：alice",
		"开始", "完成", "删除", "新任务", "添加", "刷新")

	texts := tester.Texts()
	order := []string{"第一个待办", "第二个待办", "正在做的", "已完成的"}
	previous := -1
	for _, subject := range order {
		index := textIndex(texts, subject)
		if index < 0 {
			t.Fatalf("subject %q not found; texts = %v", subject, texts)
		}
		if index <= previous {
			t.Fatalf("tasks are not status/created ordered: %v (positions %v)", order, texts)
		}
		previous = index
	}
}

func TestTasksAddCreatesAndRefreshes(t *testing.T) {
	board := newFakeTaskBoard()
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "还没有任务")

	// The add action is disabled while the draft is empty.
	h.click(tester, "添加")
	if got := board.created(); len(got) != 0 {
		t.Fatalf("Create calls with an empty draft = %+v, want none", got)
	}

	typeTask(t, tester, "整理发布说明")
	h.click(tester, "添加")

	creates := board.created()
	if len(creates) != 1 || creates[0].subject != "整理发布说明" || creates[0].description != "" {
		t.Fatalf("Create calls = %+v, want subject 整理发布说明", creates)
	}
	// The list reloads after the mutation.
	h.assertTexts(tester, "整理发布说明", "待处理")
	if got := board.listCount(); got < 2 {
		t.Fatalf("List calls = %d, want a reload after Create", got)
	}
}

func TestTasksStartAndComplete(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "写迁移脚本", Status: contracts.TaskStatusPending,
		Created: time.Now().UTC().Add(-time.Hour),
	})
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "写迁移脚本", "待处理")

	h.click(tester, "开始")
	calls := board.statuses()
	if len(calls) != 1 || calls[0].id != "t1" || calls[0].status != contracts.TaskStatusInProgress {
		t.Fatalf("SetStatus calls = %+v, want t1 -> in_progress", calls)
	}
	h.assertTexts(tester, "进行中")

	h.click(tester, "完成")
	calls = board.statuses()
	if len(calls) != 2 || calls[1].id != "t1" || calls[1].status != contracts.TaskStatusCompleted {
		t.Fatalf("SetStatus calls = %+v, want t1 -> completed", calls)
	}
	h.assertTexts(tester, "已完成")
	h.assertNoTexts(tester, "开始")
}

func TestTasksDelete(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "临时任务", Status: contracts.TaskStatusPending,
		Created: time.Now().UTC(),
	})
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "临时任务")

	h.click(tester, "删除")
	if got := board.deleted(); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("Delete calls = %v, want [t1]", got)
	}
	h.assertTexts(tester, "还没有任务")
	h.assertNoTexts(tester, "临时任务")
}

func TestTasksRefreshReloads(t *testing.T) {
	board := newFakeTaskBoard()
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "还没有任务")
	before := board.listCount()

	board.add(contracts.BoardTask{
		ID: "t1", Subject: "外部新增的任务", Status: contracts.TaskStatusPending,
		Created: time.Now().UTC(),
	})
	h.click(tester, "刷新")
	if got := board.listCount(); got <= before {
		t.Fatalf("List calls = %d after Refresh, want > %d", got, before)
	}
	h.assertTexts(tester, "外部新增的任务")
}

func TestTasksErrors(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		board := newFakeTaskBoard()
		board.listErr = errors.New("db unavailable")
		h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
		tester := h.tester()
		openTasks(t, h, tester)
		h.assertTexts(tester, "加载任务失败：db unavailable")
	})

	t.Run("create", func(t *testing.T) {
		board := newFakeTaskBoard()
		board.createErr = errors.New("disk full")
		h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
		tester := h.tester()
		openTasks(t, h, tester)
		typeTask(t, tester, "会失败")
		h.click(tester, "添加")
		h.assertTexts(tester, "创建任务失败：disk full")
	})

	t.Run("update", func(t *testing.T) {
		board := newFakeTaskBoard(contracts.BoardTask{
			ID: "t1", Subject: "任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
		})
		board.statusErr = errors.New("denied")
		h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
		tester := h.tester()
		openTasks(t, h, tester)
		h.click(tester, "开始")
		h.assertTexts(tester, "更新任务失败：denied")
	})

	t.Run("delete", func(t *testing.T) {
		board := newFakeTaskBoard(contracts.BoardTask{
			ID: "t1", Subject: "任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
		})
		board.deleteErr = errors.New("locked")
		h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
		tester := h.tester()
		openTasks(t, h, tester)
		h.click(tester, "删除")
		h.assertTexts(tester, "删除任务失败：locked")
	})
}

func TestTasksEnglishCopy(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "Ship the release", Status: contracts.TaskStatusPending,
		Owner: "alice", Created: time.Now().UTC(),
	})
	h := newTasksHarness(t, "en", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Tasks")

	h.assertTexts(tester, "Tasks", "Ship the release", "Pending", "Owner: alice",
		"Start", "Complete", "Delete", "New task", "Add", "Refresh")
}

func TestTasksEmptyAndMissingServiceEnglish(t *testing.T) {
	// No board: the English empty state explains the missing backend.
	h := newTasksHarness(t, "en", newFakeChat(), rightpanel.Config{}, nil)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Tasks")
	h.assertTexts(tester, "Task board is not enabled")

	// Empty board: the English empty list copy.
	empty := newTasksHarness(t, "en", newFakeChat(), rightpanel.Config{}, newFakeTaskBoard())
	tester = empty.tester()
	tester.Frame()
	empty.click(tester, "Tasks")
	empty.assertTexts(tester, "No tasks yet")
}

func TestTasksConfigTabLabelOverride(t *testing.T) {
	board := newFakeTaskBoard()
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{TasksTab: "Work"}, board)
	tester := h.tester()
	tester.Frame()
	// The native tab strip contributes the label twice: once as the
	// accessibility label and once as the rendered text node.
	if got := exactTextCount(tester.Texts(), "Work"); got != 2 {
		t.Fatalf("Work tab labels = %d, want 2; texts = %v", got, tester.Texts())
	}
	if got := exactTextCount(tester.Texts(), "任务"); got != 0 {
		t.Fatalf("default 任务 tab label = %d, want 0 with the override; texts = %v", got, tester.Texts())
	}
}

func TestTasksModuleDoesNotRequireTaskBoard(t *testing.T) {
	for _, requirement := range rightpanel.Module().Requires {
		if requirement.Name == contracts.KeyTaskBoard.Name() {
			t.Fatalf("rightpanel requires %q; the task board must stay an optional ctx.Get", requirement.Name)
		}
	}
}
