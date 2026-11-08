package plugins_test

// TDD tests (RED first) for the MCP server "connection config" block sourced
// from the settings mcp.servers rows (matched by server name).

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

func mcpConfigSettings(raw string) *fakePluginSettings {
	return newFakePluginSettings(map[string]string{contracts.SettingMCPServers: raw})
}

// --- tests ------------------------------------------------------------------

func TestMCPConfigStdioShowsCommandArgsAndMaskedEnv(t *testing.T) {
	store := mcpConfigSettings(`[{
		"name":"srv","transport":"stdio","command":"npx",
		"args":["-y","mcp-server"],
		"env":["API_KEY=secret-value","TOKEN=abc","NORMAL=1","AUTHORIZATION=Bearer token-123"]
	}]`)
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{Name: "srv", Status: "已连接", Tools: 2}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.click(tester, "详情")

	h.assertTexts(tester, "连接配置", "命令", "npx", "参数", "-y mcp-server", "环境",
		"API_KEY=***", "TOKEN=***", "NORMAL=1", "值已隐藏")
	h.assertNoTexts(tester, "secret-value", "token-123")
}

func TestMCPConfigHTTPShowsURL(t *testing.T) {
	store := mcpConfigSettings(`[{"name":"remote","url":"https://mcp.example.com/mcp"}]`)
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{
		Name: "remote", URL: "https://mcp.example.com/mcp", Status: "已连接", Tools: 1,
	}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.click(tester, "详情")

	h.assertTexts(tester, "连接配置", "https://mcp.example.com/mcp")
	// The HTTP row has no command/arguments; "参数" is no longer a safe probe
	// because the new (collapsed) plugin-settings section is titled 插件参数.
	h.assertNoTexts(tester, "命令", "-y mcp-server", "npx")
}

func TestMCPConfigHiddenWithoutMatchingSettingsRow(t *testing.T) {
	store := mcpConfigSettings(`[{"name":"other","command":"npx"}]`)
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{Name: "srv", Status: "已连接"}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.click(tester, "详情")
	h.assertNoTexts(tester, "连接配置", "other")

	// Assembly-only servers (no settings service at all) keep the old details.
	plain := newHarness(t, &fakePlugins{mcp: []contracts.MCPPlugin{
		{Name: "srv", URL: "http://127.0.0.1:1/mcp", Status: "已连接"},
	}}, nil, plugins.Config{})
	tester2 := plain.tester()
	plain.click(tester2, "详情")
	plain.assertTexts(tester2, "类型", "服务器")
	plain.assertNoTexts(tester2, "连接配置")
}

func TestMCPConfigBadJSONShowsParseError(t *testing.T) {
	store := mcpConfigSettings(`{not-json`)
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{
		Name: "srv", URL: "https://mcp.example.com/mcp", Status: "已连接",
	}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.click(tester, "详情")

	h.assertTexts(tester, "配置无法解析", "类型", "https://mcp.example.com/mcp")
}

func TestMCPConfigEnglishCopy(t *testing.T) {
	store := mcpConfigSettings(`[{"name":"srv","command":"uvx","args":["server"],"env":["GITHUB_TOKEN=ghp_x"]}]`)
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{Name: "srv", Status: "已连接"}}}
	h := newHarnessWithSettings(t, service, nil, plugins.Config{}, store)
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "Details")

	h.assertTexts(tester, "Connection", "Command", "uvx", "Arguments", "server",
		"Environment", "GITHUB_TOKEN=***", "value hidden")
	if got := strings.Join(tester.Texts(), "\n"); strings.Contains(got, "ghp_x") {
		t.Fatalf("English frame leaked the secret: %v", tester.Texts())
	}
}
