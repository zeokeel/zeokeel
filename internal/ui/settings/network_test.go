package settings_test

// Tests for the Network settings page: the outbound proxy URL and NO_PROXY
// fields, validation, persistence of the two net.* keys, the currently applied
// values, errors and the English copy.

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestNetworkPageDefaults covers the empty default state: no proxy (direct) and
// no NO_PROXY, with the restart hint and the save action.
func TestNetworkPageDefaults(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "网络")

	h.assertTexts(tester, "网络", "当前生效", "代理地址", "NO_PROXY", "直连",
		"代理设置由进程启动时应用", "保存")
}

// TestNetworkPageSaveWritesSettings types a proxy and a NO_PROXY list into the
// native inputs, saves, and pins both settings keys plus the success badge.
func TestNetworkPageSaveWritesSettings(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "网络")

	h.focusInput(tester, "代理地址 输入")
	tester.Type("http://127.0.0.1:7890")
	tester.Frame()
	h.focusInput(tester, "NO_PROXY 输入")
	tester.Type("localhost,127.0.0.1")
	tester.Frame()
	h.click(tester, "保存")

	if got := store.get(contracts.SettingNetProxy); got != "http://127.0.0.1:7890" {
		t.Fatalf("%s = %q, want the typed proxy", contracts.SettingNetProxy, got)
	}
	if got := store.get(contracts.SettingNetNoProxy); got != "localhost,127.0.0.1" {
		t.Fatalf("%s = %q, want the typed NO_PROXY", contracts.SettingNetNoProxy, got)
	}
	h.assertTexts(tester, "已保存", "http://127.0.0.1:7890", "localhost,127.0.0.1")
}

// TestNetworkPageInvalidURLIsNotWritten covers validation: a non-http(s) URL is
// rejected with the page's danger text and nothing is written.
func TestNetworkPageInvalidURLIsNotWritten(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "网络")

	h.focusInput(tester, "代理地址 输入")
	tester.Type("not-a-url")
	tester.Frame()
	h.click(tester, "保存")

	h.assertTexts(tester, "代理地址无效")
	if got := store.get(contracts.SettingNetProxy); got != "" {
		t.Fatalf("%s = %q after an invalid URL, want it unwritten", contracts.SettingNetProxy, got)
	}
	if len(store.setKeys()) != 0 {
		t.Fatalf("settings writes after invalid URL = %v, want none", store.setKeys())
	}
}

// TestNetworkPageSaveError covers a failing settings store.
func TestNetworkPageSaveError(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errors.New("denied")
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "网络")

	h.focusInput(tester, "代理地址 输入")
	tester.Type("http://127.0.0.1:7890")
	tester.Frame()
	h.click(tester, "保存")
	h.assertTexts(tester, "保存失败：denied")
}

// TestNetworkPageReadsStoredValues covers showing the currently applied values
// and saving an unchanged form back to the same keys.
func TestNetworkPageReadsStoredValues(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingNetProxy:   "http://old.proxy:8080",
		contracts.SettingNetNoProxy: "localhost,.internal",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "网络")

	h.assertTexts(tester, "http://old.proxy:8080", "localhost,.internal")
	h.click(tester, "保存")
	if got := store.get(contracts.SettingNetProxy); got != "http://old.proxy:8080" {
		t.Fatalf("%s = %q, want the stored proxy preserved", contracts.SettingNetProxy, got)
	}
	if got := store.get(contracts.SettingNetNoProxy); got != "localhost,.internal" {
		t.Fatalf("%s = %q, want the stored NO_PROXY preserved", contracts.SettingNetNoProxy, got)
	}
}

// TestNetworkPageEnglish covers the English copy.
func TestNetworkPageEnglish(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Network")

	h.assertTexts(tester, "Network", "Currently applied", "Proxy URL", "NO_PROXY",
		"Direct", "The proxy is applied when the process starts", "Save")
	h.click(tester, "Save")
	h.assertTexts(tester, "Saved")
}
