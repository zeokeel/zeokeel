package settings_test

// Wave 3 settings items:
//   - FR-26: the empty model list names the assembly-provided default model so
//     the page and the composer chip tell the same story;
//   - PM-11: every MCP server row shows the last connection state and tool
//     count from the plugins snapshot, plus the way to the plugins page.

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestModelPageEmptyStateNamesDefaultModel covers the model page's empty list:
// when the assembly still provides a default model, the empty state says which
// model is in effect and that adding a row overrides it.
func TestModelPageEmptyStateNamesDefaultModel(t *testing.T) {
	chat := &fakeChat{}
	chat.config = contracts.ChatConfig{Model: "gpt-4o-mini"}
	store := newFakeSettings(nil)
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")

	h.assertTexts(tester, "还没有模型", "默认模型", "gpt-4o-mini")
}

// fakePlugins is a scripted contracts.PluginsService: the MCP settings page
// reads its snapshot for the per-row connection state.
type fakePlugins struct {
	mcp []contracts.MCPPlugin
}

func (f *fakePlugins) MCP() []contracts.MCPPlugin {
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *fakePlugins) Refresh(context.Context) []contracts.MCPPlugin { return f.MCP() }

// TestMCPRowsShowPluginStatus covers PM-11: the configured server rows show the
// last connection state and tool count from the plugins snapshot.
func TestMCPRowsShowPluginStatus(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpTwoJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	provide(t, h.ctx, contracts.KeyPlugins, contracts.PluginsService(&fakePlugins{
		mcp: []contracts.MCPPlugin{
			{Name: "files", Status: "已连接", Tools: 3},
			{Name: "web", Status: "连接失败: dial tcp: connection refused"},
		},
	}))
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	h.assertTexts(tester, "已连接", "3 个工具", "连接失败", "打开插件页")
}

// TestMCPRowsHideStatusWithoutPluginsService covers the optional half: a host
// without the plugins service keeps the plain configuration rows (no fabricated
// status, no crash).
func TestMCPRowsHideStatusWithoutPluginsService(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpFilesJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	h.assertTexts(tester, "files")
	h.assertNoTexts(tester, "已连接", "3 个工具")
}

// TestMCPPageOpensPluginsPage covers the "打开插件页" action next to the server
// list: it navigates to the plugins screen where the live status lives.
func TestMCPPageOpensPluginsPage(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpFilesJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	if _, err := h.uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID: "plugins", Title: "插件", Build: func(c *ui.Context) { ui.Text(c, "插件屏") },
	}); err != nil {
		t.Fatalf("register plugins screen: %v", err)
	}
	h.click(tester, "打开插件页")
	if got := h.uiRoot.Navigator().Current(); got != "plugins" {
		t.Fatalf("current screen = %q, want plugins", got)
	}
}
