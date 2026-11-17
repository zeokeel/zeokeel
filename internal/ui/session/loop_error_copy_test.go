package session_test

// v0.2.50 (user ruling): loop protection is removed. A restored run_end with
// ErrorKind=loop — persisted by an older version's loop breaker — renders one
// neutral historical note (zh/en) instead of the trip-budget copy: there is no
// breaker left to name a budget for, and the row must not blame the model or
// suggest a setting that no longer exists. The raw durable error text stays
// visible as muted diagnostic detail (no information is hidden).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// legacyLoopErrorTexts are the durable error spellings an old version's breaker
// persisted; every one restores to the same neutral historical note.
var legacyLoopErrorTexts = []string{
	"loop guard: wall clock budget exceeded",
	"loop guard: token budget exceeded (5000000/4000000)",
	"loop guard: identical tool call repeated 5 times: echo",
	"loop guard: no file diff/mutation after 40 calls",
	"loop guard: task budget exceeded",
}

func TestLoopErrorCopyIsHistoricalNeutral(t *testing.T) {
	for _, errText := range legacyLoopErrorTexts {
		t.Run(errText, func(t *testing.T) {
			h := newHarness(t, errorKindChat(contracts.MessageErrorKindLoop, errText))
			tester := h.tester()
			h.assertTexts(tester, "该回合曾被旧版循环保护停止", errText)
			// Neither the wave-H trip-budget copy nor the legacy blanket copy
			// may appear: the breaker is gone, so no budget is named.
			h.assertNoTexts(tester,
				"循环保护已触发",
				"达到运行时长上限", "达到 token 预算",
				"达到完全重复次数上限", "达到无变更调用上限", "达到运行预算上限",
				"已停止重复或无进展的工具循环")
		})
	}
}

func TestLoopErrorCopyHistoricalEnglish(t *testing.T) {
	for _, errText := range legacyLoopErrorTexts {
		t.Run(errText, func(t *testing.T) {
			h := newHarness(t, errorKindChat(contracts.MessageErrorKindLoop, errText))
			h.ui.Locale().SetLanguage("en")
			tester := h.tester()
			tester.Frame()
			h.assertTexts(tester,
				"This turn was stopped by the legacy loop guard (removed in this version)", errText)
			h.assertNoTexts(tester,
				"Loop guard triggered",
				"run time limit reached", "token budget reached",
				"identical-call repeat limit reached", "no-change call limit reached",
				"run budget limit reached",
				"stopped a repeated or no-progress tool loop")
		})
	}
}
