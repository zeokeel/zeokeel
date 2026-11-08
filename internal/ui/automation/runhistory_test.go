package automation_test

// TDD tests (RED first) for the task card's run-history disclosure.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

func historyTask(records ...contracts.AutomationRunRecord) contracts.AutomationTask {
	return contracts.AutomationTask{
		ID: "task-1", Name: "巡检", Prompt: "run", Schedule: "every 60s", Enabled: true,
		RunHistory: records,
	}
}

func runRecord(status, summary string, at time.Time) contracts.AutomationRunRecord {
	return contracts.AutomationRunRecord{
		StartedAt: at, FinishedAt: at.Add(2 * time.Second), Status: status, Summary: summary,
	}
}

// --- tests ------------------------------------------------------------------

func TestTaskHistoryCollapsedShowsLatestSummary(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(
		runRecord(contracts.AutomationRunSuccess, "最新运行摘要", base.Add(time.Minute)),
		runRecord(contracts.AutomationRunFailure, "旧失败摘要", base),
	)
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()

	h.assertTexts(tester, "巡检", "运行历史", "成功", "最新运行摘要")
	h.assertNoTexts(tester, "旧失败摘要", "还没有运行记录")
}

func TestTaskHistoryExpandsAndCollapses(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(
		runRecord(contracts.AutomationRunSuccess, "最新运行摘要", base.Add(time.Minute)),
		runRecord(contracts.AutomationRunFailure, "旧失败摘要", base),
	)
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()

	h.click(tester, "运行历史")
	h.assertTexts(tester, "最新运行摘要", "旧失败摘要", "失败",
		base.Add(time.Minute).Local().Format("15:04:05"))
	h.click(tester, "运行历史")
	h.assertNoTexts(tester, "旧失败摘要")
}

func TestTaskHistoryEmptyState(t *testing.T) {
	h := newHarness(t, newFakeAutomation(historyTask()), automation.Config{})
	tester := h.tester()
	h.click(tester, "运行历史")
	h.assertTexts(tester, "还没有运行记录")
}

func TestTaskHistoryEnglishCopy(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(
		runRecord(contracts.AutomationRunFailure, "boom", base),
	)
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()

	h.assertTexts(tester, "Run history", "Failed")
	h.click(tester, "Run history")
	h.assertTexts(tester, "boom")
}
