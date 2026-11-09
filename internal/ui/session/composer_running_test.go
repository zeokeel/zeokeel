package session_test

// Tests for the composer staying visible and usable while a turn runs (the
// user's "发送消息后，输入框就消失了" report): the TextArea never disappears,
// queueing is the default, and the send position shows 排队 + 停止.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestComposerStaysVisibleWhileRunningWithDefaultQueue covers the default:
// with no ui.sendWhileRunning row (or a malformed one) the composer is editable
// during a run, 排队 + 停止 sit in the send position and the draft can be
// queued without touching chat.Send.
func TestComposerStaysVisibleWhileRunningWithDefaultQueue(t *testing.T) {
	h := newHarness(t, runningChat()) // no settings store: default applies
	tester := h.tester()

	// The TextArea is still laid out and the running controls are visible.
	h.assertTexts(tester, "输入消息…", "排队", "停止")
	h.assertNoTexts(tester, "发送")
	if _, ok := tester.Find("输入消息…"); !ok {
		t.Fatalf("composer input missing while running; texts = %v", tester.Texts())
	}

	// It takes focus and accepts typing while the reply streams.
	h.click(tester, "输入消息…")
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer input did not take focus while running; texts = %v", tester.Texts())
	}
	tester.Type("下一条消息")
	tester.Frame()

	// The typed draft is queueable: the input was editable and its text reached
	// the queue instead of chat.Send.
	h.click(tester, "排队")
	h.assertTexts(tester, "已排队 1 条")
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("running composer called Send: %v", sends)
	}
}

// TestComposerRunningButQueueDisabledExplainsDraft covers the explicit opt-out:
// the input stays present/editable, Enter does not send, Stop is the only send
// action and the muted hint explains that Enter is unavailable and the draft is
// kept.
func TestComposerRunningButQueueDisabledExplainsDraft(t *testing.T) {
	h := newPermissionHarness(t, runningChat(), sendWhileRunningSettings(false))
	tester := h.tester()

	h.assertTexts(tester, "输入消息…", "停止",
		"回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送")
	h.assertNoExactTexts(tester, "发送", "排队")
	if _, ok := tester.Find("输入消息…"); !ok {
		t.Fatalf("composer input missing with queueing off; texts = %v", tester.Texts())
	}

	h.click(tester, "输入消息…")
	tester.Type("保留的草稿")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("Enter sent while queueing is off: %v", sends)
	}
}

// TestComposerQueueDefaultMalformedValueOn pins the normalization: only an
// explicit "false" disables queueing.
func TestComposerQueueDefaultMalformedValueOn(t *testing.T) {
	chat := runningChat()
	settings := newPermissionSettings(map[string]string{contracts.SettingSendWhileRunning: "garbage"})
	h := newPermissionHarness(t, chat, settings)
	tester := h.tester()
	h.assertTexts(tester, "输入消息…", "排队", "停止")
	h.assertNoTexts(tester, "回复生成中，Enter 暂不可用")
}

// --- composer autofocus ------------------------------------------------------

// TestComposerAutofocusesOnOpenSwitchAndSettle covers the keyboard handoff:
// opening a conversation focuses the composer, switching conversations focuses
// it again, and a finished run returns the focus to it.
func TestComposerAutofocusesOnOpenSwitchAndSettle(t *testing.T) {
	chat := runningChat()
	h := newHarness(t, chat) // no settings: queueing default is on
	tester := h.tester()
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer not focused when the conversation opened; texts = %v", tester.Texts())
	}

	// Stopping the run is a run-settle edge: the composer gets the keyboard back.
	h.click(tester, "停止")
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer not focused after the run ended; texts = %v", tester.Texts())
	}

	// Switching conversations focuses the new conversation's composer.
	chat.addSession(contracts.ChatSession{
		ID: "sess-2", Title: "另一个会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, false)
	if err := chat.SetActiveSession("sess-2"); err != nil {
		t.Fatalf("activate sess-2: %v", err)
	}
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer not focused after switching conversations; texts = %v", tester.Texts())
	}
}

// TestComposerAutofocusAfterNewSession covers the new-conversation command: the
// freshly created conversation opens with the composer focused.
func TestComposerAutofocusAfterNewSession(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-old", Title: "旧会话", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
	}}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	var newCommand contracts.CommandSpec
	for _, command := range h.ui.Runtime().Commands() {
		if command.ID == "zeokeel.session.new" {
			newCommand = command
			break
		}
	}
	if newCommand.Run == nil {
		t.Fatalf("new-session command missing; commands = %v", h.ui.Runtime().Commands())
	}
	if err := newCommand.Run(); err != nil {
		t.Fatalf("run new-session command: %v", err)
	}
	tester.Frame()
	if active := chat.ActiveSession(); active == "" || active == "sess-old" {
		t.Fatalf("new session not active: %q", active)
	}
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer not focused after creating a conversation; texts = %v", tester.Texts())
	}
}

// TestComposerKeepsDraftAfterRunEnds covers the draft-preservation contract: a
// message typed while the reply streams stays in the composer when the run ends
// (queued or not) and can be sent with Enter afterwards.
func TestComposerKeepsDraftAfterRunEnds(t *testing.T) {
	chat := runningChat()
	h := newHarness(t, chat) // queueing default on, but the draft is not queued
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "输入消息…", "排队", "停止")

	tester.Type("保留到结束")
	tester.Frame()
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("typing sent a message: %v", sends)
	}

	h.chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "排队测试",
		Messages: []contracts.ChatMessage{{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer lost focus when the run ended; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	sends := chat.sends()
	if len(sends) != 1 || sends[0].prompt != "保留到结束" {
		t.Fatalf("kept draft send = %v, want the typed draft", sends)
	}
}
