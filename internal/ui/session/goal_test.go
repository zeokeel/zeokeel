package session_test

// Session-header goal chip (uiroot goal bridge): the compact chip, its popover
// action menu, the native clear confirmation, i18n and the
// optional-backend behavior.

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newGoalHarness activates the session module with a real uiroot GoalTracker.
func newGoalHarness(t *testing.T, chat *fakeChat) (*harness, *uiroot.Root) {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide(t, ctx, contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker()))
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}, uiRoot
}

func TestGoalChipShowsAndClears(t *testing.T) {
	h, uiRoot := newGoalHarness(t, newFakeChat())
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "目标", "删除目标记录")

	if _, err := uiRoot.GoalTracker().Set(context.Background(), "  Ship release  "); err != nil {
		t.Fatalf("Set goal: %v", err)
	}
	tester.Frame()
	// The header keeps one compact chip: the status label plus the truncated
	// goal text (the full text is the chip's tooltip).
	h.assertTexts(tester, "目标", "Ship release")

	// A replacement goal updates the chip in place.
	if _, err := uiRoot.GoalTracker().Set(context.Background(), "Ship v2"); err != nil {
		t.Fatalf("Set second goal: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "目标", "Ship v2")

	// The chip's popover carries the clear action, which confirms first.
	openGoalActions(t, h, tester, "目标")
	h.assertTexts(tester, "删除目标记录")
	h.click(tester, "删除目标记录")
	h.assertTexts(tester, "删除目标记录", "取消", "确认删除")
	h.click(tester, "确认删除")
	if _, _, _, ok := uiRoot.GoalTracker().Get(); ok {
		t.Fatal("goal survived the confirmed header delete action")
	}
	tester.Frame()
	h.assertNoTexts(tester, "目标", "删除目标记录")
}

func TestGoalChipInitializesFromExistingGoal(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, newFakeChat())
	provide(t, ctx, contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker()))
	if _, err := uiRoot.GoalTracker().Set(context.Background(), "Existing goal"); err != nil {
		t.Fatalf("Set goal: %v", err)
	}
	activate(t, ctx, session.Module(), session.Config{})
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	h := &harness{t: t, ctx: ctx, ui: uiRoot}
	h.assertTexts(tester, "目标", "Existing goal")
	h.click(tester, "目标")
	h.assertTexts(tester, "暂停目标", "完成目标", "结束目标（保留记录）", "删除目标记录")
}

func TestGoalChipEnglish(t *testing.T) {
	h, uiRoot := newGoalHarness(t, newFakeChat())
	uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	if _, err := uiRoot.GoalTracker().Set(context.Background(), "Ship release"); err != nil {
		t.Fatalf("Set goal: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Goal", "Ship release")
	h.assertNoTexts(tester, "目标")
	h.click(tester, "Goal")
	h.assertTexts(tester, "Delete goal record")
	h.click(tester, "Delete goal record")
	h.assertTexts(tester, "Cancel", "Delete")
	h.click(tester, "Delete")
	if _, _, _, ok := uiRoot.GoalTracker().Get(); ok {
		t.Fatal("goal survived the English clear action")
	}
	tester.Frame()
	h.assertNoTexts(tester, "Clear goal")
}

func TestGoalChipWithoutTracker(t *testing.T) {
	h := newHarness(t, newFakeChat())
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "目标", "删除目标记录", "Goal", "Delete goal record")
}
