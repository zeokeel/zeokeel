package regression

// Adversarial audit: automatic goal continuation (A5 fixed behavior; the
// goal.autoContinue row is no longer read). Boundary cases: one continuation is
// sent per settled run with the goal text, a goal can continue at most five
// times, a goal change resets the count, clearing the goal or pressing Stop
// suppresses it, and a queued prompt is flushed before auto-continue.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// goalTrackerFake implements contracts.GoalTracker for the audit.
type goalTrackerFake struct {
	mu      sync.Mutex
	id      string
	text    string
	status  string
	handler func(contracts.Goal, bool)
}

func (g *goalTrackerFake) SetHandler(fn func(contracts.Goal, bool)) {
	g.mu.Lock()
	g.handler = fn
	g.mu.Unlock()
}

func (g *goalTrackerFake) Set(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (g *goalTrackerFake) Get() (id, text, status string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.id, g.text, g.status, g.id != ""
}

func (g *goalTrackerFake) Clear() bool {
	g.mu.Lock()
	existed := g.id != ""
	g.id, g.text, g.status = "", "", ""
	handler := g.handler
	g.mu.Unlock()
	if handler != nil {
		handler(contracts.Goal{}, false)
	}
	return existed
}

// install sets one goal and fires the change handler.
func (g *goalTrackerFake) install(id, text string) {
	g.mu.Lock()
	g.id, g.text, g.status = id, text, "active"
	handler := g.handler
	g.mu.Unlock()
	if handler != nil {
		handler(contracts.Goal{ID: id, Text: text, Status: "active"}, true)
	}
}

// goalHarness bundles the session screen over a fake goal tracker.
type goalHarness struct {
	h      *harness
	chat   *fakeChat
	goal   *goalTrackerFake
	tester *ui.Tester
}

// startGoalAuto activates the session screen with one active session.
func startGoalAuto(t *testing.T, setting string) *goalHarness {
	t.Helper()
	h := newBareHarness(t)
	if setting != "" {
		if err := h.settings.Set("goal.autoContinue", setting); err != nil {
			t.Fatalf("set goal.autoContinue: %v", err)
		}
	}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-goal", Title: "Goal session",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "done"}}}, true)
	goal := &goalTrackerFake{}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyGoalTracker, contracts.GoalTracker(goal))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &goalHarness{h: h, chat: chat, goal: goal, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	return harness
}

// setRunning flips the fake session's Running flag.
func (g *goalHarness) setRunning(t *testing.T, running bool) {
	t.Helper()
	g.chat.mu.Lock()
	session := g.chat.sessions["sess-goal"]
	session.Running = running
	g.chat.sessions["sess-goal"] = session
	g.chat.mu.Unlock()
}

// settle flips running true then false and pumps frames so the posted
// handleRunSettled runs.
func (g *goalHarness) settle(t *testing.T) {
	t.Helper()
	g.setRunning(t, true)
	g.tester.Frame()
	g.setRunning(t, false)
	waitFor(t, "run settled", func() bool {
		g.tester.Frame()
		return true
	})
}

// pumpFrames renders frames until condition holds (the auto-continue send hops
// through runtime.Post).
func (g *goalHarness) pump(t *testing.T, what string, condition func() bool) {
	t.Helper()
	waitFor(t, what, func() bool {
		g.tester.Frame()
		return condition()
	})
}

// containsGoal reports whether one recorded send carries the continuation text.
func containsGoal(sends []string, text string) bool {
	for _, send := range sends {
		if strings.Contains(send, text) && strings.Contains(send, "请继续推进目标") {
			return true
		}
	}
	return false
}

// TestGoalAutoContinueAlwaysOn covers A5's fixed behavior: goal.autoContinue is
// no longer a settings-controlled switch, so a missing row, the old explicit
// "false", malformed values and truthy values all auto-continue the active goal.
func TestGoalAutoContinueAlwaysOn(t *testing.T) {
	for _, value := range []string{"", "false", "bogus", "1", "yes", "  TRUE  "} {
		harness := startGoalAuto(t, value)
		harness.goal.install("goal-1", "Ship it")
		harness.tester.Frame()
		harness.settle(t)
		harness.pump(t, "auto continuation for row "+value, func() bool {
			return len(harness.chat.recordedSends()) >= 1
		})
		if sends := harness.chat.recordedSends(); !containsGoal(sends, "Ship it") {
			t.Fatalf("goal.autoContinue=%q continuation sends = %v, want one", value, sends)
		}
	}
}

// TestGoalAutoContinueBoundedAndReset covers the one-per-settle behavior, the
// five-continuation cap and the goal-change reset.
func TestGoalAutoContinueBoundedAndReset(t *testing.T) {
	harness := startGoalAuto(t, "true")
	harness.goal.install("goal-1", "Ship release")
	harness.tester.Frame()

	harness.settle(t)
	harness.pump(t, "first continuation", func() bool { return len(harness.chat.recordedSends()) == 1 })
	if sends := harness.chat.recordedSends(); !containsGoal(sends, "Ship release") {
		t.Fatalf("first continuation = %v", sends)
	}
	// A second settle is a second run: it sends exactly one more continuation
	// (one per settled run, never two).
	harness.settle(t)
	harness.tester.Frame()
	if got := len(harness.chat.recordedSends()); got != 2 {
		t.Fatalf("continuations after two settled runs = %d, want 2", got)
	}

	// Further runs reach the cap; extra runs stop at five in total.
	for index := 0; index < 8; index++ {
		harness.settle(t)
		harness.tester.Frame()
	}
	waitFor(t, "auto-continue cap", func() bool {
		harness.tester.Frame()
		return true
	})
	if got := len(harness.chat.recordedSends()); got != 5 {
		t.Fatalf("auto-continue attempts = %d, want the 5-run cap", got)
	}

	// A new goal resets the counter: the next settle sends again.
	harness.goal.install("goal-2", "Next objective")
	harness.tester.Frame()
	harness.settle(t)
	harness.pump(t, "continuation for the new goal", func() bool {
		return containsGoal(harness.chat.recordedSends(), "Next objective")
	})
}

// TestGoalAutoContinueSuppressionClearAndFailure covers goal clearing/Stop
// suppression (a new run clears it) and failed sends counting toward the cap.
func TestGoalAutoContinueSuppressionClearAndFailure(t *testing.T) {
	t.Run("clear and stop suppress, new run resumes", func(t *testing.T) {
		harness := startGoalAuto(t, "true")
		harness.goal.install("goal-1", "Work")
		harness.tester.Frame()
		harness.settle(t)
		harness.pump(t, "first continuation", func() bool { return len(harness.chat.recordedSends()) >= 1 })

		// Clearing the goal suppresses the next settle.
		harness.goal.Clear()
		harness.tester.Frame()
		before := len(harness.chat.recordedSends())
		harness.settle(t)
		harness.tester.Frame()
		if got := len(harness.chat.recordedSends()); got != before {
			t.Fatalf("cleared goal still continued: %d -> %d", before, got)
		}

		// Stop while a turn is running: the slash command sets the suppression
		// flag, so the settled run does not auto-continue.
		harness.goal.install("goal-1b", "Work again")
		harness.tester.Frame()
		harness.setRunning(t, true)
		harness.tester.Frame()
		stop := sessionCommandByID(t, harness.h, "zeokeel.session.stop")
		if err := stop.Run(); err != nil {
			t.Fatalf("Run /stop: %v", err)
		}
		harness.setRunning(t, false)
		harness.tester.Frame()
		if got := len(harness.chat.recordedSends()); got != before {
			t.Fatalf("Stop-suppressed run continued: %d -> %d", before, got)
		}
		// A later run clears the suppression and continues again.
		harness.settle(t)
		harness.pump(t, "resumed continuation", func() bool { return len(harness.chat.recordedSends()) > before })
	})

	t.Run("queued prompt flushes before auto-continue", func(t *testing.T) {
		harness := startGoalAuto(t, "true")
		if err := harness.h.settings.Set("ui.sendWhileRunning", "true"); err != nil {
			t.Fatalf("set sendWhileRunning: %v", err)
		}
		harness.goal.install("goal-q", "Queue goal")
		harness.setRunning(t, true)
		harness.tester.Frame()
		// Type and queue one prompt while the run is active.
		rect, ok := harness.tester.Find("排队")
		if !ok {
			t.Fatalf("queue control missing; texts = %v", harness.tester.Texts())
		}
		harness.tester.ClickAt(rect.X-120, rect.Y-50)
		harness.tester.Frame()
		harness.tester.Type("queued prompt")
		harness.tester.Frame()
		if err := harness.tester.Click("排队"); err != nil {
			t.Fatalf("queue prompt: %v", err)
		}
		harness.tester.Frame()
		requireHasText(t, harness.tester, "已排队 1 条")

		// The settle flushes the queued prompt first and does not auto-continue
		// in the same settle.
		harness.setRunning(t, false)
		harness.pump(t, "queued flush", func() bool {
			for _, send := range harness.chat.recordedSends() {
				if strings.Contains(send, "queued prompt") {
					return true
				}
			}
			return false
		})
		for _, send := range harness.chat.recordedSends() {
			if strings.Contains(send, "请继续推进目标") {
				t.Fatalf("auto-continue raced the queued prompt: %v", harness.chat.recordedSends())
			}
		}
		// After the queued run settles, the goal continuation follows.
		harness.settle(t)
		harness.pump(t, "continuation after the queued run", func() bool {
			return containsGoal(harness.chat.recordedSends(), "Queue goal")
		})
	})

	t.Run("failures count toward the cap", func(t *testing.T) {
		harness := startGoalAuto(t, "true")
		harness.goal.install("goal-err", "Failing goal")
		harness.tester.Frame()
		harness.chat.setSendError(errors.New("agent busy"))
		for index := 0; index < 8; index++ {
			harness.settle(t)
			harness.tester.Frame()
		}
		waitFor(t, "auto-continue attempts", func() bool {
			harness.tester.Frame()
			return true
		})
		sends := harness.chat.recordedSends()
		if len(sends) != 5 {
			t.Fatalf("failed continuation attempts = %d, want 5 (failures count toward the cap)", len(sends))
		}
		requireHasText(t, harness.tester, "自动续跑发送失败")
	})
}
