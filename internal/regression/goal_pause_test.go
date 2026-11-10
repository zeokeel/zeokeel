package regression

// Adversarial audit: goal pause/resume. Negative/boundary cases: the host state
// machine (active <-> paused with no duplicate notifications), no-goal safety,
// Set resetting the status to active, handler notification counts/content,
// concurrent Pause/Resume/Get under -race, and the session chip/auto-continue
// behavior: a paused goal keeps its badge but never auto-continues until resumed.

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// goalNotice is one handler notification captured by the audit.
type goalNotice struct {
	goal contracts.Goal
	ok   bool
}

// captureGoalNotices installs a recording handler on the host tracker.
func captureGoalNotices(t *testing.T, tracker contracts.GoalTracker) (*[]goalNotice, *sync.Mutex) {
	t.Helper()
	notices := make([]goalNotice, 0)
	var mu sync.Mutex
	tracker.SetHandler(func(goal contracts.Goal, ok bool) {
		mu.Lock()
		notices = append(notices, goalNotice{goal: goal, ok: ok})
		mu.Unlock()
	})
	return &notices, &mu
}

// pauseTracker fails the test when the tracker does not expose GoalPauser.
func pauseTracker(t *testing.T, tracker contracts.GoalTracker) contracts.GoalPauser {
	t.Helper()
	pauser, ok := tracker.(contracts.GoalPauser)
	if !ok {
		t.Fatal("the host goal tracker does not implement contracts.GoalPauser")
	}
	return pauser
}

// TestGoalPauseResumeStateMachine covers the host state machine and its
// notifications through the real uiroot goal service.
func TestGoalPauseResumeStateMachine(t *testing.T) {
	h := newBareHarness(t)
	tracker := contracts.GoalTracker(h.ui.GoalTracker())
	pauser := pauseTracker(t, tracker)
	notices, mu := captureGoalNotices(t, tracker)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(*notices)
	}

	// No goal: Pause/Resume/Clear are safe and silent.
	if pauser.Pause() {
		t.Fatal("Pause without a goal reported true")
	}
	if pauser.Resume() {
		t.Fatal("Resume without a goal reported true")
	}
	if tracker.Clear() {
		t.Fatal("Clear without a goal reported true")
	}
	if got := count(); got != 0 {
		t.Fatalf("no-goal actions notified the handler %d times", got)
	}
	if _, _, _, ok := tracker.Get(); ok {
		t.Fatal("Get reported a goal after no-goal actions")
	}

	// Set: active, notified once with the text.
	if _, err := tracker.Set(context.Background(), "  Ship release  "); err != nil {
		t.Fatalf("Set: %v", err)
	}
	id, text, status, ok := tracker.Get()
	if !ok || id == "" || text != "Ship release" || status != "active" {
		t.Fatalf("goal after Set = %q/%q/%q ok=%v", id, text, status, ok)
	}
	if count() != 1 {
		t.Fatalf("Set notifications = %d, want 1", count())
	}

	// Pause: status paused, one notification; pausing again is a no-op.
	if !pauser.Pause() {
		t.Fatal("Pause reported false for an active goal")
	}
	if _, _, status, _ = tracker.Get(); status != "paused" {
		t.Fatalf("status after Pause = %q, want paused", status)
	}
	if count() != 2 {
		t.Fatalf("Pause notifications = %d, want 2", count())
	}
	if !pauser.Pause() {
		t.Fatal("second Pause reported false")
	}
	if count() != 2 {
		t.Fatalf("double Pause notified again (%d)", count())
	}

	// Resume: back to active, one notification; resume again is a no-op.
	if !pauser.Resume() {
		t.Fatal("Resume reported false for a paused goal")
	}
	if _, _, status, _ = tracker.Get(); status != "active" {
		t.Fatalf("status after Resume = %q, want active", status)
	}
	if count() != 3 {
		t.Fatalf("Resume notifications = %d, want 3", count())
	}
	if !pauser.Resume() {
		t.Fatal("second Resume reported false")
	}
	if count() != 3 {
		t.Fatalf("double Resume notified again (%d)", count())
	}

	// Set resets a paused goal to active with a fresh ID.
	if !pauser.Pause() {
		t.Fatal("Pause before re-Set failed")
	}
	oldID := id
	if _, err := tracker.Set(context.Background(), "New objective"); err != nil {
		t.Fatalf("re-Set: %v", err)
	}
	newID, text, status, _ := tracker.Get()
	if newID == oldID || text != "New objective" || status != "active" {
		t.Fatalf("re-Set goal = %q/%q/%q", newID, text, status)
	}

	// Clear notifies with ok=false and empties the tracker.
	if !tracker.Clear() {
		t.Fatal("Clear reported false with a goal")
	}
	if _, _, _, ok := tracker.Get(); ok {
		t.Fatal("goal survived Clear")
	}
	mu.Lock()
	last := (*notices)[len(*notices)-1]
	mu.Unlock()
	if last.ok || last.goal.Text != "" {
		t.Fatalf("Clear notification = %+v, want ok=false", last)
	}

	// Empty/canceled Set is rejected without touching the goal.
	if _, err := tracker.Set(context.Background(), "   "); err == nil {
		t.Fatal("Set with a blank text succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tracker.Set(canceled, "race"); err == nil {
		t.Fatal("Set with a canceled context succeeded")
	}
}

// TestGoalPauseResumeConcurrent covers concurrent state changes: no panic, no
// data race and a consistent final state (-race).
func TestGoalPauseResumeConcurrent(t *testing.T) {
	h := newBareHarness(t)
	tracker := contracts.GoalTracker(h.ui.GoalTracker())
	pauser := pauseTracker(t, tracker)
	if _, err := tracker.Set(context.Background(), "Concurrent goal"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var wg sync.WaitGroup
	for index := 0; index < 16; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch index % 4 {
			case 0:
				pauser.Pause()
			case 1:
				pauser.Resume()
			case 2:
				_, _, _, _ = tracker.Get()
			default:
				_ = tracker.Clear()
			}
		}()
	}
	wg.Wait()
	// The final state is consistent: either no goal or an active/paused goal
	// with a non-empty text.
	if id, text, status, ok := tracker.Get(); ok {
		if id == "" || text == "" || (status != "active" && status != "paused") {
			t.Fatalf("inconsistent goal after concurrency: %q/%q/%q", id, text, status)
		}
	}
}

// startGoalPauseUI activates the session screen over the real host goal bridge.
func startGoalPauseUI(t *testing.T) (*goalHarness, contracts.GoalTracker) {
	t.Helper()
	h := newBareHarness(t)
	if err := h.settings.Set(contracts.SettingGoalAutoContinue, "true"); err != nil {
		t.Fatalf("set goal.autoContinue: %v", err)
	}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-goal", Title: "Goal session",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "done"}}}, true)
	tracker := h.ui.GoalTracker()
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyGoalTracker, contracts.GoalTracker(tracker))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &goalHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	return harness, contracts.GoalTracker(tracker)
}

// TestSessionGoalPauseChipAndAutoContinue covers the session chip and the
// auto-continue gate: paused goals show 「目标已暂停」 and never continue until
// resumed; clearing hides the chip and the actions are absent without GoalPauser.
func TestSessionGoalPauseChipAndAutoContinue(t *testing.T) {
	harness, tracker := startGoalPauseUI(t)
	if _, err := tracker.Set(context.Background(), "Pausable goal"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	harness.tester.Frame()
	requireHasTexts(t, harness.tester, "目标", "Pausable goal")

	// The goal actions live in the chip's popover.
	if err := harness.tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal menu: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasTexts(t, harness.tester, "暂停目标", "删除目标记录")

	// Pause through the chip menu: the chip label changes; Resume lives in the
	// same menu.
	if err := harness.tester.Click("暂停目标"); err != nil {
		t.Fatalf("click pause: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasTexts(t, harness.tester, "目标已暂停", "Pausable goal")

	// A settled run with a paused goal must not auto-continue.
	harness.settle(t)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("paused goal auto-continued: %v", sends)
	}
	// The goal chip stays visible while paused.
	requireHasText(t, harness.tester, "Pausable goal")

	// Resume: the next settled run continues again.
	if err := harness.tester.Click("目标操作"); err != nil {
		t.Fatalf("reopen goal menu: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "继续目标")
	if err := harness.tester.Click("继续目标"); err != nil {
		t.Fatalf("click resume: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "目标")
	harness.settle(t)
	harness.pump(t, "resumed auto-continue", func() bool {
		return containsGoal(harness.chat.recordedSends(), "Pausable goal")
	})

	// Clearing the goal hides the chip.
	tracker.Clear()
	harness.tester.Frame()
	requireNoText(t, harness.tester, "Pausable goal")
	requireNoText(t, harness.tester, "暂停目标")

	// A tracker without GoalPauser hides the pause/resume action but keeps the
	// clear action.
	plain := startGoalAuto(t, "true")
	plain.goal.install("goal-plain", "Plain goal")
	plain.tester.Frame()
	requireHasTexts(t, plain.tester, "目标", "Plain goal")
	if err := plain.tester.Click("目标操作"); err != nil {
		t.Fatalf("open plain goal menu: %v; texts = %v", err, plain.tester.Texts())
	}
	plain.tester.Frame()
	requireHasText(t, plain.tester, "删除目标记录")
	requireNoText(t, plain.tester, "暂停目标")
	requireNoText(t, plain.tester, "继续目标")
}
