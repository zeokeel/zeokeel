package interaction_test

// Headless frame tests for the ask_user dialog module: option questions,
// free-text answers, cancel/Escape dismissal, the FIFO queue and disposal.

import (
	"context"
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

// harness is the minimal host: uiroot services + the fake interaction backend.
type harness struct {
	t           *testing.T
	uiRoot      *uiroot.Root
	interaction *fakeInteraction
	fiber       *cordis.Fiber
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	backend := &fakeInteraction{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide[contracts.UserInteraction](t, ctx, contracts.KeyUserInteraction, backend)

	// One screen keeps the host frame non-empty.
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "home",
		Title: "首页",
		Build: func(c *ui.Context) { ui.Text(c, "主屏幕") },
	}); err != nil {
		t.Fatalf("register home screen: %v", err)
	}
	h := &harness{t: t, uiRoot: uiRoot, interaction: backend}
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
