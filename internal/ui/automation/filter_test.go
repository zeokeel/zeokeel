package automation_test

// TDD tests (RED first) for the Automation screen's search and enable-filter.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

func filterTasks() []contracts.AutomationTask {
	return []contracts.AutomationTask{
		{ID: "t1", Name: "每日巡检", Prompt: "检查依赖是否有更新", Schedule: "every 60s", Enabled: true},
		{ID: "t2", Name: "备份数据", Prompt: "备份到对象存储", Schedule: "every 300s", Enabled: true},
		{ID: "t3", Name: "周末报告", Prompt: "汇总本周变更", Schedule: "every 600s", Enabled: false},
	}
}

func searchInput(t *testing.T, h *harness, tester *ui.Tester, query string) {
	t.Helper()
	h.click(tester, "搜索任务")
	if query == "" {
		tester.Key(ui.Cmd, ui.KeyA)
		tester.Key(0, ui.KeyBackspace)
	} else {
		tester.Type(query)
	}
	tester.Frame()
}

func TestAutomationDefaultShowsAllTasks(t *testing.T) {
	h := newHarness(t, newFakeAutomation(filterTasks()...), automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "每日巡检", "备份数据", "周末报告", "3 个任务", "筛选")
}

func TestAutomationSearchByNameAndPrompt(t *testing.T) {
	h := newHarness(t, newFakeAutomation(filterTasks()...), automation.Config{})
	tester := h.tester()

	searchInput(t, h, tester, "巡检")
	h.assertTexts(tester, "每日巡检", "1 个任务")
	h.assertNoTexts(tester, "备份数据", "周末报告")

	searchInput(t, h, tester, "")
	searchInput(t, h, tester, "依赖")
	h.assertTexts(tester, "每日巡检", "1 个任务")
	h.assertNoTexts(tester, "备份数据", "周末报告")
}

func TestAutomationSearchNoMatchAndClear(t *testing.T) {
	h := newHarness(t, newFakeAutomation(filterTasks()...), automation.Config{})
	tester := h.tester()

	searchInput(t, h, tester, "zzz")
	h.assertTexts(tester, "没有匹配的任务", "0 个任务")
	h.assertNoTexts(tester, "每日巡检", "备份数据", "周末报告")

	searchInput(t, h, tester, "")
	h.assertTexts(tester, "每日巡检", "备份数据", "周末报告", "3 个任务")
	h.assertNoTexts(tester, "没有匹配的任务")
}

func TestAutomationFilterEnabledDisabled(t *testing.T) {
	h := newHarness(t, newFakeAutomation(filterTasks()...), automation.Config{})
	tester := h.tester()

	h.click(tester, "筛选")
	h.assertTexts(tester, "全部", "仅启用", "仅停用")
	h.click(tester, "仅启用")
	h.assertTexts(tester, "每日巡检", "备份数据", "2 个任务")
	h.assertNoTexts(tester, "周末报告")

	h.click(tester, "筛选")
	h.click(tester, "仅停用")
	h.assertTexts(tester, "周末报告", "1 个任务")
	h.assertNoTexts(tester, "每日巡检", "备份数据")

	h.click(tester, "筛选")
	h.click(tester, "全部")
	h.assertTexts(tester, "每日巡检", "备份数据", "周末报告", "3 个任务")
}

func TestAutomationSearchAndFilterCombine(t *testing.T) {
	h := newHarness(t, newFakeAutomation(filterTasks()...), automation.Config{})
	tester := h.tester()

	searchInput(t, h, tester, "报告")
	h.click(tester, "筛选")
	h.click(tester, "仅启用")
	h.assertTexts(tester, "没有匹配的任务", "0 个任务")

	h.click(tester, "筛选")
	h.click(tester, "全部")
	h.assertTexts(tester, "周末报告", "1 个任务")
	h.assertNoTexts(tester, "每日巡检", "备份数据")
}

func TestAutomationFilterEnglishCopy(t *testing.T) {
	h := newHarness(t, newFakeAutomation(filterTasks()...), automation.Config{})
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Filter", "Search tasks", "3 tasks")

	h.click(tester, "Filter")
	h.assertTexts(tester, "All", "Enabled only", "Disabled only")
	h.click(tester, "Disabled only")
	h.assertTexts(tester, "周末报告", "1 task")

	h.click(tester, "Search tasks")
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "No matching tasks", "0 tasks")
}
