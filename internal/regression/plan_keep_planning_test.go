package regression

// Adversarial audit: the plan-review "继续规划" (keep-planning) feedback loop.
// Negative/boundary cases: feedback is required, a successful keep-planning
// delivers the feedback to the active session once and only then exits the
// review once, closes the overlay and toasts. The send-first order is the C-01
// contract: a Send failure leaves the review untouched (no Exit yet, retry
// possible), and an Exit failure after an accepted feedback keeps the modal
// without ever sending the same feedback twice (a retry only retries the exit).
// No active session is visible; a product without a chat service hides the
// keep-planning controls entirely.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/interaction"
)

// planReviewerFake is the optional PlanReviewer bridge the interaction module
// installs its handler on. The audit triggers reviews through handler.
type planReviewerFake struct {
	mu      sync.Mutex
	handler func(contracts.PlanReview)
	exits   []string
	exitErr error
	reviews []string
	state   string
}

func (p *planReviewerFake) SetPlanHandler(fn func(contracts.PlanReview)) {
	p.mu.Lock()
	p.handler = fn
	p.mu.Unlock()
}

func (p *planReviewerFake) Review(_ context.Context, content string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviews = append(p.reviews, content)
	return "reviewing", nil
}

func (p *planReviewerFake) Exit(action string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exits = append(p.exits, action)
	if p.exitErr != nil {
		return "", p.exitErr
	}
	p.state = "cancelled"
	return "", nil
}

func (p *planReviewerFake) State() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// trigger delivers one reviewing plan through the installed handler.
func (p *planReviewerFake) trigger(review contracts.PlanReview) {
	p.mu.Lock()
	handler := p.handler
	p.mu.Unlock()
	if handler != nil {
		handler(review)
	}
}

// exitCalls returns the recorded Exit actions.
func (p *planReviewerFake) exitCalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.exits...)
}

// notifierFake records Notify calls.
type notifierFake struct {
	mu    sync.Mutex
	level string
	text  string
	calls int
}

func (n *notifierFake) SetHandler(func(contracts.Notification)) {}

func (n *notifierFake) Notify(level, text string) contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.level, n.text = level, text
	n.calls++
	return contracts.Notification{ID: "notice-audit", Level: level, Text: text}
}

func (n *notifierFake) Dismiss(string) {}

func (n *notifierFake) snapshot() (level, text string, calls int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.level, n.text, n.calls
}

// planLoopHarness is the interaction overlay over a scripted chat/notifier.
type planLoopHarness struct {
	h        *harness
	plan     *planReviewerFake
	chat     *fakeChat
	notifier *notifierFake
	tester   *ui.Tester
}

// startPlanLoop activates the interaction overlays with the optional chat and
// notifier bridges.
func startPlanLoop(t *testing.T, activeSession bool, withChat bool, withNotifier bool) *planLoopHarness {
	t.Helper()
	h := newBareHarness(t)
	plan := &planReviewerFake{}
	provide(t, h.ctx, contracts.KeyPlanReviewer, contracts.PlanReviewer(plan))
	var chat *fakeChat
	if withChat {
		chat = newFakeChat()
		if activeSession {
			chat.addSession(contracts.ChatSession{ID: "sess-active", Title: "Plan session"}, true)
		}
		provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	}
	var notifier *notifierFake
	if withNotifier {
		notifier = &notifierFake{}
		provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	}
	activate(t, h.ctx, interaction.Module(), interaction.Config{})
	h.registerScreen("home", "首页", "home-body")
	harness := &planLoopHarness{h: h, plan: plan, chat: chat, notifier: notifier}
	harness.tester = h.tester(1200, 800)
	harness.tester.Frame()
	return harness
}

// triggerPlan queues one reviewing plan and repaints.
func (p *planLoopHarness) triggerPlan(t *testing.T) {
	t.Helper()
	p.plan.trigger(contracts.PlanReview{Content: "# 实施计划\n\n第一步", State: "reviewing"})
	p.tester.Frame()
}

// focusInput focuses one dialog input by label.
func (p *planLoopHarness) focusInput(t *testing.T, label string) {
	t.Helper()
	rect, ok := p.tester.Find(label)
	if !ok {
		t.Fatalf("input %q not found; texts = %v", label, p.tester.Texts())
	}
	p.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	p.tester.Frame()
}

// click clicks one label in the modal and repaints.
func (p *planLoopHarness) click(t *testing.T, label string) {
	t.Helper()
	if err := p.tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, p.tester.Texts())
	}
	p.tester.Frame()
}

// TestPlanKeepPlanningSendsFeedbackOnce covers the happy path: one Exit(cancel),
// one Send to the active session with the feedback wrapper, the overlay closes and
// one success toast is posted.
func TestPlanKeepPlanningSendsFeedbackOnce(t *testing.T) {
	harness := startPlanLoop(t, true, true, true)
	harness.triggerPlan(t)
	requireHasText(t, harness.tester, "计划待审阅")
	requireHasTexts(t, harness.tester, "继续规划", "继续规划反馈")

	harness.focusInput(t, "继续规划反馈")
	harness.tester.Type("请补充回滚步骤")
	harness.tester.Frame()
	harness.click(t, "继续规划")

	if exits := harness.plan.exitCalls(); len(exits) != 1 || exits[0] != "cancel" {
		t.Fatalf("Exit calls = %v, want [cancel]", exits)
	}
	sends := harness.chat.recordedSends()
	want := "sess-active:请根据以下反馈继续完善计划：\n\n请补充回滚步骤"
	if len(sends) != 1 || sends[0] != want {
		t.Fatalf("sends = %v, want [%q]", sends, want)
	}
	if level, text, calls := harness.notifier.snapshot(); calls != 1 ||
		level != contracts.NotifySuccess || text != "已发送继续规划反馈" {
		t.Fatalf("notifier = %q/%q/%d, want one success toast", level, text, calls)
	}
	requireNoText(t, harness.tester, "计划待审阅")
	requireNoText(t, harness.tester, "继续规划")
}

// TestPlanKeepPlanningRequiresAndFailures covers the disabled empty-feedback
// action and the two failure paths: a Send failure keeps the modal without a
// duplicate send; an Exit failure keeps it with no send at all.
func TestPlanKeepPlanningRequiresAndFailures(t *testing.T) {
	t.Run("empty feedback does nothing", func(t *testing.T) {
		harness := startPlanLoop(t, true, true, false)
		harness.triggerPlan(t)
		harness.click(t, "继续规划")
		if exits := harness.plan.exitCalls(); len(exits) != 0 {
			t.Fatalf("empty feedback called Exit: %v", exits)
		}
		if sends := harness.chat.recordedSends(); len(sends) != 0 {
			t.Fatalf("empty feedback sent: %v", sends)
		}
	})

	t.Run("send failure keeps review retryable", func(t *testing.T) {
		harness := startPlanLoop(t, true, true, false)
		harness.chat.setSendError(errors.New("agent busy"))
		harness.triggerPlan(t)
		harness.focusInput(t, "继续规划反馈")
		harness.tester.Type("改一下")
		harness.tester.Frame()
		harness.click(t, "继续规划")
		requireHasTexts(t, harness.tester, "agent busy", "继续规划")
		// C-01: the feedback is delivered before the review closes, so a failed
		// Send leaves the review under review — no Exit yet and the dialog can
		// be retried instead of deadlocking on a cancelled review.
		if exits := harness.plan.exitCalls(); len(exits) != 0 {
			t.Fatalf("send-failure Exit calls = %v, want none (send-first)", exits)
		}
		if sends := harness.chat.recordedSends(); len(sends) != 1 {
			t.Fatalf("send-failure sends = %v, want exactly one attempt", sends)
		}
		// Once the backend accepts, the same action delivers the feedback and
		// closes the review: one more send attempt and the first Exit.
		harness.chat.setSendError(nil)
		harness.click(t, "继续规划")
		if sends := harness.chat.recordedSends(); len(sends) != 2 {
			t.Fatalf("retry sends = %v, want two attempts (failed + accepted)", sends)
		}
		if exits := harness.plan.exitCalls(); len(exits) != 1 || exits[0] != "cancel" {
			t.Fatalf("retry Exit calls = %v, want [cancel]", exits)
		}
		requireNoText(t, harness.tester, "计划待审阅")
	})

	t.Run("exit failure keeps modal without re-sending", func(t *testing.T) {
		harness := startPlanLoop(t, true, true, false)
		harness.plan.exitErr = errors.New("plan busy")
		harness.triggerPlan(t)
		harness.focusInput(t, "继续规划反馈")
		harness.tester.Type("改一下")
		harness.tester.Frame()
		harness.click(t, "继续规划")
		requireHasTexts(t, harness.tester, "plan busy", "继续规划")
		// Send-first: the feedback was accepted before the exit failed, so the
		// retry must only retry the exit, never duplicate the feedback.
		if sends := harness.chat.recordedSends(); len(sends) != 1 {
			t.Fatalf("sends after Exit failure = %v, want the one accepted feedback", sends)
		}
		if exits := harness.plan.exitCalls(); len(exits) != 1 {
			t.Fatalf("Exit calls after failure = %v, want one attempt", exits)
		}
		harness.click(t, "继续规划")
		if sends := harness.chat.recordedSends(); len(sends) != 1 {
			t.Fatalf("retry re-sent the feedback: %v", sends)
		}
		if exits := harness.plan.exitCalls(); len(exits) != 2 {
			t.Fatalf("retry Exit calls = %v, want two attempts", exits)
		}
		// A successful exit closes the modal; the feedback stayed a single send.
		harness.plan.exitErr = nil
		harness.click(t, "继续规划")
		if sends := harness.chat.recordedSends(); len(sends) != 1 {
			t.Fatalf("final close re-sent the feedback: %v", sends)
		}
		requireNoText(t, harness.tester, "计划待审阅")
	})
}

// TestPlanKeepPlanningNoActiveSessionAndNoChat covers the missing preconditions:
// no active session keeps the modal with a readable error and no send; a product
// without chat hides the keep-planning controls but keeps approve/cancel.
func TestPlanKeepPlanningNoActiveSessionAndNoChat(t *testing.T) {
	t.Run("no active session", func(t *testing.T) {
		harness := startPlanLoop(t, false, true, false)
		harness.triggerPlan(t)
		harness.focusInput(t, "继续规划反馈")
		harness.tester.Type("改一下")
		harness.tester.Frame()
		harness.click(t, "继续规划")
		requireHasTexts(t, harness.tester, "没有活动会话", "继续规划")
		if sends := harness.chat.recordedSends(); len(sends) != 0 {
			t.Fatalf("no-active-session send = %v", sends)
		}
		// The review is untouched (no Exit) and stays closable: Escape exits.
		if exits := harness.plan.exitCalls(); len(exits) != 0 {
			t.Fatalf("no-active-session Exit calls = %v, want none", exits)
		}
		harness.tester.Key(0, ui.KeyEscape)
		harness.tester.Frame()
		harness.tester.Frame()
		requireNoText(t, harness.tester, "计划待审阅")
	})

	t.Run("no chat hides controls", func(t *testing.T) {
		harness := startPlanLoop(t, false, false, false)
		harness.triggerPlan(t)
		requireNoText(t, harness.tester, "继续规划")
		requireNoText(t, harness.tester, "继续规划反馈")
		requireHasTexts(t, harness.tester, "批准", "取消")
	})
}
