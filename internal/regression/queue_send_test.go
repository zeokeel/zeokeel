package regression

// Adversarial audit: queueing prompts while a turn is running (ui.sendWhileRunning).
// Negative/boundary cases: the switch defaults off (the composer stays disabled
// while running), only the literal "true" enables queueing, queued prompts flush
// FIFO after the run ends, a failed flush keeps the prompt for retry, and
// switching sessions clears the queue.

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// queueHarness is the session screen with one running session.
type queueHarness struct {
	h      *harness
	chat   *fakeChat
	tester *ui.Tester
}

// startQueueUI activates the session screen with a running active session and
// the given ui.sendWhileRunning row.
func startQueueUI(t *testing.T, value string) *queueHarness {
	t.Helper()
	h := newBareHarness(t)
	if value != "" {
		if err := h.settings.Set(contracts.SettingSendWhileRunning, value); err != nil {
			t.Fatalf("set %s: %v", contracts.SettingSendWhileRunning, err)
		}
	}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-run", Title: "Queue session", Running: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "开始"}}}, true)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &queueHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	return harness
}

// flipRunning flips one fake session's Running flag (the flush reads it per
// frame; no subscription notification is needed).
func (q *queueHarness) flipRunning(t *testing.T, running bool) {
	t.Helper()
	q.chat.mu.Lock()
	session := q.chat.sessions["sess-run"]
	session.Running = running
	q.chat.sessions["sess-run"] = session
	q.chat.mu.Unlock()
}

// pumpFrames renders frames until condition holds (the queue flush hops through
// runtime.Post, so one frame may only schedule it).
func (q *queueHarness) pumpFrames(t *testing.T, what string, condition func() bool) {
	t.Helper()
	waitFor(t, what, func() bool {
		q.tester.Frame()
		return condition()
	})
}

// typeQueuePrompt focuses the composer, types one prompt and queues it with
// the running composer's 排队 control, then repaints.
func (q *queueHarness) typeQueuePrompt(t *testing.T, label, prompt string) {
	t.Helper()
	rect, ok := q.tester.Find(label)
	if !ok {
		t.Fatalf("control %q not found; texts = %v", label, q.tester.Texts())
	}
	q.tester.ClickAt(rect.X-120, rect.Y-50)
	q.tester.Frame()
	q.tester.Type(prompt)
	q.tester.Frame()
	if err := q.tester.Click("排队"); err != nil {
		t.Fatalf("click 排队: %v; texts = %v", err, q.tester.Texts())
	}
	q.tester.Frame()
}

// TestQueueSwitchOffKeepsComposerDisabled covers the default: with the switch
// off/missing/bad only Stop is available while running; nothing is queued.
func TestQueueSwitchOffKeepsComposerDisabled(t *testing.T) {
	for _, value := range []string{"", "false", "bogus", "0"} {
		harness := startQueueUI(t, value)
		requireHasText(t, harness.tester, "停止")
		requireNoText(t, harness.tester, "输入消息…")
		requireNoText(t, harness.tester, "排队")
		requireNoText(t, harness.tester, "已排队")
		if got := len(harness.chat.recordedSends()); got != 0 {
			t.Fatalf("switch %q sent %d prompts", value, got)
		}
	}
}

// TestQueueFIFOFlushAndFailure covers the enabled path: two queued prompts show
// the badge, flush one-by-one after the run ends, and a failed flush keeps the
// prompt with a readable error.
func TestQueueFIFOFlushAndFailure(t *testing.T) {
	harness := startQueueUI(t, "true")
	requireHasText(t, harness.tester, "输入消息…")

	harness.typeQueuePrompt(t, "排队", "第一条")
	requireHasText(t, harness.tester, "已排队 1 条")
	harness.typeQueuePrompt(t, "排队", "第二条")
	requireHasText(t, harness.tester, "已排队 2 条")
	if got := len(harness.chat.recordedSends()); got != 0 {
		t.Fatalf("queued prompts were sent while running: %v", harness.chat.recordedSends())
	}

	// Finish the run: the first prompt flushes, the second stays queued.
	harness.flipRunning(t, false)
	harness.pumpFrames(t, "first queued flush", func() bool {
		return len(harness.chat.recordedSends()) >= 1
	})
	sends := harness.chat.recordedSends()
	if len(sends) != 1 || sends[0] != "sess-run:第一条" {
		t.Fatalf("FIFO first flush = %v", sends)
	}
	harness.flipRunning(t, true)
	harness.tester.Frame()
	requireHasText(t, harness.tester, "已排队 1 条")

	// Failed flush keeps the prompt and shows the error.
	harness.flipRunning(t, false)
	harness.chat.setSendError(errors.New("agent busy"))
	harness.pumpFrames(t, "queued flush error", func() bool {
		return harness.tester.HasText("排队发送失败：agent busy")
	})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "已排队 1 条")
	// The failed flush keeps the prompt for a retry; the attempt itself is
	// recorded by the fake chat (Send records before returning the error).
	if got := len(harness.chat.recordedSends()); got < 1 {
		t.Fatalf("failed flush recorded no attempts: %v", harness.chat.recordedSends())
	}
}

// TestQueueClearsOnSessionSwitch covers switching sessions: the pending queue
// belongs to the session it was typed in and is dropped when another session
// becomes active.
func TestQueueClearsOnSessionSwitch(t *testing.T) {
	harness := startQueueUI(t, "true")
	harness.chat.addSession(contracts.ChatSession{ID: "sess-other", Title: "Other"}, false)
	harness.typeQueuePrompt(t, "排队", "给第一个会话")
	requireHasText(t, harness.tester, "已排队 1 条")

	// Switch to another session: the queue badge disappears (queue cleared).
	if err := harness.chat.SetActiveSession("sess-other"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已排队")

	// Switching back does not resurrect or flush it.
	if err := harness.chat.SetActiveSession("sess-run"); err != nil {
		t.Fatalf("SetActiveSession back: %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已排队")
	if got := len(harness.chat.recordedSends()); got != 0 {
		t.Fatalf("queue flushed after a session switch: %v", harness.chat.recordedSends())
	}
}
