package settings_test

// v0.2.50 removal pin (user ruling): the settings surface exposes NO tool-loop
// configuration. The merged 行为与限额 page has no 循环预算 section (the four
// hard budgets and their keys are gone), the model page has no 最大迭代次数
// input (the iteration cap is assembly-level product definition, not a user
// knob), and legacy stored rows from older versions are ignored — never
// displayed, never rewritten. The approval timeout stays: it bounds approval
// waits, not tool loops.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// removedLoopBudgetLabels are the four wave-H field labels that must not exist.
var removedLoopBudgetLabels = []string{"运行时长上限", "Token 预算", "完全重复调用上限", "无变更调用上限"}

// legacyLoopRows are the removed user keys as literal strings (the contracts
// constants are gone): stored rows an older version may have persisted.
var legacyLoopRows = map[string]string{
	"agent.loopWallClockSeconds": "1800",
	"agent.loopTokenBudget":      "4000000",
	"agent.loopRepeatLimit":      "5",
	"agent.loopNoDiffCalls":      "40",
}

// requireNoInputNamed asserts no accessible input carries the label.
func requireNoInputNamed(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if _, ok := tester.Find(label + " 输入"); ok {
		t.Fatalf("the removed input %q still exists; texts = %v", label, tester.Texts())
	}
	for _, info := range tester.AccessAll() {
		if info.Name == label {
			t.Fatalf("the removed control %q is still accessible: %+v", label, info)
		}
	}
}

// TestCapabilitiesPageHasNoLoopBudgets covers the merged behavior & limits page:
// no loop-budget section, fields or teaching copy; the delegation/shell limits
// and the approval timeout stay, and the page is clean.
func TestCapabilitiesPageHasNoLoopBudgets(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	openCapabilities(t, h, tester)

	h.assertNoTexts(tester, "循环预算", "0＝不限制", "让工具自由运行", "跑飞")
	for _, label := range removedLoopBudgetLabels {
		h.assertNoTexts(tester, label)
		requireNoInputNamed(t, tester, label)
	}
	// The surviving limits and the approval timeout are untouched.
	h.assertTexts(tester, "Subagent 并发", "Team member 并发", "Shell 命令超时", "Shell 输出上限", "审批超时")
	if save := saveButton(t, tester); !save.Disabled {
		t.Fatalf("the footer's Save is enabled on a clean page: %+v", save)
	}
}

// TestCapabilitiesPageIgnoresLegacyLoopRows covers hand-edited/stored rows from
// an older version: they are not displayed, do not dirty the page, and a save of
// the surviving limits leaves them untouched (the page no longer knows them).
func TestCapabilitiesPageIgnoresLegacyLoopRows(t *testing.T) {
	store := newFakeSettings(legacyLoopRows)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openCapabilities(t, h, tester)

	for _, label := range removedLoopBudgetLabels {
		requireNoInputNamed(t, tester, label)
	}
	if save := saveButton(t, tester); !save.Disabled {
		t.Fatalf("legacy loop rows made the page dirty: %+v", save)
	}
	// Dirty a surviving limit so the footer Save is enabled, then save: the
	// edited row persists and the legacy loop rows stay untouched.
	typeNumberInput(t, h, tester, "Subagent 并发", "5")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.get("agent.subagentMaxConcurrent"); got != "5" {
		t.Fatalf("saved subagent concurrency = %q, want 5", got)
	}
	for key, value := range legacyLoopRows {
		if got := store.get(key); got != value {
			t.Fatalf("legacy row %s = %q, want it untouched (%q)", key, got, value)
		}
	}
}

// TestModelPageHasNoMaxIterationsInput covers the model page's runtime section:
// the iteration knob is gone from the collapsed summary and the advanced
// disclosure; the context budget (a compaction knob, not a tool loop) stays.
func TestModelPageHasNoMaxIterationsInput(t *testing.T) {
	store := newFakeSettings(map[string]string{"chat.maxIterations": "12"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)

	// The collapsed summary no longer mentions iterations at all.
	h.assertNoTexts(tester, "最大迭代")
	expandRuntimeAdvanced(t, h, tester)
	h.assertNoTexts(tester, "最大迭代次数", "模型/工具轮数上限")
	requireNoInputNamed(t, tester, "最大迭代次数")
	// The context budget draft survives, and the legacy iteration row is
	// neither displayed nor rewritten by a save.
	h.assertTexts(tester, "上下文预算")
	typeNumberInput(t, h, tester, "上下文预算", "4096")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.get("chat.maxIterations"); got != "12" {
		t.Fatalf("legacy chat.maxIterations row = %q, want it untouched (12)", got)
	}
	if got := store.get("chat.contextBudget"); got != "4096" {
		t.Fatalf("saved context budget = %q, want 4096", got)
	}
}
