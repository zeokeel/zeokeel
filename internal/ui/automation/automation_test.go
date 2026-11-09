package automation_test

// Headless tests for the Automation screen: the optional automation backend,
// the task list with its toggles/timestamps/errors, the inline create form,
// enable/disable Update, Run now, delete confirmation, subscription frames and
// the error states.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// --- fake AutomationService -------------------------------------------------

type fakeAutomation struct {
	mu sync.Mutex

	tasks []contracts.AutomationTask
	subs  []func()

	listErr   error
	createErr error
	updateErr error
	deleteErr error
	runErr    error

	createCalls []contracts.AutomationTask
	updateCalls []contracts.AutomationTask
	deleted     []string
	ran         []string
	unsub       bool
	nextID      int
}

func newFakeAutomation(tasks ...contracts.AutomationTask) *fakeAutomation {
	return &fakeAutomation{tasks: tasks}
}

func (f *fakeAutomation) List(context.Context) ([]contracts.AutomationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]contracts.AutomationTask(nil), f.tasks...), nil
}

func (f *fakeAutomation) Create(_ context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, task)
	if f.createErr != nil {
		return contracts.AutomationTask{}, f.createErr
	}
	f.nextID++
	task.ID = fmt.Sprintf("task-%d", f.nextID)
	f.tasks = append(f.tasks, task)
	return task, nil
}

func (f *fakeAutomation) Update(_ context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls = append(f.updateCalls, task)
	if f.updateErr != nil {
		return contracts.AutomationTask{}, f.updateErr
	}
	for index, current := range f.tasks {
		if current.ID == task.ID {
			f.tasks[index] = task
		}
	}
	return task, nil
}

func (f *fakeAutomation) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	kept := f.tasks[:0]
	for _, task := range f.tasks {
		if task.ID != id {
			kept = append(kept, task)
		}
	}
	f.tasks = kept
	return nil
}

func (f *fakeAutomation) RunNow(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, id)
	return f.runErr
}

func (f *fakeAutomation) Subscribe(fn func()) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fn)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unsub = true
	}, nil
}

func (f *fakeAutomation) notify() {
	f.mu.Lock()
	subs := append([]func(){}, f.subs...)
	f.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

func (f *fakeAutomation) snapshot() []contracts.AutomationTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.AutomationTask(nil), f.tasks...)
}

func (f *fakeAutomation) addTask(task contracts.AutomationTask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, task)
}

func (f *fakeAutomation) created() []contracts.AutomationTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.AutomationTask(nil), f.createCalls...)
}

func (f *fakeAutomation) updated() []contracts.AutomationTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.AutomationTask(nil), f.updateCalls...)
}

func (f *fakeAutomation) deletes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func (f *fakeAutomation) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

func (f *fakeAutomation) isUnsubscribed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unsub
}

// --- harness ----------------------------------------------------------------

type harness struct {
	t      *testing.T
	ctx    cordis.Context
	uiRoot *uiroot.Root
	fiber  *cordis.Fiber
}

func newHarness(t *testing.T, service contracts.AutomationService, cfg automation.Config) *harness {
	t.Helper()
	return newHarnessWithNotifier(t, service, nil, cfg)
}

// newHarnessWithNotifier is newHarness with the host toast bridge provided, for
// the delete/undo toast tests.
func newHarnessWithNotifier(t *testing.T, service contracts.AutomationService, notifier contracts.Notifier, cfg automation.Config) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	if service != nil {
		provide[contracts.AutomationService](t, ctx, contracts.KeyAutomation, service)
	}
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}
	fiber := activate(t, ctx, automation.Module(), cfg)
	return &harness{t: t, ctx: ctx, uiRoot: uiRoot, fiber: fiber}
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.uiRoot.View, 1200, 800)
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

func (h *harness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	for _, want := range wanted {
		if !tester.HasText(want) {
			h.t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

func (h *harness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if tester.HasText(text) {
			h.t.Fatalf("frame unexpectedly shows %q; texts = %v", text, tester.Texts())
		}
	}
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

type descriptorPlugin[T any] struct {
	descriptor *module.Descriptor[T]
	config     T
	inject     []string
}

func (p *descriptorPlugin[T]) Name() string { return p.descriptor.ID }

func (p *descriptorPlugin[T]) Inject() []string { return p.inject }

func (p *descriptorPlugin[T]) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

func activate[T any](t *testing.T, ctx cordis.Context, descriptor *module.Descriptor[T], config T) *cordis.Fiber {
	t.Helper()
	plugin := &descriptorPlugin[T]{descriptor: descriptor, config: config}
	inject := make([]string, 0, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		if !requirement.Optional {
			inject = append(inject, requirement.Name)
		}
	}
	plugin.inject = inject
	fiber, err := ctx.Plugin(plugin, nil)
	if err != nil {
		t.Fatalf("activate %s: %v", descriptor.ID, err)
	}
	if state := fiber.State(); state != cordis.Active {
		t.Fatalf("module %s state = %s (%v), want Active", descriptor.ID, state, fiber.ErrorDetail())
	}
	return fiber
}

// --- tests ------------------------------------------------------------------

func TestNoBackendEmptyState(t *testing.T) {
	h := newHarness(t, nil, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "自动化", "自动化后端未启用")
}

func TestTaskListRenders(t *testing.T) {
	now := time.Unix(1700000000, 0)
	service := newFakeAutomation(
		contracts.AutomationTask{
			ID: "task-1", Name: "每日依赖巡检", Prompt: "检查依赖", Schedule: "every 60s",
			Enabled: true, NextRun: now.Add(time.Hour), LastRun: now.Add(-time.Hour),
		},
		contracts.AutomationTask{
			ID: "task-2", Name: "停用任务", Prompt: "x", Schedule: "*/5 * * * *",
			Enabled: false, LastError: "exit status 1",
		},
	)
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "自动化", "新建任务",
		"每日依赖巡检", "每 1 分钟", "下次运行：", "上次运行：",
		"立即运行", "编辑", "删除",
		"停用任务", "每 5 分钟", "上次错误：exit status 1")
}

func TestCreateTaskFlow(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "还没有自动化任务")

	h.click(tester, "新建任务")
	h.assertTexts(tester, "新建自动化任务", "任务名称", "任务提示词", "运行计划", "保存")

	h.click(tester, "任务名称")
	tester.Type("每日巡检")
	h.click(tester, "任务提示词")
	tester.Type("检查依赖是否有更新")
	h.click(tester, "运行计划")
	tester.Type("every 60s")
	tester.Frame()
	h.click(tester, "保存")

	created := service.created()
	if len(created) != 1 {
		t.Fatalf("Create calls = %+v, want one", created)
	}
	if created[0].Name != "每日巡检" || created[0].Prompt != "检查依赖是否有更新" ||
		created[0].Schedule != "every 60s" || !created[0].Enabled {
		t.Fatalf("Create args = %+v", created[0])
	}
	h.assertNoTexts(tester, "新建自动化任务")
	h.assertTexts(tester, "每日巡检", "每 1 分钟")
	// The tasks are an official List: named, with rows named by the task name.
	if _, ok := tester.AccessByRole(ui.RoleList, "自动化任务"); !ok {
		t.Fatalf("automation tasks are not a named list; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "每日巡检"); !ok {
		t.Fatalf("automation rows are not exposed by name; texts = %v", tester.Texts())
	}
}

func TestCreateValidation(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "新建任务")
	// Empty form: the primary Save is disabled, so no Create may fire.
	h.click(tester, "保存")
	if got := service.created(); len(got) != 0 {
		t.Fatalf("Create calls with an empty form = %+v, want none", got)
	}
}

func TestToggleUpdatesTask(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "巡检", Prompt: "p", Schedule: "every 60s", Enabled: true,
	})
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	// The card shows the task name and a native switch in a field labeled
	// "启用"; the task name is not repeated in the switch label.
	h.assertTexts(tester, "巡检", "启用")

	h.click(tester, "启用任务")
	updated := service.updated()
	if len(updated) != 1 {
		t.Fatalf("Update calls = %+v, want one", updated)
	}
	if updated[0].ID != "task-1" || updated[0].Enabled {
		t.Fatalf("Update args = %+v, want task-1 disabled", updated[0])
	}
}

func TestRunNowAndDelete(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "巡检", Prompt: "p", Schedule: "every 60s", Enabled: true,
	})
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "巡检")

	h.click(tester, "立即运行")
	if got := service.runs(); len(got) != 1 || got[0] != "task-1" {
		t.Fatalf("RunNow calls = %v, want [task-1]", got)
	}

	h.click(tester, "删除")
	h.assertTexts(tester, "删除自动化任务")
	h.click(tester, "确认删除")
	if got := service.deletes(); len(got) != 1 || got[0] != "task-1" {
		t.Fatalf("Delete calls = %v, want [task-1]", got)
	}
	h.assertTexts(tester, "还没有自动化任务")
}

func TestSubscriptionRequestsFrame(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "还没有自动化任务")

	service.addTask(contracts.AutomationTask{
		ID: "task-1", Name: "新出现的任务", Prompt: "p", Schedule: "every 60s", Enabled: true,
	})
	service.notify()
	tester.Frame()
	h.assertTexts(tester, "新出现的任务")
}

func TestErrorsShown(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		service := newFakeAutomation()
		service.listErr = errors.New("db locked")
		h := newHarness(t, service, automation.Config{})
		tester := h.tester()
		h.assertTexts(tester, "读取任务失败：db locked")
	})

	t.Run("create", func(t *testing.T) {
		service := newFakeAutomation()
		service.createErr = errors.New("quota exceeded")
		h := newHarness(t, service, automation.Config{})
		tester := h.tester()
		h.click(tester, "新建任务")
		h.click(tester, "任务名称")
		tester.Type("任务")
		h.click(tester, "任务提示词")
		tester.Type("提示词")
		h.click(tester, "运行计划")
		tester.Type("every 60s")
		tester.Frame()
		h.click(tester, "保存")
		h.assertTexts(tester, "创建任务失败：quota exceeded")
		h.assertTexts(tester, "新建自动化任务") // the form stays open for a retry
	})

	t.Run("run", func(t *testing.T) {
		service := newFakeAutomation(contracts.AutomationTask{
			ID: "task-1", Name: "巡检", Prompt: "p", Schedule: "every 60s", Enabled: true,
		})
		service.runErr = errors.New("permission denied")
		h := newHarness(t, service, automation.Config{})
		tester := h.tester()
		h.click(tester, "立即运行")
		h.assertTexts(tester, "运行任务失败：permission denied")
	})
}

func TestCustomConfig(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{
		Title:     "Automation",
		NewLabel:  "New task",
		EmptyText: "No automations yet",
	})
	tester := h.tester()
	h.assertTexts(tester, "Automation", "New task", "No automations yet")
}

func TestDisposeUnsubscribes(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "自动化")
	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if !service.isUnsubscribed() {
		t.Fatal("automation subscription was not unwound on dispose")
	}
	tester.Frame()
	h.assertNoTexts(tester, "自动化后端未启用", "新建任务")
}

// automationNotifier records action toasts for the delete/undo test.
type automationNotifier struct {
	mu      sync.Mutex
	actions []automationActionCall
	plain   []contracts.Notification
}

type automationActionCall struct {
	level, text, actionLabel string
	action                   func()
}

func (n *automationNotifier) SetHandler(func(contracts.Notification)) {}
func (n *automationNotifier) Notify(level, text string) contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.plain = append(n.plain, contracts.Notification{ID: "plain", Level: level, Text: text})
	return n.plain[len(n.plain)-1]
}
func (n *automationNotifier) Dismiss(string) {}
func (n *automationNotifier) NotifyAction(level, text, actionLabel string, action func()) contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.actions = append(n.actions, automationActionCall{level: level, text: text, actionLabel: actionLabel, action: action})
	return contracts.Notification{ID: "action", Level: level, Text: text}
}
func (n *automationNotifier) actionCalls() []automationActionCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]automationActionCall(nil), n.actions...)
}

func TestDeleteTaskRaisesUndoToast(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "巡检", Prompt: "检查构建", Schedule: "every 60s", Enabled: true,
	})
	notifier := &automationNotifier{}
	h := newHarnessWithNotifier(t, service, notifier, automation.Config{})
	tester := h.tester()
	h.click(tester, "删除")
	h.click(tester, "确认删除")

	actions := notifier.actionCalls()
	if len(actions) != 1 {
		t.Fatalf("action toasts = %+v, want one delete notice", actions)
	}
	action := actions[0]
	if action.level != contracts.NotifySuccess || !strings.Contains(action.text, "巡检") {
		t.Fatalf("delete notice = %+v", action)
	}
	if action.actionLabel == "" || action.action == nil {
		t.Fatalf("delete notice has no undo action: %+v", action)
	}

	// Undo re-creates the same task (fresh id assigned by the backend).
	action.action()
	creates := service.createCalls
	if len(creates) != 1 {
		t.Fatalf("Create calls = %d, want the undo's one", len(creates))
	}
	if creates[0].Name != "巡检" || creates[0].Prompt != "检查构建" || creates[0].Schedule != "every 60s" {
		t.Fatalf("undo re-created %+v, want the deleted task's fields", creates[0])
	}
	if creates[0].ID != "" {
		t.Fatalf("undo re-created with fixed id %q, want a fresh id", creates[0].ID)
	}
}

// TestEditTaskFlow covers H11: a task can be edited in place (the form prefills
// the row and Save calls Update instead of Create), so changing a schedule does
// not mean deleting and recreating the task.
func TestEditTaskFlow(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "每周巡检", Prompt: "检查依赖", Schedule: "0 9 * * *",
		Enabled: true, NextRun: time.Unix(1700003600, 0),
	})
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "每周巡检", "每天 09:00")

	h.click(tester, "编辑")
	h.assertTexts(tester, "编辑自动化任务", "任务名称", "运行计划")

	// Replace the name and the schedule, then save.
	h.click(tester, "任务名称")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("每周巡检 v2")
	h.click(tester, "运行计划")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("*/30 * * * *")
	tester.Frame()
	h.click(tester, "保存")

	updates := service.updated()
	if len(updates) != 1 {
		t.Fatalf("Update calls = %+v, want one", updates)
	}
	updated := updates[0]
	if updated.ID != "task-1" || updated.Name != "每周巡检 v2" ||
		updated.Schedule != "*/30 * * * *" || !updated.Enabled {
		t.Fatalf("Update args = %+v", updated)
	}
	if !updated.NextRun.Equal(time.Unix(1700003600, 0)) {
		t.Fatalf("Update lost the task's next run: %v", updated.NextRun)
	}
	h.assertNoTexts(tester, "编辑自动化任务")
}

// TestTaskCardHumanizesSchedules covers the human-readable schedule line: the
// documented shapes lead with "每 N 分钟/小时" or "每天 HH:MM" and an
// unrecognized expression falls back to the raw text.
func TestTaskCardHumanizesSchedules(t *testing.T) {
	service := newFakeAutomation(
		contracts.AutomationTask{ID: "a", Name: "分钟", Prompt: "p", Schedule: "*/15 * * * *", Enabled: true},
		contracts.AutomationTask{ID: "b", Name: "小时", Prompt: "p", Schedule: "0 */4 * * *", Enabled: true},
		contracts.AutomationTask{ID: "c", Name: "每天", Prompt: "p", Schedule: "30 9 * * *", Enabled: true},
		contracts.AutomationTask{ID: "d", Name: "原样", Prompt: "p", Schedule: "unsupported schedule", Enabled: true},
	)
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "每 15 分钟", "每 4 小时", "每天 09:30", "unsupported schedule")
}
