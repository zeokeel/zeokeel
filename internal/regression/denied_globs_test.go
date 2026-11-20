package regression

// RG-10 (2026-11 audit): the opt-in denied-globs field on the Data & privacy
// page. The UI edits toolkit.deniedGlobs as comma-separated globs, encodes the
// JSON list the toolkit reads on save, loads the stored list back into the
// draft, and discloses that the default empty hides nothing, the shell bypass
// remains, and a change applies after a restart.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestDeniedGlobsSaveEncodesJSONList covers the save path: typing comma-separated
// globs and pressing the footer Save writes the JSON string list the toolkit
// reads, and the status reminds the user a restart applies it.
func TestDeniedGlobsSaveEncodesJSONList(t *testing.T) {
	harness := startDataPrivacy(t, newFakeChat())
	tester := harness.tester

	// The disclosure states the opt-in default, the shell bypass and the restart.
	requireHasText(t, tester, "文件隐藏（高级）")
	requireHasText(t, tester, "默认留空＝不隐藏任何内容")
	requireHasText(t, tester, "shell 旁路仍在")
	requireHasText(t, tester, "修改后重启应用生效")

	// The draft starts empty (opt-in default) so Save is disabled.
	if err := tester.Click("隐藏的文件 输入"); err != nil {
		t.Fatalf("focus globs input: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("**/.env, **/*.pem")
	tester.Frame()
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("save globs: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "已保存（重启应用后生效）")
	if got := harness.h.settings.Get("toolkit.deniedGlobs", ""); got != `["**/.env","**/*.pem"]` {
		t.Fatalf("saved deniedGlobs = %q, want the JSON list", got)
	}
}

// TestDeniedGlobsLoadsStoredList covers the load path: a stored JSON list renders
// as the comma-separated draft when the page opens.
func TestDeniedGlobsLoadsStoredList(t *testing.T) {
	h := newBareHarness(t)
	if err := h.settings.Set("toolkit.deniedGlobs", `["**/.env","**/*.key"]`); err != nil {
		t.Fatalf("seed deniedGlobs: %v", err)
	}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	provide(t, h.ctx, contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.2.3"})
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 800)
	tester.Frame()
	openSettingsPage(t, tester, 1280, 800, "数据与隐私")
	// A text field's value lives in its accessible Value, not the frame texts.
	info, ok := tester.Access("隐藏的文件 输入")
	if !ok || info.Value != "**/.env, **/*.key" {
		t.Fatalf("loaded globs draft = %+v/%v, want **/.env, **/*.key", info, ok)
	}
}

// TestDeniedGlobsClearOptsOut covers clearing the field: an empty draft saves the
// empty list, so no file is hidden (the opt-in default).
func TestDeniedGlobsClearOptsOut(t *testing.T) {
	h := newBareHarness(t)
	if err := h.settings.Set("toolkit.deniedGlobs", `["**/.env"]`); err != nil {
		t.Fatalf("seed deniedGlobs: %v", err)
	}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	provide(t, h.ctx, contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.2.3"})
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 800)
	tester.Frame()
	openSettingsPage(t, tester, 1280, 800, "数据与隐私")
	if err := tester.Click("隐藏的文件 输入"); err != nil {
		t.Fatalf("focus globs input: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("save cleared globs: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := h.settings.Get("toolkit.deniedGlobs", ""); got != "[]" {
		t.Fatalf("cleared deniedGlobs = %q, want the empty list", got)
	}
}
