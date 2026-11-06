package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// openBuiltinPlugins opens Settings > Built-in plugins on the live fixture.
func openBuiltinPlugins(t *testing.T, h *liveApp, tt *ui.Tester) {
	t.Helper()
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Built-in plugins"); err != nil {
		t.Fatalf("click Built-in plugins: %v", err)
	}
}

// TestBuiltinPluginsListsLSP proves the LSP tool is a first-class built-in
// plugin: it has a row in Settings > Built-in plugins (default on), the row
// describes the tool and its lsp.servers configuration, and toggling it
// persists plugin.lsp.enabled so the next agent build drops the tool.
func TestBuiltinPluginsListsLSP(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openBuiltinPlugins(t, h, tt)

	if !tt.HasText("Language server tools") {
		t.Fatalf("built-in plugins page missing the LSP row: %v", tt.Texts())
	}
	if !tt.HasText("lsp.servers") {
		t.Fatalf("LSP row does not mention its lsp.servers configuration: %v", tt.Texts())
	}
	// Default on: the toggle is currently enabled, so one click disables it.
	if !tt.HasText("Enable Language server tools") {
		t.Fatalf("LSP toggle does not report its default-on state: %v", tt.Texts())
	}
	tt.Click("Language server tools")
	tt.Frame()
	raw, err := h.store.GetSetting(session.PluginSettingKey("lsp"))
	if err != nil {
		t.Fatalf("read plugin.lsp.enabled: %v", err)
	}
	if raw != "false" {
		t.Fatalf("plugin.lsp.enabled = %q, want false after the toggle", raw)
	}
	if enabled, err := h.ctrl.PluginEnabled("lsp"); err != nil || enabled {
		t.Fatalf("PluginEnabled(lsp) = %v, %v; want false", enabled, err)
	}
	if !tt.HasText("Plugin disabled") {
		t.Fatalf("disable toast missing: %v", tt.Texts())
	}
	// Toggling back restores the default.
	tt.Click("Language server tools")
	tt.Frame()
	if raw, err := h.store.GetSetting(session.PluginSettingKey("lsp")); err != nil || raw != "true" {
		t.Fatalf("plugin.lsp.enabled = %q, %v; want true after the second toggle", raw, err)
	}
	if enabled, err := h.ctrl.PluginEnabled("lsp"); err != nil || !enabled {
		t.Fatalf("PluginEnabled(lsp) after re-enable = %v, %v; want true", enabled, err)
	}
}

// TestPluginsPageListsLSP proves the top-level Plugins page lists the LSP
// plugin next to the other built-ins and toggles the same persisted key.
func TestPluginsPageListsLSP(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins nav: %v", err)
	}
	if !tt.HasText("Language server tools") {
		t.Fatalf("Plugins page missing the LSP row: %v", tt.Texts())
	}
	if err := tt.Click("Enable Language server tools"); err != nil {
		t.Fatalf("click the LSP switch: %v", err)
	}
	if raw, err := h.store.GetSetting(session.PluginSettingKey("lsp")); err != nil || raw != "false" {
		t.Fatalf("plugin.lsp.enabled = %q, %v; want false from the Plugins page toggle", raw, err)
	}
}

// TestChinesePluginsPageLocalizesLSP proves the LSP row stays bilingual without
// an i18n catalog entry: the built-in plugin definitions carry the Chinese title
// and description inline, like the other plugin rows.
func TestChinesePluginsPageLocalizesLSP(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("内置插件"); err != nil {
		t.Fatalf("open Chinese Built-in plugins: %v", err)
	}
	if !tt.HasText("语言服务器工具") {
		t.Fatalf("Chinese built-in plugins page missing the LSP row: %v", tt.Texts())
	}
	if tt.HasText("Language server tools") {
		t.Fatalf("Chinese page still shows the English LSP title: %v", tt.Texts())
	}
	if !tt.HasText("启用语言服务器工具") {
		t.Fatalf("Chinese LSP toggle label missing: %v", tt.Texts())
	}

	tt.Key(0, ui.KeyEscape)
	h.app.Screen = Plugins
	tt.Frame()
	if !tt.HasText("语言服务器工具") {
		t.Fatalf("Chinese Plugins page missing the LSP row: %v", tt.Texts())
	}
	if !tt.HasText("启用语言服务器工具") {
		t.Fatalf("Chinese Plugins page missing the localized LSP switch: %v", tt.Texts())
	}
}
