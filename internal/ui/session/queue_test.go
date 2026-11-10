package session_test

// Tests for queueing prompts while a turn is running (ui.sendWhileRunning).

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
	return newPermissionSettings(map[string]string{contracts.SettingSendWhileRunning: value})
}

// focusComposer clicks the text area above the send button.
func queueFocusComposer(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("button %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X-120, rect.Y-50)
	tester.Frame()
}

// TestQueueDisabledWhileRunningKeepsComposerVisible covers the explicit opt-out:
// the TextArea stays visible and editable, Enter does not send (it inserts a
// newline), only Stop is in the send position, and the muted hint says the
// draft is kept for after the reply.
func TestQueueDisabledWhileRunningKeepsComposerVisible(t *testing.T) {
	h := newPermissionHarness(t, runningChat(), sendWhileRunningSettings(false))
	tester := h.tester()
	h.assertTexts(tester, "输入消息…", "停止",
		"回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送")
	h.assertNoExactTexts(tester, "发送")
	if got := exactTextCount(tester.Texts(), "排队"); got != 0 {
		t.Fatalf("queue control rendered while the setting is off; texts = %v", tester.Texts())
	}

	// The draft is editable and Enter must not call chat.Send.
	queueFocusComposer(t, tester, "停止")
	tester.Type("先写好")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("Enter sent while the queue is disabled: %v", sends)
	}

	// When the reply ends the kept draft is still there: Enter sends it.
	h.chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	tester.Frame()
	h.assertTexts(tester, "发送")
	h.assertNoExactTexts(tester, "排队")
	h.assertNoTexts(tester, "回复生成中")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	sends := h.chat.sends()
	if len(sends) != 1 || sends[0].prompt != "先写好" {
		t.Fatalf("kept draft not sent after the run ended: sends = %v", sends)
	}
}

// TestQueueWhileRunningDefaultOn covers the new default: missing/empty/malformed
// rows mean queueing is enabled, so the running composer shows 排队 and the hint
// about Enter being unavailable does not appear.
func TestQueueWhileRunningDefaultOn(t *testing.T) {
	for _, values := range []map[string]string{
		nil,
		{contracts.SettingSendWhileRunning: ""},
		{contracts.SettingSendWhileRunning: "bogus"},
	} {
		h := newPermissionHarness(t, runningChat(), newPermissionSettings(values))
		tester := h.tester()
		h.assertTexts(tester, "输入消息…", "排队", "停止")
		h.assertNoTexts(tester, "回复生成中")
	}
}

func TestQueueWhileRunningEnqueuesAndSendsAfterRun(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	h.assertTexts(tester, "输入消息…", "排队", "停止")

	queueFocusComposer(t, tester, "排队")
	tester.Type("第一条")
	tester.Frame()
	h.click(tester, "排队")
	h.assertTexts(tester, "已排队 1 条")
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("queued prompt sent while running: %v", sends)
	}

	// Finish the run: the first queued prompt is sent.
	h.chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	tester.Frame()
	sends := h.chat.sends()
	if len(sends) != 1 || sends[0].prompt != "第一条" || sends[0].sessionID != "sess-1" {
		t.Fatalf("sends after first run = %v", sends)
	}

	// The queued send starts another run; queue the second prompt and finish it.
	queueFocusComposer(t, tester, "停止")
	tester.Type("第二条")
	tester.Frame()
	h.click(tester, "排队")
	h.assertTexts(tester, "已排队 1 条")
	h.chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m3", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	tester.Frame()
	sends = h.chat.sends()
	if len(sends) != 2 || sends[0].prompt != "第一条" || sends[1].prompt != "第二条" {
		t.Fatalf("sends after second run = %v", sends)
	}
}

func TestQueueSendFailureKeepsPromptAndShowsError(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	h.chat.sendErr = errors.New("agent busy")
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("会失败")
	tester.Frame()
	h.click(tester, "排队")
	h.chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	tester.Frame()
	h.assertTexts(tester, "排队发送失败：agent busy", "已排队 1 条")
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("failed queued send recorded: %v", sends)
	}
}

// TestQueueSendFailureDoesNotRetryEveryFrame is the S-01 regression: a failed
// queued delivery pauses the queue, and the screen's frame-time fallback must
// not re-send the same prompt every frame (that was a self-sustaining redraw +
// Send loop). The explicit 重试 action is the only retry path, and it clears
// the pause and the error.
func TestQueueSendFailureDoesNotRetryEveryFrame(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	h.chat.sendErr = errors.New("agent busy")
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("会失败")
	tester.Frame()
	h.click(tester, "排队")
	h.chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	tester.Frame()
	if got := h.chat.attempts(); got != 1 {
		t.Fatalf("Send attempts after the failed delivery = %d, want 1", got)
	}
	h.assertTexts(tester, "排队发送失败：agent busy", "已排队 1 条")
	// Later frames must not retry: the paused queue and its error keep the
	// frame fallback quiet.
	for frame := 0; frame < 4; frame++ {
		tester.Frame()
	}
	if got := h.chat.attempts(); got != 1 {
		t.Fatalf("Send attempts after 4 more frames = %d, want 1 (no automatic retry)", got)
	}
	// 重试 is the explicit recovery: the backend recovers, the retry resumes the
	// paused queue and delivers the prompt exactly once.
	h.chat.sendErr = nil
	h.click(tester, "重试")
	tester.Frame()
	if got := h.chat.attempts(); got != 2 {
		t.Fatalf("Send attempts after 重试 = %d, want 2", got)
	}
	if sends := h.chat.sends(); len(sends) != 1 || sends[0].prompt != "会失败" {
		t.Fatalf("delivered sends after 重试 = %v, want [会失败]", sends)
	}
	h.assertNoTexts(tester, "排队发送失败：agent busy", "已排队 1 条")
}

func TestQueueEmptyDraftNotQueued(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("   ")
	tester.Frame()
	h.click(tester, "排队")
	h.assertNoTexts(tester, "已排队 1 条", "已排队 2 条")
}

// TestStopPausesQueuedPrompts covers the stop semantics fix: pressing 停止 must
// stop the run, not immediately start the next queued prompt. The queue is kept
// for review and the popover offers an explicit 发送排队消息 action.
func TestStopPausesQueuedPrompts(t *testing.T) {
	h := newPermissionHarness(t, runningChat(), sendWhileRunningSettings(true))
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("停后再说")
	tester.Frame()
	h.click(tester, "排队")
	h.assertTexts(tester, "已排队 1 条")

	h.click(tester, "停止")
	if cancels := h.chat.cancels(); len(cancels) != 1 || cancels[0] != "sess-1" {
		t.Fatalf("Cancel calls = %v, want [sess-1]", cancels)
	}
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("stopping started the queued prompt automatically: %v", sends)
	}
	h.assertTexts(tester, "已排队 1 条")

	// The queue popover says it is paused and offers the explicit send.
	h.click(tester, "已排队 1 条")
	h.assertTexts(tester, "排队消息", "已停止：排队消息不会自动发送，可手动发送或取消", "发送排队消息")
	h.click(tester, "发送排队消息")
	sends := h.chat.sends()
	if len(sends) != 1 || sends[0].prompt != "停后再说" {
		t.Fatalf("explicit queued send = %v, want the queued prompt", sends)
	}
	h.assertNoTexts(tester, "已排队 1 条")
}

// TestQueueStaysWithItsSession covers the data-loss fix: switching conversations
// keeps the queued prompts of the session they were typed in, and the module
// subscription flushes them when that session's run ends even while the user is
// looking at another conversation.
func TestQueueStaysWithItsSession(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("给旧会话")
	tester.Frame()
	h.click(tester, "排队")
	h.assertTexts(tester, "已排队 1 条")

	h.chat.addSession(contracts.ChatSession{
		ID: "sess-2", Title: "另一个会话", Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	tester.Frame()
	// The queue belongs to sess-1: no badge on another conversation's composer.
	h.assertNoTexts(tester, "已排队 1 条")

	// The old session settles while sess-2 is on screen: the module-level
	// subscription sends the queued prompt into *its* session, not the visible
	// one and not nowhere.
	h.chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	tester.Frame()
	sends := h.chat.sends()
	if len(sends) != 1 || sends[0].sessionID != "sess-1" || sends[0].prompt != "给旧会话" {
		t.Fatalf("queued prompt after switching sessions = %v, want it sent into sess-1", sends)
	}

	// Returning to sess-1 shows its user message and no leftover queue badge.
	if err := h.chat.SetActiveSession("sess-1"); err != nil {
		t.Fatalf("activate sess-1: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "给旧会话")
	h.assertNoTexts(tester, "已排队 1 条")
}

func TestQueueEnglishCopy(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	queueFocusComposer(t, tester, "Queue")
	tester.Type("queued")
	tester.Frame()
	h.click(tester, "Queue")
	h.assertTexts(tester, "1 queued")
}

// TestQueuedPromptsCanBeCancelled covers H9: the queued-count badge opens a
// list of the queued prompts, each can be cancelled (it is not sent when the
// run ends) and the whole queue can be cleared.
func TestQueuedPromptsCanBeCancelled(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("第一条")
	tester.Frame()
	h.click(tester, "排队")
	queueFocusComposer(t, tester, "停止")
	tester.Type("第二条")
	tester.Frame()
	h.click(tester, "排队")
	h.assertTexts(tester, "已排队 2 条")

	// The badge opens the queue popover: both prompts are visible, each row has
	// a cancel action and there is a clear-all footer.
	h.click(tester, "已排队 2 条")
	h.assertTexts(tester, "第一条", "第二条", "取消排队", "清空全部")
	h.click(tester, "取消排队")
	h.assertTexts(tester, "已排队 1 条")
	h.assertNoTexts(tester, "第一条")

	// The cancelled prompt is really dropped: only the second is sent.
	h.chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	tester.Frame()
	sends := h.chat.sends()
	if len(sends) != 1 || sends[0].prompt != "第二条" {
		t.Fatalf("sends after cancelling one = %v, want only the second prompt", sends)
	}
}

// TestQueuedPromptsClearAll covers the queue popover's clear-all footer.
func TestQueuedPromptsClearAll(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	queueFocusComposer(t, tester, "排队")
	tester.Type("甲")
	tester.Frame()
	h.click(tester, "排队")
	queueFocusComposer(t, tester, "停止")
	tester.Type("乙")
	tester.Frame()
	h.click(tester, "排队")
	h.click(tester, "已排队 2 条")
	h.click(tester, "清空全部")
	h.assertNoTexts(tester, "已排队")
	h.chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	tester.Frame()
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("cleared queue still sent: %v", sends)
	}
}

// TestQueuePopoverUsesOfficialList covers the queue popover's migration to the
// official ui.List: the queued prompts are list rows named by their prompt
// (list/listitem semantics for assistive technology), and the per-row cancel
// action still empties the queue.
func TestQueuePopoverUsesOfficialList(t *testing.T) {
	h := newPermissionHarness(t, runningChat(), sendWhileRunningSettings(true))
	tester := h.tester()
	for _, prompt := range []string{"第一条排队", "第二条排队"} {
		queueFocusComposer(t, tester, "排队")
		tester.Type(prompt)
		tester.Frame()
		h.click(tester, "排队")
	}
	h.assertTexts(tester, "已排队 2 条")

	h.click(tester, "已排队 2 条")
	if _, ok := tester.AccessByRole(ui.RoleList, "排队消息"); !ok {
		t.Fatalf("queued prompts are not a list; texts = %v", tester.Texts())
	}
	for _, prompt := range []string{"第一条排队", "第二条排队"} {
		if _, ok := tester.AccessByRole(ui.RoleListItem, prompt); !ok {
			t.Fatalf("queued prompt %q is not a list row; texts = %v", prompt, tester.Texts())
		}
	}
	h.click(tester, "取消排队")
	h.assertTexts(tester, "已排队 1 条")
	if _, ok := tester.AccessByRole(ui.RoleListItem, "第一条排队"); ok {
		t.Fatalf("cancelled prompt still a row; texts = %v", tester.Texts())
	}
}
