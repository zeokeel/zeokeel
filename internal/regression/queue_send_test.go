package regression

// Adversarial audit: the backend prompt inbox (DSH inbox parity). A prompt sent
// while a session runs goes into the agent backend's per-session PromptQueue;
// the UI only reads it (badge/popover), removes entries and never sends or
// drains it. Stop keeps the inbox, conversation switches never leak another
// session's queue, a host without PromptQueue keeps the old conflict behavior,
// and a running conflict racing the frame's read model is absorbed by enqueue.

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
// the given ui.sendWhileRunning row (kept on disk; the UI no longer reads it:
// the backend inbox owns queueing).
func startQueueUI(t *testing.T, value string) *queueHarness {
	t.Helper()
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-run", Title: "Queue session", Running: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "开始"}}}, true)
	return startQueueUIWithService(t, value, chat)
}

// chatWithoutQueue exposes only contracts.ChatService, hiding the fake's
// PromptQueue half to model a host/backend without the inbox.
type chatWithoutQueue struct{ contracts.ChatService }

// startQueueUIWithService boots the session screen over an explicit chat
// service (the fake itself, or a wrapper that hides one optional half).
func startQueueUIWithService(t *testing.T, value string, chat *fakeChat) *queueHarness {
	t.Helper()
	h := newBareHarness(t)
	if value != "" {
		if err := h.settings.Set("ui.sendWhileRunning", value); err != nil {
			t.Fatalf("set ui.sendWhileRunning: %v", err)
		}
	}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &queueHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	return harness
}

// startQueueUIWithoutBackend boots the session screen over the fake behind a
// contracts.ChatService-only wrapper (no PromptQueue half).
func startQueueUIWithoutBackend(t *testing.T, chat *fakeChat) *queueHarness {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chatWithoutQueue{ChatService: chat}))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &queueHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	return harness
}

// flipRunning flips one fake session's Running flag and notifies listeners.
func (q *queueHarness) flipRunning(t *testing.T, running bool) {
	t.Helper()
	session, ok := q.chat.Session("sess-run")
	if !ok {
		t.Fatal("fake session sess-run is missing")
	}
	session.Running = running
	q.chat.updateSession(session)
}

// typeQueuePrompt focuses the composer, types one prompt and queues it with the
// running composer's 排队 control, then repaints.
func (q *queueHarness) typeQueuePrompt(t *testing.T, label, prompt string) {
	t.Helper()
	q.tester.Frame()
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

// TestQueueBackendInboxKeepsComposerVisible covers the fixed behavior: the
// retired ui.sendWhileRunning row never disables the backend inbox — the
// running composer shows 排队 + 停止, Enter/排队 hands the prompt to
// PromptQueue.EnqueuePrompt and never calls Send.
func TestQueueBackendInboxKeepsComposerVisible(t *testing.T) {
	for _, value := range []string{"", "true", "bogus", "false"} {
		harness := startQueueUI(t, value)
		requireHasText(t, harness.tester, "输入消息…")
		requireHasText(t, harness.tester, "排队")
		requireHasText(t, harness.tester, "停止")
		requireNoText(t, harness.tester, "回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送")
		harness.typeQueuePrompt(t, "排队", "默认排队")
		requireHasText(t, harness.tester, "已排队 1 条")
		if sends := harness.chat.recordedSends(); len(sends) != 0 {
			t.Fatalf("value %q: Enter sent while running instead of enqueueing: %v", value, sends)
		}
		if queued := harness.chat.queueFor("sess-run"); len(queued) != 1 || queued[0].Text != "默认排队" {
			t.Fatalf("value %q: backend inbox = %+v", value, queued)
		}
	}
}

// TestQueueDrainFIFOByBackend covers the backend drain: the UI never sends the
// queued prompts itself; when the backend starts the next one the badge shrinks
// and the transcript shows the new turn (strict FIFO order preserved by the
// backend).
func TestQueueDrainFIFOByBackend(t *testing.T) {
	harness := startQueueUI(t, "true")
	harness.typeQueuePrompt(t, "排队", "第一条")
	requireHasText(t, harness.tester, "已排队 1 条")
	harness.typeQueuePrompt(t, "排队", "第二条")
	requireHasText(t, harness.tester, "已排队 2 条")
	if got := len(harness.chat.recordedSends()); got != 0 {
		t.Fatalf("queued prompts were sent while running: %v", harness.chat.recordedSends())
	}

	// The run settles; the backend drains the first entry.
	harness.flipRunning(t, false)
	if !harness.chat.drainQueuedPrompt("sess-run") {
		t.Fatal("fake backend did not drain the inbox")
	}
	harness.tester.Frame()
	harness.tester.Frame()
	sends := harness.chat.recordedSends()
	if len(sends) != 1 || sends[0] != "sess-run:第一条" {
		t.Fatalf("backend FIFO drain = %v, want the first prompt", sends)
	}
	requireHasText(t, harness.tester, "已排队 1 条")

	// The drained prompt started its own run; settle it and drain the second.
	harness.flipRunning(t, false)
	if !harness.chat.drainQueuedPrompt("sess-run") {
		t.Fatal("fake backend did not drain the second entry")
	}
	harness.tester.Frame()
	harness.tester.Frame()
	sends = harness.chat.recordedSends()
	if len(sends) != 2 || sends[1] != "sess-run:第二条" {
		t.Fatalf("backend FIFO drain = %v, want the second prompt", sends)
	}
	requireNoText(t, harness.tester, "已排队")
	if queued := harness.chat.queueFor("sess-run"); len(queued) != 0 {
		t.Fatalf("inbox after both drains = %+v", queued)
	}
}

// TestQueueStopKeepsInbox: 停止 cancels the run and never clears or sends the
// queued prompts; the popover offers remove/clear only (no local pause state).
func TestQueueStopKeepsInbox(t *testing.T) {
	harness := startQueueUI(t, "true")
	harness.typeQueuePrompt(t, "排队", "停后保留")
	requireHasText(t, harness.tester, "已排队 1 条")

	if err := harness.tester.Click("停止"); err != nil {
		t.Fatalf("click 停止: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	if cancels := harness.chat.cancelCalls(); len(cancels) != 1 || cancels[0] != "sess-run" {
		t.Fatalf("Cancel calls = %v, want [sess-run]", cancels)
	}
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("stopping sent the queued prompt: %v", sends)
	}
	if queued := harness.chat.queueFor("sess-run"); len(queued) != 1 {
		t.Fatalf("stopping cleared the backend inbox: %+v", queued)
	}
	requireHasText(t, harness.tester, "已排队 1 条")

	// The popover lists the backend inbox; the old paused/resume copy is gone.
	if err := harness.tester.Click("已排队 1 条"); err != nil {
		t.Fatalf("open queue popover: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "排队消息")
	requireHasText(t, harness.tester, "取消排队")
	requireHasText(t, harness.tester, "清空全部")
	requireNoText(t, harness.tester, "已停止：排队消息不会自动发送")
	requireNoText(t, harness.tester, "发送排队消息")
}

// TestQueueRemoveAndClearGoToBackend covers the popover mutations: a row
// removal calls RemoveQueuedPrompt (the backend entry is really gone) and
// clear-all empties the rest.
func TestQueueRemoveAndClearGoToBackend(t *testing.T) {
	harness := startQueueUI(t, "true")
	harness.typeQueuePrompt(t, "排队", "甲条")
	harness.typeQueuePrompt(t, "排队", "乙条")
	requireHasText(t, harness.tester, "已排队 2 条")

	if err := harness.tester.Click("已排队 2 条"); err != nil {
		t.Fatalf("open queue popover: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("取消排队"); err != nil {
		t.Fatalf("remove queued row: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	queued := harness.chat.queueFor("sess-run")
	if len(queued) != 1 || queued[0].Text != "乙条" {
		t.Fatalf("inbox after removing the first row = %+v", queued)
	}
	requireHasText(t, harness.tester, "已排队 1 条")

	if !harness.tester.HasText("排队消息") {
		if err := harness.tester.Click("已排队 1 条"); err != nil {
			t.Fatalf("reopen queue popover: %v", err)
		}
		harness.tester.Frame()
	}
	if err := harness.tester.Click("清空全部"); err != nil {
		t.Fatalf("clear queue: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已排队")
	if queued := harness.chat.queueFor("sess-run"); len(queued) != 0 {
		t.Fatalf("clear-all left backend entries: %+v", queued)
	}
	// A cleared inbox is not sent when the run later settles.
	harness.flipRunning(t, false)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("cleared inbox still sent: %v", sends)
	}
}

// TestQueueErrorsStayVisible covers both inbox failures: an enqueue failure
// keeps the draft and reports through the composer; a removal failure shows in
// the popover and leaves the entry in place.
func TestQueueErrorsStayVisible(t *testing.T) {
	harness := startQueueUI(t, "true")

	harness.chat.setEnqueueError(errors.New("inbox full"))
	harness.typeQueuePrompt(t, "排队", "会失败")
	requireHasText(t, harness.tester, "发送失败：inbox full")
	if queued := harness.chat.queueFor("sess-run"); len(queued) != 0 {
		t.Fatalf("failed enqueue left an entry: %+v", queued)
	}
	// The draft is kept: it is delivered once the run ends.
	harness.chat.setEnqueueError(nil)
	harness.flipRunning(t, false)
	harness.tester.Frame()
	harness.tester.Key(0, ui.KeyEnter)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 1 || sends[0] != "sess-run:会失败" {
		t.Fatalf("kept draft after a failed enqueue = %v", sends)
	}

	// Removal failure: the row stays and the popover says why.
	harness.flipRunning(t, true)
	harness.typeQueuePrompt(t, "排队", "移除失败")
	requireHasText(t, harness.tester, "已排队 1 条")
	harness.chat.setRemoveError(errors.New("inbox locked"))
	if err := harness.tester.Click("已排队 1 条"); err != nil {
		t.Fatalf("open queue popover: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("取消排队"); err != nil {
		t.Fatalf("remove queued row: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "移除排队消息失败：inbox locked")
	if queued := harness.chat.queueFor("sess-run"); len(queued) != 1 {
		t.Fatalf("failed remove dropped the backend entry: %+v", queued)
	}
}

// TestQueueWithoutBackendKeepsConflictBehavior: a ChatService without
// PromptQueue shows no queue control while running and Enter never sends.
func TestQueueWithoutBackendKeepsConflictBehavior(t *testing.T) {
	harness := startQueueUIWithoutBackend(t, func() *fakeChat {
		chat := newFakeChat()
		chat.addSession(contracts.ChatSession{ID: "sess-run", Title: "Queue session", Running: true,
			Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "开始"}}}, true)
		return chat
	}())
	requireHasText(t, harness.tester, "输入消息…")
	requireHasText(t, harness.tester, "停止")
	requireNoExactText(t, harness.tester, "排队")
	requireHasText(t, harness.tester, "回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送")

	// Enter while running only inserts a newline; the draft is kept and the
	// backend never sees a Send for it.
	rect, ok := harness.tester.Find("停止")
	if !ok {
		t.Fatalf("stop control missing; texts = %v", harness.tester.Texts())
	}
	harness.tester.ClickAt(rect.X-120, rect.Y-50)
	harness.tester.Frame()
	harness.tester.Type("没有队列后端")
	harness.tester.Frame()
	harness.tester.Key(0, ui.KeyEnter)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("Enter sent while running without a queue backend: %v", sends)
	}
	// The run ends: the kept draft is delivered normally.
	harness.flipRunning(t, false)
	harness.tester.Frame()
	harness.tester.Key(0, ui.KeyEnter)
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 1 || sends[0] != "sess-run:没有队列后端" {
		t.Fatalf("kept draft after a run without a queue backend = %v", sends)
	}
}

// TestQueuePerSessionAndOwnerDrain: the badge belongs to the session it was
// typed in; switching away hides (never drops) it, and the backend drain goes
// to its owner even while another conversation is on screen.
func TestQueuePerSessionAndOwnerDrain(t *testing.T) {
	harness := startQueueUI(t, "true")
	harness.chat.addSession(contracts.ChatSession{ID: "sess-other", Title: "Other"}, false)
	harness.typeQueuePrompt(t, "排队", "给第一个会话")
	requireHasText(t, harness.tester, "已排队 1 条")

	if err := harness.chat.SetActiveSession("sess-other"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	requireNoText(t, harness.tester, "已排队")

	if err := harness.chat.SetActiveSession("sess-run"); err != nil {
		t.Fatalf("SetActiveSession back: %v", err)
	}
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	requireHasText(t, harness.tester, "已排队 1 条")

	// Leave again, settle its run: the backend drains it into its owner.
	if err := harness.chat.SetActiveSession("sess-other"); err != nil {
		t.Fatalf("SetActiveSession away: %v", err)
	}
	harness.flipRunning(t, false)
	if !harness.chat.drainQueuedPrompt("sess-run") {
		t.Fatal("fake backend did not drain the inbox")
	}
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	sends := harness.chat.recordedSends()
	if len(sends) != 1 || sends[0] != "sess-run:给第一个会话" {
		t.Fatalf("queue drain = %v, want it sent into sess-run only", sends)
	}

	if err := harness.chat.SetActiveSession("sess-run"); err != nil {
		t.Fatalf("SetActiveSession owner: %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已排队")
}

// TestQueueTOCTOUAbsorbedByInbox: the run starts between the frame's read model
// and Send; the running conflict is absorbed into the backend inbox instead of
// surfacing a spurious send error.
func TestQueueTOCTOUAbsorbedByInbox(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-idle", Title: "Idle",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "done"}}}, true)
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()

	chat.setRaceToRunning()
	rect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(rect.X-120, rect.Y-50)
	tester.Frame()
	tester.Type("竞态发送")
	tester.Frame()
	if err := tester.Click("发送"); err != nil {
		t.Fatalf("click 发送: %v", err)
	}
	tester.Frame()

	if queued := chat.queueFor("sess-idle"); len(queued) != 1 || queued[0].Text != "竞态发送" {
		t.Fatalf("TOCTOU prompt was not enqueued: %+v", queued)
	}
	if sends := chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("TOCTOU prompt was recorded as sent: %v", sends)
	}
	requireNoText(t, tester, "发送失败")
}
