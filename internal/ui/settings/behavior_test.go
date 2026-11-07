package settings_test

// TDD tests (RED first) for the Behavior page: the default agent permission.

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestBehaviorPageDefaultsAsk(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "行为", "默认代理权限", "当前：询问",
		"询问", "工作区", "完全访问", "从不",
		"每次危险操作都会询问", "修改立即生效")
	if raw := store.get(contracts.SettingAgentPermission); raw != "" {
		t.Fatalf("defaults wrote the permission row: %q", raw)
	}
}

func TestBehaviorPageSelectsPermission(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")

	h.click(tester, "询问") // open the current-value menu
	h.assertTexts(tester, "完全访问")
	h.click(tester, "完全访问")
	if raw := store.get(contracts.SettingAgentPermission); raw != contracts.PermissionFull {
		t.Fatalf("selecting full wrote %q", raw)
	}
	h.assertTexts(tester, "已保存", "当前：完全访问")

	h.click(tester, "完全访问") // reopen with the new current label
	h.click(tester, "从不")
	if raw := store.get(contracts.SettingAgentPermission); raw != "never" {
		t.Fatalf("selecting never wrote %q", raw)
	}
	h.assertTexts(tester, "当前：从不")
}

func TestBehaviorPageBadValueNormalizedAsk(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAgentPermission: "bogus-mode",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.assertTexts(tester, "当前：询问")
	h.click(tester, "询问")
	h.click(tester, "工作区")
	if raw := store.get(contracts.SettingAgentPermission); raw != contracts.PermissionWorkspace {
		t.Fatalf("selecting workspace wrote %q", raw)
	}
}

func TestBehaviorPageWriteFailure(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errBehaviorStoreWrite
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	h.click(tester, "询问")
	h.click(tester, "工作区")
	h.assertTexts(tester, "保存失败：")
}

func TestBehaviorPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	h.assertTexts(tester, "Behavior", "Default agent permission", "Current: Ask",
		"Ask", "Workspace", "Full access", "Never",
		"Changes apply immediately")
	h.click(tester, "Ask")
	h.click(tester, "Full access")
	if raw := store.get(contracts.SettingAgentPermission); raw != contracts.PermissionFull {
		t.Fatalf("English selection wrote %q", raw)
	}
	h.assertTexts(tester, "Saved", "Current: Full access")
}

// errBehaviorStoreWrite scripts one settings write failure.
var errBehaviorStoreWrite = errors.New("behavior store boom")
