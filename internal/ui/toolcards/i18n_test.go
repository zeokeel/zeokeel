package toolcards_test

// i18n frame tests for the real tool cards: status labels, the summary
// disclosure and the category hint follow the host locale per frame; the demo
// card keeps its historical raw content by design.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/toolcards"
)

func TestToolCardsEnglishCopyAndLanguageSwitch(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{
		{ID: "b1", Name: "bash", Status: "running", Summary: "output text"},
		{ID: "r1", Name: "read_file", Status: "ok"},
		{ID: "s1", Name: "subagent", Status: "error"},
	}, nil, nil)
	tester := h.tester()

	// Default Chinese copy is unchanged.
	h.assertTexts(tester, "运行中", "完成", "失败", "详情")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Running", "Completed", "Failed",
		"Details")
	h.assertNoTexts(tester, "运行中", "完成", "失败", "详情")

	// Switching back restores the Chinese copy on the next frame.
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "运行中", "完成", "失败", "详情")
	h.assertNoTexts(tester, "Running", "Completed", "Failed", "Files", "Details")
}

func TestToolCardsDemoCardStaysRaw(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{{
		ID: "demo-1", Name: "demo", Status: "ok", Summary: "demo summary",
	}}, nil, nil)
	tester := h.tester()

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	// The demo card is the host's capability probe and keeps its raw content;
	// the internal call ID is hidden like on the real cards (H15).
	h.assertTexts(tester, "工具调用: demo", "demo summary", "ok")
	h.assertNoTexts(tester, "demo-1")
}
