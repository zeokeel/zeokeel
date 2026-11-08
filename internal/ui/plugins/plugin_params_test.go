package plugins_test

// Tests for the Plugins page's plugin parameter section: only the settings keys
// that are actually consumed are shown, sensitive values are masked, editable
// rows open a native modal that persists through settings, and read-only rows
// (restart/compile-time) have no edit action.

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// newParamsHarness opens the Plugins page with a settings store.
func newParamsHarness(t *testing.T, values map[string]string) (*harness, *fakePluginSettings) {
	t.Helper()
	store := newFakePluginSettings(values)
	h := newHarness(t, nil, nil, plugins.Config{})
	provide[contracts.SettingsService](t, h.ctx, contracts.KeySettings, store)
	return h, store
}

// focusParamInput clicks one TextInput by its composite label ("label input").
func focusParamInput(t *testing.T, h *harness, tester *ui.Tester, label string) {
	t.Helper()
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("input %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

func TestPluginParamsSectionRendersWiredKeys(t *testing.T) {
	h, _ := newParamsHarness(t, map[string]string{
		contracts.SettingWebSearchEndpoint: "https://search.example.com/search",
		contracts.SettingWebSearchAPIKey:   "sk-secret-value",
	})
	tester := h.tester()
	h.assertTexts(tester, "插件参数")
	h.assertNoTexts(tester, "web.search.endpoint", "sk-secret-value")

	h.click(tester, "显示参数")
	h.assertTexts(tester,
		"搜索端点", "web.search.endpoint", "https://search.example.com/search",
		"API Key", "web.search.apiKey", "API Key 已设置",
		"工作区 mcp.json", "mcp.workspaceOptIn", "需重启",
		"子代理深度", "子代理并发", "编译期常量")
	h.assertNoTexts(tester, "sk-secret-value")
}

func TestPluginParamsMasksUnsetAPIKey(t *testing.T) {
	h, _ := newParamsHarness(t, nil)
	tester := h.tester()
	h.click(tester, "显示参数")
	h.assertTexts(tester, "API Key 未设置")
}

func TestPluginParamsEditPersists(t *testing.T) {
	h, store := newParamsHarness(t, map[string]string{
		contracts.SettingWebSearchEndpoint: "https://old.example.com/search",
	})
	tester := h.tester()
	h.click(tester, "显示参数")
	h.click(tester, "编辑 搜索端点")
	h.assertTexts(tester, "编辑参数", "保存", "取消")

	focusParamInput(t, h, tester, "新值：搜索端点")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("https://new.example.com/search")
	tester.Frame()
	h.click(tester, "保存")

	if got := store.get(contracts.SettingWebSearchEndpoint); got != "https://new.example.com/search" {
		t.Fatalf("%s = %q", contracts.SettingWebSearchEndpoint, got)
	}
	h.assertTexts(tester, "已保存", "https://new.example.com/search")
	h.assertNoTexts(tester, "编辑参数")
}

func TestPluginParamsSensitiveEditKeepsValuePrivate(t *testing.T) {
	h, store := newParamsHarness(t, map[string]string{
		contracts.SettingWebSearchAPIKey: "sk-old",
	})
	tester := h.tester()
	h.click(tester, "显示参数")
	h.click(tester, "编辑 API Key")
	h.assertNoTexts(tester, "sk-old")
	focusParamInput(t, h, tester, "新值：API Key")
	tester.Type("sk-new")
	tester.Frame()
	h.click(tester, "保存")
	if got := store.get(contracts.SettingWebSearchAPIKey); got != "sk-new" {
		t.Fatalf("%s = %q, want sk-new", contracts.SettingWebSearchAPIKey, got)
	}
	h.assertNoTexts(tester, "sk-new")
}

func TestPluginParamsEditFailureDanger(t *testing.T) {
	h, store := newParamsHarness(t, nil)
	store.setErr = errors.New("settings read-only")
	tester := h.tester()
	h.click(tester, "显示参数")
	h.click(tester, "编辑 搜索端点")
	focusParamInput(t, h, tester, "新值：搜索端点")
	tester.Type("https://x.example.com/search")
	tester.Frame()
	h.click(tester, "保存")
	h.assertTexts(tester, "保存失败：settings read-only")
}

func TestPluginParamsReadOnlyRowsHaveNoEdit(t *testing.T) {
	h, _ := newParamsHarness(t, nil)
	tester := h.tester()
	h.click(tester, "显示参数")
	h.assertNoTexts(tester, "编辑 工作区 mcp.json", "编辑 子代理深度", "编辑 子代理并发")
}

func TestPluginParamsHiddenWithoutSettings(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "插件参数", "web.search.endpoint")
}

func TestPluginParamsEnglish(t *testing.T) {
	h, _ := newParamsHarness(t, nil)
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Plugin settings")
	h.click(tester, "Show parameters")
	h.assertTexts(tester,
		"Search endpoint", "web.search.endpoint", "API Key", "API key not set",
		"Workspace mcp.json", "Restart required",
		"Delegation depth", "Delegation concurrency", "Compile-time constant")
	h.click(tester, "Edit Search endpoint")
	h.assertTexts(tester, "Edit parameter", "Save", "Cancel")
}
