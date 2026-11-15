package session_test

// Tests for the restored-run interruption notice: the agent backend's read-model
// ChatSession.Interrupted is shown as one quiet notice above the transcript with
// a 在输入框继续 (focus the composer, never replay the turn) and a dismiss
// action; the
// next run (when the backend clears the hint) makes it disappear, and a normal
// session never shows it.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func interruptedSession(id, title string) contracts.ChatSession {
	return contracts.ChatSession{
		ID: id, Title: title, Interrupted: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下发布检查"}},
	}
}

func interruptedChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(interruptedSession("sess-int", "中断会话"), true)
	return chat
}

func TestInterruptedNoticeShownOnRestore(t *testing.T) {
	h := newHarness(t, interruptedChat())
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "上次运行被中断",
		"未重放；请检查工作区状态，然后在输入框继续。", "在输入框继续")
	if _, ok := tester.Access("忽略上次运行中断提示"); !ok {
		t.Fatalf("interrupted notice lacks the dismiss action; texts = %v", tester.Texts())
	}
}

func TestInterruptedContinueFocusesComposer(t *testing.T) {
	h := newHarness(t, interruptedChat())
	tester := h.tester()
	tester.Frame()

	// Move the keyboard focus away from the composer first (a toolbar menu, not
	// the composer input), so the assertion is about the action and not the
	// screen's opening autofocus.
	h.click(tester, "会话设置")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	// The action names its outcome instead of a vague 继续: it hands the user
	// the composer's input and says so in the tooltip too.
	cont, ok := tester.AccessByRole(ui.RoleButton, "在输入框继续")
	if !ok {
		t.Fatalf("interrupted notice lacks the explicit continue-in-composer action; texts = %v", tester.Texts())
	}
	if cont.Description != "把光标移到输入框；发送内容由你决定" {
		t.Fatalf("continue action tooltip = %q, want the visible-feedback explanation", cont.Description)
	}
	h.click(tester, "在输入框继续")
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("在输入框继续 did not focus the composer; texts = %v", tester.Texts())
	}
	// Continuing must not replay the turn or hide the explanation: only the next
	// run (backend clears Interrupted) removes the notice.
	h.assertTexts(tester, "上次运行被中断")
	if sends := h.chat.sends(); len(sends) != 0 {
		t.Fatalf("在输入框继续 replayed a prompt: %v", sends)
	}
}

func TestInterruptedDismissHidesOnlyThatSession(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(interruptedSession("sess-a", "会话 A"), true)
	chat.addSession(interruptedSession("sess-b", "会话 B"), false)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "上次运行被中断")

	h.click(tester, "忽略上次运行中断提示")
	tester.Frame()
	h.assertNoTexts(tester, "上次运行被中断")

	// The dismissal is per session for this app run: another restored session
	// still explains itself, and returning to the dismissed one stays quiet.
	if err := chat.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("activate sess-b: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "上次运行被中断")
	if err := chat.SetActiveSession("sess-a"); err != nil {
		t.Fatalf("activate sess-a: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "上次运行被中断")
}

func TestInterruptedNoticeClearsWithNextRun(t *testing.T) {
	chat := interruptedChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "上次运行被中断")

	// The backend clears the hint when the next run starts (or after a restore
	// refresh that no longer sees an unfinished checkpoint); the notice follows
	// the read model, never its own cached state.
	chat.updateSession(contracts.ChatSession{
		ID: "sess-int", Title: "中断会话", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下发布检查"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "继续处理", Streaming: true},
		},
	})
	tester.Frame()
	h.assertNoTexts(tester, "上次运行被中断")

	chat.updateSession(contracts.ChatSession{
		ID: "sess-int", Title: "中断会话",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下发布检查"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"},
		},
	})
	tester.Frame()
	h.assertNoTexts(tester, "上次运行被中断")
}

func TestInterruptedNoticeAbsentForNormalSession(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-ok", Title: "正常会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "上次运行被中断",
		"未重放；请检查工作区状态，然后在输入框继续。", "在输入框继续", "忽略上次运行中断提示")
}

func TestInterruptedNoticeEnglish(t *testing.T) {
	h := newHarness(t, interruptedChat())
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Last run was interrupted",
		"The turn was not replayed; check the workspace, then continue in the composer.",
		"Continue in the composer")
	if cont, ok := tester.AccessByRole(ui.RoleButton, "Continue in the composer"); !ok ||
		cont.Description != "Puts the cursor in the composer; you decide what to send" {
		t.Fatalf("English continue action = %+v found %v", cont, ok)
	}
	if _, ok := tester.Access("Dismiss the interrupted-run notice"); !ok {
		t.Fatalf("English interrupted notice lacks the dismiss action; texts = %v", tester.Texts())
	}
}
