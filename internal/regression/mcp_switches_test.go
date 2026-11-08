package regression

// Adversarial audit: MCP server-level switches (mcp.disabledServers). The toolkit
// filter is unexported; this test-only file reaches it through a //go:linkname
// declaration with the exported toolkit.MCPServer type. The plugins page switch
// is exercised through the real screen frame: restored disabled rows, enable
// writes with case-insensitive matching/sorted JSON, and malformed config.

import (
	"context"
	"testing"
	_ "unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
	pluginsui "github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// toolkitFilterDisabledServers is toolkit.filterDisabledMCPServers by linkname.
//
//go:linkname toolkitFilterDisabledServers github.com/zeokeel/zeokeel/internal/modules/toolkit.filterDisabledMCPServers
func toolkitFilterDisabledServers(servers []toolkit.MCPServer, raw string,
	logf func(format string, args ...any)) ([]toolkit.MCPServer, int)

// pluginNames returns the names of the servers kept by the filter.
func mcpsFilteredNames(servers []toolkit.MCPServer) []string {
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, server.Name)
	}
	return names
}

// TestMCPDisabledServersFilterTable covers the toolkit filter: case/whitespace
// normalization, empty/invalid rows, unknown names and bad JSON (never filters).
func TestMCPDisabledServersFilterTable(t *testing.T) {
	servers := []toolkit.MCPServer{
		{Name: "remote", URL: "http://127.0.0.1:1/mcp"},
		{Name: "Remote", URL: "http://127.0.0.1:2/mcp"},
		{Name: "secondary", URL: "http://127.0.0.1:3/mcp"},
	}
	cases := []struct {
		name    string
		raw     string
		kept    []string
		skipped int
	}{
		{"empty", "", []string{"remote", "Remote", "secondary"}, 0},
		{"empty array", "[]", []string{"remote", "Remote", "secondary"}, 0},
		{"blank entries", `["","   "]`, []string{"remote", "Remote", "secondary"}, 0},
		{"case+space insensitive", `[" REMOTE "]`, []string{"secondary"}, 2},
		{"one name", `["secondary"]`, []string{"remote", "Remote"}, 1},
		{"unknown name", `["missing"]`, []string{"remote", "Remote", "secondary"}, 0},
		{"bad json ignored", `["remote"`, []string{"remote", "Remote", "secondary"}, 0},
		{"wrong json type", `"remote"`, []string{"remote", "Remote", "secondary"}, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var logs []string
			kept, skipped := toolkitFilterDisabledServers(servers, test.raw, func(format string, args ...any) {
				logs = append(logs, format)
			})
			if got := mcpsFilteredNames(kept); len(got) != len(test.kept) {
				t.Fatalf("kept = %v, want %v", got, test.kept)
			}
			for index, name := range test.kept {
				if kept[index].Name != name {
					t.Fatalf("kept = %v, want %v", mcpsFilteredNames(kept), test.kept)
				}
			}
			if skipped != test.skipped {
				t.Fatalf("skipped = %d, want %d", skipped, test.skipped)
			}
			if test.raw == `["remote"` && len(logs) == 0 {
				t.Fatalf("malformed JSON was not logged")
			}
		})
	}
}

// pluginsServiceFake exposes a scripted live MCP list to the plugins screen.
type pluginsServiceFake struct{ mcp []contracts.MCPPlugin }

func (f *pluginsServiceFake) MCP() []contracts.MCPPlugin {
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *pluginsServiceFake) Refresh(context.Context) []contracts.MCPPlugin { return f.MCP() }

// startPluginsUI activates the plugins screen with the given live list.
func startPluginsUI(t *testing.T, service contracts.PluginsService, values map[string]string) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	for key, value := range values {
		if err := h.settings.Set(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	provide(t, h.ctx, contracts.KeyPlugins, service)
	activate(t, h.ctx, pluginsui.Module(), pluginsui.Config{})
	h.push(pluginsui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()
	return h, tester
}

// clickPlugins clicks one label and repaints.
func clickPlugins(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// TestPluginsMCPServerSwitch covers the plugins screen: a disabled
// settings-sourced server is restored with its URL and badge, enabling it writes
// the sorted remaining list, a bad disabled row is surfaced as a status error
// without crashing, and a live server can be disabled/enabled case-insensitively.
func TestPluginsMCPServerSwitch(t *testing.T) {
	t.Run("restore and enable", func(t *testing.T) {
		h, tester := startPluginsUI(t, &pluginsServiceFake{}, map[string]string{
			contracts.SettingMCPServers:         `[{"name":"srv","url":"http://127.0.0.1:9/mcp"}]`,
			contracts.SettingMCPDisabledServers: `["srv"]`,
		})
		requireHasText(t, tester, "srv")
		requireHasText(t, tester, "已停用（重启生效）")
		clickPlugins(t, tester, "详情")
		requireHasText(t, tester, "http://127.0.0.1:9/mcp")
		clickPlugins(t, tester, "启用")
		if got := h.settings.Get(contracts.SettingMCPDisabledServers, ""); got != "[]" {
			t.Fatalf("disabledServers after enable = %q, want []", got)
		}
		requireNoText(t, tester, "已停用（重启生效）")
	})

	t.Run("disable live server sorted", func(t *testing.T) {
		h, tester := startPluginsUI(t, &pluginsServiceFake{mcp: []contracts.MCPPlugin{
			{Name: "a", Status: "已连接", URL: "http://127.0.0.1:9/mcp"},
		}}, map[string]string{contracts.SettingMCPDisabledServers: `["zeta"]`})
		clickPlugins(t, tester, "详情")
		clickPlugins(t, tester, "启用")
		if got := h.settings.Get(contracts.SettingMCPDisabledServers, ""); got != `["a","zeta"]` {
			t.Fatalf("disabledServers after disabling a = %q, want the sorted combined list", got)
		}
	})

	t.Run("bad config surfaced", func(t *testing.T) {
		_, tester := startPluginsUI(t, &pluginsServiceFake{mcp: []contracts.MCPPlugin{{Name: "srv", Status: "已连接"}}},
			map[string]string{contracts.SettingMCPDisabledServers: `["srv"`})
		requireHasText(t, tester, "禁用服务器配置无效")
		// The screen still renders the servers.
		requireHasText(t, tester, "srv")
	})
}
