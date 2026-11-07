package regression

// Adversarial audit: the interaction UI module (ask_user / artifact / plan
// dialogs) rendered through the real product view with fake host bridges. The
// negative cases feed the module empty and invalid payloads and assert the frame
// never panics, unknown plan states are ignored, and disposal cancels every
// pending question/plan so an agent tool cannot hang.

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	uiinteraction "github.com/zeokeel/zeokeel/internal/ui/interaction"
)

// interactionHarness boots the interaction UI module with scripted bridges.
type interactionHarness struct {
	h           *harness
	fiber       *cordis.Fiber
	interaction *fakeInteraction
	presenter   *fakePresenter
	reviewer    *fakeReviewer
	tester      *ui.Tester
}

// startInteractionUI activates the interaction module on bare host services and
// installs the three fake bridges before activation.
func startInteractionUI(t *testing.T) *interactionHarness {
	t.Helper()
	h := newBareHarness(t)
	interaction := &fakeInteraction{}
	presenter := &fakePresenter{}
	reviewer := newFakeReviewer("idle")
	provide(t, h.ctx, contracts.KeyUserInteraction, contracts.UserInteraction(interaction))
	provide(t, h.ctx, contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(presenter))
	provide(t, h.ctx, contracts.KeyPlanReviewer, contracts.PlanReviewer(reviewer))
	fiber := activate(t, h.ctx, uiinteraction.Module(), uiinteraction.Config{})
	h.registerScreen("audit-home", "审计首页", "audit-body")
	return &interactionHarness{
		h: h, fiber: fiber, interaction: interaction, presenter: presenter,
		reviewer: reviewer, tester: h.tester(1280, 800),
	}
}

// TestInteractionModuleNoBridgesIsNoop covers the minimal-host path: with no
// interaction/artifact/plan bridge the module must not register a dialog slot.
func TestInteractionModuleNoBridgesIsNoop(t *testing.T) {
	h := newBareHarness(t)
	activate(t, h.ctx, uiinteraction.Module(), uiinteraction.Config{})
	h.registerScreen("audit-home", "审计首页", "audit-body")
	tester := h.tester(1200, 800)
	tester.Frame()
	requireHasText(t, tester, "audit-body")
	for _, name := range h.ui.Runtime().Slots() {
		if name == contracts.SlotOverlayDialogs {
			t.Fatalf("the interaction module registered %q without any bridge", name)
		}
	}
	tester.Frame() // another frame: still no panic, no residue
}

// TestInteractionModuleEmptyQueueRendersNothing: an empty overlay queue builds a
// plain frame with no dialog text and no panic.
func TestInteractionModuleEmptyQueueRendersNothing(t *testing.T) {
	harness := startInteractionUI(t)
	harness.tester.Frame()
	requireHasText(t, harness.tester, "audit-body")
	for _, unwanted := range []string{"需要你的确认", "工件预览", "计划待审阅"} {
		requireNoText(t, harness.tester, unwanted)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "需要你的确认")
}

// TestInteractionModuleEmptyArtifact covers a host bridge delivering a
// completely empty artifact payload: the dialog falls back to host copy instead
// of panicking or showing a blank modal.
func TestInteractionModuleEmptyArtifact(t *testing.T) {
	harness := startInteractionUI(t)
	if !harness.presenter.trigger(contracts.Artifact{ID: "artifact-empty"}) {
		t.Fatal("the interaction module did not install an artifact handler")
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "工件预览") // fallback title
	requireHasText(t, harness.tester, "（无内容）")

	if err := harness.tester.Click("关闭"); err != nil {
		t.Fatalf("click Close: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "工件预览")
	requireHasText(t, harness.tester, "audit-body")
}

// TestInteractionModuleEmptyQuestion covers an empty question body with no
// options: the free-text dialog renders, Cancel resolves it and the next frame
// is clean.
func TestInteractionModuleEmptyQuestion(t *testing.T) {
	harness := startInteractionUI(t)
	if !harness.interaction.trigger(contracts.UserQuestion{ID: "q-empty"}) {
		t.Fatal("the interaction module did not install a question handler")
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "需要你的确认")
	requireHasText(t, harness.tester, "取消")

	if err := harness.tester.Click("取消"); err != nil {
		t.Fatalf("click Cancel: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "需要你的确认")
	if calls := harness.interaction.cancelCalls(); len(calls) != 1 || calls[0] != "q-empty" {
		t.Fatalf("interaction.Cancel calls = %v, want [q-empty]", calls)
	}
}

// TestInteractionModuleEscapeDismisses covers native Modal dismissal (Escape):
// the module must reuse the question's cancel semantics and advance the queue.
func TestInteractionModuleEscapeDismisses(t *testing.T) {
	harness := startInteractionUI(t)
	harness.interaction.trigger(contracts.UserQuestion{ID: "q-escape", Text: "continue?", Options: []string{"yes", "no"}})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "continue?")

	harness.tester.Key(0, ui.KeyEscape)
	harness.tester.Frame()
	requireNoText(t, harness.tester, "continue?")
	if calls := harness.interaction.cancelCalls(); len(calls) != 1 || calls[0] != "q-escape" {
		t.Fatalf("Cancel calls after Escape = %v, want [q-escape]", calls)
	}
}

// TestInteractionModuleQuestionQueueFIFO covers two queued questions: only the
// oldest is shown, answering it opens the next one on the following frame.
func TestInteractionModuleQuestionQueueFIFO(t *testing.T) {
	harness := startInteractionUI(t)
	harness.interaction.trigger(contracts.UserQuestion{ID: "q-first", Text: "first question", Options: []string{"ok"}})
	harness.interaction.trigger(contracts.UserQuestion{ID: "q-second", Text: "second question", Options: []string{"ok"}})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "first question")
	requireNoText(t, harness.tester, "second question")

	if err := harness.tester.Click("ok"); err != nil {
		t.Fatalf("click first answer: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "first question")
	requireHasText(t, harness.tester, "second question")
	if answers := harness.interaction.answerCalls(); len(answers) != 1 || answers[0] != "q-first=ok" {
		t.Fatalf("answers = %v, want [q-first=ok]", answers)
	}
}

// TestInteractionModuleInvalidPlanStates covers bogus and empty plan payloads
// (no dialog, no panic) and the plan error path: a failing Exit keeps the dialog
// open with a readable error instead of closing silently.
func TestInteractionModuleInvalidPlanStates(t *testing.T) {
	harness := startInteractionUI(t)

	// Reviewing with empty content is ignored by the module's queue.
	harness.reviewer.trigger(contracts.PlanReview{State: "reviewing", Content: "   "})
	harness.tester.Frame()
	requireNoText(t, harness.tester, "计划待审阅")
	requireHasText(t, harness.tester, "audit-body")

	// Unknown state is ignored.
	harness.reviewer.trigger(contracts.PlanReview{State: "bogus", Content: "plan"})
	harness.tester.Frame()
	requireNoText(t, harness.tester, "计划待审阅")

	// A real review opens the dialog.
	harness.reviewer.trigger(contracts.PlanReview{State: "reviewing", Content: "步骤：打开界面"})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "计划待审阅")

	// Exit fails: the dialog stays open and shows the localized error prefix.
	harness.reviewer.mu.Lock()
	harness.reviewer.exitErr = errors.New("approval backend down")
	harness.reviewer.mu.Unlock()
	if err := harness.tester.Click("批准"); err != nil {
		t.Fatalf("click Approve: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "计划待审阅")
	requireHasText(t, harness.tester, "批准失败")
	requireHasText(t, harness.tester, "approval backend down")

	// A working Exit closes the dialog.
	harness.reviewer.mu.Lock()
	harness.reviewer.exitErr = nil
	harness.reviewer.mu.Unlock()
	if err := harness.tester.Click("批准"); err != nil {
		t.Fatalf("click Approve (healthy): %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "计划待审阅")
	if exits := harness.reviewer.exits(); len(exits) < 2 || exits[0] != "approve" || exits[1] != "approve" {
		t.Fatalf("reviewer exits = %v, want two approve attempts", exits)
	}
}

// TestInteractionModuleDisposalCancelsPending covers module disposal while a
// question and a plan review are pending: cleanup cancels the question, exits
// the plan and leaves no dialog behind (no agent tool can hang on it).
func TestInteractionModuleDisposalCancelsPending(t *testing.T) {
	harness := startInteractionUI(t)
	harness.interaction.trigger(contracts.UserQuestion{ID: "q-dispose", Text: "still there?"})
	harness.reviewer.trigger(contracts.PlanReview{State: "reviewing", Content: "plan under review"})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "still there?")

	if err := harness.fiber.Dispose(); err != nil {
		t.Fatalf("dispose interaction module: %v", err)
	}
	if calls := harness.interaction.cancelCalls(); len(calls) != 1 || calls[0] != "q-dispose" {
		t.Fatalf("Cancel calls after disposal = %v, want [q-dispose]", calls)
	}
	exits := harness.reviewer.exits()
	if len(exits) != 1 || exits[0] != "cancel" {
		t.Fatalf("plan exits after disposal = %v, want [cancel]", exits)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "still there?")
	requireNoText(t, harness.tester, "计划待审阅")
	requireHasText(t, harness.tester, "audit-body")
}
