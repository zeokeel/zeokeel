package settings_test

// Frame tests for RG-04 (2026-11 wave D, UI side): the behavior page's run-limits
// form gains an automation approval-timeout number input (default 0 = disabled),
// whose description makes clear it only bounds approvals from automation-origin
// runs — interactive approvals keep waiting as before. It persists through the
// footer Save like the other limits and cleans back to an empty row at 0.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestBehaviorApprovalTimeoutDefaultAndDescription covers the default (0 = off)
// and the automation-only disclosure.
func TestBehaviorApprovalTimeoutDefaultAndDescription(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为与限额")

	h.assertTexts(tester, "审批超时", "仅约束自动化来源")
	requireNumberValue(t, tester, "审批超时", "0")
}

// TestBehaviorApprovalTimeoutSaves covers the save path: a non-zero timeout
// persists through the footer Save; restoring the default (0) cleans the row.
func TestBehaviorApprovalTimeoutSaves(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为与限额")

	typeNumberInput(t, h, tester, "审批超时", "300")
	h.assertTexts(tester, "未保存")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.Get(contracts.SettingAgentApprovalTimeoutSeconds, ""); got != "300" {
		t.Fatalf("%s = %q, want 300", contracts.SettingAgentApprovalTimeoutSeconds, got)
	}

	// Restoring the default disables the timeout and cleans the stored row.
	h.click(tester, "恢复默认值")
	requireNumberValue(t, tester, "审批超时", "0")
	h.click(tester, "保存")
	if got := store.Get(contracts.SettingAgentApprovalTimeoutSeconds, ""); got != "" {
		t.Fatalf("default approval timeout persisted as %q, want a cleaned row", got)
	}
}
