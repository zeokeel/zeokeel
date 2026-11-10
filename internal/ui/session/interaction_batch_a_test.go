package session_test

// Frame tests for Batch A of the conversation-core interaction audit:
// failed-turn retry (S-01), cancelled ≠ failed (S-05), Esc/Cmd+. interrupt
// (S-06), actions hidden while streaming (S-11), transient copy feedback and
// 32x32 action targets (S-15), and session-level errors cleared on switch
// (S-16). Each test drives the real product view through ui.NewTester and
// asserts visible labels, accessible states or recorded service calls.

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestCancelledToolRowIsNeutral covers S-05: a user-initiated stop is not a
// tool failure. The row says 已取消 in the neutral color and never repeats the
// raw English cancellation text as a red failure.
func TestCancelledToolRowIsNeutral(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "取消",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{
				ID: "m2", Role: contracts.ChatRoleAssistant, Text: "好的",
				Error: "cancelled", ErrorKind: contracts.MessageErrorKindCancelled,
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "bash", Status: "cancelled",
				}},
			},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "运行命令", "已取消")
	h.assertNoTexts(tester, "失败", "context canceled", "cancelled")
}

// focusComposerForRunningTest focuses the composer while a turn runs: the send
// control is replaced by 停止 then, so the shared focusComposer helper (which
// anchors on 发送) cannot be used.
func focusComposerForRunningTest(t *testing.T, tester *ui.Tester) {
	t.Helper()
	rect, ok := tester.Find("输入消息…")
	if !ok {
		t.Fatalf("composer placeholder missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer TextArea did not take focus; texts = %v", tester.Texts())
	}
}

// TestEscCancelsRunningTurnMenuFirst covers S-06: Esc interrupts a running
// turn, but an open suggestion menu owns Esc first (closing the menu never
// stops the turn). The screen shortcut must respect the same order.
func TestEscCancelsRunningTurnMenuFirst(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "中断", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "写个函数"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "正在写", Streaming: true},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposerForRunningTest(t, tester)

	// The slash draft opens a suggestion menu: the first Esc only dismisses it.
	tester.Type("/")
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if calls := chat.cancels(); len(calls) != 0 {
		t.Fatalf("Esc with a suggestion menu open cancelled the run: %v", calls)
	}
	if session, ok := chat.Session("sess-1"); !ok || !session.Running {
		t.Fatal("the run stopped even though Esc was meant to close the menu")
	}

	// With no menu open, Esc is the interrupt.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if calls := chat.cancels(); len(calls) != 1 || calls[0] != "sess-1" {
		t.Fatalf("Esc did not cancel the running turn: calls = %v", calls)
	}
	if session, ok := chat.Session("sess-1"); !ok || session.Running {
		t.Fatal("Cancel was called but the session still runs")
	}
}

// TestCmdPeriodCancelsRunningTurn covers the keyboard interrupt's second chord
// and the screen scope: Cmd+. stops the turn even when the shortcut is handled
// outside the composer's text hook.
func TestCmdPeriodCancelsRunningTurn(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "中断键", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "写个函数"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "正在写", Streaming: true},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposerForRunningTest(t, tester)
	tester.Key(ui.Cmd, ui.KeyPeriod)
	tester.Frame()
	if calls := chat.cancels(); len(calls) != 1 || calls[0] != "sess-1" {
		t.Fatalf("Cmd+. did not cancel the running turn: calls = %v", calls)
	}
}

// TestStreamingMessageHidesActions covers S-11: a streaming reply has no meta
// row at all (no time, no usage summary, no ⋯ actions); they appear once the
// turn is finished.
func TestStreamingMessageHidesActions(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "流式", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "正在写", Streaming: true,
				Usage: &contracts.MessageUsage{DurationMS: 1000, TotalTokens: 100}},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	if _, ok := tester.Find("消息操作"); ok {
		t.Fatalf("message-actions entry shown on a streaming reply; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "用时 1.0s", "100 tokens")

	chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "流式", Running: false,
		Messages: []contracts.ChatMessage{
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "写好了"},
		},
	})
	tester.Frame()
	h.assertTexts(tester, "消息操作")
	h.click(tester, "消息操作")
	h.assertTexts(tester, "复制消息")
}

// TestCopyWritesClipboardAndToasts covers the compact meta row's copy action:
// choosing 复制消息 in the ⋯ menu writes the native clipboard and raises the
// transient 已复制 toast instead of a resident check state.
func TestCopyWritesClipboardAndToasts(t *testing.T) {
	notifier := &fakeSessionNotifier{}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "复制",
		Messages: []contracts.ChatMessage{
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	h := newToastHarness(t, chat, notifier)
	tester := h.tester()
	h.click(tester, "消息操作")
	h.click(tester, "复制消息")
	if got := tester.Clipboard(); got != "回答" {
		t.Fatalf("clipboard = %q, want the assistant text", got)
	}
	toasts := notifier.toasts()
	if len(toasts) != 1 || toasts[0].text != "已复制" {
		t.Fatalf("copy toasts = %+v, want one 已复制", toasts)
	}
}

// TestSessionErrorsClearedOnSwitch covers S-16: a regenerate/command failure
// belongs to its conversation; switching conversations clears the stale row.
func TestSessionErrorsClearedOnSwitch(t *testing.T) {
	chat := newExtendedChat()
	chat.regenErr = errors.New("backend unavailable")
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "A",
		Messages: []contracts.ChatMessage{
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	chat.addSession(contracts.ChatSession{ID: "sess-2", Title: "B"}, false)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	h.click(tester, "消息操作")
	h.click(tester, "重新生成")
	h.assertTexts(tester, "重新生成失败：backend unavailable")

	if err := chat.SetActiveSession("sess-2"); err != nil {
		t.Fatalf("set active: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "重新生成失败")
}
