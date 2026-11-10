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

func TestMemoryListSemanticsAndKeyboard(t *testing.T) {
	store := newFakeMemoryStore(
		memoryEntry("mem-1", "Alpha 计划"),
		memoryEntry("mem-2", "Beta 计划"),
	)
	h := memoryPageHarness(t, store)
	tester := h.tester()

	requireList(t, tester, "记忆列表")
	if first := requireListItem(t, tester, "Alpha 计划"); first.PosInSet != 1 || first.SetSize != 2 {
		t.Fatalf("first row position = %d of %d, want 1 of 2", first.PosInSet, first.SetSize)
	}
	requireListItem(t, tester, "Beta 计划")

	// A click chooses the row; Down moves the List's selection.
	chooseListItem(t, h, tester, "Alpha 计划")
	if !requireListItem(t, tester, "Alpha 计划").Selected {
		t.Fatalf("clicked memory row is not selected")
	}
	pressFocusedKey(t, tester, ui.KeyDown)
	if !requireListItem(t, tester, "Beta 计划").Selected {
		t.Fatalf("Down did not move the memory selection; texts = %v", tester.Texts())
	}
	if requireListItem(t, tester, "Alpha 计划").Selected {
		t.Fatalf("previous memory row stayed selected after Down")
	}
	// Enter invents no row action: the edit dialog stays closed.
	pressFocusedKey(t, tester, ui.KeyEnter)
	if tester.HasText("编辑记忆") {
		t.Fatalf("Enter on a memory row opened an action the pre-migration row did not have")
	}
	// The row's ⋯ menu still works.
	h.click(tester, "记忆操作")
	h.click(tester, "删除记忆")
	h.assertTexts(tester, "删除记忆")
	h.click(tester, "取消")

	// Filtering keeps the choice on its item: choose Beta, then narrow the list
	// to Beta; the row keeps its key and stays chosen.
	chooseListItem(t, h, tester, "Beta 计划")
	h.focusInput(tester, "搜索记忆")
	tester.Type("Beta")
	tester.Frame()
	requireList(t, tester, "记忆列表")
	if !requireListItem(t, tester, "Beta 计划").Selected {
		t.Fatalf("filtered memory list lost the chosen row; texts = %v", tester.Texts())
	}
	if tester.HasText("Alpha 计划") {
		t.Fatalf("search filter did not narrow the list; texts = %v", tester.Texts())
	}
}

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

// --- MCP servers ------------------------------------------------------------

func TestMCPListSemanticsAndKeyboard(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpTwoJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	requireList(t, tester, "MCP 服务器列表")
	requireListItem(t, tester, "files")
	requireListItem(t, tester, "web")

	chooseListItem(t, h, tester, "files")
	if !requireListItem(t, tester, "files").Selected {
		t.Fatalf("clicked MCP row is not selected")
	}
	pressFocusedKey(t, tester, ui.KeyDown)
	if !requireListItem(t, tester, "web").Selected {
		t.Fatalf("Down did not move the MCP selection; texts = %v", tester.Texts())
	}
	// The row's ⋯ remove action keeps its confirmation flow.
	h.click(tester, "服务器操作")
	h.click(tester, "移除")
	h.assertTexts(tester, "移除服务器")
	h.click(tester, "取消")
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

// --- presets ----------------------------------------------------------------

func TestPromptPresetListSemanticsKeyboardAndReorderKeys(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)

	requireList(t, tester, "预设列表")
	requireListItem(t, tester, "简洁回答")
	requireListItem(t, tester, "代码审查")

	chooseListItem(t, h, tester, "简洁回答")
	if !requireListItem(t, tester, "简洁回答").Selected {
		t.Fatalf("clicked preset row is not selected")
	}
	pressFocusedKey(t, tester, ui.KeyDown)
	if !requireListItem(t, tester, "代码审查").Selected {
		t.Fatalf("Down did not move the preset selection; texts = %v", tester.Texts())
	}

	// Choose the first row, move it down: the row state and the choice follow
	// the item's key, and the inline move action still persists the new order.
	chooseListItem(t, h, tester, "简洁回答")
	if err := tester.Click("预设操作"); err != nil {
		t.Fatalf("open preset row menu: %v; texts = %v", err, tester.Texts())
	}
	if err := tester.Click("下移"); err != nil {
		t.Fatalf("click 下移: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	stored := decodePresets(t, store.get(contracts.SettingPromptPresets))
	if len(stored) < 2 || stored[0].Name != "代码审查" || stored[1].Name != "简洁回答" {
		t.Fatalf("stored presets after move = %+v", stored)
	}
	if !requireListItem(t, tester, "简洁回答").Selected {
		t.Fatalf("reorder lost the chosen preset (the key must follow the item); texts = %v", tester.Texts())
	}
	// Enter invents no row action: no delete/restore dialog opens.
	pressFocusedKey(t, tester, ui.KeyEnter)
	if tester.HasText("确认删除") {
		t.Fatalf("Enter on a preset row opened an action the pre-migration row did not have")
	}
}

func TestAgentPresetListSemanticsAndKeyboard(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAgentPresets: `[{"id":"p1","name":"Reviewer"},{"id":"p2","name":"Writer"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)

	requireList(t, tester, "代理预设列表")
	requireListItem(t, tester, "Reviewer")
	requireListItem(t, tester, "Writer")

	chooseListItem(t, h, tester, "Reviewer")
	if !requireListItem(t, tester, "Reviewer").Selected {
		t.Fatalf("clicked agent-preset row is not selected")
	}
	pressFocusedKey(t, tester, ui.KeyDown)
	if !requireListItem(t, tester, "Writer").Selected {
		t.Fatalf("Down did not move the agent-preset selection; texts = %v", tester.Texts())
	}
}
