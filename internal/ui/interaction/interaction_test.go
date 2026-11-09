package interaction_test

// Headless frame tests for the ask_user dialog module: option questions,
// free-text answers, cancel/Escape dismissal, the FIFO queue and disposal.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/interaction"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeInteraction is a contracts.UserInteraction whose handler the test drives
// synchronously; Answer/Cancel are recorded.
type fakeInteraction struct {
	mu      sync.Mutex
	handler func(contracts.UserQuestion)
	cleared bool
	answers []answerCall
	cancels []string
}

type answerCall struct {
	id     string
	answer string
}

func (f *fakeInteraction) SetHandler(fn func(contracts.UserQuestion)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = fn
	if fn == nil {
		f.cleared = true
	}
}

func (f *fakeInteraction) Ask(context.Context, string, []string) (string, error) {
	return "", nil
}

func (f *fakeInteraction) Answer(id, answer string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, answerCall{id: id, answer: answer})
}

func (f *fakeInteraction) Cancel(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, id)
}

func (f *fakeInteraction) trigger(question contracts.UserQuestion) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(question)
	}
}

func (f *fakeInteraction) answered() []answerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]answerCall(nil), f.answers...)
}

func (f *fakeInteraction) cancelled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancels...)
}

func (f *fakeInteraction) isCleared() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cleared
}

// fakeArtifactPresenter captures the artifact handler; Present is unused by the
// dialog module (the host records the artifact and returns its id).
type fakeArtifactPresenter struct {
	mu      sync.Mutex
	handler func(contracts.Artifact)
	cleared bool
}

func (f *fakeArtifactPresenter) SetArtifactHandler(fn func(contracts.Artifact)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = fn
	if fn == nil {
		f.cleared = true
	}
}

func (f *fakeArtifactPresenter) Present(context.Context, string, string) (string, error) {
	return "", nil
}

func (f *fakeArtifactPresenter) trigger(artifact contracts.Artifact) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(artifact)
	}
}

func (f *fakeArtifactPresenter) isCleared() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cleared
}

// fakePlanReviewer captures the plan handler and records Exit decisions.
type fakePlanReviewer struct {
	mu      sync.Mutex
	handler func(contracts.PlanReview)
	cleared bool
	exits   []string
	exitErr error
	state   string
}

func (f *fakePlanReviewer) SetPlanHandler(fn func(contracts.PlanReview)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = fn
	if fn == nil {
		f.cleared = true
	}
}

func (f *fakePlanReviewer) Review(context.Context, string) (string, error) {
	return "", nil
}

func (f *fakePlanReviewer) Exit(action string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exits = append(f.exits, action)
	if f.exitErr != nil {
		return "", f.exitErr
	}
	switch action {
	case "approve":
		f.state = "approved"
	case "cancel":
		f.state = "cancelled"
	}
	return action, nil
}

func (f *fakePlanReviewer) State() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakePlanReviewer) trigger(review contracts.PlanReview) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(review)
	}
}

func (f *fakePlanReviewer) exitCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.exits...)
}

func (f *fakePlanReviewer) isCleared() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cleared
}

// harness is the minimal host: uiroot services + the fake interaction backend.
type harness struct {
	t           *testing.T
	uiRoot      *uiroot.Root
	interaction *fakeInteraction
	artifacts   *fakeArtifactPresenter
	plan        *fakePlanReviewer
	chat        *planChat
	fiber       *cordis.Fiber
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessFull(t, true, true)
}

// newAskOnlyHarness runs the module without the artifact/plan bridges (the
// degraded no-op mode for a minimal host).
func newAskOnlyHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessFull(t, false, false)
}

// planChat is a minimal ChatService: the plan-review feedback path needs the
// active session and Send, nothing else.
type planChat struct {
	mu     sync.Mutex
	active string
	sends  []string
}

func (c *planChat) Config() contracts.ChatConfig          { return contracts.ChatConfig{} }
func (c *planChat) Sessions() []contracts.ChatSessionInfo { return nil }
func (c *planChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (c *planChat) ActiveSession() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}
func (c *planChat) SetActiveSession(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = id
	return nil
}
func (c *planChat) NewSession(string) (string, error) { return "", nil }
func (c *planChat) DeleteSession(string) error        { return nil }
func (c *planChat) Send(_, prompt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, prompt)
	return nil
}
func (c *planChat) Cancel(string) error { return nil }
func (c *planChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}
func (c *planChat) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sends...)
}

func newHarnessFull(t *testing.T, withArtifacts, withPlan bool) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	backend := &fakeInteraction{}
	planChatService := &planChat{active: "sess-plan"}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, planChatService)
	provide[contracts.UserInteraction](t, ctx, contracts.KeyUserInteraction, backend)
	artifacts := &fakeArtifactPresenter{}
	plan := &fakePlanReviewer{}
	if withArtifacts {
		provide[contracts.ArtifactPresenter](t, ctx, contracts.KeyArtifactPresenter, artifacts)
	}
	if withPlan {
		provide[contracts.PlanReviewer](t, ctx, contracts.KeyPlanReviewer, plan)
	}

	// One screen keeps the host frame non-empty.
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "home",
		Title: "首页",
		Build: func(c *ui.Context) { ui.Text(c, "主屏幕") },
	}); err != nil {
		t.Fatalf("register home screen: %v", err)
	}
	h := &harness{t: t, uiRoot: uiRoot, interaction: backend, artifacts: artifacts, plan: plan, chat: planChatService}
	h.fiber = activate(t, ctx, interaction.Module(), interaction.Config{})
	return h
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.uiRoot.View, 1200, 800)
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

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	// Build the frame after the click handler mutated the dialogue state.
	tester.Frame()
}

func (h *harness) focusInput(tester *ui.Tester, label string) {
	h.t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		h.t.Fatalf("input labeled %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

// --- tests ------------------------------------------------------------------

func TestOptionQuestionAnswers(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{
		ID: "q1", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	})
	tester.Frame()
	h.assertTexts(tester, "需要你的确认", "部署到哪个环境？", "staging", "prod", "取消")

	h.click(tester, "prod")
	answers := h.interaction.answered()
	if len(answers) != 1 || answers[0].id != "q1" || answers[0].answer != "prod" {
		t.Fatalf("answers = %+v, want q1/prod", answers)
	}
	if cancels := h.interaction.cancelled(); len(cancels) != 0 {
		t.Fatalf("cancelled = %v, want none", cancels)
	}
	tester.Frame()
	h.assertNoTexts(tester, "部署到哪个环境？")
}

func TestFreeTextQuestionAnswers(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{ID: "q2", Text: "你的名字？"})
	tester.Frame()
	h.assertTexts(tester, "需要你的确认", "你的名字？", "回答", "提交", "取消")

	h.focusInput(tester, "回答")
	tester.Type("小明")
	tester.Frame()
	h.click(tester, "提交")

	answers := h.interaction.answered()
	if len(answers) != 1 || answers[0].id != "q2" || answers[0].answer != "小明" {
		t.Fatalf("answers = %+v, want q2/小明", answers)
	}
	tester.Frame()
	h.assertNoTexts(tester, "你的名字？")
}

func TestCancelButtonCancels(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{ID: "q3", Text: "继续吗？", Options: []string{"继续"}})
	tester.Frame()
	h.click(tester, "取消")

	if cancels := h.interaction.cancelled(); len(cancels) != 1 || cancels[0] != "q3" {
		t.Fatalf("cancelled = %v, want [q3]", cancels)
	}
	if answers := h.interaction.answered(); len(answers) != 0 {
		t.Fatalf("answers = %v, want none", answers)
	}
}

// TestOptionQuestionStrayEnterCancelsInsteadOfAnswering covers C-03's safety
// contract: Enter right after the dialog opens (the user may still be pressing
// the key that sent the prompt) must not answer the first option.
func TestOptionQuestionStrayEnterCancelsInsteadOfAnswering(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.interaction.trigger(contracts.UserQuestion{
		ID: "q-stray", Text: "部署到哪个环境？", Options: []string{"生产环境", "测试环境"},
	})
	tester.Frame()
	if !tester.Focused("取消") {
		t.Fatalf("no neutral initial focus: %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if answers := h.interaction.answered(); len(answers) != 0 {
		t.Fatalf("stray Enter answered %+v, want no answer", answers)
	}
	if cancels := h.interaction.cancelled(); len(cancels) != 1 || cancels[0] != "q-stray" {
		t.Fatalf("cancellations = %v, want [q-stray]", cancels)
	}
	tester.Frame()
	h.assertNoTexts(tester, "部署到哪个环境？")
}

// TestOptionQuestionExplicitTabEnterAnswers covers the explicit keyboard path:
// Tab reaches an option and Enter chooses it.
func TestOptionQuestionExplicitTabEnterAnswers(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.interaction.trigger(contracts.UserQuestion{
		ID: "q-tab", Text: "部署到哪个环境？", Options: []string{"生产环境", "测试环境"},
	})
	tester.Frame()
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	if !tester.Focused("生产环境") {
		t.Fatalf("Tab did not reach the first option; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	answers := h.interaction.answered()
	if len(answers) != 1 || answers[0].id != "q-tab" || answers[0].answer != "生产环境" {
		t.Fatalf("answers = %+v, want q-tab/生产环境", answers)
	}
}

// TestOptionQuestionOtherAnswer covers C-14 (partial): an options question has
// a fixed trailing "Other…" choice whose inline input becomes the answer.
func TestOptionQuestionOtherAnswer(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.interaction.trigger(contracts.UserQuestion{
		ID: "q-other", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	})
	tester.Frame()
	if !tester.HasText("其他…") {
		t.Fatalf("the Other choice is missing; texts = %v", tester.Texts())
	}
	h.click(tester, "其他…")
	if !tester.HasText("输入自定义回答") && !tester.Focused("其他回答") {
		t.Fatalf("the inline Other input did not open; texts = %v", tester.Texts())
	}
	if !tester.Focused("其他回答") {
		t.Fatalf("the Other input did not take the keyboard; texts = %v", tester.Texts())
	}
	// The option buttons stay visible: entering Other is not a commitment.
	h.assertTexts(tester, "staging", "prod", "Enter = 提交自定义回答")
	tester.Type("预发布环境")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	answers := h.interaction.answered()
	if len(answers) != 1 || answers[0].id != "q-other" || answers[0].answer != "预发布环境" {
		t.Fatalf("answers = %+v, want q-other/预发布环境", answers)
	}
	tester.Frame()
	h.assertNoTexts(tester, "部署到哪个环境？")
}

// TestOptionQuestionOtherEscapeCollapses covers the Other input's Escape
// contract: it returns to the options instead of dismissing the question, and
// the choice state does not leak into the next question.
func TestOptionQuestionOtherEscapeCollapses(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.interaction.trigger(contracts.UserQuestion{
		ID: "q-esc", Text: "部署到哪个环境？", Options: []string{"staging", "prod"},
	})
	tester.Frame()
	h.click(tester, "其他…")
	tester.Type("临时")
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if answers, cancels := h.interaction.answered(), h.interaction.cancelled(); len(answers) != 0 || len(cancels) != 0 {
		t.Fatalf("Escape submitted/cancelled: answers=%+v cancels=%v", answers, cancels)
	}
	h.assertTexts(tester, "部署到哪个环境？", "其他…", "Esc = 取消")
	h.assertNoTexts(tester, "输入自定义回答")
	// The options still decide; picking one answers the question.
	h.click(tester, "staging")
	if answers := h.interaction.answered(); len(answers) != 1 || answers[0].answer != "staging" {
		t.Fatalf("answers = %+v, want staging", answers)
	}
	// A following options question opens on the options, not on a stale input.
	h.interaction.trigger(contracts.UserQuestion{ID: "q-next", Text: "下一个？", Options: []string{"好"}})
	tester.Frame()
	h.assertTexts(tester, "下一个？", "其他…")
	h.assertNoTexts(tester, "输入自定义回答")
}

func TestEscapeCancels(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{ID: "q4", Text: "确认删除？", Options: []string{"删除"}})
	tester.Frame()
	h.assertTexts(tester, "确认删除？")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if cancels := h.interaction.cancelled(); len(cancels) != 1 || cancels[0] != "q4" {
		t.Fatalf("cancelled = %v, want [q4]", cancels)
	}
	tester.Frame()
	h.assertNoTexts(tester, "确认删除？")
}

func TestQuestionQueueIsFIFO(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{ID: "a", Text: "第一个问题？", Options: []string{"A"}})
	h.interaction.trigger(contracts.UserQuestion{ID: "b", Text: "第二个问题？", Options: []string{"B"}})
	tester.Frame()

	h.assertTexts(tester, "第一个问题？")
	h.assertNoTexts(tester, "第二个问题？")
	h.click(tester, "A")

	tester.Frame()
	h.assertTexts(tester, "第二个问题？")
	h.assertNoTexts(tester, "第一个问题？")
	h.click(tester, "B")

	if answers := h.interaction.answered(); len(answers) != 2 ||
		answers[0].id != "a" || answers[1].id != "b" {
		t.Fatalf("answers = %+v, want a then b", answers)
	}
	tester.Frame()
	h.assertNoTexts(tester, "第二个问题？")
}

func TestDisposeClearsHandlerAndCancelsPending(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{ID: "d1", Text: "待回答一", Options: []string{"好"}})
	h.interaction.trigger(contracts.UserQuestion{ID: "d2", Text: "待回答二", Options: []string{"好"}})
	tester.Frame()

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if !h.interaction.isCleared() {
		t.Fatal("dispose did not clear the interaction handler")
	}
	cancels := h.interaction.cancelled()
	if len(cancels) != 2 || cancels[0] != "d1" || cancels[1] != "d2" {
		t.Fatalf("cancelled after dispose = %v, want [d1 d2]", cancels)
	}
	tester.Frame()
	h.assertNoTexts(tester, "待回答一", "待回答二")
}

func TestEnglishCopy(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")

	h.interaction.trigger(contracts.UserQuestion{ID: "e1", Text: "Deploy?", Options: []string{"yes"}})
	tester.Frame()
	h.assertTexts(tester, "Needs your input", "Deploy?", "yes", "Cancel")

	h.click(tester, "Cancel")
	h.interaction.trigger(contracts.UserQuestion{ID: "e2", Text: "Your name?"})
	tester.Frame()
	h.assertTexts(tester, "Needs your input", "Your name?", "Answer", "Submit", "Cancel")
}

// descriptorPlugin adapts a module.Descriptor to the kernel plugin interfaces.
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

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// --- artifacts --------------------------------------------------------------

func TestArtifactPreviewShowsMarkdownAndCloses(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.artifacts.trigger(contracts.Artifact{
		ID: "a1", Title: "巡检报告", Body: "# 报告标题\n\n正文内容",
	})
	tester.Frame()
	h.assertTexts(tester, "巡检报告", "报告标题", "正文内容", "关闭")

	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("click close: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertNoTexts(tester, "巡检报告", "正文内容")
}

func TestArtifactQueueIsFIFO(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.artifacts.trigger(contracts.Artifact{ID: "a1", Title: "第一个工件", Body: "一"})
	h.artifacts.trigger(contracts.Artifact{ID: "a2", Title: "第二个工件", Body: "二"})
	tester.Frame()
	h.assertTexts(tester, "第一个工件")
	h.assertNoTexts(tester, "第二个工件")

	h.click(tester, "关闭")
	tester.Frame()
	h.assertTexts(tester, "第二个工件")
	h.assertNoTexts(tester, "第一个工件")
	h.click(tester, "关闭")
	tester.Frame()
	h.assertNoTexts(tester, "第二个工件")
}

// --- plan review ------------------------------------------------------------

func TestPlanReviewApprove(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.plan.trigger(contracts.PlanReview{Content: "# 实施计划\n\n第一步", State: "reviewing"})
	tester.Frame()
	h.assertTexts(tester, "计划待审阅", "实施计划", "第一步", "批准", "取消计划")

	h.click(tester, "批准")
	if exits := h.plan.exitCalls(); len(exits) != 1 || exits[0] != "approve" {
		t.Fatalf("Exit calls = %v, want [approve]", exits)
	}
	tester.Frame()
	h.assertNoTexts(tester, "计划待审阅")
}

func TestPlanReviewCancelAndEscape(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.plan.trigger(contracts.PlanReview{Content: "计划内容", State: "reviewing"})
	tester.Frame()
	h.click(tester, "取消计划")
	if exits := h.plan.exitCalls(); len(exits) != 1 || exits[0] != "cancel" {
		t.Fatalf("Exit calls = %v, want [cancel]", exits)
	}

	// Escape must not cancel a plan: the review stays open and only an
	// explicit footer action decides. The footer says so.
	h.plan.trigger(contracts.PlanReview{Content: "第二份计划", State: "reviewing"})
	tester.Frame()
	h.assertTexts(tester, "计划待审阅", "Esc 不会取消计划")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if exits := h.plan.exitCalls(); len(exits) != 1 {
		t.Fatalf("Exit calls after Escape = %v, want the earlier explicit cancel only", exits)
	}
	// C-16: Escape is not a silent no-op any more — it shows why it did nothing.
	h.assertTexts(tester, "计划待审阅", "Esc 不能关闭计划审阅")

	// The explicit cancel is still available and cancels.
	h.click(tester, "取消计划")
	exits := h.plan.exitCalls()
	if len(exits) != 2 || exits[1] != "cancel" {
		t.Fatalf("Exit calls after the cancel action = %v, want a second cancel", exits)
	}
	tester.Frame()
	h.assertNoTexts(tester, "计划待审阅")
}

func TestPlanReviewErrorStaysVisible(t *testing.T) {
	h := newHarness(t)
	h.plan.exitErr = errors.New("plan is busy")
	tester := h.tester()

	h.plan.trigger(contracts.PlanReview{Content: "计划内容", State: "reviewing"})
	tester.Frame()
	h.click(tester, "批准")
	h.assertTexts(tester, "批准失败：plan is busy", "批准")
}

// --- mixed queue / i18n / degraded -----------------------------------------

func TestAskAndArtifactShareOneQueue(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{ID: "q1", Text: "先回答问题？", Options: []string{"好"}})
	h.artifacts.trigger(contracts.Artifact{ID: "a1", Title: "后显示的工件", Body: "工件正文"})
	tester.Frame()
	h.assertTexts(tester, "先回答问题？")
	h.assertNoTexts(tester, "后显示的工件")

	h.click(tester, "好")
	tester.Frame()
	h.assertTexts(tester, "后显示的工件")
	h.click(tester, "关闭")
	tester.Frame()
	h.assertNoTexts(tester, "后显示的工件")
}

func TestEnglishOverlayCopy(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")

	h.artifacts.trigger(contracts.Artifact{ID: "a1", Title: "Report", Body: "Body"})
	tester.Frame()
	h.assertTexts(tester, "Report", "Close")

	h.artifacts.trigger(contracts.Artifact{ID: "a2", Title: "Second", Body: "Body"})
	tester.Frame()
	h.click(tester, "Close")
	tester.Frame()
	h.click(tester, "Close")
	tester.Frame()

	h.plan.trigger(contracts.PlanReview{Content: "Plan body", State: "reviewing"})
	tester.Frame()
	h.assertTexts(tester, "Plan review", "Approve", "Cancel")
}

func TestDegradesWithoutOptionalBridges(t *testing.T) {
	// Only UserInteraction is provided: the ask dialog keeps working and the
	// module still registers its slot without touching the missing bridges.
	h := newAskOnlyHarness(t)
	tester := h.tester()
	h.interaction.trigger(contracts.UserQuestion{ID: "q1", Text: "还能提问吗？", Options: []string{"能"}})
	tester.Frame()
	h.assertTexts(tester, "需要你的确认", "还能提问吗？")
	h.click(tester, "能")
	if answers := h.interaction.answered(); len(answers) != 1 || answers[0].answer != "能" {
		t.Fatalf("answers = %+v, want q1/能", answers)
	}
}

func TestDisposeClearsAllHandlers(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.artifacts.trigger(contracts.Artifact{ID: "a1", Title: "工件", Body: "内容"})
	h.plan.trigger(contracts.PlanReview{Content: "计划", State: "reviewing"})
	tester.Frame()

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if !h.interaction.isCleared() || !h.artifacts.isCleared() || !h.plan.isCleared() {
		t.Fatalf("handlers not cleared: interaction=%v artifacts=%v plan=%v",
			h.interaction.isCleared(), h.artifacts.isCleared(), h.plan.isCleared())
	}
	if exits := h.plan.exitCalls(); len(exits) != 1 || exits[0] != "cancel" {
		t.Fatalf("reviewing plan was not cancelled on dispose: %v", exits)
	}
	tester.Frame()
	h.assertNoTexts(tester, "工件")
}

// TestQuestionOptionsStackedAndCancelInFooter covers the ask_user layout: the
// equal choices stack vertically in one column, and cancel sits in the footer
// to the left of nothing (the options are the primary actions).
func TestQuestionOptionsStackedAndCancelInFooter(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()

	h.interaction.trigger(contracts.UserQuestion{ID: "q1", Text: "选一个", Options: []string{"staging", "prod"}})
	tester.Frame()
	first, ok := tester.Find("staging")
	if !ok {
		t.Fatalf("option missing; texts = %v", tester.Texts())
	}
	second, ok := tester.Find("prod")
	if !ok {
		t.Fatalf("second option missing; texts = %v", tester.Texts())
	}
	if first.X+first.W/2 != second.X+second.W/2 {
		t.Fatalf("options are not stacked in one column: centers = %.1f and %.1f",
			first.X+first.W/2, second.X+second.W/2)
	}
	if second.Y <= first.Y {
		t.Fatalf("options are not vertical: y = %.1f then %.1f", first.Y, second.Y)
	}
	// C-03: the dialog focuses Cancel, not the first option, so a stray Enter
	// cannot silently answer with an option the user never chose.
	if !tester.Focused("取消") {
		t.Fatalf("the cancel action does not hold the initial focus; texts = %v", tester.Texts())
	}
	if tester.Focused("staging") {
		t.Fatal("the first option holds the initial focus")
	}

	h.click(tester, "staging")
	if answers := h.interaction.answered(); len(answers) != 1 || answers[0].answer != "staging" {
		t.Fatalf("answers = %+v, want staging", answers)
	}
}

// TestPlanReviewEnterKeepsPlanning covers H3: Enter in the feedback input never
// approves the plan. With text it sends the feedback through Keep planning;
// with no text it does nothing, so the feedback can never be dropped by an
// approval-by-accident.
func TestPlanReviewEnterKeepsPlanning(t *testing.T) {
	h := newHarness(t)
	tester := h.tester()
	h.plan.trigger(contracts.PlanReview{Content: "计划内容", State: "reviewing"})
	tester.Frame()
	h.assertTexts(tester, "继续规划反馈", "输入反馈后按 Enter = 继续规划")

	// Empty feedback: Enter leaves the review open and approves nothing.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if exits := h.plan.exitCalls(); len(exits) != 0 {
		t.Fatalf("Exit calls with empty feedback = %v, want none", exits)
	}
	h.assertTexts(tester, "计划待审阅")

	// Typed feedback: Enter keeps planning and sends the feedback to the agent.
	if err := tester.Click("继续规划反馈"); err != nil {
		t.Fatalf("focus feedback: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Type("先补回归测试")
	tester.Frame()
	h.assertTexts(tester, "Enter = 继续规划（发送反馈给 agent）")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	exits := h.plan.exitCalls()
	if len(exits) != 1 || exits[0] != "cancel" {
		t.Fatalf("Exit calls after Enter with feedback = %v, want [cancel]", exits)
	}
	sends := h.chat.sent()
	if len(sends) != 1 || !strings.Contains(sends[0], "先补回归测试") {
		t.Fatalf("feedback sends = %v, want the typed feedback delivered", sends)
	}
	tester.Frame()
	h.assertNoTexts(tester, "计划待审阅")

	// Approving stays an explicit action.
	h2 := newHarness(t)
	tester2 := h2.tester()
	h2.plan.trigger(contracts.PlanReview{Content: "第二份计划", State: "reviewing"})
	tester2.Frame()
	h2.click(tester2, "批准")
	if exits := h2.plan.exitCalls(); len(exits) != 1 || exits[0] != "approve" {
		t.Fatalf("Exit calls after clicking Approve = %v, want [approve]", exits)
	}
}
