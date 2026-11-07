package settings_test

// TDD tests (RED first) for the per-level notification filters.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// errStoreWrite scripts one settings write failure.
var errStoreWrite = errors.New("store write boom")

// storedHiddenLevels parses the hidden-levels settings row.
func storedHiddenLevels(t *testing.T, store *fakeSettings) []string {
	t.Helper()
	raw := store.get(contracts.SettingNotificationHiddenLevels)
	if raw == "" {
		return nil
	}
	var levels []string
	if err := json.Unmarshal([]byte(raw), &levels); err != nil {
		t.Fatalf("stored hidden levels %q is not valid JSON: %v", raw, err)
	}
	return levels
}

func TestNotificationsHiddenLevelToggles(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.assertTexts(tester, "信息", "成功", "警告", "错误", "按级别过滤")

	h.click(tester, "成功")
	if got := storedHiddenLevels(t, store); len(got) != 1 || got[0] != "success" {
		t.Fatalf("hiding success stored %v", got)
	}
	h.click(tester, "成功")
	if got := storedHiddenLevels(t, store); len(got) != 0 {
		t.Fatalf("showing success stored %v", got)
	}

	// Multiple levels coexist in the stored list.
	h.click(tester, "信息")
	h.click(tester, "错误")
	got := storedHiddenLevels(t, store)
	if len(got) != 2 || got[0] != "info" || got[1] != "error" {
		t.Fatalf("hidden levels = %v, want [info error]", got)
	}
}

func TestNotificationsHiddenLevelsBadValue(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingNotificationHiddenLevels: "{bad-json",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	// The malformed row displays as all levels shown; one toggle writes a
	// valid list.
	h.click(tester, "警告")
	if got := storedHiddenLevels(t, store); len(got) != 1 || got[0] != "warning" {
		t.Fatalf("hidden levels after malformed row = %v", got)
	}
}

func TestNotificationsHiddenLevelsSaveFailures(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errStoreWrite
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.click(tester, "警告")
	h.assertTexts(tester, "保存失败：")
}

func TestNotificationsHiddenLevelsEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Notifications")
	h.assertTexts(tester, "Filter by level", "Info", "Success", "Warning", "Error")
	h.click(tester, "Success")
	if got := storedHiddenLevels(t, store); len(got) != 1 || got[0] != "success" {
		t.Fatalf("English toggle stored %v", got)
	}
}
