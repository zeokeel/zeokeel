package uikit_test

// Headless frame tests for uikit.PlanReviewDialog (AGENT-SURFACES.md S5): the
// product's plan review dialog. They pin the safety contract — no default
// action, Enter with feedback keeps planning while an empty feedback never
// approves, Escape does not cancel but shows a visible one-shot RoleAlert
// warning, and a failed decision's error line lets Escape close the review.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// planReviewTestState records the callbacks the dialog fires.
type planReviewTestState struct {
	approve int
	cancel  int
	kept    []string
	dismiss int
}

// planReviewTestView draws one plan review dialog over a plain product frame.
func planReviewTestView(state *planReviewTestState, feedback *string, plan contracts.PlanReview, errText string, locale contracts.LocaleService) func(*ui.Context) {
	options := uikit.PlanReviewOptions{
		Plan:     plan,
		Feedback: feedback,
		Err:      errText,
		Locale:   locale,
		OnApprove: func() {
			state.approve++
		},
		OnCancel: func() {
			state.cancel++
		},
		OnKeep: func(feedbackText string) {
			state.kept = append(state.kept, feedbackText)
		},
		OnDismiss: func() {
			state.dismiss++
		},
	}
	return func(c *ui.Context) {
		ui.Text(c, "主屏幕")
		uikit.PlanReviewDialog(c, options)
	}
}

// planReviewHarness starts one plan review dialog and returns its tester.
func planReviewHarness(t *testing.T, feedback *string, errText string) (*planReviewTestState, *ui.Tester) {
	t.Helper()
	state := &planReviewTestState{}
	tester := ui.NewTester(planReviewTestView(state, feedback,
		contracts.PlanReview{Content: "# 实施计划\n\n第一步", State: "reviewing"}, errText, nil), 1200, 800)
	tester.Frame()
	return state, tester
}

// planReviewFocus focuses a dialog input by its label, as a pointer click does.
func planReviewFocus(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("input %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

// TestPlanReviewDialogRendersPlanAndSafeFooter pins the content and the footer:
// Approve is the rightmost Primary action, Cancel plan the leftmost ordinary
// action, Keep planning sits between them, and no action is the Default.
func TestPlanReviewDialogRendersPlanAndSafeFooter(t *testing.T) {
	feedback := ""
	state, tester := planReviewHarness(t, &feedback, "")

	for _, want := range []string{
		"计划待审阅", "实施计划", "第一步", "继续规划反馈",
		"取消计划", "继续规划", "批准",
		"输入反馈后按 Enter = 继续规划 · 点击「批准」批准计划 · Esc 不会取消计划",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	cancel, ok := tester.Find("取消计划")
	if !ok {
		t.Fatalf("cancel action missing; texts = %v", tester.Texts())
	}
	keep, ok := tester.Find("继续规划")
	if !ok {
		t.Fatalf("keep-planning action missing; texts = %v", tester.Texts())
	}
	approve, ok := tester.Find("批准")
	if !ok {
		t.Fatalf("approve action missing; texts = %v", tester.Texts())
	}
	if !(cancel.X < keep.X && keep.X < approve.X) {
		t.Fatalf("footer order x = cancel %.1f, keep %.1f, approve %.1f; want cancel < keep < approve",
			cancel.X, keep.X, approve.X)
	}
	if tester.Focused("批准") || tester.Focused("取消计划") || tester.Focused("继续规划") {
		t.Fatalf("the plan review must not autofocus an action; texts = %v", tester.Texts())
	}
	// The empty feedback disables Keep planning and a stray Enter decides
	// nothing: it can never approve.
	keepInfo, ok := tester.AccessByRole(ui.RoleButton, "继续规划")
	if !ok {
		t.Fatalf("keep-planning button missing from the access tree; texts = %v", tester.Texts())
	}
	if !keepInfo.Disabled {
		t.Fatal("Keep planning is enabled with an empty feedback")
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if state.approve != 0 || state.cancel != 0 || len(state.kept) != 0 {
		t.Fatalf("stray Enter fired callbacks: approve=%d cancel=%d keep=%v", state.approve, state.cancel, state.kept)
	}
	if !tester.HasText("计划待审阅") {
		t.Fatalf("stray Enter closed the review; texts = %v", tester.Texts())
	}
}

// TestPlanReviewDialogApproveAndCancel covers the two explicit decisions.
func TestPlanReviewDialogApproveAndCancel(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		state, tester := planReviewHarness(t, nil, "")
		if err := tester.Click("批准"); err != nil {
			t.Fatalf("click Approve: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if state.approve != 1 || state.cancel != 0 || state.dismiss != 0 {
			t.Fatalf("callbacks after Approve = approve %d, cancel %d, dismiss %d; want approve only",
				state.approve, state.cancel, state.dismiss)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		state, tester := planReviewHarness(t, nil, "")
		if err := tester.Click("取消计划"); err != nil {
			t.Fatalf("click Cancel plan: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if state.cancel != 1 || state.approve != 0 || state.dismiss != 0 {
			t.Fatalf("callbacks after Cancel plan = approve %d, cancel %d, dismiss %d; want cancel only",
				state.approve, state.cancel, state.dismiss)
		}
	})
}

// TestPlanReviewDialogKeepPlanning covers H3: Enter in the feedback input never
// approves. With text it keeps planning with the feedback; an empty feedback
// decides nothing.
func TestPlanReviewDialogKeepPlanning(t *testing.T) {
	t.Run("Enter with feedback", func(t *testing.T) {
		feedback := ""
		state, tester := planReviewHarness(t, &feedback, "")
		planReviewFocus(t, tester, "继续规划反馈")
		tester.Type("请补充回滚步骤")
		tester.Frame()
		if !tester.HasText("Enter = 继续规划（发送反馈给 agent）") {
			t.Fatalf("the hint did not follow the typed feedback; texts = %v", tester.Texts())
		}
		// The action becomes available with the typed feedback.
		keepInfo, ok := tester.AccessByRole(ui.RoleButton, "继续规划")
		if !ok || keepInfo.Disabled {
			t.Fatalf("Keep planning is not enabled with feedback; info = %+v ok=%v", keepInfo, ok)
		}
		tester.Key(0, ui.KeyEnter)
		tester.Frame()
		if len(state.kept) != 1 || state.kept[0] != "请补充回滚步骤" {
			t.Fatalf("keep-planning feedback = %v, want [请补充回滚步骤]", state.kept)
		}
		if state.approve != 0 || state.cancel != 0 {
			t.Fatalf("Enter with feedback approved/cancelled: approve=%d cancel=%d", state.approve, state.cancel)
		}
	})
	t.Run("action click with feedback", func(t *testing.T) {
		feedback := ""
		state, tester := planReviewHarness(t, &feedback, "")
		planReviewFocus(t, tester, "继续规划反馈")
		tester.Type("改一下")
		tester.Frame()
		if err := tester.Click("继续规划"); err != nil {
			t.Fatalf("click Keep planning: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if len(state.kept) != 1 || state.kept[0] != "改一下" {
			t.Fatalf("keep-planning feedback = %v, want [改一下]", state.kept)
		}
	})
	t.Run("empty feedback decides nothing", func(t *testing.T) {
		feedback := ""
		state, tester := planReviewHarness(t, &feedback, "")
		// Clicking the disabled action is a no-op.
		if err := tester.Click("继续规划"); err != nil {
			t.Fatalf("click disabled Keep planning: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if len(state.kept) != 0 || state.approve != 0 || state.cancel != 0 {
			t.Fatalf("empty feedback fired callbacks: keep=%v approve=%d cancel=%d",
				state.kept, state.approve, state.cancel)
		}
	})
}

// TestPlanReviewDialogEscapeDoesNotCancel covers the central S5 contract:
// Escape never cancels a plan. It reopens the dialog with a visible one-shot
// WarningText RoleAlert instead of looking dead.
func TestPlanReviewDialogEscapeDoesNotCancel(t *testing.T) {
	feedback := ""
	state, tester := planReviewHarness(t, &feedback, "")
	const warning = "Esc 不能关闭计划审阅：请点击「批准」或「取消计划」，或输入反馈继续规划。"
	if tester.HasText(warning) {
		t.Fatalf("the Escape warning is visible before Escape; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyEscape)

	if state.cancel != 0 || state.approve != 0 || len(state.kept) != 0 || state.dismiss != 0 {
		t.Fatalf("Escape fired callbacks: cancel=%d approve=%d keep=%v dismiss=%d",
			state.cancel, state.approve, state.kept, state.dismiss)
	}
	if !tester.HasText("计划待审阅") {
		t.Fatalf("Escape closed the plan review; texts = %v", tester.Texts())
	}
	if !tester.HasText(warning) {
		t.Fatalf("Escape gave no visible feedback; texts = %v", tester.Texts())
	}
	// The warning is an alert live region, not only colored text.
	if info, ok := tester.AccessByRole(ui.RoleAlert, ""); !ok || !strings.Contains(info.Name, "Esc 不能关闭计划审阅") {
		t.Fatalf("the Escape warning is not a RoleAlert live region: info=%+v ok=%v", info, ok)
	}

	// A second Escape is still not a cancel.
	tester.Key(0, ui.KeyEscape)
	if state.cancel != 0 || state.dismiss != 0 {
		t.Fatalf("a repeated Escape cancelled the plan: cancel=%d dismiss=%d", state.cancel, state.dismiss)
	}
	if !tester.HasText("计划待审阅") {
		t.Fatalf("a repeated Escape closed the plan review; texts = %v", tester.Texts())
	}
}

// TestPlanReviewDialogErrorClosesOnEscape covers the failure escape hatch: a
// failed decision stays visible, and Escape then closes the review instead of
// trapping the window.
func TestPlanReviewDialogErrorClosesOnEscape(t *testing.T) {
	state, tester := planReviewHarness(t, nil, "批准失败：plan is busy")
	if !tester.HasText("批准失败：plan is busy") {
		t.Fatalf("the error line is missing; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleAlert, ""); !ok {
		t.Fatalf("the error line is not a RoleAlert; texts = %v", tester.Texts())
	}
	if !tester.HasText("Esc = 关闭审阅（计划未批准）") {
		t.Fatalf("the error-state Escape hint is missing; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyEscape)

	if state.dismiss != 1 {
		t.Fatalf("dismissals after Escape on a failed review = %d, want 1", state.dismiss)
	}
	if state.cancel != 0 || state.approve != 0 {
		t.Fatalf("Escape on a failed review decided the plan: cancel=%d approve=%d", state.cancel, state.approve)
	}
	if tester.HasText("计划待审阅") {
		t.Fatalf("the failed review was not closed by Escape; texts = %v", tester.Texts())
	}
}

// TestPlanReviewDialogWithoutChatHidesKeepPlanning covers the degraded host: no
// feedback pointer (no chat bridge) hides the feedback loop and keeps the two
// real decisions.
func TestPlanReviewDialogWithoutChatHidesKeepPlanning(t *testing.T) {
	_, tester := planReviewHarness(t, nil, "")
	if tester.HasText("继续规划") || tester.HasText("继续规划反馈") {
		t.Fatalf("keep-planning controls rendered without a chat bridge; texts = %v", tester.Texts())
	}
	if !tester.HasText("批准") || !tester.HasText("取消计划") {
		t.Fatalf("approve/cancel missing; texts = %v", tester.Texts())
	}
	if !tester.HasText("点击「批准」批准计划 · Esc 不会取消计划") {
		t.Fatalf("the no-feedback hint is missing; texts = %v", tester.Texts())
	}
}

// TestPlanReviewDialogEnglishCopy covers the localized copy of the component.
func TestPlanReviewDialogEnglishCopy(t *testing.T) {
	feedback := ""
	state := &planReviewTestState{}
	tester := ui.NewTester(planReviewTestView(state, &feedback,
		contracts.PlanReview{Content: "Plan body", State: "reviewing"}, "", &entryDialogTestLocale{lang: "en"}), 1200, 800)
	tester.Frame()
	for _, want := range []string{
		"Plan review", "Plan body", "Keep-planning feedback",
		"Cancel plan", "Keep planning", "Approve",
	} {
		if !tester.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}
