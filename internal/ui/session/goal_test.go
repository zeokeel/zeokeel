package session_test

// Goal v2 composer-dock tests: the dock shows the ACTIVE conversation's phase,
// objective, automatic-continuation round budget, blocked reason and activation;
// the human actions (pause/resume/complete/edit/clear) call the durable
// GoalService directly; a conversation switch never shows another goal; and the
// transcript renders engine-injected continuation rounds as goal rounds instead
// of human bubbles.

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestGoalDockShowsActiveGoalRoundsAndActivation(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seedGoal("sess-goal", "Ship release", contracts.GoalPhaseActive,
		contracts.GoalActivationArmed, 3, contracts.DefaultMaxGoalRounds)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "目标进行中", "Ship release", "3/256 轮", "目标操作")
	h.assertNoTexts(tester, "继续目标", "等待继续")

	openGoalActions(t, h, tester, "目标进行中")
	h.assertTexts(tester, "暂停目标", "完成目标", "编辑目标", "清除目标记录")
	h.assertNoTexts(tester, "继续目标")
}

func TestGoalDockDisarmedOffersContinue(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seedGoal("sess-goal", "Ship release", contracts.GoalPhaseActive,
		contracts.GoalActivationDisarmed, 2, contracts.DefaultMaxGoalRounds)
	tester := h.tester()
	tester.Frame()

	// A restart (or a user Stop without a persisted pause) leaves the goal
	// active but disarmed: the dock says it waits for the human and offers the
	// one-click resume.
	h.assertTexts(tester, "等待继续", "Ship release", "继续目标")
	h.click(tester, "继续目标")
	tester.Frame()

	if got := goals.wantGoal("sess-goal"); got.Activation != contracts.GoalActivationArmed {
		t.Fatalf("activation after continue = %q, want armed", got.Activation)
	}
	h.assertTexts(tester, "目标进行中")
	h.assertNoTexts(tester, "等待继续")
}

func TestGoalDockPausedResume(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seedGoal("sess-goal", "Ship release", contracts.GoalPhasePaused,
		contracts.GoalActivationDisarmed, 1, contracts.DefaultMaxGoalRounds)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "目标已暂停", "继续目标")
	openGoalActions(t, h, tester, "目标已暂停")
	h.assertTexts(tester, "继续目标", "编辑目标", "清除目标记录")
	h.assertNoTexts(tester, "暂停目标")

	h.click(tester, "继续目标")
	tester.Frame()
	if got := goals.wantGoal("sess-goal"); got.Phase != contracts.GoalPhaseActive ||
		got.Activation != contracts.GoalActivationArmed {
		t.Fatalf("goal after continue = %+v, want active+armed", got)
	}
	h.assertTexts(tester, "目标进行中")
}

func TestGoalDockPauseAndComplete(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	openGoalActions(t, h, tester, "目标进行中")
	h.click(tester, "暂停目标")
	tester.Frame()
	if got := goals.wantGoal("sess-goal"); got.Phase != contracts.GoalPhasePaused {
		t.Fatalf("phase after pause = %q, want paused", got.Phase)
	}
	h.assertTexts(tester, "目标已暂停")

	openGoalActions(t, h, tester, "目标已暂停")
	h.click(tester, "完成目标")
	tester.Frame()
	if got := goals.wantGoal("sess-goal"); got.Phase != contracts.GoalPhaseComplete {
		t.Fatalf("phase after complete = %q, want complete", got.Phase)
	}
	h.assertTexts(tester, "目标已完成")
	openGoalActions(t, h, tester, "目标已完成")
	h.assertTexts(tester, "编辑目标", "清除目标记录")
	h.assertNoTexts(tester, "暂停目标", "完成目标", "继续目标")
}

func TestGoalDockBlockedShowsReasonAndResume(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seedBlocked("sess-goal", contracts.GoalBlockedCodeModelReported,
		"waiting on credentials", 2, contracts.DefaultMaxGoalRounds)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "目标受阻", "受阻原因：waiting on credentials", "继续目标")
	h.click(tester, "继续目标")
	tester.Frame()
	got := goals.wantGoal("sess-goal")
	if got.Phase != contracts.GoalPhaseActive || got.BlockedReason != nil {
		t.Fatalf("goal after continue = %+v, want active without a blocked reason", got)
	}
}

func TestGoalDockRoundLimitBlockedPointsAtTheCap(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seedBlocked("sess-goal", contracts.GoalBlockedCodeRoundLimit,
		"the goal reached its automatic continuation round limit (3/3)", 3, 3)
	tester := h.tester()
	tester.Frame()

	// The reason is actionable (raise the cap) instead of the backend's English
	// diagnostic.
	h.assertTexts(tester, "目标受阻", "自动续跑已达轮次上限（3/3）：在「编辑目标」中调高上限后可继续。")

	openGoalActions(t, h, tester, "目标受阻")
	h.assertTexts(tester, "轮次已用尽")
	// The disabled menu entry cannot resume an exhausted goal: the service never
	// sees an UpdateGoal call.
	h.click(tester, "继续目标")
	if updates := goals.updates(); len(updates) != 0 {
		t.Fatalf("resuming an exhausted goal reached the service: %+v", updates)
	}
}

func TestGoalDockClearConfirmsAndDeletes(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	openGoalActions(t, h, tester, "目标进行中")
	h.click(tester, "清除目标记录")
	h.assertTexts(tester, "清除目标", "取消", "确认清除")
	h.click(tester, "确认清除")
	tester.Frame()

	if got := goals.clears(); len(got) != 1 || got[0].ID == "" {
		t.Fatalf("ClearGoal calls = %+v, want one compare-and-set clear", got)
	}
	if _, ok := goals.Goal("sess-goal"); ok {
		t.Fatal("the goal survived the confirmed clear")
	}
	h.assertNoTexts(tester, "目标进行中", "Ship release", "清除目标记录")
}

func TestGoalDockIsPerSession(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-a", Title: "A"}, true)
	chat.addSession(contracts.ChatSession{ID: "sess-b", Title: "B"}, false)
	h, goals := newGoalHarness(t, chat)
	goals.seed("sess-a", "Goal A")
	goals.seed("sess-b", "Goal B")
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "Goal A")
	h.assertNoTexts(tester, "Goal B")

	if err := chat.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("switch to session B: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Goal B")
	h.assertNoTexts(tester, "Goal A")

	// Clearing A while B is visible must not blank B's dock (the UI never keeps
	// two goals around).
	a := goals.wantGoal("sess-a")
	if _, err := goals.ClearGoal(context.Background(), "sess-a",
		contracts.GoalRef{ID: a.ID, Revision: a.Revision}); err != nil {
		t.Fatalf("clear session A: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Goal B")
	h.assertNoTexts(tester, "Goal A")
}

func TestGoalDockInitializesFromExistingGoal(t *testing.T) {
	goals := newFakeGoalService()
	goals.seed("sess-goal", "Existing goal")
	h := newGoalHarnessSeeded(t, goalChat(), goals)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "目标进行中", "Existing goal")
}

func TestGoalDockEnglish(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	h.ui.Locale().SetLanguage("en")
	goals.seedGoal("sess-goal", "Ship release", contracts.GoalPhaseActive,
		contracts.GoalActivationArmed, 1, contracts.DefaultMaxGoalRounds)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "Goal active", "Ship release", "1/256 rounds")
	openGoalActions(t, h, tester, "Goal active")
	h.assertTexts(tester, "Pause goal", "Complete goal", "Edit goal", "Delete goal record")
}

func TestGoalDockWithoutGoalService(t *testing.T) {
	h := newHarness(t, goalChat())
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "目标操作", "Goal actions", "目标进行中", "Goal active")
}

func TestGoalDockPauseFailureIsLocalizedDanger(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	goals.mu.Lock()
	goals.updateErr = errors.New("goal store down")
	goals.mu.Unlock()
	tester := h.tester()
	tester.Frame()

	openGoalActions(t, h, tester, "目标进行中")
	h.click(tester, "暂停目标")
	tester.Frame()
	h.assertTexts(tester, "暂停目标失败：goal store down")
}

func TestGoalRoundMessageIsNotAUserBubble(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-goal", Title: "ship it",
		Messages: []contracts.ChatMessage{
			{
				ID:   "m-round",
				Role: contracts.ChatRoleUser,
				Text: "Continue working toward the active goal.\n\nGoal: Ship release\nRound 2/256",
				GoalRound: &contracts.GoalRoundInfo{
					GoalID: "goal-1", Round: 2, MaxGoalRounds: 256,
				},
			},
			{ID: "m-answer", Role: contracts.ChatRoleAssistant, Text: "Working on it."},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "目标续跑", "第 2/256 轮")
	// The synthetic round is not a human message: it has no user-message actions
	// and never renders the user's role metadata.
	h.assertNoTexts(tester, "用户消息操作")
}

// newGoalHarnessSeeded builds a goal harness around a pre-seeded fake service,
// so the first frame exercises the initialization path.
func newGoalHarnessSeeded(t *testing.T, chat *fakeChat, goals *fakeGoalService) *harness {
	t.Helper()
	h, _ := newGoalHarnessWithService(t, chat, goals)
	return h
}
