package plugins_test

// Tests for the MCP server-level switch (mcp.disabledServers): the card toggle,
// the disabled note, restoring a disabled-but-configured server from
// settings.mcp.servers, case-insensitive stored matching and failure rollback.

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

func TestMCPServerSwitchDisablesAndEnables(t *testing.T) {
	store := newFakePluginSettings(nil)
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{
		Name: "srv", URL: "http://127.0.0.1:9/mcp", Status: "已连接", Tools: 2,
	}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.assertTexts(tester, "srv", "详情")

	// The server's switch lives on its row, visible without expanding details.
	h.click(tester, "启用服务器 srv")
	if got := store.get(contracts.SettingMCPDisabledServers); got != `["srv"]` {
		t.Fatalf("%s = %q, want [%q]", contracts.SettingMCPDisabledServers, got, "srv")
	}
	h.assertTexts(tester, "已停用（重启生效）", "已停用（重启后断开）")

	// Enabling removes it again.
	h.click(tester, "启用服务器 srv")
	if got := store.get(contracts.SettingMCPDisabledServers); got != "[]" {
		t.Fatalf("%s after enable = %q, want []", contracts.SettingMCPDisabledServers, got)
	}
	h.assertTexts(tester, "已启用（重启后连接）")
	h.assertNoTexts(tester, "已停用（重启生效）")
}

func TestMCPServerRestoredFromSettingsWhenDisabled(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingMCPServers:         `[{"name":"srv","url":"http://127.0.0.1:9/mcp"}]`,
		contracts.SettingMCPDisabledServers: `["srv"]`,
	})
	// The toolkit skipped the disabled server at activation: the plugins service
	// has no entry, so the page restores the row from settings.mcp.servers.
	service := &fakePlugins{}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.assertTexts(tester, "srv", "已停用（重启生效）")

	h.click(tester, "详情")
	h.assertTexts(tester, "http://127.0.0.1:9/mcp")
	h.click(tester, "启用服务器 srv")
	if got := store.get(contracts.SettingMCPDisabledServers); got != "[]" {
		t.Fatalf("%s after restore/enable = %q, want []", contracts.SettingMCPDisabledServers, got)
	}
	// E-05: the just-enabled row must not vanish; it says it connects after the
	// next restart (the toolkit starts servers at launch).
	h.assertTexts(tester, "srv", "已启用（重启后连接）")
	h.assertNoTexts(tester, "还没有配置 MCP 服务器")
}

func TestMCPServerSwitchDoesNotRestoreAssemblyRows(t *testing.T) {
	// A disabled server that only the assembly provides cannot be rebuilt from
	// settings.mcp.servers: no synthetic row appears.
	store := newFakePluginSettings(map[string]string{
		contracts.SettingMCPDisabledServers: `["assembly-only"]`,
	})
	service := &fakePlugins{}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.assertNoTexts(tester, "assembly-only")
}

func TestMCPServerSwitchCaseInsensitiveAndSorted(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingMCPDisabledServers: `["B","a"]`,
	})
	service := &fakePlugins{mcp: []contracts.MCPPlugin{
		{Name: "a", Status: "已连接"}, {Name: "b", Status: "已连接"},
	}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.assertTexts(tester, "已停用（重启生效）")

	// Enabling "a" (stored as "a") removes only the case-insensitive match.
	h.click(tester, "启用服务器 a")
	if got := store.get(contracts.SettingMCPDisabledServers); got != `["B"]` {
		t.Fatalf("%s after enable a = %q, want [\"B\"]", contracts.SettingMCPDisabledServers, got)
	}
}

func TestMCPServerSwitchFailureRollsBack(t *testing.T) {
	store := newFakePluginSettings(nil)
	store.setErr = errors.New("denied")
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{Name: "srv", Status: "已连接"}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.click(tester, "启用服务器 srv")
	h.assertTexts(tester, "保存失败：denied")
	h.assertNoTexts(tester, "已停用（重启生效）")
}

func TestMCPServerSwitchEnglish(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingMCPDisabledServers: `["srv"]`,
	})
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{Name: "srv", Status: "已连接"}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Disabled (restart applies)")
	h.click(tester, "Enable server srv")
	if got := store.get(contracts.SettingMCPDisabledServers); got != "[]" {
		t.Fatalf("%s after enable = %q, want []", contracts.SettingMCPDisabledServers, got)
	}
}
