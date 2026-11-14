package session_test

// Tests for the backend prompt inbox (DSH inbox parity): a prompt sent while a
// session runs lives in the agent backend's per-session PromptQueue, the UI only
// reads it (badge/popover), removes entries and never sends or drains them
// itself; a stop keeps the queue (the backend owns the drain decision) and a
// conversation switch never shows another session's inbox.

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

func runningChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "开始"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "工作中", Streaming: true},
		},
	}, true)
	return chat
}

func sendWhileRunningSettings(enabled bool) *permissionSettings {
	value := "false"
	if enabled {
		value = "true"
	}
	return newPermissionSettings(map[string]string{"ui.sendWhileRunning": value})
}

// focusComposerInput clicks above the send/queue control to put the keyboard
// into the composer input.
func queueFocusComposer(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("button %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X-120, rect.Y-50)
	tester.Frame()
}

// queuePrompt types one prompt and queues it through the running composer's
// 排队 control.
func queuePrompt(t *testing.T, h *harness, tester *ui.Tester, prompt string) {
	t.Helper()
	queueFocusComposer(t, tester, "排队")
	tester.Type(prompt)
	tester.Frame()
	h.click(tester, "排队")
}

// hasQueueRow reports a popover row for prompt (the row appends its local send
// time, so this checks containment rather than an exact label).
func hasQueueRow(t *testing.T, tester *ui.Tester, prompt string) bool {
	t.Helper()
	for _, text := range tester.Texts() {
		if strings.Contains(text, prompt) && strings.Contains(text, " · ") {
			return true
		}
	}
	return false
}

// --- inbox flow -------------------------------------------------------------

// TestQueueWhileRunningEnqueuesInBackend covers the core move: Enter/排队 while
// a turn runs calls PromptQueue.EnqueuePrompt (never chat.Send), the draft moves
// to the backend inbox, and the composer shows the badge.
func TestQueueWhileRunningEnqueuesInBackend(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	tester := h.tester()
	h.assertTexts(tester, "输入消息…", "排队", "停止")
	h.assertNoTexts(tester, "已排队")

	queuePrompt(t, h, tester, "第一条排队")
	h.assertTexts(tester, "已排队 1 条")
	h.assertNoTexts(tester, "第一条排队") // the draft moved to the queue
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("queued prompt sent while running: %v", sends)
	}
	queued := chat.queueFor("sess-1")
	if len(queued) != 1 || queued[0].Text != "第一条排队" || queued[0].ID == "" {
		t.Fatalf("backend inbox = %+v, want one durable entry", queued)
	}
}

// TestQueueEnterKeyEnqueues: Enter while running queues instead of sending.
func TestQueueEnterKeyEnqueues(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("键盘排队")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "已排队 1 条")
	if queued := chat.queueFor("sess-1"); len(queued) != 1 || queued[0].Text != "键盘排队" {
		t.Fatalf("Enter inbox = %+v", queued)
	}
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("Enter sent while running: %v", sends)
	}
}

// TestQueueSettleDrainShowsNextTurn covers the backend drain: when a run settles
// the agent backend sends the first inbox entry itself. The UI only reflects the
// new state (badge gone, queued prompt now a transcript turn).
func TestQueueSettleDrainShowsNextTurn(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	tester := h.tester()
	queuePrompt(t, h, tester, "后台会自动发出")
	h.assertTexts(tester, "已排队 1 条")

	// The run settles; the backend (simulated) starts the next queued prompt.
	chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	if !chat.drainQueuedPrompt("sess-1") {
		t.Fatal("fake backend did not drain the inbox")
	}
	tester.Frame()
	tester.Frame()

	sends := chat.sends()
	if len(sends) != 1 || sends[0].prompt != "后台会自动发出" || sends[0].sessionID != "sess-1" {
		t.Fatalf("backend drain sends = %v", sends)
	}
	if queued := chat.queueFor("sess-1"); len(queued) != 0 {
		t.Fatalf("inbox not empty after drain: %+v", queued)
	}
	h.assertNoTexts(tester, "已排队")
	h.assertTexts(tester, "后台会自动发出")
}

// TestQueueStopKeepsBackendInbox covers the stop semantics: the UI calls Cancel
// and never clears (or sends) the queued prompts; the inbox stays visible and
// the backend decides what happens to it.
func TestQueueStopKeepsBackendInbox(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	tester := h.tester()
	queuePrompt(t, h, tester, "停后保留")

	h.click(tester, "停止")
	if cancels := chat.cancels(); len(cancels) != 1 || cancels[0] != "sess-1" {
		t.Fatalf("Cancel calls = %v, want [sess-1]", cancels)
	}
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("stopping sent the queued prompt: %v", sends)
	}
	if queued := chat.queueFor("sess-1"); len(queued) != 1 {
		t.Fatalf("stopping cleared the backend inbox: %+v", queued)
	}
	tester.Frame()
	h.assertTexts(tester, "已排队 1 条")

	// The popover no longer claims a paused/resume UI state: it lists the
	// backend inbox and offers remove/clear only.
	h.click(tester, "已排队 1 条")
	h.assertTexts(tester, "排队消息", "停后保留", "取消排队", "清空全部")
	h.assertNoTexts(tester, "已停止：排队消息不会自动发送", "发送排队消息")
}

// TestQueueRemoveOneAndClearAll covers the popover actions: a row removal calls
// RemoveQueuedPrompt (the entry is really gone from the backend) and clear-all
// removes the rest.
func TestQueueRemoveOneAndClearAll(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	tester := h.tester()
	queuePrompt(t, h, tester, "第一条")
	queuePrompt(t, h, tester, "第二条")
	h.assertTexts(tester, "已排队 2 条")

	h.click(tester, "已排队 2 条")
	if _, ok := tester.AccessByRole(ui.RoleList, "排队消息"); !ok {
		t.Fatalf("queued prompts are not a list; texts = %v", tester.Texts())
	}
	if !hasQueueRow(t, tester, "第一条") || !hasQueueRow(t, tester, "第二条") {
		t.Fatalf("popover rows missing (order/time); texts = %v", tester.Texts())
	}
	h.click(tester, "取消排队")
	h.assertTexts(tester, "已排队 1 条")
	queued := chat.queueFor("sess-1")
	if len(queued) != 1 || queued[0].Text != "第二条" {
		t.Fatalf("inbox after removing the first row = %+v", queued)
	}

	// The popover stays open after a row removal; reopen it only if it closed.
	if !tester.HasText("排队消息") {
		h.click(tester, "已排队 1 条")
	}
	h.assertTexts(tester, "清空全部")
	h.click(tester, "清空全部")
	h.assertNoTexts(tester, "已排队")
	if queued := chat.queueFor("sess-1"); len(queued) != 0 {
		t.Fatalf("clear-all left entries: %+v", queued)
	}
	// A cleared inbox stays cleared when the backend later settles the run.
	chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	tester.Frame()
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("cleared inbox still sent: %v", sends)
	}
}

// TestQueueEmptyState hides the badge (and popover) while the inbox is empty,
// and the queueing control remains available for new text.
func TestQueueEmptyState(t *testing.T) {
	h := newPermissionHarness(t, runningChat(), sendWhileRunningSettings(true))
	tester := h.tester()
	h.assertTexts(tester, "排队")
	h.assertNoTexts(tester, "已排队", "排队消息")
	if queued := h.chat.queueFor("sess-1"); len(queued) != 0 {
		t.Fatalf("inbox seeded unexpectedly: %+v", queued)
	}
}

// TestQueuePerSession: the inbox is read per session; the active conversation
// never shows another session's queue, and the backend drain targets its owner.
func TestQueuePerSession(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	tester := h.tester()
	queuePrompt(t, h, tester, "给旧会话")
	h.assertTexts(tester, "已排队 1 条")

	chat.addSession(contracts.ChatSession{
		ID: "sess-2", Title: "另一个会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	tester.Frame()
	h.assertNoTexts(tester, "已排队 1 条")

	chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	chat.drainQueuedPrompt("sess-1")
	tester.Frame()
	sends := chat.sends()
	if len(sends) != 1 || sends[0].sessionID != "sess-1" || sends[0].prompt != "给旧会话" {
		t.Fatalf("backend drain after switching = %v, want it sent into sess-1", sends)
	}

	if err := chat.SetActiveSession("sess-1"); err != nil {
		t.Fatalf("activate sess-1: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "给旧会话")
	h.assertNoTexts(tester, "已排队 1 条")
}

// TestQueueBackendErrors covers the two backend failures the UI can face:
// enqueue keeps the draft and reports through the composer; remove reports in
// the popover without dropping other rows.
func TestQueueBackendErrors(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	tester := h.tester()

	chat.setEnqueueError(errors.New("inbox full"))
	queuePrompt(t, h, tester, "会失败")
	h.assertTexts(tester, "发送失败：inbox full")
	if queued := chat.queueFor("sess-1"); len(queued) != 0 {
		t.Fatalf("failed enqueue left an entry: %+v", queued)
	}
	// The draft is kept for a retry: once the run is over Enter delivers it.
	chat.setEnqueueError(nil)
	chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if sends := chat.sends(); len(sends) != 1 || sends[0].prompt != "会失败" {
		t.Fatalf("kept draft after a failed enqueue = %v", sends)
	}

	chat.setEnqueueError(nil)
	queuePrompt(t, h, tester, "会被移除失败保护")
	h.assertTexts(tester, "已排队 1 条")
	chat.setRemoveError(errors.New("inbox locked"))
	h.click(tester, "已排队 1 条")
	h.click(tester, "取消排队")
	h.assertTexts(tester, "移除排队消息失败：inbox locked")
	if queued := chat.queueFor("sess-1"); len(queued) != 1 {
		t.Fatalf("failed remove dropped the entry: %+v", queued)
	}
}

// TestQueueTOCTOUFallsBackToEnqueue: the run starts between the frame's read
// model and Send; the running conflict is absorbed by the backend inbox instead
// of showing a spurious send failure.
func TestQueueTOCTOUFallsBackToEnqueue(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-idle", Title: "空闲会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	chat.setRaceToRunning()
	queueFocusComposer(t, tester, "发送")
	tester.Type("竞态发送")
	tester.Frame()
	h.click(tester, "发送")
	tester.Frame()

	if queued := chat.queueFor("sess-idle"); len(queued) != 1 || queued[0].Text != "竞态发送" {
		t.Fatalf("TOCTOU prompt was not enqueued: %+v", queued)
	}
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("TOCTOU prompt was recorded as sent: %v", sends)
	}
	h.assertNoTexts(tester, "发送失败")
}

// TestQueueStoredOptOutIgnored pins the fixed behavior behind the move: the
// retired ui.sendWhileRunning row never disables the backend inbox.
func TestQueueStoredOptOutIgnored(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(false))
	tester := h.tester()
	h.assertTexts(tester, "输入消息…", "排队", "停止")
	queuePrompt(t, h, tester, "仍然排队")
	h.assertTexts(tester, "已排队 1 条")
	if queued := chat.queueFor("sess-1"); len(queued) != 1 {
		t.Fatalf("inbox = %+v, want the queued prompt", queued)
	}
}

// chatWithoutQueue exposes only contracts.ChatService, hiding the fake's
// PromptQueue half to model a host/backend without the inbox.
type chatWithoutQueue struct{ contracts.ChatService }

// TestQueueWithoutBackendKeepsConflictBehavior: a ChatService without
// PromptQueue shows no queue control while running and never sends through
// Enter; the draft is kept for the next idle moment.
func TestQueueWithoutBackendKeepsConflictBehavior(t *testing.T) {
	chat := runningChat()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chatWithoutQueue{ChatService: chat})
	activate(t, ctx, session.Module(), session.Config{})
	h := &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "输入消息…", "停止", "回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送")
	h.assertNoExactTexts(tester, "排队")
	queueFocusComposer(t, tester, "停止")
	tester.Type("没有队列后端")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("Enter sent while running without a queue backend: %v", sends)
	}
	// The composer keeps the draft: once the run ends Enter delivers it.
	chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m3", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if sends := chat.sends(); len(sends) != 1 || sends[0].prompt != "没有队列后端" {
		t.Fatalf("draft after a run without a queue backend = %v", sends)
	}
}

// TestQueueEnglishCopy covers the localized badge/popover copy.
func TestQueueEnglishCopy(t *testing.T) {
	chat := runningChat()
	h := newPermissionHarness(t, chat, sendWhileRunningSettings(true))
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	queueFocusComposer(t, tester, "Queue")
	tester.Type("queued")
	tester.Frame()
	h.click(tester, "Queue")
	h.assertTexts(tester, "1 queued")
	if queued := chat.queueFor("sess-1"); len(queued) != 1 {
		t.Fatalf("English inbox = %+v", queued)
	}
	// The popover title and row action are localized too.
	h.click(tester, "1 queued")
	h.assertTexts(tester, "Queued messages", "Cancel queued")
}
