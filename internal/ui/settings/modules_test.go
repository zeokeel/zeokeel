package settings_test

// Tests for the diagnostics page's loaded-module list (host KeyModuleLister):
// the count row, the expandable mono rows, the 40-row cap, absence without the
// service and the English copy.

import (
	"fmt"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// modulesLister is a scripted contracts.ModuleLister.
type modulesLister struct{ modules []contracts.ModuleInfo }

func (l modulesLister) Modules() []contracts.ModuleInfo {
	return append([]contracts.ModuleInfo(nil), l.modules...)
}

func TestDiagnosticsLoadedModules(t *testing.T) {
	lister := modulesLister{modules: []contracts.ModuleInfo{
		{EntryID: "appinfo", ModuleID: "zeokeel/appinfo", State: "active"},
		{EntryID: "store", ModuleID: "zeokeel/sessionstore", State: "active"},
		{EntryID: "skills", ModuleID: "zeokeel/skills", State: "failed"},
	}}
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "zh")
	provide[contracts.ModuleLister](t, h.ctx, contracts.KeyModuleLister, lister)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "诊断")

	h.assertTexts(tester, "已加载模块 3 个")
	h.assertNoTexts(tester, "zeokeel/appinfo", "zeokeel/skills")
	h.click(tester, "展开模块列表")
	h.assertTexts(tester, "appinfo", "zeokeel/appinfo", "active",
		"store", "zeokeel/sessionstore", "skills", "zeokeel/skills", "failed")
}

func TestDiagnosticsLoadedModulesCappedAt40(t *testing.T) {
	modules := make([]contracts.ModuleInfo, 0, 42)
	for index := 0; index < 42; index++ {
		modules = append(modules, contracts.ModuleInfo{
			EntryID: fmt.Sprintf("entry-%02d", index), ModuleID: fmt.Sprintf("mod/%02d", index), State: "active",
		})
	}
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	provide[contracts.ModuleLister](t, h.ctx, contracts.KeyModuleLister, modulesLister{modules: modules})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "诊断")
	h.assertTexts(tester, "已加载模块 42 个")
	h.click(tester, "展开模块列表")
	h.assertTexts(tester, "entry-39", "…")
	h.assertNoTexts(tester, "entry-40", "entry-41")
}

func TestDiagnosticsLoadedModulesHiddenWithoutService(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "诊断")
	h.assertNoTexts(tester, "已加载模块", "展开模块列表")
}

func TestDiagnosticsLoadedModulesEnglish(t *testing.T) {
	lister := modulesLister{modules: []contracts.ModuleInfo{
		{EntryID: "appinfo", ModuleID: "zeokeel/appinfo", State: "active"},
	}}
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	provide[contracts.ModuleLister](t, h.ctx, contracts.KeyModuleLister, lister)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Diagnostics")
	h.assertTexts(tester, "Loaded modules: 1")
	h.click(tester, "Show modules")
	h.assertTexts(tester, "appinfo", "zeokeel/appinfo", "active")
}
