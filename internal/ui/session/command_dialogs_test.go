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

// fakeGoalTracker is a scripted contracts.GoalTracker.
type fakeGoalTracker struct {
	mu      sync.Mutex
	id      string
	text    string
	setErr  error
	sets    []string
	clears  int
	handler func(contracts.Goal, bool)
}

func (f *fakeGoalTracker) SetHandler(fn func(contracts.Goal, bool)) {
	f.mu.Lock()
	f.handler = fn
	f.mu.Unlock()
}

func (f *fakeGoalTracker) Set(_ context.Context, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, text)
	if f.setErr != nil {
		return "", f.setErr
	}
	f.id = "goal-1"
	f.text = text
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(contracts.Goal{ID: f.id, Text: text, Status: "active"}, true)
	}
	f.mu.Lock()
	return f.id, nil
}

func (f *fakeGoalTracker) Get() (string, string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.id == "" {
		return "", "", "", false
	}
	return f.id, f.text, "active", true
}

func (f *fakeGoalTracker) Clear() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	existed := f.id != ""
	f.id = ""
	f.text = ""
	f.clears++
	handler := f.handler
	f.mu.Unlock()
	if existed && handler != nil {
		handler(contracts.Goal{}, false)
	}
	f.mu.Lock()
	return existed
}

func (f *fakeGoalTracker) setCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sets...)
}

func (f *fakeGoalTracker) clearCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clears
}

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
	goal contracts.GoalTracker, plan contracts.PlanReviewer, memory contracts.MemoryStore) *harness {
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
		provide[contracts.GoalTracker](t, ctx, contracts.KeyGoalTracker, goal)
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
	goal := &fakeGoalTracker{id: "g0", text: "旧目标"}
	h := newCommandDialogHarness(t, chat, "zh", goal, &fakePlanBridger{}, &fakeMemory{})
	wanted := []struct{ id, title, hint string }{
		{"zeokeel.session.goal", "设置目标", "为当前会话设置/清除长期目标"},
		{"zeokeel.session.plan", "审阅计划", "提交一份计划给用户批准"},
		{"zeokeel.session.memory", "添加记忆", "保存一条全局记忆"},
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
	// export/permission/settings + goal/plan/memory/feedback + stop/file/help +
	// compact).
	count := 0
	for _, command := range h.ui.Runtime().Commands() {
		if strings.HasPrefix(command.ID, "zeokeel.session.") {
			count++
		}
	}
	if count != 15 {
		t.Fatalf("session commands = %d, want 15", count)
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

// --- /goal ------------------------------------------------------------------

func TestGoalDialogPrefillsSavesAndCloses(t *testing.T) {
	goal := &fakeGoalTracker{id: "g0", text: "旧目标"}
	h := newCommandDialogHarness(t, permissionChat(), "zh", goal, nil, nil)
	tester := h.tester()

	h.typeSlash(tester)
	h.click(tester, "设置目标")
	h.assertTexts(tester, "设置目标", "保存", "删除目标记录", "取消")

	// The input is prefilled with the active goal: saving it unchanged writes
	// the same text back.
	h.click(tester, "保存")
	if got := goal.setCalls(); len(got) != 1 || got[0] != "旧目标" {
		t.Fatalf("GoalTracker.Set calls = %v, want [旧目标]", got)
	}
	h.assertNoTexts(tester, "设置目标")

	// A fresh harness edits the prefilled goal and saves the new text.
	goal2 := &fakeGoalTracker{id: "g0", text: "旧目标"}
	h2 := newCommandDialogHarness(t, permissionChat(), "zh", goal2, nil, nil)
	tester2 := h2.tester()
	h2.typeSlash(tester2)
	h2.click(tester2, "设置目标")
	focusDialogInput(t, tester2, "目标内容")
	tester2.Key(ui.Cmd, ui.KeyA)
	tester2.Key(0, ui.KeyBackspace)
	tester2.Type("新目标")
	tester2.Frame()
	h2.click(tester2, "保存")
	if got := goal2.setCalls(); len(got) != 1 || got[0] != "新目标" {
		t.Fatalf("GoalTracker.Set calls = %v, want [新目标]", got)
	}
}

func TestGoalDialogClearAndCancel(t *testing.T) {
	// An existing goal makes the dialog offer its destructive clear action,
	// which asks through the native AlertDialog before clearing.
	goal := &fakeGoalTracker{id: "g0", text: "旧目标"}
	h := newCommandDialogHarness(t, permissionChat(), "zh", goal, nil, nil)
	tester := h.tester()

	h.typeSlash(tester)
	h.click(tester, "设置目标")
	h.click(tester, "删除目标记录")
	h.assertNoTexts(tester, "设置目标")
	h.assertTexts(tester, "删除目标记录", "取消", "确认删除")
	h.click(tester, "确认删除")
	if got := goal.clearCalls(); got != 1 {
		t.Fatalf("GoalTracker.Clear calls = %d, want 1", got)
	}

	h.typeSlash(tester)
	h.click(tester, "设置目标")
	h.click(tester, "取消")
	h.assertNoTexts(tester, "设置目标")
}

func TestGoalDialogEmptySaveDisabled(t *testing.T) {
	goal := &fakeGoalTracker{}
	h := newCommandDialogHarness(t, permissionChat(), "zh", goal, nil, nil)
	tester := h.tester()
	h.typeSlash(tester)
	h.click(tester, "设置目标")
	h.click(tester, "保存")
	if got := goal.setCalls(); len(got) != 0 {
		t.Fatalf("empty goal was saved: %v", got)
	}
	// The dialog stays open after the disabled save.
	h.assertTexts(tester, "保存", "取消")
}

func TestGoalDialogErrorShown(t *testing.T) {
	goal := &fakeGoalTracker{id: "g0", text: "旧目标", setErr: errors.New("goal store down")}
	h := newCommandDialogHarness(t, permissionChat(), "zh", goal, nil, nil)
	tester := h.tester()
	h.typeSlash(tester)
	h.click(tester, "设置目标")
	h.click(tester, "保存")
	h.assertTexts(tester, "保存目标失败：goal store down")
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

// --- /memory ----------------------------------------------------------------

func TestMemoryDialogAddsAndShowsBadge(t *testing.T) {
	memory := &fakeMemory{}
	h := newCommandDialogHarness(t, permissionChat(), "zh", nil, nil, memory)
	tester := h.tester()

	h.typeSlash(tester)
	h.click(tester, "添加记忆")
	h.assertTexts(tester, "添加记忆", "记忆内容", "添加", "取消")
	focusDialogInput(t, tester, "记忆内容")
	tester.Type("记住这次结论")
	tester.Frame()
	h.click(tester, "添加")

	added := memory.addCalls()
	if len(added) != 1 || added[0].Text != "记住这次结论" || added[0].Scope != "global" {
		t.Fatalf("MemoryStore.Add calls = %+v, want global/记住这次结论", added)
	}
	h.assertNoTexts(tester, "取消")
	// The add success is a transient toast, not a resident badge.
	h.assertNoTexts(tester, "已添加")
}

func TestMemoryDialogErrorShown(t *testing.T) {
	memory := &fakeMemory{addErr: errors.New("memory full")}
	h := newCommandDialogHarness(t, permissionChat(), "zh", nil, nil, memory)
	tester := h.tester()
	h.typeSlash(tester)
	h.click(tester, "添加记忆")
	focusDialogInput(t, tester, "记忆内容")
	tester.Type("记住")
	tester.Frame()
	h.click(tester, "添加")
	h.assertTexts(tester, "添加记忆失败：memory full")
}

func TestMemoryDialogEmptyAddDisabled(t *testing.T) {
	memory := &fakeMemory{}
	h := newCommandDialogHarness(t, permissionChat(), "zh", nil, nil, memory)
	tester := h.tester()
	h.typeSlash(tester)
	h.click(tester, "添加记忆")
	h.click(tester, "添加")
	if got := memory.addCalls(); len(got) != 0 {
		t.Fatalf("empty memory was added: %v", got)
	}
}

// --- i18n -------------------------------------------------------------------

func TestCommandDialogsEnglishCopy(t *testing.T) {
	h := newCommandDialogHarness(t, permissionChat(), "en",
		&fakeGoalTracker{id: "g0", text: "old goal"}, &fakePlanBridger{}, &fakeMemory{})
	tester := h.tester()
	tester.Frame()

	h.typeSlash(tester)
	h.click(tester, "Set goal")
	h.assertTexts(tester, "Goal", "Save", "Delete goal record", "Cancel")

	h.click(tester, "Cancel")
	h.typeSlash(tester)
	h.click(tester, "Review plan")
	h.assertTexts(tester, "Submit plan", "Plan content", "Submit review", "Cancel")

	h.click(tester, "Cancel")
	h.typeSlash(tester)
	h.click(tester, "Add memory")
	h.assertTexts(tester, "Add memory", "Memory text", "Add", "Cancel")
}
