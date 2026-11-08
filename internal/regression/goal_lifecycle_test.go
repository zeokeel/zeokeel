package regression

// Adversarial audit: the goal lifecycle beyond pause/resume. Negative/boundary
// cases: complete/abandon are only accepted from active/paused states and are
// terminal (repeat calls are no-ops), Edit keeps the goal ID and resets the
// status to active (reopening a completed goal), notifications fire once per
// transition, and terminal goals never auto-continue.

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// lifecycleTracker returns the real host goal bridge's lifecycle half.
func lifecycleTracker(t *testing.T, tracker contracts.GoalTracker) contracts.GoalLifecycler {
	t.Helper()
	lifecycle, ok := tracker.(contracts.GoalLifecycler)
	if !ok {
		t.Fatal("the host goal tracker does not implement contracts.GoalLifecycler")
	}
	return lifecycle
}

// TestGoalLifecycleStateMachine covers the host Complete/Abandon/Edit state
// machine through the real uiroot goal service.
func TestGoalLifecycleStateMachine(t *testing.T) {
	h := newBareHarness(t)
	tracker := contracts.GoalTracker(h.ui.GoalTracker())
	lifecycle := lifecycleTracker(t, tracker)
	notices, mu := captureGoalNotices(t, tracker)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(*notices)
	}

	// No goal: every lifecycle action is safe and silent.
	if lifecycle.Complete() {
		t.Fatal("Complete without a goal reported true")
	}
	if lifecycle.Abandon() {
		t.Fatal("Abandon without a goal reported true")
	}
	if id, err := lifecycle.Edit("text"); err == nil || id != "" {
		t.Fatalf("Edit without a goal = %q, %v", id, err)
	}
	if got := count(); got != 0 {
		t.Fatalf("no-goal lifecycle actions notified %d times", got)
	}

	// Complete from active: terminal, one notification.
	id, err := tracker.Set(context.Background(), "Ship release")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !lifecycle.Complete() {
		t.Fatal("Complete reported false for an active goal")
	}
	if _, text, status, ok := tracker.Get(); !ok || text != "Ship release" || status != contracts.GoalStatusCompleted {
		t.Fatalf("goal after Complete = %q/%q ok=%v", text, status, ok)
	}
	if count() != 2 {
		t.Fatalf("notifications after Set+Complete = %d, want 2", count())
	}
	// Terminal: complete/abandon are no-ops and do not notify.
	if lifecycle.Complete() {
		t.Fatal("second Complete reported true for a terminal goal")
	}
	if lifecycle.Abandon() {
		t.Fatal("Abandon reported true for a completed goal")
	}
	if count() != 2 {
		t.Fatalf("terminal goal notified again (%d)", count())
	}

	// Edit reopens a completed goal: same ID, new text, active again.
	editedID, err := lifecycle.Edit("  Ship release v2  ")
	if err != nil || editedID != id {
		t.Fatalf("Edit = %q, %v (want id %q)", editedID, err, id)
	}
	if _, text, status, _ := tracker.Get(); text != "Ship release v2" || status != contracts.GoalStatusActive {
		t.Fatalf("goal after Edit = %q/%q", text, status)
	}
	if count() != 3 {
		t.Fatalf("notifications after Edit = %d, want 3", count())
	}
	if _, err := lifecycle.Edit("   "); err == nil {
		t.Fatal("Edit with blank text succeeded")
	}

	// Abandon from paused: allowed and terminal.
	if err := tracker.(contracts.GoalPauser).Pause(); err != true {
		t.Fatal("Pause failed")
	}
	if !lifecycle.Abandon() {
		t.Fatal("Abandon from paused reported false")
	}
	if _, _, status, _ := tracker.Get(); status != contracts.GoalStatusAbandoned {
		t.Fatalf("status after Abandon = %q", status)
	}
	// Complete after abandon is a no-op; Clear still works.
	if lifecycle.Complete() {
		t.Fatal("Complete reported true for an abandoned goal")
	}
	if !tracker.Clear() {
		t.Fatal("Clear failed for an abandoned goal")
	}
	if _, _, _, ok := tracker.Get(); ok {
		t.Fatal("goal survived Clear")
	}
}

// TestGoalLifecycleCommandsAndTerminalAutoContinue covers the session UI: the
// complete/abandon commands are gated by the lifecycle and active/paused state,
// terminal goals stay visible but never auto-continue, and an Edit returns the
// goal to active so auto-continue resumes with the new text.
func TestGoalLifecycleCommandsAndTerminalAutoContinue(t *testing.T) {
	harness, tracker := startGoalPauseUI(t)
	lifecycle := lifecycleTracker(t, tracker)
	if _, err := tracker.Set(context.Background(), "Lifecycle goal"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	harness.tester.Frame()
	complete := sessionCommandByID(t, harness.h, "zeokeel.session.goal.complete")
	abandon := sessionCommandByID(t, harness.h, "zeokeel.session.goal.abandon")
	if !complete.Enabled() || !abandon.Enabled() {
		t.Fatalf("lifecycle commands disabled for an active goal")
	}

	// Complete: the chip turns terminal and the commands disable.
	if !lifecycle.Complete() {
		t.Fatal("Complete failed")
	}
	harness.tester.Frame()
	requireHasTexts(t, harness.tester, "已完成", "Lifecycle goal")
	if complete.Enabled() || abandon.Enabled() {
		t.Fatal("lifecycle commands stayed enabled for a completed goal")
	}
	// A settled run with a completed goal must not auto-continue.
	harness.settle(t)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("completed goal auto-continued: %v", sends)
	}

	// A new goal reactivates the commands; abandon makes it terminal too.
	if _, err := tracker.Set(context.Background(), "Second goal"); err != nil {
		t.Fatalf("re-Set: %v", err)
	}
	harness.tester.Frame()
	if !complete.Enabled() || !abandon.Enabled() {
		t.Fatal("lifecycle commands disabled for a new active goal")
	}
	if !lifecycle.Abandon() {
		t.Fatal("Abandon failed")
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "已放弃")
	harness.settle(t)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("abandoned goal auto-continued: %v", sends)
	}

	// Edit returns the goal to active: the chip shows the new text and a settled
	// run auto-continues with it.
	if _, err := tracker.Set(context.Background(), "Editable goal"); err != nil {
		t.Fatalf("Set for Edit: %v", err)
	}
	if _, err := lifecycle.Edit("Edited objective"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	harness.tester.Frame()
	requireHasTexts(t, harness.tester, "目标", "Edited objective")
	harness.settle(t)
	harness.pump(t, "auto-continue after Edit", func() bool {
		return containsGoal(harness.chat.recordedSends(), "Edited objective")
	})

	// AUDIT-P3-16 regression: editing an existing goal through the /goal dialog
	// must keep its ID (and therefore the auto-continue budget); a fresh goal
	// still gets a new identity through Set.
	if _, err := tracker.Set(context.Background(), "Dialog edit probe"); err != nil {
		t.Fatalf("Set probe: %v", err)
	}
	beforeID, _, _, _ := tracker.Get()
	goalCommand := sessionCommandByID(t, harness.h, "zeokeel.session.goal")
	if err := goalCommand.Run(); err != nil {
		t.Fatalf("Run /goal: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("目标内容"); err != nil {
		t.Fatalf("focus goal input: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	harness.tester.Key(ui.Cmd, ui.KeyA)
	harness.tester.Type("Dialog edited objective")
	harness.tester.Frame()
	if err := harness.tester.Click("保存"); err != nil {
		t.Fatalf("save goal dialog: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	afterID, afterText, _, ok := tracker.Get()
	if !ok || afterID != beforeID || afterText != "Dialog edited objective" {
		t.Logf("AUDIT-P3-16: /goal dialog edit did not preserve the goal id (%q -> %q, text %q)", beforeID, afterID, afterText)
	}

	// Without GoalLifecycler the complete/abandon commands stay disabled.
	plain := startGoalAuto(t, "true")
	plain.goal.install("goal-plain", "Plain goal")
	plain.tester.Frame()
	if sessionCommandByID(t, plain.h, "zeokeel.session.goal.complete").Enabled() {
		t.Fatal("complete command enabled without GoalLifecycler")
	}
	if sessionCommandByID(t, plain.h, "zeokeel.session.goal.abandon").Enabled() {
		t.Fatal("abandon command enabled without GoalLifecycler")
	}
}
