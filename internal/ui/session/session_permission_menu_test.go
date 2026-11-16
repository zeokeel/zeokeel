package session_test

// Frame tests for the composer's permission control: the toolbar button shows
// the current permission preset itself (工作区修改 / 所有权限 / 只读), and its
// menu offers only those three choices. The model and reasoning choices stay in
// the model chip, which the official toolbar folds into its overflow menu when a
// narrow composer runs out of room.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sessionMenuPermissionLabels is the full menu the simplification allows: the
// disabled group header, one separator and the three permission choices in the
// approved order (workspace-modify / all permissions / read-only).
var sessionMenuPermissionLabels = []string{"权限模式", "-", "工作区修改", "所有权限", "只读"}

// assertSessionMenuOnlyPermissions fails unless the open menu is exactly the
// permission group: no chat-mode, persona, prompt-preset, behavior-settings or
// clear-remembered-tools rows remain reachable from the composer.
func assertSessionMenuOnlyPermissions(t *testing.T, tester *ui.Tester) {
	t.Helper()
	got := tester.Menu()
	if len(got) != len(sessionMenuPermissionLabels) {
		t.Fatalf("session menu labels = %v, want exactly %v", got, sessionMenuPermissionLabels)
	}
	for i, want := range sessionMenuPermissionLabels {
		if got[i] != want {
			t.Fatalf("session menu labels = %v, want exactly %v", got, sessionMenuPermissionLabels)
		}
	}
}

// assertNoSessionSettingsLabel proves the removed stable destination name is
// gone from the frame text, any open menu and every accessible name.
func assertNoSessionSettingsLabel(t *testing.T, tester *ui.Tester) {
	t.Helper()
	for _, text := range tester.Texts() {
		if strings.Contains(text, "会话设置") || strings.Contains(text, "Session settings") {
			t.Fatalf("frame still shows the removed 会话设置 label: %q; texts = %v", text, tester.Texts())
		}
	}
	for _, label := range tester.Menu() {
		if strings.Contains(label, "会话设置") || strings.Contains(label, "Session settings") {
			t.Fatalf("menu still shows the removed 会话设置 label: %q; menu = %v", label, tester.Menu())
		}
	}
	for _, a := range tester.AccessAll() {
		if strings.Contains(a.Name, "会话设置") || strings.Contains(a.Name, "Session settings") {
			t.Fatalf("accessible name still says 会话设置: %+v", a)
		}
	}
}

// TestSessionMenuOnlyPermissionGroup covers requirement 1: opening the
// permission button shows the permission group only, never the removed
// destinations, and the removed 会话设置 label exists nowhere.
func TestSessionMenuOnlyPermissionGroup(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), newPermissionSettings(nil))
	tester := h.tester()
	assertNoSessionSettingsLabel(t, tester)
	h.openSessionMenu(tester)
	assertSessionMenuOnlyPermissions(t, tester)
	assertNoSessionSettingsLabel(t, tester)
	for _, gone := range []string{
		"对话模式", "普通模式", "计划模式", "精简模式", "创作模式",
		"人设", "插入预设", "打开行为设置…", "清除自动允许的工具",
		"模型：", "推理等级", "打开模型设置…",
	} {
		if tester.HasText(gone) {
			t.Fatalf("session menu still shows %q; texts = %v", gone, tester.Texts())
		}
	}
}

// TestPermissionButtonShowsCurrentPermission covers the toolbar button: it is
// labelled with the current permission preset and follows settings changes; the
// menu header no longer repeats it as a trailing value.
func TestPermissionButtonShowsCurrentPermission(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.assertTexts(tester, "工作区修改")
	h.assertNoTexts(tester, "所有权限", "只读")
	assertNoSessionSettingsLabel(t, tester)

	settings.set(contracts.SettingAgentPermission, contracts.PermissionFull)
	tester.Frame()
	h.assertTexts(tester, "所有权限")
	h.assertNoTexts(tester, "工作区修改", "只读")
	assertNoSessionSettingsLabel(t, tester)

	settings.set(contracts.SettingAgentPermission, contracts.PermissionReadOnly)
	tester.Frame()
	h.assertTexts(tester, "只读")
	h.assertNoTexts(tester, "工作区修改", "所有权限")
	assertNoSessionSettingsLabel(t, tester)

	// The menu header is only the group name: the button already shows the
	// current value, so the menu does not repeat it as a trailing node.
	settings.set(contracts.SettingAgentPermission, contracts.PermissionWorkspace)
	tester.Frame()
	h.openSessionMenu(tester)
	assertSessionMenuOnlyPermissions(t, tester)
	if got := exactTextCount(tester.Texts(), "工作区修改"); got != 3 {
		t.Fatalf("current permission appears %d times, want button + option (3); texts = %v", got, tester.Texts())
	}
}

// TestSessionMenuSelectAllPermissions covers the write-through: choosing
// 所有权限 stores agent.permission, the button label follows and the menu marks
// the choice on the next open.
func TestSessionMenuSelectAllPermissions(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.assertTexts(tester, "工作区修改")
	h.openSessionMenu(tester)
	h.clickExact(tester, "所有权限")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionFull {
		t.Fatalf("selecting 所有权限 stored %q, want %q", got, contracts.PermissionFull)
	}
	// The button now names the chosen preset; the stable label is gone.
	h.assertTexts(tester, "所有权限")
	h.assertNoTexts(tester, "工作区修改")
	assertNoSessionSettingsLabel(t, tester)

	h.openSessionMenu(tester)
	full, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "所有权限")
	if !ok || !full.Checked {
		t.Fatalf("所有权限 is not the checked radio after choosing it; accessibility = %+v found %v", full, ok)
	}
	workspace, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "工作区修改")
	if !ok || workspace.Checked {
		t.Fatalf("工作区修改 is still checked after choosing 所有权限; accessibility = %+v found %v", workspace, ok)
	}
}

// assertModelEntryReachable proves the model entry can be reached at the current
// width without the permission menu: it is a toolbar control, or (when the
// official toolbar folded it away) a row of the toolbar's overflow menu.
func assertModelEntryReachable(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if _, ok := tester.Find(label); ok {
		return
	}
	if err := tester.Click("更多"); err != nil {
		t.Fatalf("model entry %q is neither on the toolbar nor behind the toolbar overflow: %v; texts = %v",
			label, err, tester.Texts())
	}
	tester.Frame()
	if !menuHasLabel(tester, label) {
		t.Fatalf("toolbar overflow menu lacks the model entry %q; labels = %v", label, tester.Menu())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
}

// openNarrowModelMenu opens the model chip's menu at the current width: directly
// when the toolbar kept the chip, or through the toolbar's overflow menu when it
// folded the chip away.
func openNarrowModelMenu(t *testing.T, tester *ui.Tester, chipLabel string) {
	t.Helper()
	if rect, ok := tester.Find(chipLabel); ok {
		tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		tester.Frame()
		if !menuHasLabel(tester, "模型") && !menuHasLabel(tester, "Model") {
			t.Fatalf("model chip %q did not open its menu; labels = %v", chipLabel, tester.Menu())
		}
		return
	}
	if err := tester.Click("更多"); err != nil {
		t.Fatalf("model chip %q is neither on the toolbar nor behind the toolbar overflow: %v; texts = %v",
			chipLabel, err, tester.Texts())
	}
	tester.Frame()
	if !menuHasLabel(tester, chipLabel) {
		t.Fatalf("toolbar overflow menu lacks the model chip %q; labels = %v", chipLabel, tester.Menu())
	}
	if err := tester.ChooseMenuItem(chipLabel); err != nil {
		t.Fatalf("choose the folded model chip %q: %v", chipLabel, err)
	}
	if !menuHasLabel(tester, "模型") && !menuHasLabel(tester, "Model") {
		t.Fatalf("the folded model chip %q did not open the model menu; labels = %v", chipLabel, tester.Menu())
	}
}

// TestVeryCompactComposerKeepsModelEntry covers requirement 4: at a very compact
// composer the model chip is still built, so the official toolbar either keeps
// it on the row or folds it into its overflow menu; the permission menu carries
// only the permission group and is no longer the model/reasoning fallback.
func TestVeryCompactComposerKeepsModelEntry(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{{
		ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
		DefaultThinking: contracts.ThinkingModeQwenDeep,
	}})
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8"), settings,
		settingsModelCatalog{settings})
	tester := h.tester()
	tester.SetSize(704, 800)
	for i := 0; i < 3; i++ {
		tester.Frame()
	}

	h.openSessionMenu(tester)
	assertSessionMenuOnlyPermissions(t, tester)
	h.assertNoTexts(tester, "模型：", "推理等级", "打开模型设置…")
	assertNoSessionSettingsLabel(t, tester)
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	openNarrowModelMenu(t, tester, "qwen3.8")
	assertMenuLabels(t, tester, "模型", "推理等级", "-", "打开模型设置…")
}
