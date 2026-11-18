package automation_test

// Frame tests for the task card's run-history disclosure and the two run states
// (delivered vs completed/failed/interrupted).

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func historyTask(records ...contracts.AutomationRunRecord) contracts.AutomationTask {
	return contracts.AutomationTask{
		ID: "task-1", Name: "巡检", Prompt: "run", Schedule: "every 60s", Enabled: true,
		RunHistory: records,
	}
}

// deliveredRecord builds a run whose prompt was accepted but whose session has
// not settled yet (the two-state record's first state).
func deliveredRecord(summary string, at time.Time, sessionID string) contracts.AutomationRunRecord {
	return contracts.AutomationRunRecord{
		ID: "run-delivered", StartedAt: at, DeliveredAt: at.Add(time.Second),
		Status: contracts.AutomationRunDelivered, Summary: summary, SessionID: sessionID,
	}
}

// completedRecord builds a settled run with a delivery and completion instant.
func completedRecord(status, summary string, at time.Time) contracts.AutomationRunRecord {
	return contracts.AutomationRunRecord{
		ID: "run-settled", StartedAt: at, DeliveredAt: at.Add(time.Second),
		CompletedAt: at.Add(3 * time.Second), Status: status, Summary: summary,
		DurationMS: 2000,
	}
}

// --- tests ------------------------------------------------------------------

func TestTaskHistoryCollapsedDeliveredState(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(
		deliveredRecord("最新运行摘要", base.Add(time.Minute), "sess-9"),
		completedRecord(contracts.AutomationRunFailure, "旧失败摘要", base),
	)
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()

	// The collapsed row shows the delivered state and never claims a result.
	// The collapsed line carries the state and the hint; the session link moved
	// into the row's ⋯ menu (density rule: three visible targets per row).
	h.assertTexts(tester, "巡检", "运行历史", "已启动", "最新运行摘要", "结果由会话决定")
	h.assertNoTexts(tester, "打开会话")
	h.assertNoTexts(tester, "旧失败摘要", "还没有运行记录")
}

func TestTaskHistoryCollapsedCompletedState(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(
		completedRecord(contracts.AutomationRunSuccess, "最新运行摘要", base.Add(time.Minute)),
	)
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()

	// A settled success is 已完成, not a bare delivery.
	h.assertTexts(tester, "已完成", "最新运行摘要")
	h.assertNoTexts(tester, "已启动", "结果由会话决定")
}

func TestTaskHistoryExpandsAndCollapses(t *testing.T) {
	base := time.Unix(1700000000, 0)
	task := historyTask(
		completedRecord(contracts.AutomationRunSuccess, "最新运行摘要", base.Add(time.Minute)),
		completedRecord(contracts.AutomationRunFailure, "旧失败摘要", base),
	)
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()

	h.click(tester, "运行历史")
	// The run time is a full localized date+time: a clock alone cannot tell
	// yesterday's run from today's.
	wantTime := uikit.FormatDateTime(h.uiRoot.Locale(), base.Add(time.Minute))
	h.assertTexts(tester, "最新运行摘要", "旧失败摘要", "失败", wantTime)
	// Delivery and completion are separate facts in the row.
	h.assertTexts(tester, "完成：", "耗时：")
	if base.Add(time.Minute).Local().Format("15:04:05") == wantTime {
		t.Fatalf("run time %q is still a bare clock", wantTime)
	}
	h.click(tester, "运行历史")
	h.assertNoTexts(tester, "旧失败摘要")
}

func TestTaskHistoryInterruptedAndErrorDetail(t *testing.T) {
	base := time.Unix(1700000000, 0)
	interrupted := completedRecord(contracts.AutomationRunInterrupted, "ran in chat session sess-1", base)
	interrupted.Error = "cancelled"
	errorRecord := completedRecord(contracts.AutomationRunFailure, "", base.Add(time.Minute))
	errorRecord.Error = "provider exploded"
	h := newHarness(t, newFakeAutomation(historyTask(errorRecord, interrupted)), automation.Config{})
	tester := h.tester()

	// The newest failed run carries its cause; the cancellation stays silent.
	h.assertTexts(tester, "失败", "provider exploded")
	h.assertNoTexts(tester, "cancelled", "已中断")
	h.click(tester, "运行历史")
	h.assertTexts(tester, "已中断")
	h.assertNoTexts(tester, "cancelled")
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
		completedRecord(contracts.AutomationRunFailure, "boom", base),
	)
	h := newHarness(t, newFakeAutomation(task), automation.Config{})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()

	h.assertTexts(tester, "Run history", "Failed")
	h.click(tester, "Run history")
	h.assertTexts(tester, "boom", "Completed:", "Duration:")
}

func TestTaskHistoryDeliveredEnglishCopy(t *testing.T) {
	base := time.Unix(1700000000, 0)
	h := newHarness(t, newFakeAutomation(historyTask(deliveredRecord("summary", base, "sess-1"))), automation.Config{})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Started", "The result depends on that session")
}
