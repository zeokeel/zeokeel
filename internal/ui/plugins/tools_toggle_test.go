package plugins_test

// Tests for the generic tools.disabled switches (built-in tools) alongside the
// legacy MCP-only switches.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

func newGenericToolRegistry() *rigtools.Registry {
	return rigtools.NewRegistry(
		fakeTool{name: "read_file", desc: "Read files"},
		fakeTool{name: "bash", desc: "Run shell"},
		fakeTool{name: "web_fetch", desc: "Fetch web"},
		fakeTool{name: "mcp__srv__echo", desc: "MCP echo"},
	)
}

func newGenericHarness(t *testing.T, store *fakePluginSettings, toolkit *rigtools.Registry) *harness {
	t.Helper()
	return newHarnessWithSettings(t, nil, toolkit, plugins.Config{}, store)
}

func TestBuiltinToolSwitchWritesToolsDisabled(t *testing.T) {
	store := newFakePluginSettings(nil)
	h := newGenericHarness(t, store, newGenericToolRegistry())
	tester := h.tester()
	h.click(tester, "搜索工具")
	tester.Type("read_file")
	tester.Frame()
	h.assertTexts(tester, "read_file", "启用工具")
	h.click(tester, "启用工具 read_file")
	if got := store.get(contracts.SettingToolsDisabled); got != `["read_file"]` {
		t.Fatalf("tools.disabled after disable = %q", got)
	}
	if got := store.get(contracts.SettingMCPDisabledTools); got != "" {
		t.Fatalf("MCP setting written for a built-in tool: %q", got)
	}
	// Toggling the same row again enables it.
	h.click(tester, "启用工具 read_file")
	if got := store.get(contracts.SettingToolsDisabled); got != "[]" {
		t.Fatalf("tools.disabled after enable = %q", got)
	}
}

func TestBuiltinFilteredEntryRestoredFromSettings(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingToolsDisabled: `["read_file"]`,
	})
	h := newGenericHarness(t, store, newGenericToolRegistry())
	tester := h.tester()
	h.assertTexts(tester, "read_file", "已禁用（重启生效）", "启用工具")
	h.click(tester, "搜索工具")
	tester.Type("read_file")
	tester.Frame()
	h.click(tester, "启用工具 read_file")
	if got := store.get(contracts.SettingToolsDisabled); got != "[]" {
		t.Fatalf("tools.disabled after re-enable = %q", got)
	}
}

// TestToolRestartHintClearsWhenSwitchReverts pins E-13: the "restart needed"
// hint tracks the net difference from the startup state, so flipping a tool off
// and back on clears it instead of demanding a pointless restart.
func TestToolRestartHintClearsWhenSwitchReverts(t *testing.T) {
	store := newFakePluginSettings(nil)
	h := newGenericHarness(t, store, newGenericToolRegistry())
	tester := h.tester()
	h.click(tester, "搜索工具")
	tester.Type("read_file")
	tester.Frame()
	h.assertNoTexts(tester, "待重启")

	h.click(tester, "启用工具 read_file")
	h.assertTexts(tester, "待重启")
	h.click(tester, "启用工具 read_file")
	h.assertNoTexts(tester, "待重启")
	if got := store.get(contracts.SettingToolsDisabled); got != "[]" {
		t.Fatalf("tools.disabled after revert = %q, want []", got)
	}
}

func TestBuiltinAndMCPTogglesCoexist(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingToolsDisabled:    `["read_file"]`,
		contracts.SettingMCPDisabledTools: `["mcp__srv__echo"]`,
	})
	h := newGenericHarness(t, store, newGenericToolRegistry())
	tester := h.tester()
	h.assertTexts(tester, "read_file", "mcp__srv__echo", "已禁用（重启生效）")

	// Re-enable the built-in: only tools.disabled changes.
	h.click(tester, "搜索工具")
	tester.Type("read_file")
	tester.Frame()
	h.click(tester, "启用工具 read_file")
	if got := store.get(contracts.SettingToolsDisabled); got != "[]" {
		t.Fatalf("tools.disabled = %q", got)
	}
	if got := store.get(contracts.SettingMCPDisabledTools); got != `["mcp__srv__echo"]` {
		t.Fatalf("MCP setting changed = %q", got)
	}

	// Clear search and re-enable the MCP tool: only mcp.disabledTools changes.
	h.click(tester, "搜索工具")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.click(tester, "搜索工具")
	tester.Type("mcp__srv__echo")
	tester.Frame()
	h.click(tester, "启用工具 mcp__srv__echo")
	if got := store.get(contracts.SettingMCPDisabledTools); got != "[]" {
		t.Fatalf("mcp.disabledTools = %q", got)
	}
	if got := store.get(contracts.SettingToolsDisabled); got != "[]" {
		t.Fatalf("tools.disabled changed = %q", got)
	}
}

func TestToolsDisabledBadJSONAndEnglish(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingToolsDisabled: "not-json",
	})
	h := newGenericHarness(t, store, newGenericToolRegistry())
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Enable tool", "Invalid disabled-tools configuration")
	if strings.Contains(strings.Join(tester.Texts(), " "), "工具") {
		t.Fatalf("Chinese copy in English frame: %v", tester.Texts())
	}
}
