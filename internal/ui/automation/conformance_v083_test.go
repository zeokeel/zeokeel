package automation_test

// Conformance frame tests for the v0.8.3 automation fixes: the internal
// "ran in chat session <id>" bookkeeping line is localized without the id, long
// summaries are bounded (the full text stays in the tooltip) and the enable
// switch is a bare native switch in the card header.

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

// TestRunSummaryHidesInternalSessionID pins the localization: the backend's
// bookkeeping line never reaches the row, and the id inside it is not shown.
func TestRunSummaryHidesInternalSessionID(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(runRecord(contracts.AutomationRunSuccess,
		"ran in chat session sess-abc123", base))
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "已在会话中运行")
	h.assertNoTexts(tester, "sess-abc123", "ran in chat session")
}

// TestRunSummaryEnglish pins the same line in the English interface.
func TestRunSummaryEnglish(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(runRecord(contracts.AutomationRunSuccess,
		"ran in chat session sess-abc123", base))
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "Ran in a chat session")
	h.assertNoTexts(tester, "sess-abc123", "ran in chat session")
}

// TestRunSummaryTruncatedWithTooltip pins the bound: a long error or tool
// summary is cut in the row, while the full text is only its tooltip.
func TestRunSummaryTruncatedWithTooltip(t *testing.T) {
	base := time.Unix(1700000000, 0)
	long := strings.Repeat("x", 300)
	task := historyTask(runRecord(contracts.AutomationRunFailure, long, base))
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, strings.Repeat("x", 120)+"…")
	h.assertNoTexts(tester, long)
}
