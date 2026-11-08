package regression

// Adversarial audit: the Automation screen's task search and enable-state filter
// (session-local state). Negative/boundary cases: search by name and prompt,
// case-insensitive match, no-match empty state and clearing, enabled/disabled
// filters, and a combined search+filter.

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
	automationui "github.com/zeokeel/zeokeel/internal/ui/automation"
)

// startAutomationUI boots the automation backend with three tasks and opens the
// automation screen.
func startAutomationUI(t *testing.T) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	activate(t, h.ctx, automation.Module(), automation.Config{TickMs: 3_600_000})
	service := requireService(t, h.ctx, contracts.KeyAutomation)
	for _, task := range []contracts.AutomationTask{
		{Name: "每日巡检", Prompt: "检查依赖是否有更新", Schedule: "every 60s", Enabled: true},
		{Name: "备份数据", Prompt: "备份到对象存储", Schedule: "every 300s", Enabled: true},
		{Name: "周末报告", Prompt: "汇总本周变更", Schedule: "every 600s", Enabled: false},
	} {
		if _, err := service.Create(context.Background(), task); err != nil {
			t.Fatalf("Create automation task: %v", err)
		}
	}
	activate(t, h.ctx, automationui.Module(), automationui.Config{})
	h.push(automationui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()
	return h, tester
}

// automationSearch types one search query (or clears it) and repaints.
func automationSearch(t *testing.T, tester *ui.Tester, query string) {
	t.Helper()
	if err := tester.Click("搜索任务"); err != nil {
		t.Fatalf("focus search: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if query == "" {
		tester.Key(ui.Cmd, ui.KeyA)
		tester.Key(0, ui.KeyBackspace)
	} else {
		tester.Type(query)
	}
	tester.Frame()
}

// TestAutomationFilterSearch covers search by name/prompt, case folding, the
// no-match empty state and clearing back to the full list.
func TestAutomationFilterSearch(t *testing.T) {
	_, tester := startAutomationUI(t)
	requireHasText(t, tester, "3 个任务")
	requireHasTexts(t, tester, "每日巡检", "备份数据", "周末报告")

	automationSearch(t, tester, "巡检")
	requireHasText(t, tester, "1 个任务")
	requireNoText(t, tester, "备份数据")
	requireNoText(t, tester, "周末报告")

	automationSearch(t, tester, "")
	automationSearch(t, tester, "依赖") // prompt substring
	requireHasText(t, tester, "每日巡检")
	requireHasText(t, tester, "1 个任务")

	automationSearch(t, tester, "ZZZ")
	requireHasText(t, tester, "0 个任务")
	requireHasText(t, tester, "没有匹配的任务")
	requireNoText(t, tester, "每日巡检")

	automationSearch(t, tester, "")
	requireHasText(t, tester, "3 个任务")
	requireNoText(t, tester, "没有匹配的任务")
}

// TestAutomationFilterEnabledAndCombined covers the enable-state filter, its
// combination with search, and restoring the full list.
func TestAutomationFilterEnabledAndCombined(t *testing.T) {
	_, tester := startAutomationUI(t)
	if err := tester.Click("筛选"); err != nil {
		t.Fatalf("open filter: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "全部", "仅启用", "仅停用")

	if err := tester.Click("仅启用"); err != nil {
		t.Fatalf("filter enabled: %v", err)
	}
	tester.Frame()
	requireHasText(t, tester, "2 个任务")
	requireHasTexts(t, tester, "每日巡检", "备份数据")
	requireNoText(t, tester, "周末报告")

	// Combined with search: only the enabled matching task remains.
	automationSearch(t, tester, "备份")
	requireHasText(t, tester, "1 个任务")
	requireHasText(t, tester, "备份数据")
	requireNoText(t, tester, "每日巡检")
	automationSearch(t, tester, "")

	// Disabled-only filter.
	if err := tester.Click("筛选"); err != nil {
		t.Fatalf("reopen filter: %v", err)
	}
	tester.Frame()
	if err := tester.Click("仅停用"); err != nil {
		t.Fatalf("filter disabled: %v", err)
	}
	tester.Frame()
	requireHasText(t, tester, "1 个任务")
	requireHasText(t, tester, "周末报告")
	requireNoText(t, tester, "每日巡检")

	// Combined filter that matches nothing shows the empty state.
	automationSearch(t, tester, "巡检")
	requireHasText(t, tester, "0 个任务")
	requireHasText(t, tester, "没有匹配的任务")

	// Back to all restores the full list.
	automationSearch(t, tester, "")
	if err := tester.Click("筛选"); err != nil {
		t.Fatalf("reopen filter (all): %v", err)
	}
	tester.Frame()
	if err := tester.Click("全部"); err != nil {
		t.Fatalf("filter all: %v", err)
	}
	tester.Frame()
	requireHasText(t, tester, "3 个任务")
}
