package session_test

// Tests for multi-step assistant turns: consecutive finished tool steps fold
// into one process summary with only the final result expanded, and the latest
// reply carries a bottom copy button.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestAssistantStepsFoldIntoOneTurn covers the one-row turn: three finished
// assistant steps with tools become a single 过程 summary (combined count), the
// intermediate replies stay folded, only the final result shows, and expanding
// restores every step's timeline.
func TestAssistantStepsFoldIntoOneTurn(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	h := newHarness(t, foldChat(
		contracts.ChatMessage{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
		contracts.ChatMessage{
			ID: "a1", Role: contracts.ChatRoleAssistant, Text: "第一步结果",
			Reasoning: "先想一想",
			Tools:     []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "ok", Summary: "ls"}},
			Usage:     &contracts.MessageUsage{DurationMS: 1000, TotalTokens: 1000},
			CreatedAt: base,
		},
		contracts.ChatMessage{
			ID: "a2", Role: contracts.ChatRoleAssistant, Text: "第二步结果",
			Tools: []contracts.ToolCall{{ID: "t2", Name: "read_file", Status: "ok", Summary: "main.go"}},
			Usage: &contracts.MessageUsage{DurationMS: 1000, TotalTokens: 1000}, CreatedAt: base,
		},
		contracts.ChatMessage{
			ID: "a3", Role: contracts.ChatRoleAssistant, Text: "最终结果",
			Usage: &contracts.MessageUsage{DurationMS: 1000, TotalTokens: 1000}, CreatedAt: base,
		},
	))
	tester := h.tester()

	summary := "过程 · 思考 · 2 个工具调用"
	h.assertTexts(tester, summary, "最终结果", "用时 3.0s · 3K tokens")
	h.assertNoTexts(tester, "第一步结果", "第二步结果", "运行命令", "读取文件")

	h.click(tester, summary)
	h.assertTexts(tester, "第一步结果", "第二步结果", "运行命令", "读取文件", "最终结果")
}

// TestLatestReplyHasBottomCopyButton covers the latest reply's bottom copy
// affordance: one labeled 复制 button under the answer, copying the reply text.
func TestLatestReplyHasBottomCopyButton(t *testing.T) {
	h := newHarness(t, foldChat(
		contracts.ChatMessage{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
		contracts.ChatMessage{
			ID: "a1", Role: contracts.ChatRoleAssistant, Text: "最终答复",
			Reasoning: "想想",
			Tools:     []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "ok", Summary: "ls"}},
		},
	))
	tester := h.tester()
	if _, ok := tester.AccessByRole(ui.RoleButton, "复制回复"); !ok {
		t.Fatalf("latest reply has no bottom copy button; texts = %v", tester.Texts())
	}
	if !tester.HasText("复制") {
		t.Fatalf("bottom copy button shows no visible label; texts = %v", tester.Texts())
	}
	if err := tester.Click("复制"); err != nil {
		t.Fatalf("click bottom copy button: %v; texts = %v", err, tester.Texts())
	}
	if got := tester.Clipboard(); got != "最终答复" {
		t.Fatalf("clipboard = %q, want the reply text", got)
	}
}
