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

func TestQueueDisabledWhileRunningKeepsComposerDisabled(t *testing.T) {
	h := newPermissionHarness(t, runningChat(), sendWhileRunningSettings(false))
	tester := h.tester()
	h.assertNoTexts(tester, "输入消息…", "发送")
	h.assertTexts(tester, "停止", "正在生成回复…")
}

func TestQueueWhileRunningBadValueIsOff(t *testing.T) {
	settings := newPermissionSettings(map[string]string{contracts.SettingSendWhileRunning: "bogus"})
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	h.assertNoTexts(tester, "输入消息…", "发送")
}

func TestQueueWhileRunningEnqueuesAndSendsAfterRun(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	h.assertTexts(tester, "输入消息…", "发送", "停止")

	queueFocusComposer(t, tester, "发送")
	tester.Type("第一条")
	tester.Frame()
	h.click(tester, "发送")
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
	h.click(tester, "发送")
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
	queueFocusComposer(t, tester, "发送")
	tester.Type("会失败")
	tester.Frame()
	h.click(tester, "发送")
	h.chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	tester.Frame()
	h.assertTexts(tester, "排队发送失败：agent busy", "已排队 1 条")
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("failed queued send recorded: %v", sends)
	}
}

func TestQueueEmptyDraftNotQueued(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	queueFocusComposer(t, tester, "发送")
	tester.Type("   ")
	tester.Frame()
	h.click(tester, "发送")
	h.assertNoTexts(tester, "已排队 1 条", "已排队 2 条")
}

func TestQueueClearsWhenSessionChanges(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	queueFocusComposer(t, tester, "发送")
	tester.Type("给旧会话")
	tester.Frame()
	h.click(tester, "发送")
	h.assertTexts(tester, "已排队 1 条")

	h.chat.addSession(contracts.ChatSession{
		ID: "sess-2", Title: "另一个会话", Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	// The old session's idle notification arrives before the next frame: the
	// queue must not be sent into the session the user left.
	h.chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "排队测试"})
	tester.Frame()
	h.assertNoTexts(tester, "已排队 1 条")
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("queue sent after switching sessions: %v", sends)
	}
}

func TestQueueEnglishCopy(t *testing.T) {
	settings := sendWhileRunningSettings(true)
	h := newPermissionHarness(t, runningChat(), settings)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	queueFocusComposer(t, tester, "Send")
	tester.Type("queued")
	tester.Frame()
	h.click(tester, "Send")
	h.assertTexts(tester, "1 queued")
}
