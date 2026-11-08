package settings_test

// Tests for the H1/H2 settings fixes: explicit-save pages put their single
// primary action in the host's fixed footer (visible without scrolling) and
// report unsaved edits so closing the surface asks first; the model page's
// connection check tests the drafts, not the saved store.

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// closeSettings clicks the settings surface's close button.
func closeSettings(h *harness, tester *ui.Tester) {
	h.t.Helper()
	h.click(tester, "关闭设置")
}

// TestH1ModelFooterPinnedAndGuarded covers the model page: Save is in the fixed
// footer (visible without scrolling to the end of the body), editing shows the
// host "unsaved" state, and closing with unsaved edits asks first.
func TestH1ModelFooterPinnedAndGuarded(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")

	// The save action is pinned in the footer: a direct click (no scrolling)
	// reaches it on the first screen.
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("footer save is not visible without scrolling: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()

	h.focusInput(tester, "Base URL 输入")
	tester.Type("https://draft.example/v1")
	tester.Frame()
	h.assertTexts(tester, "未保存")

	closeSettings(h, tester)
	if !h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("closing with unsaved edits closed the settings surface")
	}
	h.assertTexts(tester, "有未保存的修改", "继续编辑", "放弃修改")
	h.click(tester, "继续编辑")
	h.assertNoTexts(tester, "有未保存的修改")

	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	closeSettings(h, tester)
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("closing after save kept the settings surface open")
	}
	if got := h.settings.get(contracts.SettingsChatBaseURL); got != "https://draft.example/v1" {
		t.Fatalf("saved base URL = %q", got)
	}
}

// TestH1AppearanceFooterDirty covers the appearance page: Apply is pinned and
// an unapplied hotkey draft guards closing; Apply clears it.
func TestH1AppearanceFooterDirty(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")
	if err := tester.Click("应用"); err != nil {
		t.Fatalf("footer apply is not visible without scrolling: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()

	h.focusInput(tester, "全局热键 输入")
	tester.Type("CmdOrCtrl+Alt+K")
	tester.Frame()
	h.assertTexts(tester, "未保存")
	closeSettings(h, tester)
	if !h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("closing with an unapplied hotkey closed the settings surface")
	}
	h.assertTexts(tester, "有未保存的修改")
	h.click(tester, "继续编辑")
	h.click(tester, "应用")
	h.assertTexts(tester, "已保存")
	closeSettings(h, tester)
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("closing after Apply kept the settings surface open")
	}
	if got := store.get(contracts.SettingGlobalHotkey); got != "CmdOrCtrl+Alt+K" {
		t.Fatalf("applied hotkey = %q", got)
	}
}

// TestH1NetworkAndSearchDirty covers the explicit-save network and search pages:
// their footers save, and a draft difference marks the page unsaved.
func TestH1NetworkAndSearchDirty(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "网络")
	h.focusInput(tester, "代理地址 输入")
	tester.Type("http://127.0.0.1:7890")
	tester.Frame()
	h.assertTexts(tester, "未保存")
	closeSettings(h, tester)
	if !h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("closing with an unsaved proxy closed the settings surface")
	}
	h.click(tester, "放弃修改")
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("discarding the edits did not close the settings surface")
	}

	h2 := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester2 := h2.tester()
	tester2.Frame()
	h2.click(tester2, "搜索")
	h2.focusInput(tester2, "搜索端点 输入")
	tester2.Type("https://search.example.com/search")
	tester2.Frame()
	h2.assertTexts(tester2, "未保存")
	closeSettings(h2, tester2)
	if !h2.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("closing with an unsaved endpoint closed the settings surface")
	}
}

// TestH2ModelCheckUsesDrafts covers the H2 fix: the check sends the typed base
// URL (not the saved one) and does not persist anything.
func TestH2ModelCheckUsesDrafts(t *testing.T) {
	var gotURL string
	withModelListGetter(t, func(_ context.Context, baseURL, _ string) (int, []byte, error) {
		gotURL = baseURL
		return 200, nil, nil
	})
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatBaseURL: "https://saved.example/v1",
		contracts.SettingsChatModel:   "saved-model",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.focusInput(tester, "Base URL 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("https://draft.example/v1")
	tester.Frame()
	h.click(tester, "测试当前输入")
	waitModelCheckText(t, h, tester, "连接正常")
	if gotURL != "https://draft.example/v1" {
		t.Fatalf("check used %q, want the typed draft", gotURL)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://saved.example/v1" {
		t.Fatalf("the check saved the draft: %q", got)
	}
}

// TestN1DiscardResetsModelPage covers the host's Discard hook end to end: a
// dirty model page, "放弃修改", then reopening shows the saved values with no
// unsaved state and no close guard.
func TestN1DiscardResetsModelPage(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "saved-model",
		contracts.SettingsChatBaseURL: "https://saved.example/v1",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")

	h.focusInput(tester, "Base URL 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("https://draft.example/v1")
	tester.Frame()
	h.assertTexts(tester, "未保存")

	closeSettings(h, tester)
	h.assertTexts(tester, "有未保存的修改")
	h.click(tester, "放弃修改")
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("discard did not close the settings surface")
	}

	// Reopen: the abandoned draft is gone, so the page is clean and saving
	// writes the saved values again (never the discarded draft).
	h.uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	h.click(tester, "模型")
	h.assertNoTexts(tester, "未保存")
	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://saved.example/v1" {
		t.Fatalf("saving after discard wrote %q, want the saved value", got)
	}
	closeSettings(h, tester)
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("a clean page after discard still guarded closing")
	}
}

// TestN1DiscardResetsOtherDirtyPages covers the appearance, network and search
// Discard hooks at behavior level: discard closes, reopen is clean and closes
// without the unsaved guard.
func TestN1DiscardResetsOtherDirtyPages(t *testing.T) {
	cases := []struct {
		page   string
		edit   func(t *testing.T, h *harness, tester *ui.Tester)
		reopen func(t *testing.T, h *harness, tester *ui.Tester)
	}{
		{
			page: "外观",
			edit: func(t *testing.T, h *harness, tester *ui.Tester) {
				h.focusInput(tester, "全局热键 输入")
				tester.Type("CmdOrCtrl+Alt+J")
				tester.Frame()
			},
			reopen: func(t *testing.T, h *harness, tester *ui.Tester) { h.click(tester, "外观") },
		},
		{
			page: "网络",
			edit: func(t *testing.T, h *harness, tester *ui.Tester) {
				h.focusInput(tester, "代理地址 输入")
				tester.Type("http://127.0.0.1:7890")
				tester.Frame()
			},
			reopen: func(t *testing.T, h *harness, tester *ui.Tester) { h.click(tester, "网络") },
		},
		{
			page: "搜索",
			edit: func(t *testing.T, h *harness, tester *ui.Tester) {
				h.focusInput(tester, "搜索端点 输入")
				tester.Type("https://search.example.com/search")
				tester.Frame()
			},
			reopen: func(t *testing.T, h *harness, tester *ui.Tester) { h.click(tester, "搜索") },
		},
	}
	for _, test := range cases {
		test := test
		t.Run(test.page, func(t *testing.T) {
			store := newFakeSettings(nil)
			h := newHarness(t, &fakeChat{}, store, settings.Config{})
			tester := h.tester()
			tester.Frame()
			h.click(tester, test.page)
			test.edit(t, h, tester)
			h.assertTexts(tester, "未保存")

			closeSettings(h, tester)
			h.assertTexts(tester, "有未保存的修改")
			h.click(tester, "放弃修改")
			if h.uiRoot.Navigator().SettingsOpen() {
				t.Fatal("discard did not close the settings surface")
			}

			h.uiRoot.Navigator().ToggleSettings()
			tester.Frame()
			test.reopen(t, h, tester)
			h.assertNoTexts(tester, "未保存")
			closeSettings(h, tester)
			if h.uiRoot.Navigator().SettingsOpen() {
				t.Fatal("a clean page after discard still guarded closing")
			}
		})
	}
}

// TestN3AnyDirtyPageGuardsClosing covers the host's any-dirty-page close guard:
// an edit made on the model page still guards closing after switching to a page
// that applies immediately.
func TestN3AnyDirtyPageGuardsClosing(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingsChatModel: "saved-model"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.focusInput(tester, "模型 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("dirty-model")
	tester.Frame()

	// Switch to a page with no Dirty: the host's header still reports the
	// unsaved edit made on the model page (N3), and the close guard fires.
	h.click(tester, "行为")
	h.assertTexts(tester, "未保存")
	closeSettings(h, tester)
	if !h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("closing did not guard an unsaved edit made on another page")
	}
	h.assertTexts(tester, "有未保存的修改")
	h.click(tester, "放弃修改")
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("discard after a page switch did not close")
	}
	// The model-page edit was discarded by the host's Discard hook.
	h.uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	h.click(tester, "模型")
	h.assertNoTexts(tester, "未保存")
	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatModel); got != "saved-model" {
		t.Fatalf("saved model after discard = %q, want saved-model", got)
	}
}
