package session_test

// TDD tests (RED first) for the /goal, /plan and /memory slash commands and
// their native dialogs.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakePlanBridger is a scripted contracts.PlanReviewer.
type fakePlanBridger struct {
	mu        sync.Mutex
	reviews   []string
	reviewErr error
	state     string
}

func (f *fakePlanBridger) SetPlanHandler(func(contracts.PlanReview)) {}

func (f *fakePlanBridger) Review(_ context.Context, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviews = append(f.reviews, content)
	if f.reviewErr != nil {
		return "", f.reviewErr
	}
	f.state = "reviewing"
	return "reviewing", nil
}

func (f *fakePlanBridger) Exit(action string) (string, error) { return action, nil }

func (f *fakePlanBridger) State() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakePlanBridger) reviewCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reviews...)
}

// fakeMemory is a scripted contracts.MemoryStore.
type fakeMemory struct {
	mu     sync.Mutex
	added  []contracts.Memory
	addErr error
}

func (f *fakeMemory) List(context.Context) ([]contracts.Memory, error) { return nil, nil }

func (f *fakeMemory) Add(_ context.Context, memory contracts.Memory) (contracts.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, memory)
	if f.addErr != nil {
		return contracts.Memory{}, f.addErr
	}
	return memory, nil
}

func (f *fakeMemory) Remove(context.Context, string) bool { return true }

func (f *fakeMemory) Search(context.Context, string) ([]contracts.Memory, error) {
	return nil, nil
}

func (f *fakeMemory) addCalls() []contracts.Memory {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.Memory(nil), f.added...)
}

// newCommandDialogHarness builds the session screen with any combination of the
// optional goal/plan/memory backends (nil providers stay absent).
func newCommandDialogHarness(t *testing.T, chat *fakeChat, language string,
	goal contracts.GoalService, plan contracts.PlanReviewer, memory contracts.MemoryStore) *harness {
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
	if goal != nil {
		provide[contracts.GoalService](t, ctx, contracts.KeyGoal, goal)
	}
	if plan != nil {
		provide[contracts.PlanReviewer](t, ctx, contracts.KeyPlanReviewer, plan)
	}
	if memory != nil {
		provide[contracts.MemoryStore](t, ctx, contracts.KeyMemory, memory)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

func findSessionCommand(t *testing.T, h *harness, id string) contracts.CommandSpec {
	t.Helper()
	for _, command := range h.ui.Runtime().Commands() {
		if command.ID == id {
			return command
		}
	}
	t.Fatalf("command %q is not registered; commands = %v", id, h.ui.Runtime().Commands())
	return contracts.CommandSpec{}
}

func focusDialogInput(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("dialog input %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

// --- command registration ---------------------------------------------------

func TestCommandFamilyRegisteredAndEnabled(t *testing.T) {
	chat := permissionChat()
	goal := newFakeGoalService()
	goal.seed("sess-1", "旧目标")
	h := newCommandDialogHarness(t, chat, "zh", goal, &fakePlanBridger{}, &fakeMemory{})
	wanted := []struct{ id, title, hint string }{
		{"zeokeel.session.goal", "设置目标", "为当前会话创建或编辑长期目标"},
		{"zeokeel.session.plan", "审阅计划", "提交一份计划给用户批准"},
	}
	for _, want := range wanted {
		command := findSessionCommand(t, h, want.id)
		if command.Title != want.title || command.Hint != want.hint {
			t.Fatalf("%s = %q/%q, want %q/%q", want.id, command.Title, command.Hint, want.title, want.hint)
		}
		if command.Enabled == nil || !command.Enabled() {
			t.Fatalf("%s is not enabled with its backend and an active session", want.id)
		}
	}

	// The session command family now registers thirteen commands (new/fork/
	// export/permission/settings + goal/plan/memory + stop/file/help +
	// compact).
	count := 0
	for _, command := range h.ui.Runtime().Commands() {
		if strings.HasPrefix(command.ID, "zeokeel.session.") {
			count++
		}
	}
	if count != 13 {
		t.Fatalf("session commands = %d, want 13", count)
	}

	// Without the optional backends the commands stay registered but disabled.
	bare := newCommandDialogHarness(t, permissionChat(), "zh", nil, nil, nil)
	for _, want := range wanted {
		command := findSessionCommand(t, bare, want.id)
		if command.Enabled != nil && command.Enabled() {
			t.Fatalf("%s is enabled without its backend", want.id)
		}
	}
}

// --- /plan ------------------------------------------------------------------

func TestPlanDialogSubmitsAndCloses(t *testing.T) {
	plan := &fakePlanBridger{}
	h := newCommandDialogHarness(t, permissionChat(), "zh", nil, plan, nil)
	tester := h.tester()

	h.typeSlash(tester)
	h.click(tester, "审阅计划")
	h.assertTexts(tester, "提交计划", "计划内容", "提交审阅", "取消")

	focusDialogInput(t, tester, "计划内容")
	tester.Type("1. 先修登录")
	tester.Frame()
	h.click(tester, "提交审阅")
	if got := plan.reviewCalls(); len(got) != 1 || got[0] != "1. 先修登录" {
		t.Fatalf("PlanReviewer.Review calls = %v, want [1. 先修登录]", got)
	}
	h.assertNoTexts(tester, "提交审阅")
}

func TestPlanDialogEmptySubmitDisabled(t *testing.T) {
	plan := &fakePlanBridger{}
	h := newCommandDialogHarness(t, permissionChat(), "zh", nil, plan, nil)
	tester := h.tester()
	h.typeSlash(tester)
	h.click(tester, "审阅计划")
	h.click(tester, "提交审阅")
	if got := plan.reviewCalls(); len(got) != 0 {
		t.Fatalf("empty plan was submitted: %v", got)
	}
	h.assertTexts(tester, "提交审阅")
}

func TestPlanDialogErrorShown(t *testing.T) {
	plan := &fakePlanBridger{reviewErr: errors.New("plan busy")}
	h := newCommandDialogHarness(t, permissionChat(), "zh", nil, plan, nil)
	tester := h.tester()
	h.typeSlash(tester)
	h.click(tester, "审阅计划")
	focusDialogInput(t, tester, "计划内容")
	tester.Type("计划")
	tester.Frame()
	h.click(tester, "提交审阅")
	h.assertTexts(tester, "提交计划失败：plan busy")
}

// --- i18n -------------------------------------------------------------------
