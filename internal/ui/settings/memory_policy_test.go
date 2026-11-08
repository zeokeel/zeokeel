package settings_test

// Tests for the Memory page's deterministic consolidation policy row: prefill
// from settings, persist + call arguments, report text, invalid numbers, hidden
// without a consolidator, failures and English.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// fakeMemoryConsolidator is a fakeMemoryStore that also implements
// contracts.MemoryConsolidator.
type fakeMemoryConsolidator struct {
	*fakeMemoryStore
	mu     sync.Mutex
	calls  []contracts.MemoryPolicy
	report contracts.MemoryConsolidationReport
	err    error
}

func newFakeMemoryConsolidator(items ...contracts.Memory) *fakeMemoryConsolidator {
	return &fakeMemoryConsolidator{fakeMemoryStore: newFakeMemoryStore(items...)}
}

func (f *fakeMemoryConsolidator) Consolidate(_ context.Context, policy contracts.MemoryPolicy) (contracts.MemoryConsolidationReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, policy)
	return f.report, f.err
}

func (f *fakeMemoryConsolidator) policies() []contracts.MemoryPolicy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.MemoryPolicy(nil), f.calls...)
}

// memoryPolicyHarness opens the Memory page with settings values and a store.
func memoryPolicyHarness(t *testing.T, store contracts.MemoryStore, values map[string]string) (*harness, *fakeSettings) {
	t.Helper()
	storeSettings := newFakeSettings(values)
	h := newHarness(t, &fakeChat{}, storeSettings, settings.Config{})
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, store)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "记忆")
	return h, storeSettings
}

func TestMemoryPolicyPrefillAndApply(t *testing.T) {
	consolidator := newFakeMemoryConsolidator()
	consolidator.report = contracts.MemoryConsolidationReport{RemovedExpired: 2, RemovedOverCap: 3, Remaining: 5}
	h, store := memoryPolicyHarness(t, consolidator, map[string]string{
		contracts.SettingMemoryMaxEntries: "20",
		contracts.SettingMemoryMaxAgeDays: "30",
	})
	tester := h.tester()
	h.assertTexts(tester, "整理策略", "最大条目数", "保留天数", "按策略整理")

	h.focusInput(tester, "最大条目数 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("12")
	h.focusInput(tester, "保留天数 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("7")
	tester.Frame()
	h.click(tester, "按策略整理")

	if got := store.get(contracts.SettingMemoryMaxEntries); got != "12" {
		t.Fatalf("%s = %q, want 12", contracts.SettingMemoryMaxEntries, got)
	}
	if got := store.get(contracts.SettingMemoryMaxAgeDays); got != "7" {
		t.Fatalf("%s = %q, want 7", contracts.SettingMemoryMaxAgeDays, got)
	}
	policies := consolidator.policies()
	if len(policies) != 1 || policies[0].MaxEntries != 12 || policies[0].MaxAge != 7*24*time.Hour {
		t.Fatalf("policies = %+v, want MaxEntries 12 MaxAge 7d", policies)
	}
	h.assertTexts(tester, "已整理：过期 2 / 超限 3 / 剩余 5")
}

func TestMemoryPolicyDefaultsEmpty(t *testing.T) {
	consolidator := newFakeMemoryConsolidator()
	h, _ := memoryPolicyHarness(t, consolidator, nil)
	tester := h.tester()
	h.assertTexts(tester, "整理策略", "按策略整理")
	h.click(tester, "按策略整理")
	policies := consolidator.policies()
	if len(policies) != 1 || policies[0] != (contracts.MemoryPolicy{}) {
		t.Fatalf("default policies = %+v, want zero policy", policies)
	}
}

func TestMemoryPolicyInvalidNumberNormalizedToZero(t *testing.T) {
	consolidator := newFakeMemoryConsolidator()
	consolidator.report = contracts.MemoryConsolidationReport{Remaining: 1}
	h, store := memoryPolicyHarness(t, consolidator, nil)
	tester := h.tester()
	h.focusInput(tester, "最大条目数 输入")
	tester.Type("banana")
	tester.Frame()
	h.click(tester, "按策略整理")
	if got := store.get(contracts.SettingMemoryMaxEntries); got != "0" {
		t.Fatalf("%s = %q, want normalized 0", contracts.SettingMemoryMaxEntries, got)
	}
	h.assertTexts(tester, "无效数字，已按 0（不限）处理")
	policies := consolidator.policies()
	if len(policies) != 1 || policies[0].MaxEntries != 0 {
		t.Fatalf("policies = %+v, want MaxEntries 0", policies)
	}
}

func TestMemoryPolicyHiddenWithoutConsolidator(t *testing.T) {
	store := newFakeMemoryStore()
	h, _ := memoryPolicyHarness(t, store, nil)
	tester := h.tester()
	h.assertTexts(tester, "整理记忆", "全部清空")
	h.assertNoTexts(tester, "整理策略", "最大条目数", "按策略整理")
}

func TestMemoryPolicyFailureDanger(t *testing.T) {
	consolidator := newFakeMemoryConsolidator()
	consolidator.err = errors.New("store locked")
	h, _ := memoryPolicyHarness(t, consolidator, nil)
	tester := h.tester()
	h.click(tester, "按策略整理")
	h.assertTexts(tester, "整理失败：store locked")
}

func TestMemoryPolicyEnglish(t *testing.T) {
	consolidator := newFakeMemoryConsolidator()
	consolidator.report = contracts.MemoryConsolidationReport{RemovedExpired: 1, Remaining: 4}
	h, store := memoryPolicyHarness(t, consolidator, map[string]string{
		contracts.SettingMemoryMaxEntries: "10",
		contracts.SettingMemoryMaxAgeDays: "3",
	})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Consolidation policy", "Max entries", "Max age (days)", "Consolidate")
	h.click(tester, "Consolidate")
	if got := store.get(contracts.SettingMemoryMaxEntries); got != "10" {
		t.Fatalf("%s = %q, want 10", contracts.SettingMemoryMaxEntries, got)
	}
	h.assertTexts(tester, "Consolidated: expired 1 / over cap 0 / remaining 4")
}
