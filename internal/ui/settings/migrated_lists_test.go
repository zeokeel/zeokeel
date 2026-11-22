package settings_test

// Frame tests of the settings collections migrated to the official ui.List: the
// list/listitem accessibility roles and row names, keyboard row selection, key
// stability across filtering/reordering, and the preserved inline row actions.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// requireList asserts the official List with the given accessible name.
func requireList(t *testing.T, tester *ui.Tester, name string) ui.AccessInfo {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleList, name)
	if !ok {
		t.Fatalf("frame has no list named %q; texts = %v", name, tester.Texts())
	}
	return info
}

// requireListItem asserts one row is exposed as a list item named by its text.
func requireListItem(t *testing.T, tester *ui.Tester, name string) ui.AccessInfo {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleListItem, name)
	if !ok {
		t.Fatalf("frame has no list item named %q; texts = %v", name, tester.Texts())
	}
	return info
}

// chooseListItem chooses one row with the pointer: it clicks the row body so
// the List's own click selection picks it, scrolling the settings body first
// when the row is outside the visible page area.
func chooseListItem(t *testing.T, h *harness, tester *ui.Tester, name string) {
	t.Helper()
	info := requireListItem(t, tester, name)
	for range 24 {
		centerX, centerY := info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2
		if info.Bounds.W > 0 && info.Bounds.H > 0 &&
			centerX >= 260 && centerX <= 1000 && centerY >= 90 && centerY <= 760 {
			tester.ClickAt(centerX, centerY)
			tester.Frame()
			return
		}
		// Settings surface at the 1200x800 test size: navigation 190-390, page
		// body 390-1010, panel 24-776.
		x, y := float32(700), float32(500)
		if centerX < 400 {
			x = 300
		}
		delta := centerY - 480
		if delta > 220 {
			delta = 220
		}
		if delta < -220 {
			delta = -220
		}
		if delta == 0 {
			delta = 120
		}
		tester.Scroll(x, y, 0, delta)
		tester.Frame()
		info = requireListItem(t, tester, name)
	}
	t.Fatalf("could not bring list row %q into view; texts = %v", name, tester.Texts())
}

// pressFocusedKey sends one key to the row the pointer just chose.
func pressFocusedKey(t *testing.T, tester *ui.Tester, key ui.Key) {
	t.Helper()
	tester.Key(0, key)
	tester.Frame()
}

// --- memory page ------------------------------------------------------------

// --- model catalog ----------------------------------------------------------

func TestModelCatalogListSemanticsAndKeyboard(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "deepseek-chat", Name: "DeepSeek Chat", BaseURL: "https://api.deepseek.com/v1"},
			contracts.ModelInfo{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", BaseURL: "https://api.deepseek.com/v1"},
		),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")

	requireList(t, tester, "模型列表")
	requireListItem(t, tester, "DeepSeek Chat")
	requireListItem(t, tester, "DeepSeek Reasoner")

	chooseListItem(t, h, tester, "DeepSeek Chat")
	if !requireListItem(t, tester, "DeepSeek Chat").Selected {
		t.Fatalf("clicked model row is not selected")
	}
	pressFocusedKey(t, tester, ui.KeyDown)
	if !requireListItem(t, tester, "DeepSeek Reasoner").Selected {
		t.Fatalf("Down did not move the model selection; texts = %v", tester.Texts())
	}
	// Enter invents no row action (the editor stays closed).
	pressFocusedKey(t, tester, ui.KeyEnter)
	if tester.HasText("保存模型") {
		t.Fatalf("Enter on a model row opened an action the pre-migration row did not have")
	}
	// The inline edit action still opens the editor.
	h.click(tester, "编辑模型 deepseek-chat")
	h.assertTexts(tester, "编辑模型", "保存模型")
}

// --- LSP servers ------------------------------------------------------------

func TestLSPListSemanticsAndKeyboard(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServers: `[{"name":"go","command":"gopls"},{"name":"rs","command":"rust-analyzer"}]`,
	})
	h, tester := lspPage(t, store)

	requireList(t, tester, "语言服务器列表")
	requireListItem(t, tester, "go")
	requireListItem(t, tester, "rs")

	chooseListItem(t, h, tester, "go")
	if !requireListItem(t, tester, "go").Selected {
		t.Fatalf("clicked LSP row is not selected")
	}
	pressFocusedKey(t, tester, ui.KeyDown)
	if !requireListItem(t, tester, "rs").Selected {
		t.Fatalf("Down did not move the LSP selection; texts = %v", tester.Texts())
	}
	// The inline set-active action still writes the store.
	h.click(tester, "设为活动")
	if got := store.get(contracts.SettingLSPActive); got != "rs" {
		t.Fatalf("stored lsp.active = %q, want rs", got)
	}
}
