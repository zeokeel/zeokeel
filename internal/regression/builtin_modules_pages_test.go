package regression

// Adversarial audit: the built-in module inventory on the diagnostics and plugins
// pages. The diagnostics list shows the count and expands with a 40-row cap and a
// trailing "…"; the plugins page shows a collapsed read-only section that expands
// on demand; both sections disappear when the host provides no lister.

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	pluginsui "github.com/zeokeel/zeokeel/internal/ui/plugins"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// moduleListerFake is a scripted contracts.ModuleLister.
type moduleListerFake struct{ modules []contracts.ModuleInfo }

func (l moduleListerFake) Modules() []contracts.ModuleInfo {
	return append([]contracts.ModuleInfo(nil), l.modules...)
}

// modulesCatalog builds count synthetic module rows.
func modulesCatalog(count int) []contracts.ModuleInfo {
	modules := make([]contracts.ModuleInfo, 0, count)
	for index := 0; index < count; index++ {
		modules = append(modules, contracts.ModuleInfo{
			EntryID:  fmt.Sprintf("entry-%02d", index),
			ModuleID: fmt.Sprintf("mod/%02d", index),
			State:    "active",
		})
	}
	return modules
}

// openDiagnostics opens the settings diagnostics page with an optional lister.
func openDiagnostics(t *testing.T, lister contracts.ModuleLister) *ui.Tester {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	if lister != nil {
		provide(t, h.ctx, contracts.KeyModuleLister, lister)
	}
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 800)
	tester.Frame()
	// The grouped navigation scrolls; open the page through its stable deep link
	// instead of a nav row that may sit below the fold.
	if err := h.ui.OpenSettingsPage("diagnostics"); err != nil {
		t.Fatalf("open diagnostics: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	return tester
}

// TestDiagnosticsBuiltInModules covers the diagnostics inventory: count row,
// collapsed by default, expansion rows, the 40-row cap and the hidden state.
func TestDiagnosticsBuiltInModules(t *testing.T) {
	tester := openDiagnostics(t, moduleListerFake{modules: modulesCatalog(42)})
	requireHasText(t, tester, "已加载模块 42 个")
	requireNoText(t, tester, "entry-00") // collapsed
	if err := tester.Click("展开模块列表"); err != nil {
		t.Fatalf("expand modules: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "entry-00", "entry-39", "…")
	requireNoText(t, tester, "entry-40")
	requireNoText(t, tester, "entry-41")

	// Without the optional lister the section is hidden.
	tester = openDiagnostics(t, nil)
	requireNoText(t, tester, "已加载模块")
	requireNoText(t, tester, "展开模块列表")
}

// TestPluginsBuiltInModules covers the plugins page's read-only section: count,
// collapsed state, expansion and the hidden state without a lister.
func TestPluginsBuiltInModules(t *testing.T) {
	start := func(t *testing.T, lister contracts.ModuleLister) *ui.Tester {
		t.Helper()
		h := newBareHarness(t)
		provide(t, h.ctx, contracts.KeyPlugins, contracts.PluginsService(&pluginsServiceFake{}))
		if lister != nil {
			provide(t, h.ctx, contracts.KeyModuleLister, lister)
		}
		activate(t, h.ctx, pluginsui.Module(), pluginsui.Config{})
		h.push(pluginsui.ScreenID)
		tester := h.tester(1280, 800)
		tester.Frame()
		return tester
	}

	tester := start(t, moduleListerFake{modules: modulesCatalog(3)})
	requireHasTexts(t, tester, "内置模块", "3 个模块", "编译期装配，只读。")
	requireNoText(t, tester, "mod/00")
	if err := tester.Click("展开模块列表"); err != nil {
		t.Fatalf("expand plugins modules: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "mod/00", "active", "mod/02")

	// Without the optional lister the section is hidden entirely.
	tester = start(t, nil)
	requireNoText(t, tester, "内置模块")
	requireNoText(t, tester, "展开模块列表")
}
