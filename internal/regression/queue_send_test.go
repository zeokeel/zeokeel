package regression

// Adversarial audit: queueing prompts while a turn is running (ui.sendWhileRunning).
// Queueing defaults on (only the literal "false" disables it), the composer
// stays visible and editable while a run streams, queued prompts flush FIFO
// after the run ends, a failed flush keeps the prompt for retry, and a queue
// belongs to the session it was typed in: switching conversations keeps it and
// the module-level flush delivers it to its owner even while another session is
// on screen.

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

// emitSessionChange notifies the module's subscribers like the agent backend
// does after a session state change. The queue flush listens on this path, so a
// session settling while another one is active still delivers its queue.
func (f *fakeChat) emitSessionChange(sessionID string) {
	f.mu.Lock()
	subs := append([]func(string){}, f.subs...)
	f.mu.Unlock()
	for _, sub := range subs {
		if sub != nil {
			sub(sessionID)
		}
	}
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

// TestQueueSwitchOffKeepsComposerVisible covers the new default and the explicit
// opt-out:
//   - missing/empty/malformed values (and any value but the literal "false")
//     keep queueing on, so the running composer shows the input plus 排队 and
//     Enter queues instead of sending;
//   - "false" keeps the input visible and editable but Enter must not call
//     Send, only Stop sits in the send position and the draft is kept.
func TestQueueSwitchOffKeepsComposerVisible(t *testing.T) {
	// Queueing is the default: only "false" disables it.
	for _, value := range []string{"", "true", "bogus", "0"} {
		harness := startQueueUI(t, value)
		requireHasText(t, harness.tester, "输入消息…")
		requireHasText(t, harness.tester, "排队")
		requireHasText(t, harness.tester, "停止")
		if _, ok := harness.tester.Find("发送"); ok {
			t.Fatalf("value %q: send button rendered while running; texts = %v", value, harness.tester.Texts())
		}
		harness.typeQueuePrompt(t, "排队", "默认排队")
		requireHasText(t, harness.tester, "已排队 1 条")
		if sends := harness.chat.recordedSends(); len(sends) != 0 {
			t.Fatalf("value %q: Enter sent while running instead of queueing: %v", value, sends)
		}
	}

	// The explicit opt-out: input visible/editable, Enter inert, Stop only.
	harness := startQueueUI(t, "false")
	requireHasText(t, harness.tester, "输入消息…")
	requireHasText(t, harness.tester, "停止")
	requireHasText(t, harness.tester, "回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送")
	requireNoText(t, harness.tester, "已排队")
	if _, ok := harness.tester.Find("发送"); ok {
		t.Fatalf("queueing off: send button rendered while running; texts = %v", harness.tester.Texts())
	}
	if _, ok := harness.tester.Find("排队"); ok {
		t.Fatalf("queueing off: 排队 rendered; texts = %v", harness.tester.Texts())
	}

	// Focus the input (directly above the Stop control), type and press Enter:
	// the draft stays put and chat.Send is never called.
	stop, ok := harness.tester.Find("停止")
	if !ok {
		t.Fatalf("stop control missing; texts = %v", harness.tester.Texts())
	}
	harness.tester.ClickAt(stop.X-120, stop.Y-50)
	harness.tester.Frame()
	harness.tester.Type("保留的草稿")
	harness.tester.Frame()
	harness.tester.Key(0, ui.KeyEnter)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("Enter sent while queueing is explicitly off: %v", sends)
	}
	requireNoText(t, harness.tester, "已排队")

	// The draft survived the run: when it ends, the send position returns and
	// Enter sends the kept text.
	harness.flipRunning(t, false)
	harness.tester.Frame()
	requireHasText(t, harness.tester, "发送")
	harness.tester.Key(0, ui.KeyEnter)
	harness.tester.Frame()
	sends := harness.chat.recordedSends()
	if len(sends) != 1 || sends[0] != "sess-run:保留的草稿" {
		t.Fatalf("kept draft send = %v, want the typed draft", sends)
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

// TestQueuePersistsPerSessionAndFlushesToOwner covers the per-session queue: the
// badge belongs to the conversation it was typed in, switching away hides it
// (and never drops it), switching back shows it again, and the module-level
// flush delivers it into its owning session when that run settles even while
// another conversation is on screen.
func TestQueuePersistsPerSessionAndFlushesToOwner(t *testing.T) {
	harness := startQueueUI(t, "true")
	harness.chat.addSession(contracts.ChatSession{ID: "sess-other", Title: "Other"}, false)
	harness.typeQueuePrompt(t, "排队", "给第一个会话")
	requireHasText(t, harness.tester, "已排队 1 条")

	// Switch to another session: no badge there, but the queue is not dropped.
	if err := harness.chat.SetActiveSession("sess-other"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	requireNoText(t, harness.tester, "已排队")

	// Switching back still shows the preserved queue.
	if err := harness.chat.SetActiveSession("sess-run"); err != nil {
		t.Fatalf("SetActiveSession back: %v", err)
	}
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	requireHasText(t, harness.tester, "已排队 1 条")

	// Leave again, then let sess-run settle: the module-level subscription
	// flushes the prompt into sess-run, not into the visible conversation.
	if err := harness.chat.SetActiveSession("sess-other"); err != nil {
		t.Fatalf("SetActiveSession away: %v", err)
	}
	harness.flipRunning(t, false)
	harness.chat.emitSessionChange("sess-run")
	harness.pumpFrames(t, "queued prompt flush into its session", func() bool {
		return len(harness.chat.recordedSends()) >= 1
	})
	sends := harness.chat.recordedSends()
	if len(sends) != 1 || sends[0] != "sess-run:给第一个会话" {
		t.Fatalf("queue flush = %v, want it sent into sess-run only", sends)
	}

	// The owning session has no leftover badge when reopened.
	if err := harness.chat.SetActiveSession("sess-run"); err != nil {
		t.Fatalf("SetActiveSession owner: %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已排队")
}
