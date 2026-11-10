package session_test

// Tests for the transcript's density refactor (A+B): the compact right-aligned
// meta row (time · usage · ⋯ menu) and the folded process band (one summary row
// for a finished turn's reasoning and tool calls, expandable to the timeline).

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// foldChat is a fake chat whose active conversation holds exactly the given
// messages, so the transcript's ⋯ entries are unambiguous.
func foldChat(messages ...contracts.ChatMessage) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "折叠会话", Messages: messages}, true)
	return chat
}

// foldMessage is a finished assistant turn with reasoning, two tool calls, a
// reply and usage: the shape the process fold is for.
func foldMessage() contracts.ChatMessage {
	return contracts.ChatMessage{
		ID: "m1", Role: contracts.ChatRoleAssistant,
		Text:      "最终答复",
		Reasoning: "先想一想，再运行命令。",
		Usage:     &contracts.MessageUsage{DurationMS: 3200, TotalTokens: 1234},
		Tools: []contracts.ToolCall{
			{ID: "t1", Name: "bash", Status: "ok", Summary: "ls"},
			{ID: "t2", Name: "read_file", Status: "ok", Summary: "main.go"},
		},
	}
}

// TestAssistantProcessFoldsToSummary covers the folded default: one summary row
// replaces the timeline, the reply body and the meta row stay visible, and the
// reasoning/tool rows are one click away.
func TestAssistantProcessFoldsToSummary(t *testing.T) {
	h := newHarness(t, foldChat(foldMessage()))
	tester := h.tester()

	summary := "过程 · 思考 · 2 个工具调用"
	h.assertTexts(tester, summary, "最终答复", "用时 3.2s · 1.2K tokens", "消息操作")
	// The folded timeline is not rendered: no tool rows, no reasoning panel.
	h.assertNoTexts(tester, "运行命令", "读取文件", "先想一想，再运行命令。")
	if info, ok := tester.AccessByRole(ui.RoleDisclosure, summary); !ok || info.Expanded {
		t.Fatalf("process summary disclosure = %+v found %v, want a closed disclosure", info, ok)
	}

	// Expanding renders the full timeline (reasoning row + tool rows + reply),
	// with the reply body rendered once (no extra copy under the summary). Every
	// process row names its type and status, so the reader can tell a reasoning
	// row from a tool row at a glance.
	h.click(tester, summary)
	h.assertTexts(tester, "运行命令", "读取文件", "思考", "最终答复", "完成")
	if got := textsContaining(tester.Texts(), "最终答复"); got != 1 {
		t.Fatalf("expanded timeline renders the reply %d times, want once; texts = %v", got, tester.Texts())
	}

	if info, ok := tester.AccessByRole(ui.RoleDisclosure, summary); !ok || !info.Expanded {
		t.Fatalf("process summary disclosure = %+v found %v, want an open disclosure", info, ok)
	}
	// The reasoning panel is its own disclosure: opening it shows the thinking.
	h.clickExact(tester, "思考")
	h.assertTexts(tester, "先想一想，再运行命令。")

	// Collapsing again folds the timeline; the state survives further frames.
	h.click(tester, summary)
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, summary)
	if tester.HasText("运行命令") {
		t.Fatalf("collapsed process still shows its tool rows; texts = %v", tester.Texts())
	}
}

// TestAssistantProcessFoldedKeepsDecisionCards covers the prominent cards: a
// folded turn still shows the decision tools (ask_user etc.) below the reply, in
// call order, while the ordinary tool rows stay folded.
func TestAssistantProcessFoldedKeepsDecisionCards(t *testing.T) {
	message := foldMessage()
	message.Tools = []contracts.ToolCall{
		{ID: "t1", Name: "bash", Status: "ok", Summary: "ls"},
		{ID: "t2", Name: "ask_user", Status: "ok", Summary: "要继续吗？"},
	}
	h := newHarness(t, foldChat(message))
	tester := h.tester()

	h.assertTexts(tester, "过程 · 思考 · 2 个工具调用", "最终答复", "向用户提问")
	if tester.HasText("运行命令") {
		t.Fatalf("folded turn shows its compact tool row; texts = %v", tester.Texts())
	}
	// The decision card reads after the reply body.
	body, ok1 := tester.Find("最终答复")
	card, ok2 := tester.Find("向用户提问")
	if !ok1 || !ok2 || card.Y < body.Y {
		t.Fatalf("decision card placement = body %+v found %v, card %+v found %v", body, ok1, card, ok2)
	}
}

// TestAssistantProcessStreamingStaysExpanded covers the live turn: it never
// folds, the reasoning is open and streaming, the tool row reads inline and the
// meta row is absent.
func TestAssistantProcessStreamingStaysExpanded(t *testing.T) {
	message := contracts.ChatMessage{
		ID: "m1", Role: contracts.ChatRoleAssistant, Streaming: true,
		Text:      "正在写",
		Reasoning: "先想一想",
		Tools:     []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "running", Summary: "go build"}},
	}
	h := newHarness(t, foldChat(message))
	tester := h.tester()

	h.assertTexts(tester, "思考中…", "先想一想", "运行命令", "正在写", "生成中")
	if tester.HasText("过程 ·") {
		t.Fatalf("streaming turn folded its process; texts = %v", tester.Texts())
	}
	if rect, ok := tester.Find("消息操作"); ok {
		t.Fatalf("streaming turn has a meta row at %+v", rect)
	}
}

// TestAssistantProcessSingleToolStaysInline covers the non-foldable shape: one
// tool with no reasoning keeps its row inline instead of a one-item summary.
func TestAssistantProcessSingleToolStaysInline(t *testing.T) {
	message := contracts.ChatMessage{
		ID: "m1", Role: contracts.ChatRoleAssistant, Text: "查到了",
		Tools: []contracts.ToolCall{{ID: "t1", Name: "read_file", Status: "ok", Summary: "main.go"}},
	}
	h := newHarness(t, foldChat(message))
	tester := h.tester()
	h.assertTexts(tester, "读取文件", "查到了")
	if tester.HasText("过程 ·") {
		t.Fatalf("single-tool turn folded its process; texts = %v", tester.Texts())
	}
}

// TestActivityBandIndentsProcessRows covers the band layout: process rows sit
// Space4 inside the reading column while the reply prose stays at its left edge,
// and the English fold summary uses the same parts.
func TestActivityBandIndentsProcessRows(t *testing.T) {
	message := contracts.ChatMessage{
		ID: "m1", Role: contracts.ChatRoleAssistant, Streaming: true,
		Text:      "正文段落",
		Reasoning: "想一下",
		Tools:     []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "running", Summary: "go build"}},
	}
	h := newHarness(t, foldChat(message))
	tester := h.tester()

	body, ok1 := tester.Find("正文段落")
	tool, ok2 := tester.Find("运行命令")
	reasoning, ok3 := tester.Find("思考中…")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("band nodes missing; texts = %v", tester.Texts())
	}
	if tool.X <= body.X || reasoning.X <= body.X {
		t.Fatalf("activity band not indented: body X=%.1f tool X=%.1f reasoning X=%.1f",
			body.X, tool.X, reasoning.X)
	}
	if tool.X-body.X < 8 {
		t.Fatalf("activity band indent = %.1f DIP, want the Space4 band", tool.X-body.X)
	}
	// The running tool row spells its status out beside the action name.
	h.assertTexts(tester, "运行中")
}

// TestAssistantMetaRowShowsTimeAndCompactUsage pins the always-on meta row: the
// time of day, the compact usage summary and the ⋯ entry share one line, and no
// raw token count appears anywhere.
func TestAssistantMetaRowShowsTimeAndCompactUsage(t *testing.T) {
	created := time.Date(2026, 11, 9, 18, 41, 0, 0, time.Local)
	message := contracts.ChatMessage{
		ID: "m1", Role: contracts.ChatRoleAssistant, Text: "回答", CreatedAt: created,
		Usage: &contracts.MessageUsage{DurationMS: 358800, PromptTokens: 171737,
			CompletionTokens: 1898, TotalTokens: 173635, CachedTokens: 92892},
	}
	h := newHarness(t, foldChat(message))
	tester := h.tester()

	h.assertTexts(tester, "18:41", "用时 358.8s · 173.6K tokens", "消息操作")
	h.assertNoTexts(tester, "173,635", "171,737", "1,898", "92,892")
	// The time keeps the full timestamp in its tooltip.
	if info, ok := tester.Access("18:41"); !ok || info.Description != "2026-11-09 18:41" {
		t.Fatalf("time tooltip = %+v found %v, want the full timestamp", info, ok)
	}
	// The detail submenu stays compact too.
	h.click(tester, "消息操作")
	h.click(tester, "用量明细")
	h.assertTexts(tester, "输入 tokens", "171.7K", "输出 tokens", "1.9K", "缓存 tokens", "92.9K")
	h.assertNoTexts(tester, "173,635", "171,737", "1,898", "92,892")
}

// TestAssistantMetaRowTimeOnlyWithoutActions covers the quiet end state: a turn
// with nothing to copy, rate, regenerate or break down shows only its time.
func TestAssistantMetaRowTimeOnlyWithoutActions(t *testing.T) {
	message := contracts.ChatMessage{
		ID: "m1", Role: contracts.ChatRoleAssistant, Text: "",
		CreatedAt: time.Date(2026, 11, 9, 9, 30, 0, 0, time.Local),
	}
	h := newHarness(t, foldChat(message))
	tester := h.tester()
	h.assertTexts(tester, "09:30")
	if _, ok := tester.Find("消息操作"); ok {
		t.Fatalf("message-actions entry rendered with no available action; texts = %v", tester.Texts())
	}
}

// TestUserMessageMetaUsesMoreMenu covers the user side: the old side-by-side
// copy/edit icons are now one ⋯ menu on the same compact meta line.
func TestUserMessageMetaUsesMoreMenu(t *testing.T) {
	message := contracts.ChatMessage{
		ID: "u1", Role: contracts.ChatRoleUser, Text: "原始问题",
		CreatedAt: time.Date(2026, 11, 9, 17, 5, 0, 0, time.Local),
	}
	h := newHarness(t, foldChat(message))
	tester := h.tester()
	h.assertTexts(tester, "17:05", "用户消息操作")
	h.assertNoTexts(tester, "复制原文", "编辑消息")
	h.click(tester, "用户消息操作")
	h.assertTexts(tester, "复制原文", "编辑消息")
	h.click(tester, "复制原文")
	if got := tester.Clipboard(); got != "原始问题" {
		t.Fatalf("clipboard = %q, want the user text", got)
	}
}

// TestAssistantProcessFoldEnglishCopy pins the localized summary parts.
func TestAssistantProcessFoldEnglishCopy(t *testing.T) {
	h := newHarness(t, foldChat(foldMessage()))
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	summary := "Process · Reasoning · 2 tool calls"
	h.assertTexts(tester, summary)
	if !strings.Contains(summary, "2 tool calls") {
		t.Fatalf("summary = %q", summary)
	}
}

// TestFailedTurnMetaRowHasNoActions covers the failed-turn rule: the error row
// and the labeled retry stay, while the meta row only reports time/usage and
// offers no actions (regenerate must not judge a failed answer).
func TestFailedTurnMetaRowHasNoActions(t *testing.T) {
	message := contracts.ChatMessage{
		ID: "m1", Role: contracts.ChatRoleAssistant, Text: "部分回答",
		Error: "HTTP 401 Unauthorized", ErrorKind: contracts.MessageErrorKindAuth,
		CreatedAt: time.Date(2026, 11, 9, 18, 41, 0, 0, time.Local),
		Usage:     &contracts.MessageUsage{DurationMS: 1200, TotalTokens: 460},
	}
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "失败会话",
		Messages: []contracts.ChatMessage{message}}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "鉴权失败", "重试", "18:41", "用时 1.2s · 460 tokens")
	if _, ok := tester.Find("消息操作"); ok {
		t.Fatalf("failed turn rendered a message-actions entry; texts = %v", tester.Texts())
	}
}
