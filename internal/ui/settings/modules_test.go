package settings_test

// Tests for the diagnostics page's loaded-module list (host KeyModuleLister):
// the count row, the expandable mono rows, the 40-row cap, absence without the
// service and the English copy.

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

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
	// Nothing of the collection shows until the disclosure opens.
	h.assertNoTexts(tester, "zeokeel/appinfo", "zeokeel/skills")
	h.click(tester, "展开模块列表")
	h.assertTexts(tester, "appinfo", "zeokeel/appinfo", "active",
		"store", "zeokeel/sessionstore", "skills", "zeokeel/skills", "failed")

	// The rows are the official List: list/listitem roles, keyed and named by
	// the assembly entry id, single-row selection for the keyboard.
	requireList(t, tester, "已加载模块列表")
	first := requireListItem(t, tester, "appinfo")
	if first.PosInSet != 1 || first.SetSize != 3 {
		t.Fatalf("first module row position = %d of %d, want 1 of 3", first.PosInSet, first.SetSize)
	}
	requireListItem(t, tester, "store")
	requireListItem(t, tester, "skills")
	if err := tester.Click("appinfo"); err != nil {
		t.Fatalf("click module row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	if !requireListItem(t, tester, "store").Selected {
		t.Fatalf("Down did not move the loaded-module selection; texts = %v", tester.Texts())
	}
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
	if rows := requireList(t, tester, "已加载模块列表"); rows.Bounds.H <= 0 {
		t.Fatalf("loaded-module list has no laid-out area")
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "entry-40"); ok {
		t.Fatalf("the loaded-module list renders past the 40-row cap")
	}
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
