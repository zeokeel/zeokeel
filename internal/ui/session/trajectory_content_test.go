package session_test

// US-04 (2026-11 audit): the 轨迹 tab must live up to its name — the execution
// trace includes the thinking segments and a reply-body summary, not only the
// tool calls. The tab keeps its name; the pane gains the missing content.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestTrajectoryIncludesReasoningAndBody is the decisive US-04 assertion: a
// two-iteration turn's trajectory lists think(1) → tool → think(2) → body
// summary, each row able to jump back to the conversation.
func TestTrajectoryIncludesReasoningAndBody(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "完整轨迹",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑两轮"},
			twoIterationLiveMessage(),
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "轨迹")
	tester.Frame()

	// The thinking segments appear as the shared reasoning disclosures.
	if n := countReasoningDisclosures(tester, "思考"); n != 2 {
		t.Fatalf("US-04: trajectory shows %d thinking rows, want 2; texts = %v", n, tester.Texts())
	}
	// The reply body appears as a summary row (the full prose stays in the
	// conversation tab; the trajectory carries the preview).
	h.assertTexts(tester, "正文", "FINAL-ANSWER", "读取文件")

	// Chronological order: think1 → tool → think2 → body.
	disclosures := make([]ui.Rect, 0, 2)
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleDisclosure && info.Name == "思考" {
			disclosures = append(disclosures, info.Bounds)
		}
	}
	tool, okTool := tester.Find("读取文件")
	body, okBody := tester.Find("FINAL-ANSWER")
	if !okTool || !okBody || len(disclosures) != 2 {
		t.Fatalf("trajectory parts missing; texts = %v", tester.Texts())
	}
	if !(disclosures[0].Y < tool.Y && tool.Y < disclosures[1].Y && disclosures[1].Y < body.Y) {
		t.Fatalf("US-04: trajectory order wrong: think1=%.1f tool=%.1f think2=%.1f body=%.1f",
			disclosures[0].Y, tool.Y, disclosures[1].Y, body.Y)
	}

	// A thinking row jumps back to its message, like the tool rows do: the jump
	// is the row's trailing action, revealed on hover/focus (VD-17).
	if err := tester.Click("在对话中定位 思考"); err != nil {
		t.Fatalf("thinking row has no locate action: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "FINAL-ANSWER")
	h.assertNoTexts(tester, "会话轨迹")
}

// TestTrajectoryEmptyStateEnglish keeps the empty-state copy translated (the
// i18n switch test's trajectory pane now carries content instead).
func TestTrajectoryEmptyStateEnglish(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "empty trace",
		Messages: []contracts.ChatMessage{{ID: "u1", Role: contracts.ChatRoleUser, Text: "hi"}},
	}, true)
	h := newHarness(t, chat)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Trajectory")
	h.assertTexts(tester, "No execution trace in this conversation yet.")
}

// TestTrajectoryBodyOnlyTurn covers a pure-text turn: the trajectory is not
// empty anymore — it carries the reply summary (the tab name stays honest).
func TestTrajectoryBodyOnlyTurn(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "纯文本轨迹",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "你好"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "你好，有什么可以帮你？"},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "轨迹")
	tester.Frame()

	h.assertTexts(tester, "正文", "你好，有什么可以帮你？")
	h.assertNoTexts(tester, "本次会话还没有工具调用")
}
