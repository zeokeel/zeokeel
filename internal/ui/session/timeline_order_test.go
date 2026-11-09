package session_test

// The transcript reads in event order: text before a tool call stays above its
// card, later text below it (ToolCall.TextOffset). Legacy messages without
// offsets keep the old whole-text-then-cards fallback.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func timelineChat(message contracts.ChatMessage) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:       "sess-1",
		Title:    "时序会话",
		Messages: []contracts.ChatMessage{message},
	}, true)
	return chat
}

func TestAssistantTimelineInterleavesTextAndToolCards(t *testing.T) {
	before := "BEFORE-TEXT"
	after := "AFTER-TEXT"
	message := contracts.ChatMessage{
		ID:   "m1",
		Role: contracts.ChatRoleAssistant,
		Text: before + after,
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", Summary: "echo hi", TextOffset: len(before)},
		},
	}
	h := newHarness(t, timelineChat(message))
	tester := h.tester()

	h.assertTexts(tester, before, after, "运行命令")
	pre, ok := tester.Find(before)
	if !ok {
		t.Fatalf("pre-tool text missing; texts = %v", tester.Texts())
	}
	card, ok := tester.Find("运行命令")
	if !ok {
		t.Fatalf("tool row missing; texts = %v", tester.Texts())
	}
	post, ok := tester.Find(after)
	if !ok {
		t.Fatalf("post-tool text missing; texts = %v", tester.Texts())
	}
	if pre.Y+pre.H > card.Y {
		t.Fatalf("pre-tool text at %+v is not above the tool row at %+v", pre, card)
	}
	if post.Y < card.Y {
		t.Fatalf("post-tool text at %+v is not below the tool row at %+v", post, card)
	}
}

func TestAssistantTimelineKeepsConsecutiveToolsTogether(t *testing.T) {
	before := "START"
	after := "END"
	message := contracts.ChatMessage{
		ID:   "m1",
		Role: contracts.ChatRoleAssistant,
		Text: before + after,
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", TextOffset: len(before)},
			{ID: "t2", Name: "read_file", Status: "ok", TextOffset: len(before)},
		},
	}
	h := newHarness(t, timelineChat(message))
	tester := h.tester()
	h.assertTexts(tester, before, after, "运行命令", "读取文件")
	first, ok1 := tester.Find("运行命令")
	second, ok2 := tester.Find("读取文件")
	post, ok3 := tester.Find(after)
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("timeline nodes missing; texts = %v", tester.Texts())
	}
	if first.Y+first.H > second.Y+1 {
		t.Fatalf("same-offset tools not consecutive: 运行命令 %+v 读取文件 %+v", first, second)
	}
	if post.Y < second.Y {
		t.Fatalf("post-tool text above the second card: text %+v card %+v", post, second)
	}
}

func TestAssistantTimelineLegacyMessagesKeepTextFirst(t *testing.T) {
	// Restored/legacy history has no offsets: text renders first, cards after.
	message := contracts.ChatMessage{
		ID:   "m1",
		Role: contracts.ChatRoleAssistant,
		Text: "LEGACY-TEXT",
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", Summary: "ls"},
		},
	}
	h := newHarness(t, timelineChat(message))
	tester := h.tester()
	h.assertTexts(tester, "LEGACY-TEXT", "运行命令")
	text, ok1 := tester.Find("LEGACY-TEXT")
	card, ok2 := tester.Find("运行命令")
	if !ok1 || !ok2 {
		t.Fatalf("legacy nodes missing; texts = %v", tester.Texts())
	}
	if text.Y+text.H > card.Y {
		t.Fatalf("legacy text at %+v not above the card at %+v", text, card)
	}
}

func TestAssistantTimelineTimelineSummaryPreviewIsQuiet(t *testing.T) {
	message := contracts.ChatMessage{
		ID:   "m1",
		Role: contracts.ChatRoleAssistant,
		Text: "DONE",
		Tools: []contracts.ToolCall{{
			ID: "t1", Name: "bash", Status: "ok",
			Summary: "line one\nline two with a fairly long result to preview", TextOffset: len("DONE"),
		}},
	}
	h := newHarness(t, timelineChat(message))
	tester := h.tester()
	// The row carries the friendly name as its disclosure and a one-line preview
	// with the line breaks folded to spaces (the short result starts expanded
	// per H15).
	h.assertTexts(tester, "运行命令")
	const folded = "line one line two with a fairly long result to preview"
	found := false
	for _, text := range tester.Texts() {
		if text == folded {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("one-line preview %q missing; texts = %v", folded, tester.Texts())
	}
	if info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); !ok || !info.Expanded {
		t.Fatalf("preview row disclosure: %+v found %v", info, ok)
	}
}
