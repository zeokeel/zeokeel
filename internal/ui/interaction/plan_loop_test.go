package interaction_test

// Tests for the plan review keep-planning feedback loop.

import (
	"errors"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/interaction"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// loopChat is a minimal active-session ChatService recording Send.
type loopChat struct {
	mu       sync.Mutex
	active   string
	sendErr  error
	sends    []string
	sessions []string
}

func (c *loopChat) Config() contracts.ChatConfig          { return contracts.ChatConfig{} }
func (c *loopChat) Sessions() []contracts.ChatSessionInfo { return nil }
func (c *loopChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (c *loopChat) ActiveSession() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}
func (c *loopChat) SetActiveSession(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = id
	return nil
}
func (c *loopChat) NewSession(string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = append(c.sessions, "new")
	return "new", nil
}
func (c *loopChat) DeleteSession(string) error { return nil }
func (c *loopChat) Send(sessionID, prompt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, sessionID+"|"+prompt)
	return c.sendErr
}
func (c *loopChat) Cancel(string) error { return nil }
func (c *loopChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

func (c *loopChat) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sends...)
}

// loopNotifier records toasts.
type loopNotifier struct {
	mu     sync.Mutex
	level  string
	text   string
	notify int
}

func (n *loopNotifier) SetHandler(func(contracts.Notification)) {}
func (n *loopNotifier) Notify(level, text string) contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.level = level
	n.text = text
	n.notify++
	return contracts.Notification{ID: "n", Level: level, Text: text}
}
func (n *loopNotifier) Dismiss(string) {}

func (n *loopNotifier) snapshot() (string, string, int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.level, n.text, n.notify
}

type loopHarness struct {
	t      *testing.T
	uiRoot *uiroot.Root
	plan   *fakePlanReviewer
	chat   *loopChat
	notify *loopNotifier
}

func newLoopHarness(t *testing.T, chat *loopChat, notifier *loopNotifier) *loopHarness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	plan := &fakePlanReviewer{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide[contracts.PlanReviewer](t, ctx, contracts.KeyPlanReviewer, plan)
	if chat != nil {
		provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	}
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID: "home", Title: "首页", Build: func(c *ui.Context) { ui.Text(c, "主屏幕") },
	}); err != nil {
		t.Fatalf("register home: %v", err)
	}
	activate(t, ctx, interaction.Module(), interaction.Config{})
	return &loopHarness{t: t, uiRoot: uiRoot, plan: plan, chat: chat, notify: notifier}
}

func (h *loopHarness) tester() *ui.Tester { return ui.NewTester(h.uiRoot.View, 1200, 800) }

func (h *loopHarness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

func (h *loopHarness) focus(tester *ui.Tester, label string) {
	h.t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		h.t.Fatalf("input %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

func (h *loopHarness) triggerPlan(tester *ui.Tester) {
	h.t.Helper()
	h.plan.trigger(contracts.PlanReview{Content: "# 实施计划\n\n第一步", State: "reviewing"})
	tester.Frame()
}

func TestKeepPlanningSendsFeedbackAndCloses(t *testing.T) {
	chat := &loopChat{active: "sess-1"}
	notifier := &loopNotifier{}
	h := newLoopHarness(t, chat, notifier)
	tester := h.tester()
	h.triggerPlan(tester)
	if !tester.HasText("继续规划反馈") || !tester.HasText("继续规划") {
		t.Fatalf("keep-planning controls missing: %v", tester.Texts())
	}

	h.focus(tester, "继续规划反馈")
	tester.Type("请补充回滚步骤")
	tester.Frame()
	h.click(tester, "继续规划")

	if exits := h.plan.exitCalls(); len(exits) != 1 || exits[0] != "cancel" {
		t.Fatalf("Exit calls = %v, want [cancel]", exits)
	}
	sends := chat.sent()
	if len(sends) != 1 || sends[0] != "sess-1|请根据以下反馈继续完善计划：\n\n请补充回滚步骤" {
		t.Fatalf("sends = %v", sends)
	}
	tester.Frame()
	if tester.HasText("计划待审阅") {
		t.Fatalf("plan modal stayed open: %v", tester.Texts())
	}
	level, text, calls := notifier.snapshot()
	if calls != 1 || level != contracts.NotifySuccess || text != "已发送继续规划反馈" {
		t.Fatalf("notifier = %q/%q/%d", level, text, calls)
	}
}

func TestKeepPlanningRequiresFeedback(t *testing.T) {
	chat := &loopChat{active: "sess-1"}
	h := newLoopHarness(t, chat, nil)
	tester := h.tester()
	h.triggerPlan(tester)
	h.click(tester, "继续规划")
	if exits := h.plan.exitCalls(); len(exits) != 0 || len(chat.sent()) != 0 {
		t.Fatalf("empty feedback triggered Exit/Send: %v/%v", exits, chat.sent())
	}
}

func TestKeepPlanningSendFailureKeepsModal(t *testing.T) {
	chat := &loopChat{active: "sess-1", sendErr: errors.New("agent busy")}
	h := newLoopHarness(t, chat, nil)
	tester := h.tester()
	h.triggerPlan(tester)
	h.focus(tester, "继续规划反馈")
	tester.Type("改一下")
	tester.Frame()
	h.click(tester, "继续规划")
	if !tester.HasText("agent busy") || !tester.HasText("继续规划") {
		t.Fatalf("send failure did not keep the modal: %v", tester.Texts())
	}
	if exits := h.plan.exitCalls(); len(exits) != 1 {
		t.Fatalf("Exit calls = %v", exits)
	}
}

func TestKeepPlanningExitFailureKeepsModal(t *testing.T) {
	chat := &loopChat{active: "sess-1"}
	h := newLoopHarness(t, chat, nil)
	h.plan.exitErr = errors.New("plan busy")
	tester := h.tester()
	h.triggerPlan(tester)
	h.focus(tester, "继续规划反馈")
	tester.Type("改一下")
	tester.Frame()
	h.click(tester, "继续规划")
	if !tester.HasText("plan busy") || !tester.HasText("继续规划") {
		t.Fatalf("exit failure did not keep the modal: %v", tester.Texts())
	}
	if len(chat.sent()) != 0 {
		t.Fatalf("send after Exit failure = %v", chat.sent())
	}
}

func TestKeepPlanningWithoutActiveSession(t *testing.T) {
	chat := &loopChat{}
	h := newLoopHarness(t, chat, nil)
	tester := h.tester()
	h.triggerPlan(tester)
	h.focus(tester, "继续规划反馈")
	tester.Type("改一下")
	tester.Frame()
	h.click(tester, "继续规划")
	if !tester.HasText("没有活动会话") {
		t.Fatalf("missing no-active-session error: %v", tester.Texts())
	}
	if len(chat.sent()) != 0 {
		t.Fatalf("sent without an active session: %v", chat.sent())
	}
	if !tester.HasText("继续规划") {
		t.Fatalf("modal closed on no active session: %v", tester.Texts())
	}
}

func TestKeepPlanningHiddenWithoutChat(t *testing.T) {
	h := newLoopHarness(t, nil, nil)
	tester := h.tester()
	h.triggerPlan(tester)
	if tester.HasText("继续规划") || tester.HasText("继续规划反馈") {
		t.Fatalf("keep-planning controls rendered without chat: %v", tester.Texts())
	}
	if !tester.HasText("批准") || !tester.HasText("取消") {
		t.Fatalf("approve/cancel missing: %v", tester.Texts())
	}
}

func TestKeepPlanningEnglishCopy(t *testing.T) {
	chat := &loopChat{active: "sess-1"}
	notifier := &loopNotifier{}
	h := newLoopHarness(t, chat, notifier)
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	h.triggerPlan(tester)
	if !tester.HasText("Keep-planning feedback") || !tester.HasText("Keep planning") {
		t.Fatalf("English keep-planning copy missing: %v", tester.Texts())
	}
	h.focus(tester, "Keep-planning feedback")
	tester.Type("add rollback steps")
	tester.Frame()
	h.click(tester, "Keep planning")
	if _, text, calls := notifier.snapshot(); calls != 1 || text != "Sent keep-planning feedback" {
		t.Fatalf("English notifier = %q/%d", text, calls)
	}
}
