package settings_test

// Tests for the settings 搜索 page: the web-search endpoint/API key rows, the
// configured badge, endpoint validation and the save/error status.

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func openSearchPage(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "搜索")
}

func clearAndType(t *testing.T, h *harness, tester *ui.Tester, label, value string) {
	t.Helper()
	h.focusInput(tester, label)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type(value)
	tester.Frame()
}

// --- tests ------------------------------------------------------------------

func TestSearchPageRendersConfiguredState(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: "https://search.example.com/search",
		contracts.SettingWebSearchAPIKey:   "sk-stored",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openSearchPage(t, h, tester)

	h.assertTexts(tester, "搜索", "搜索后端", "已配置", "API Key 已设置",
		"搜索端点", "API Key",
		"web_search 工具调用的搜索后端",
		"修改后下一次 web_search 调用生效")
	// Saving an untouched form keeps the stored rows.
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingWebSearchEndpoint); got != "https://search.example.com/search" {
		t.Fatalf("endpoint = %q, want the stored value", got)
	}
	if got := store.get(contracts.SettingWebSearchAPIKey); got != "sk-stored" {
		t.Fatalf("API key = %q, want the untouched stored key", got)
	}
}

func TestSearchPageSavesBothRows(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openSearchPage(t, h, tester)
	h.assertTexts(tester, "未配置")

	clearAndType(t, h, tester, "搜索端点 输入", "https://search.example.com/search")
	clearAndType(t, h, tester, "API Key 输入", "sk-new-key")
	h.click(tester, "保存")

	h.assertTexts(tester, "已保存", "已配置", "API Key 已设置")
	if got := store.get(contracts.SettingWebSearchEndpoint); got != "https://search.example.com/search" {
		t.Fatalf("%s = %q", contracts.SettingWebSearchEndpoint, got)
	}
	if got := store.get(contracts.SettingWebSearchAPIKey); got != "sk-new-key" {
		t.Fatalf("%s = %q", contracts.SettingWebSearchAPIKey, got)
	}
}

func TestSearchPageRejectsInvalidEndpoint(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: "https://old.example.com/search",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openSearchPage(t, h, tester)

	clearAndType(t, h, tester, "搜索端点 输入", "not-a-url")
	h.click(tester, "保存")
	h.assertTexts(tester, "端点无效：请输入 http(s) URL")
	if got := store.get(contracts.SettingWebSearchEndpoint); got != "https://old.example.com/search" {
		t.Fatalf("invalid endpoint was written: %q", got)
	}
}

func TestSearchPageSaveError(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errors.New("disk is read-only")
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openSearchPage(t, h, tester)

	clearAndType(t, h, tester, "搜索端点 输入", "https://search.example.com/search")
	h.click(tester, "保存")
	h.assertTexts(tester, "保存失败：disk is read-only")
	h.assertNoTexts(tester, "已保存")
}

func TestSearchPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: "https://search.example.com/search",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()

	h.click(tester, "Search")
	h.assertTexts(tester, "Search", "Search backend", "Configured",
		"Search endpoint", "Save",
		"Changes apply to the next web_search call")
	h.assertNoTexts(tester, "搜索后端", "已配置")

	// Switch back: the page title and copy return to Chinese on the next frame.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "搜索后端", "已配置", "保存")
}
