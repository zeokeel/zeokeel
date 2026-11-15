package regression

// Goal v2 adversarial UI audit: the dock only ever shows the active
// conversation's goal (no cross-session leakage), every action goes through the
// durable GoalService with its compare-and-set revision, a stale revision is
// rejected instead of overwriting, clearing really removes the record, and the
// settled-run continuation that used to live in this UI module is gone (the
// engine drives it now; the UI must never send a continuation prompt).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func auditSession(id, title string) contracts.ChatSession {
	return contracts.ChatSession{
		ID: id, Title: title, CreatedAt: time.Unix(1_700_000_000, 0),
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "done"}},
	}
}

func TestGoalDockIsPerSession(t *testing.T) {
	goals := newRegressionGoalService()
	goals.seed("sess-a", contracts.GoalPhaseActive, contracts.GoalActivationArmed, 2, 0)
	goals.seed("sess-b", contracts.GoalPhasePaused, contracts.GoalActivationDisarmed, 0, 0)
	ui := startGoalUI(t, goals, auditSession("sess-a", "A"), auditSession("sess-b", "B"))

	requireHasText(t, ui.tester, "目标执行中")
	requireHasText(t, ui.tester, "2/256 轮")
	requireNoText(t, ui.tester, "目标已暂停")

	if err := ui.chat.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("switch to session B: %v", err)
	}
	ui.tester.Frame()
	requireHasText(t, ui.tester, "目标已暂停")
	requireHasText(t, ui.tester, "继续目标")
	requireNoText(t, ui.tester, "目标执行中")
}

func TestGoalDockActionsGoThroughGoalService(t *testing.T) {
	goals := newRegressionGoalService()
	goals.seed("sess-a", contracts.GoalPhaseActive, contracts.GoalActivationArmed, 0, 0)
	ui := startGoalUI(t, goals, auditSession("sess-a", "A"))

	if err := ui.tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal menu: %v; texts = %v", err, ui.tester.Texts())
	}
	ui.tester.Frame()
	if err := ui.tester.Click("暂停目标"); err != nil {
		t.Fatalf("pause goal: %v", err)
	}
	ui.tester.Frame()
	if goal, ok := goals.goal("sess-a"); !ok || goal.Phase != contracts.GoalPhasePaused {
		t.Fatalf("goal after pause = %+v (ok=%v), want paused", goal, ok)
	}
	requireHasText(t, ui.tester, "目标已暂停")

	if err := ui.tester.Click("继续目标"); err != nil {
		t.Fatalf("continue goal: %v", err)
	}
	ui.tester.Frame()
	if goal, ok := goals.goal("sess-a"); !ok || goal.Phase != contracts.GoalPhaseActive ||
		goal.Activation != contracts.GoalActivationArmed {
		t.Fatalf("goal after continue = %+v (ok=%v), want active+armed", goal, ok)
	}

	if err := ui.tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal menu: %v", err)
	}
	ui.tester.Frame()
	updatesBefore := len(goals.updateCalls())
	if err := ui.tester.Click("完成目标"); err != nil {
		t.Fatalf("complete goal: %v", err)
	}
	ui.tester.Frame()
	if goal, ok := goals.goal("sess-a"); !ok || goal.Phase != contracts.GoalPhaseComplete {
		t.Fatalf("goal after complete = %+v (ok=%v), want complete", goal, ok)
	}
	if updates := goals.updateCalls(); len(updates) <= updatesBefore ||
		updates[len(updates)-1].Action != contracts.GoalActionComplete {
		t.Fatalf("last goal update = %+v, want complete", goals.updateCalls())
	}
	requireHasText(t, ui.tester, "目标已完成")
}

func TestGoalStaleRevisionIsRejected(t *testing.T) {
	goals := newRegressionGoalService()
	goals.seed("sess-a", contracts.GoalPhaseActive, contracts.GoalActivationArmed, 0, 0)
	ui := startGoalUI(t, goals, auditSession("sess-a", "A"))

	// Another writer landed first: the UI's cached compare-and-set pair no
	// longer matches, so the service rejects the pause instead of overwriting.
	goals.mu.Lock()
	goals.updateErr = contracts.ErrGoalStaleRef
	goals.mu.Unlock()
	if err := ui.tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal menu: %v", err)
	}
	ui.tester.Frame()
	if err := ui.tester.Click("暂停目标"); err != nil {
		t.Fatalf("pause goal: %v", err)
	}
	ui.tester.Frame()
	requireHasText(t, ui.tester, "目标已被其他操作更新，请重试")
	if goal, _ := goals.goal("sess-a"); goal.Phase != contracts.GoalPhaseActive {
		t.Fatalf("stale pause changed the goal to %q", goal.Phase)
	}
}

func TestGoalClearRemovesRecordAndAllowsNewGoal(t *testing.T) {
	goals := newRegressionGoalService()
	seeded := goals.seed("sess-a", contracts.GoalPhaseActive, contracts.GoalActivationArmed, 0, 0)
	ui := startGoalUI(t, goals, auditSession("sess-a", "A"))

	if err := ui.tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal menu: %v", err)
	}
	ui.tester.Frame()
	if err := ui.tester.Click("清除目标记录"); err != nil {
		t.Fatalf("clear goal: %v", err)
	}
	ui.tester.Frame()
	requireHasText(t, ui.tester, "确认清除")
	if err := ui.tester.Click("确认清除"); err != nil {
		t.Fatalf("confirm clear: %v", err)
	}
	ui.tester.Frame()
	if goals.clearCalls() != 1 {
		t.Fatalf("ClearGoal calls = %d, want 1", goals.clearCalls())
	}
	if _, ok := goals.goal("sess-a"); ok {
		t.Fatal("the goal survived the confirmed clear")
	}
	requireNoText(t, ui.tester, "ship the audit")

	// A cleared goal can be created again. The dialog opens as create because no
	// goal exists, and the new instance gets a fresh identity.
	created, err := goals.CreateGoal(context.Background(), "sess-a", "new objective", 0)
	if err != nil {
		t.Fatalf("CreateGoal after clear: %v", err)
	}
	if created.ID == seeded.ID || created.Revision != 1 {
		t.Fatalf("new goal = %+v, want a new id at revision 1", created)
	}
	ui.tester.Frame()
	requireHasText(t, ui.tester, "new objective")
}

func TestSettledRunNeverSendsAContinuationPrompt(t *testing.T) {
	goals := newRegressionGoalService()
	goals.seed("sess-a", contracts.GoalPhaseActive, contracts.GoalActivationArmed, 0, 0)
	ui := startGoalUI(t, goals, auditSession("sess-a", "A"))

	// Running -> idle on the active session, the exact transition the retired
	// UI auto-continue observed. The agent backend owns the DSH goal rounds now;
	// the session module must not send anything.
	running := auditSession("sess-a", "A")
	running.Running = true
	ui.chat.updateSession(running)
	ui.tester.Frame()
	ui.chat.updateSession(auditSession("sess-a", "A"))
	ui.tester.Frame()
	ui.tester.Frame()

	for _, send := range ui.chat.recordedSends() {
		if strings.Contains(send, "继续推进目标") || strings.Contains(send, "Continue working toward the goal") {
			t.Fatalf("the session UI sent a goal continuation prompt: %q", send)
		}
	}
	if goals.admitRounds != 0 {
		t.Fatalf("the session UI admitted goal rounds: %d", goals.admitRounds)
	}
}
