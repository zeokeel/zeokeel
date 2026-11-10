package session_test

// Tests for the session screen's English/Chinese copy: locale changes apply on
// the next frame (tab strip, header menu, composer, empty states, trajectory,
// memory, running states and export status), while zh stays the default.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSessionI18nSwitchToEnglishAndBack(t *testing.T) {
	exportTitle := ""
	setExportSaver(t, func(title, defaultName, content string) (string, error) {
		exportTitle = title
		return "/tmp/export.md", nil
	})
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "会话一",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: "user", Text: "你好"},
			{ID: "m2", Role: "assistant", Text: "你好呀", Reasoning: "因为需要回复"},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	// Default: the existing Chinese copy is unchanged.
	h.assertTexts(tester, "对话", "轨迹", "记忆", "会话操作", "输入消息…", "发送", "思考")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester,
		"Chat", "Trajectory", "Memory", "Conversation actions",
		"Ask ZeoKeel…", "Send", "Reasoning")

	// The header overflow menu is translated too.
	openConversationActions(t, h, tester)
	h.assertTexts(tester, "Copy session ID", "Copy conversation", "Export Markdown")
	h.click(tester, "Copy session ID")
	if got := tester.Clipboard(); got != "sess-1" {
		t.Fatalf("clipboard = %q, want sess-1", got)
	}

	// Export dialog title uses English copy: the seam receives it as its first
	// argument (the success feedback is a toast).
	openConversationActions(t, h, tester)
	h.click(tester, "Export Markdown")
	h.assertNoTexts(tester, "导出失败", "Export failed")
	if exportTitle != "Export conversation" {
		t.Fatalf("export dialog title = %q, want Export conversation", exportTitle)
	}

	// The trajectory and memory panes' empty states are translated.
	h.click(tester, "Trajectory")
	h.assertTexts(tester, "No tool calls in this conversation yet.")
	h.click(tester, "Memory")
	h.assertTexts(tester, "Memory backend not enabled")
	// The tab strip keeps the focus: Home returns to the first tab (clicking
	// the tab label is ambiguous with the page title).
	tester.Key(0, ui.KeyHome)
	tester.Frame()
	h.assertTexts(tester, "Send")

	// Switching back restores the Chinese copy on the next frame.
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "对话", "轨迹", "记忆", "会话操作", "输入消息…", "发送", "思考")
}

func TestSessionI18nEmptyHero(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-empty", Title: "空会话"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "开始新对话", "还没有消息，开始一段新对话吧。", "新对话")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester,
		"Start a new conversation",
		"No messages yet. Start a new conversation.",
		"New conversation",
		"Ask ZeoKeel…", "Send")
	h.assertNoTexts(tester, "开始新对话")
}

func TestSessionI18nRunningAndToolStatus(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:      "sess-run",
		Title:   "运行中会话",
		Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: "user", Text: "跑一下"},
			{ID: "m2", Role: "assistant", Text: "好的", Reasoning: "先思考", Tools: []contracts.ToolCall{
				{ID: "t1", Name: "mystery_tool", Status: "running"},
			}},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "运行中", "停止", "思考", "运行中")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Running", "Stop", "Reasoning")
	h.assertNoTexts(tester, "停止")
}
