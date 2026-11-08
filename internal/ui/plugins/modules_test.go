package plugins_test

// Tests for the Plugins page's built-in module section (host KeyModuleLister):
// hidden without the service, collapsed by default, expandable read-only rows
// with state badges, and the English copy.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

type modulesLister struct{ modules []contracts.ModuleInfo }

func (l modulesLister) Modules() []contracts.ModuleInfo {
	return append([]contracts.ModuleInfo(nil), l.modules...)
}

func newHarnessWithModules(t *testing.T, lister contracts.ModuleLister) *harness {
	t.Helper()
	h := newHarness(t, nil, nil, plugins.Config{})
	provide[contracts.ModuleLister](t, h.ctx, contracts.KeyModuleLister, lister)
	return h
}

func TestPluginsBuiltInModulesCollapsedAndExpanded(t *testing.T) {
	lister := modulesLister{modules: []contracts.ModuleInfo{
		{EntryID: "appinfo", ModuleID: "zeokeel/appinfo", State: "active"},
		{EntryID: "store", ModuleID: "zeokeel/sessionstore", State: "disposed"},
	}}
	h := newHarnessWithModules(t, lister)
	tester := h.tester()
	h.assertTexts(tester, "内置模块", "2 个模块", "编译期装配，只读")
	h.assertNoTexts(tester, "zeokeel/appinfo")

	h.click(tester, "展开模块列表")
	h.assertTexts(tester, "appinfo", "zeokeel/appinfo", "active",
		"store", "zeokeel/sessionstore", "disposed")
}

func TestPluginsBuiltInModulesHiddenWithoutService(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "内置模块", "编译期装配，只读")
}

func TestPluginsBuiltInModulesEnglish(t *testing.T) {
	lister := modulesLister{modules: []contracts.ModuleInfo{
		{EntryID: "appinfo", ModuleID: "zeokeel/appinfo", State: "active"},
	}}
	h := newHarnessWithModules(t, lister)
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Built-in modules", "1 module", "Compiled-in, read-only")
	h.click(tester, "Show modules")
	h.assertTexts(tester, "appinfo", "zeokeel/appinfo", "active")
}
