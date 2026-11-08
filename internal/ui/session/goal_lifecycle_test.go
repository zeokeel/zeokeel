package session_test

// TDD tests (RED first) for the goal lifecycle UI: complete/abandon actions on
// the header chip, the two slash commands and auto-continue suppression.

import (
	"errors"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// lifecycleTracker is a GoalTracker + GoalPauser + GoalLifecycler fake.
type lifecycleTracker struct {
	*pauseTracker

	mu            sync.Mutex
	completeCalls int
	abandonCalls  int
	editCalls     int
	lifecycleFail bool
}

func newLifecycleTracker() *lifecycleTracker {
	return &lifecycleTracker{pauseTracker: &pauseTracker{}}
}

// transition moves an active/paused goal to status, notifying the handler.
func (l *lifecycleTracker) transition(status string) bool {
	l.pauseTracker.mu.Lock()
	had := l.pauseTracker.id != ""
	fail := l.lifecycleFail
	var (
		handler func(contracts.Goal, bool)
		goal    contracts.Goal
	)
	if had && !fail {
		l.pauseTracker.status = status
		handler = l.pauseTracker.handler
		goal = contracts.Goal{ID: l.pauseTracker.id, Text: l.pauseTracker.text, Status: status}
	}
	l.pauseTracker.mu.Unlock()
	if had && !fail && handler != nil {
		handler(goal, true)
	}
	return had && !fail
}

func (l *lifecycleTracker) Complete() bool {
	l.mu.Lock()
	l.completeCalls++
	l.mu.Unlock()
	return l.transition(contracts.GoalStatusCompleted)
}

func (l *lifecycleTracker) Abandon() bool {
	l.mu.Lock()
	l.abandonCalls++
	l.mu.Unlock()
	return l.transition(contracts.GoalStatusAbandoned)
}

func (l *lifecycleTracker) Edit(text string) (string, error) {
	l.mu.Lock()
	l.editCalls++
	l.mu.Unlock()
	l.pauseTracker.mu.Lock()
	id := l.pauseTracker.id
	l.pauseTracker.mu.Unlock()
	if id == "" {
		return "", errors.New("no goal")
	}
	l.pauseTracker.setGoal(id, text, contracts.GoalStatusActive)
	return id, nil
}

func (l *lifecycleTracker) lifecycleCalls() (complete, abandon, edit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.completeCalls, l.abandonCalls, l.editCalls
}

// --- header chip ------------------------------------------------------------

func TestGoalChipLifecycleComplete(t *testing.T) {
	chat := autoContinueChat()
	goal := newLifecycleTracker()
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester.Frame()
	h.assertTexts(tester, "完成目标", "放弃目标", "暂停目标", "清除目标")

	h.click(tester, "完成目标")
	if complete, _, _ := goal.lifecycleCalls(); complete != 1 {
		t.Fatalf("Complete calls = %d", complete)
	}
	tester.Frame()
	h.assertTexts(tester, "已完成", "清除目标")
	h.assertNoTexts(tester, "完成目标", "放弃目标", "暂停目标", "继续目标")
}

func TestGoalChipLifecycleAbandon(t *testing.T) {
	chat := autoContinueChat()
	goal := newLifecycleTracker()
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester.Frame()
	h.click(tester, "放弃目标")
	if _, abandon, _ := goal.lifecycleCalls(); abandon != 1 {
		t.Fatalf("Abandon calls = %d", abandon)
	}
	tester.Frame()
	h.assertTexts(tester, "已放弃", "清除目标")
	h.assertNoTexts(tester, "完成目标", "放弃目标", "暂停目标")
}

func TestGoalChipLifecycleHiddenWithoutLifecycler(t *testing.T) {
	chat := autoContinueChat()
	goal := &pauseTracker{}
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester.Frame()
	h.assertTexts(tester, "暂停目标", "清除目标")
	h.assertNoTexts(tester, "完成目标", "放弃目标")
}

func TestGoalChipLifecycleFailureDanger(t *testing.T) {
	chat := autoContinueChat()
	goal := newLifecycleTracker()
	goal.lifecycleFail = true
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester.Frame()
	h.click(tester, "完成目标")
	tester.Frame()
	h.assertTexts(tester, "完成目标失败")
}

func TestGoalChipLifecycleEnglish(t *testing.T) {
	chat := autoContinueChat()
	goal := newLifecycleTracker()
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", contracts.GoalStatusActive)
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Complete goal", "Abandon goal", "Pause goal", "Clear goal")
	h.click(tester, "Complete goal")
	tester.Frame()
	h.assertTexts(tester, "Completed", "Clear goal")
	h.assertNoTexts(tester, "Complete goal", "Abandon goal", "Pause goal")
}

// --- slash commands ---------------------------------------------------------

func TestGoalLifecycleCommandsRegisteredAndEnabled(t *testing.T) {
	chat := autoContinueChat()
	goal := newLifecycleTracker()
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester.Frame()

	wanted := []struct{ id, title, hint string }{
		{"zeokeel.session.goal.complete", "完成目标", "将当前目标标记为已完成"},
		{"zeokeel.session.goal.abandon", "放弃目标", "放弃当前目标"},
	}
	for _, want := range wanted {
		command := findSessionCommand(t, h, want.id)
		if command.Title != want.title || command.Hint != want.hint {
			t.Fatalf("%s = %q/%q, want %q/%q", want.id, command.Title, command.Hint, want.title, want.hint)
		}
		if command.Enabled == nil || !command.Enabled() {
			t.Fatalf("%s is disabled with an active goal and a lifecycler", want.id)
		}
	}
	count := 0
	for _, command := range h.ui.Runtime().Commands() {
		if len(command.ID) > len("zeokeel.session.") && command.ID[:len("zeokeel.session.")] == "zeokeel.session." {
			count++
		}
	}
	if count != 15 {
		t.Fatalf("session commands = %d, want 15", count)
	}

	// Completed goals disable both commands; a tracker without the lifecycle
	// half keeps them disabled too.
	goal.setGoal("g1", "ship it", contracts.GoalStatusCompleted)
	tester.Frame()
	for _, want := range wanted {
		command := findSessionCommand(t, h, want.id)
		if command.Enabled != nil && command.Enabled() {
			t.Fatalf("%s enabled for a completed goal", want.id)
		}
	}
	bare := &pauseTracker{}
	h2, tester2 := newGoalPauseHarness(t, autoContinueChat(), nil, bare)
	bare.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester2.Frame()
	if command := findSessionCommand(t, h2, "zeokeel.session.goal.complete"); command.Enabled != nil && command.Enabled() {
		t.Fatal("complete command enabled without a lifecycler")
	}
}

func TestGoalLifecycleCommandsRun(t *testing.T) {
	chat := autoContinueChat()
	goal := newLifecycleTracker()
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester.Frame()

	complete := findSessionCommand(t, h, "zeokeel.session.goal.complete")
	if err := complete.Run(); err != nil {
		t.Fatalf("complete Run: %v", err)
	}
	if completeCalls, _, _ := goal.lifecycleCalls(); completeCalls != 1 {
		t.Fatalf("Complete calls = %d", completeCalls)
	}
	tester.Frame()
	h.assertTexts(tester, "目标已完成", "已完成")

	// Failure surfaces in DangerText.
	goal2 := newLifecycleTracker()
	goal2.lifecycleFail = true
	h2, tester2 := newGoalPauseHarness(t, autoContinueChat(), nil, goal2)
	goal2.setGoal("g1", "ship it", contracts.GoalStatusActive)
	tester2.Frame()
	abandon := findSessionCommand(t, h2, "zeokeel.session.goal.abandon")
	if err := abandon.Run(); err != nil {
		t.Fatalf("abandon Run: %v", err)
	}
	tester2.Frame()
	h2.assertTexts(tester2, "放弃目标失败")
}

func TestGoalAutoContinueSkipsCompletedAndAbandoned(t *testing.T) {
	for _, status := range []string{contracts.GoalStatusCompleted, contracts.GoalStatusAbandoned} {
		status := status
		t.Run(status, func(t *testing.T) {
			chat := autoContinueChat()
			goal := newLifecycleTracker()
			_, tester := newGoalPauseHarness(t, chat, autoContinueSettings(true), goal)
			goal.setGoal("g1", "ship it", status)
			tester.Frame()
			settleRun(t, chat, tester)
			if sends := autoSends(chat); len(sends) != 0 {
				t.Fatalf("auto-continue ran for a %s goal: %v", status, sends)
			}
		})
	}
}
