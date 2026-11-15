package session_test

// Goal v2 /goal dialog and command-palette tests: creating a goal writes the
// active session, editing keeps the goal id/revision (and can raise the round
// cap), localized GoalService errors stay in the dialog, and the complete/clear
// commands act through the durable service.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func openGoalDialogFromDock(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	openGoalActions(t, h, tester, "")
	if tester.HasText("编辑目标") {
		h.click(tester, "编辑目标")
		return
	}
	if tester.HasText("Edit goal") {
		h.click(tester, "Edit goal")
		return
	}
	t.Fatalf("goal edit entry missing; texts = %v", tester.Texts())
}

func TestGoalDialogCreatesGoalForActiveSession(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	tester := h.tester()
	tester.Frame()

	h.typeSlash(tester)
	h.click(tester, "设置目标")
	h.assertTexts(tester, "设置目标", "目标内容", "保存", "取消")
	h.assertNoTexts(tester, "删除目标记录", "自动续跑上限（轮）")

	focusDialogInput(t, tester, "目标内容")
	tester.Type("Ship release")
	tester.Frame()
	h.click(tester, "保存")
	tester.Frame()

	creates := goals.creates()
	if len(creates) != 1 {
		t.Fatalf("CreateGoal calls = %+v, want one", creates)
	}
	if creates[0].sessionID != "sess-goal" || creates[0].objective != "Ship release" {
		t.Fatalf("CreateGoal call = %+v, want the active session and the typed objective", creates[0])
	}
	h.assertNoTexts(tester, "目标内容")
	h.assertTexts(tester, "目标执行中", "Ship release")
}

func TestGoalDialogEmptySaveDisabled(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	tester := h.tester()
	tester.Frame()

	h.typeSlash(tester)
	h.click(tester, "设置目标")
	h.click(tester, "保存")
	if creates := goals.creates(); len(creates) != 0 {
		t.Fatalf("empty goal was created: %+v", creates)
	}
	h.assertTexts(tester, "保存", "取消")
}

func TestGoalDialogEditsExistingGoalKeepingRef(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	seeded := goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	openGoalDialogFromDock(t, h, tester)
	h.assertTexts(tester, "设置目标", "自动续跑上限（轮）", "保存", "取消")
	if !tester.HasText("Ship release") {
		t.Fatalf("the dialog did not prefill the current objective; texts = %v", tester.Texts())
	}

	focusDialogInput(t, tester, "目标内容")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("Ship v2")
	tester.Frame()
	h.click(tester, "保存")
	tester.Frame()

	updates := goals.updates()
	if len(updates) != 1 {
		t.Fatalf("UpdateGoal calls = %+v, want one edit", updates)
	}
	update := updates[0]
	if update.update.Action != contracts.GoalActionEdit ||
		update.update.Objective != "Ship v2" {
		t.Fatalf("edit update = %+v, want an objective edit", update.update)
	}
	if update.ref.ID != seeded.ID || update.ref.Revision != seeded.Revision {
		t.Fatalf("edit ref = %+v, want the current id/revision %+v", update.ref, seeded)
	}
	if got := goals.wantGoal("sess-goal"); got.Objective != "Ship v2" || got.ID != seeded.ID {
		t.Fatalf("goal after edit = %+v, want the same id with the new objective", got)
	}
	h.assertTexts(tester, "Ship v2")
}

func TestGoalDialogRaisesCapThenResume(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seedBlocked("sess-goal", contracts.GoalBlockedCodeRoundLimit,
		"the goal reached its automatic continuation round limit (3/3)", 3, 3)
	tester := h.tester()
	tester.Frame()

	openGoalDialogFromDock(t, h, tester)
	focusDialogInput(t, tester, "自动续跑上限（轮）")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("10")
	tester.Frame()
	h.click(tester, "保存")
	tester.Frame()

	updates := goals.updates()
	if len(updates) != 1 || updates[0].update.Action != contracts.GoalActionEdit ||
		updates[0].update.MaxGoalRounds != 10 {
		t.Fatalf("cap edit = %+v, want MaxGoalRounds 10", updates)
	}
	if got := goals.wantGoal("sess-goal"); got.Phase != contracts.GoalPhaseBlocked || got.MaxGoalRounds != 10 {
		t.Fatalf("goal after cap edit = %+v, want still blocked with cap 10", got)
	}

	// The raised cap makes the human continue action valid again.
	h.click(tester, "继续目标")
	tester.Frame()
	if got := goals.wantGoal("sess-goal"); got.Phase != contracts.GoalPhaseActive ||
		got.Activation != contracts.GoalActivationArmed || got.MaxGoalRounds != 10 {
		t.Fatalf("goal after continue = %+v, want active+armed with cap 10", got)
	}
}

func TestGoalDialogConflictIsLocalized(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.mu.Lock()
	goals.createErr = contracts.ErrGoalConflict
	goals.mu.Unlock()
	tester := h.tester()
	tester.Frame()

	h.typeSlash(tester)
	h.click(tester, "设置目标")
	focusDialogInput(t, tester, "目标内容")
	tester.Type("Ship release")
	tester.Frame()
	h.click(tester, "保存")
	h.assertTexts(tester, "保存目标失败：当前会话已有未完成目标，请先编辑或完成它。")
	h.assertTexts(tester, "保存", "取消")
}

func TestGoalDialogStaleRefIsLocalizedAndRefreshes(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	openGoalDialogFromDock(t, h, tester)
	// Another human/tool change lands between opening and saving: the answer's
	// revision is stale and must be rejected, not overwrite the newer state.
	goals.mu.Lock()
	goals.updateErr = contracts.ErrGoalStaleRef
	goals.mu.Unlock()
	h.click(tester, "保存")
	h.assertTexts(tester, "保存目标失败：目标已被其他操作更新，请重试。")
}

func TestGoalDialogDeleteRecordOpensClearConfirmation(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	openGoalDialogFromDock(t, h, tester)
	h.click(tester, "删除目标记录")
	h.assertNoTexts(tester, "目标内容")
	h.assertTexts(tester, "清除目标", "取消", "确认清除")
	h.click(tester, "确认清除")
	tester.Frame()
	if _, ok := goals.Goal("sess-goal"); ok {
		t.Fatal("the goal survived the dialog's confirmed delete")
	}
}

func TestGoalCommandsRegisteredAndGated(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	for _, want := range []struct{ id, title, hint string }{
		{"zeokeel.session.goal", "设置目标", "为当前会话创建或编辑长期目标"},
		{"zeokeel.session.goal.complete", "完成目标", "将当前目标标记为已完成"},
		{"zeokeel.session.goal.clear", "清除目标", "删除当前目标及其记录（需确认）"},
	} {
		command := findSessionCommand(t, h, want.id)
		if command.Title != want.title || command.Hint != want.hint {
			t.Fatalf("%s = %q/%q, want %q/%q", want.id, command.Title, command.Hint, want.title, want.hint)
		}
		if command.Enabled == nil || !command.Enabled() {
			t.Fatalf("%s is disabled with an active goal", want.id)
		}
	}
	count := 0
	for _, command := range h.ui.Runtime().Commands() {
		if len(command.ID) > len("zeokeel.session.") && command.ID[:len("zeokeel.session.")] == "zeokeel.session." {
			count++
		}
	}
	if count != 14 {
		t.Fatalf("session commands = %d, want 14", count)
	}

	// A complete goal disables complete; clear stays available; a product
	// without the goal backend disables the whole family.
	complete := findSessionCommand(t, h, "zeokeel.session.goal.complete")
	if err := complete.Run(); err != nil {
		t.Fatalf("complete Run: %v", err)
	}
	tester.Frame()
	if got := goals.wantGoal("sess-goal"); got.Phase != contracts.GoalPhaseComplete {
		t.Fatalf("phase after the complete command = %q, want complete", got.Phase)
	}
	if command := findSessionCommand(t, h, "zeokeel.session.goal.complete"); command.Enabled != nil && command.Enabled() {
		t.Fatal("complete command enabled for a complete goal")
	}
	h.assertTexts(tester, "目标已完成")

	bare := newHarness(t, goalChat())
	bare.tester().Frame()
	for _, id := range []string{"zeokeel.session.goal", "zeokeel.session.goal.complete", "zeokeel.session.goal.clear"} {
		command := findSessionCommand(t, bare, id)
		if command.Enabled != nil && command.Enabled() {
			t.Fatalf("%s enabled without the goal backend", id)
		}
	}
}

func TestGoalClearCommandOpensConfirmation(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	command := findSessionCommand(t, h, "zeokeel.session.goal.clear")
	if err := command.Run(); err != nil {
		t.Fatalf("clear Run: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "清除目标", "取消", "确认清除")
	h.click(tester, "确认清除")
	tester.Frame()
	if _, ok := goals.Goal("sess-goal"); ok {
		t.Fatal("the clear command left the goal in place")
	}
}

func TestGoalDialogEnglishCopy(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()

	openGoalActions(t, h, tester, "Goal running")
	h.click(tester, "Edit goal")
	h.assertTexts(tester, "Goal", "Goal text", "Round cap", "Save", "Cancel", "Delete goal record")
}
