package session_test

// The transcript reads in event order: text before a tool call stays above its
// card, later text below it (ToolCall.TextOffset). The explicit
// ChatMessage.ToolTimelineKnown flag says the offsets were recorded for a live
// turn; messages that predate it keep the old whole-text-then-cards fallback.

import (
	"strings"
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
		ID:                "m1",
		Role:              contracts.ChatRoleAssistant,
		Text:              before + after,
		ToolTimelineKnown: true,
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", Summary: "echo hi", TextOffset: len(before)},
		},
	}
	h := newVerboseWorkStepsHarness(t, timelineChat(message))
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
		ID:                "m1",
		Role:              contracts.ChatRoleAssistant,
		Text:              before + after,
		ToolTimelineKnown: true,
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", TextOffset: len(before)},
			{ID: "t2", Name: "read_file", Status: "ok", TextOffset: len(before)},
		},
	}
	h := newHarness(t, timelineChat(message))
	tester := h.tester()
	// Two calls with no reasoning fold the process by default; open the summary
	// to read the timeline order.
	h.click(tester, "过程 · 2 个工具调用")
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
	h := newVerboseWorkStepsHarness(t, timelineChat(message))
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

// TestAssistantTimelineLiveZeroOffsetsKeepCallsFirst is the reported-bug
// regression: a live turn whose model called tools BEFORE writing any prose has
// every TextOffset == 0. With the explicit live-timeline flag that is an
// ordered timeline ("all calls happened before the text"), not a legacy message,
// so the tool rows must render above the reply.
func TestAssistantTimelineLiveZeroOffsetsKeepCallsFirst(t *testing.T) {
	message := contracts.ChatMessage{
		ID:                "m1",
		Role:              contracts.ChatRoleAssistant,
		Text:              "REPLY-AFTER-TOOLS",
		ToolTimelineKnown: true,
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", Summary: "go build ./..."},
			{ID: "t2", Name: "read_file", Status: "ok"},
		},
	}
	h := newHarness(t, timelineChat(message))
	tester := h.tester()
	// Two calls fold the process by default; expand it to read the order.
	h.click(tester, "过程 · 2 个工具调用")
	h.assertTexts(tester, "REPLY-AFTER-TOOLS", "运行命令", "读取文件")
	first, ok1 := tester.Find("运行命令")
	second, ok2 := tester.Find("读取文件")
	reply, ok3 := tester.Find("REPLY-AFTER-TOOLS")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("timeline nodes missing; texts = %v", tester.Texts())
	}
	if first.Y+first.H > second.Y+1 {
		t.Fatalf("tool rows out of call order: 运行命令 %+v 读取文件 %+v", first, second)
	}
	if reply.Y < second.Y+second.H {
		t.Fatalf("reply at %+v is not below the tool rows (last row %+v)", reply, second)
	}
}

// TestAssistantTimelineFlagFalseKeepsLegacyOrder locks the gate: an explicit
// false flag keeps the whole-text-then-cards fallback even when some offsets
// were recorded, because only live turns certify their timeline.
func TestAssistantTimelineFlagFalseKeepsLegacyOrder(t *testing.T) {
	message := contracts.ChatMessage{
		ID:   "m1",
		Role: contracts.ChatRoleAssistant,
		Text: "LEGACY-TEXT\n\nTAIL",
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", TextOffset: len("LEGACY-TEXT")},
		},
	}
	h := newVerboseWorkStepsHarness(t, timelineChat(message))
	tester := h.tester()
	h.assertTexts(tester, "LEGACY-TEXT", "TAIL", "运行命令")
	text, ok1 := tester.Find("LEGACY-TEXT")
	tail, ok2 := tester.Find("TAIL")
	card, ok3 := tester.Find("运行命令")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("legacy nodes missing; texts = %v", tester.Texts())
	}
	if text.Y+text.H > card.Y || tail.Y+tail.H > card.Y {
		t.Fatalf("legacy text at %+v/%+v not wholly above the card at %+v", text, tail, card)
	}
}

func TestShortToolResultRenderedOnce(t *testing.T) {
	message := contracts.ChatMessage{
		ID:                "m1",
		Role:              contracts.ChatRoleAssistant,
		Text:              "DONE",
		ToolTimelineKnown: true,
		Tools: []contracts.ToolCall{{
			ID: "t1", Name: "bash", Status: "ok",
			Summary: "line one\nline two with a fairly long result to preview", TextOffset: len("DONE"),
		}},
	}
	h := newVerboseWorkStepsHarness(t, timelineChat(message))
	tester := h.tester()
	h.assertTexts(tester, "运行命令")
	// The short result starts expanded (H15); the open panel is the one place
	// the result reads, so the trigger must not repeat it as a preview.
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok || !info.Expanded {
		t.Fatalf("short result disclosure: %+v found %v", info, ok)
	}
	openCount := 0
	for _, text := range tester.Texts() {
		if strings.Contains(text, "line one") {
			openCount++
		}
	}
	if openCount != 1 {
		t.Fatalf("short tool result rendered %d times while open, want once; texts = %v", openCount, tester.Texts())
	}
	// Folded, the row shows the single-line preview (line breaks collapsed).
	h.click(tester, "运行命令")
	folded := ""
	for _, text := range tester.Texts() {
		if strings.Contains(text, "line one line two") {
			folded = text
		}
	}
	if folded != "line one line two with a fairly long result to preview" {
		t.Fatalf("folded preview = %q, want the one-line summary; texts = %v", folded, tester.Texts())
	}
}
