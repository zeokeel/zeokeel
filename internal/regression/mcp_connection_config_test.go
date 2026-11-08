package regression

// Adversarial audit: the plugins page's MCP connection-config details. The
// settings-derived block masks secret env values (KEY/TOKEN/SECRET/PASSWORD/
// AUTHORIZATION) while keeping keys and non-secret values; an HTTP row shows the
// URL only; no matching settings row means no block; a malformed mcp.servers row
// shows a parse error; the English copy is localized.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	pluginsui "github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// expandFirstPluginServer opens the first server card's details.
func expandFirstPluginServer(t *testing.T, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("详情"); err != nil {
		t.Fatalf("expand server: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
}

// frameText joins the rendered labels for leak assertions.
func frameText(tester *ui.Tester) string { return strings.Join(tester.Texts(), "\n") }

// TestMCPConnectionConfigMasksSecrets covers a stdio settings row: command/args
// and the masked env entries, with no raw secret value in the frame.
func TestMCPConnectionConfigMasksSecrets(t *testing.T) {
	h, tester := startPluginsUI(t, &pluginsServiceFake{mcp: []contracts.MCPPlugin{
		{Name: "srv", Status: "已连接"},
	}}, map[string]string{
		contracts.SettingMCPServers: `[{"name":"srv","transport":"stdio","command":"npx",` +
			`"args":["-y","server-pkg"],` +
			`"env":["API_KEY=sk-secret-123","GITHUB_TOKEN=ghp-token-456",` +
			`"client_secret=sec-789","DB_PASSWORD=pw-000",` +
			`"AUTHORIZATION=Bearer bearer-111","NORMAL=visible-value"]}]`,
	})
	_ = h
	expandFirstPluginServer(t, tester)
	text := frameText(tester)
	for _, want := range []string{"连接配置", "命令", "npx", "参数", "-y server-pkg",
		"API_KEY=*** · 值已隐藏", "GITHUB_TOKEN=*** · 值已隐藏",
		"client_secret=*** · 值已隐藏", "DB_PASSWORD=*** · 值已隐藏",
		"AUTHORIZATION=*** · 值已隐藏", "NORMAL=visible-value"} {
		if !strings.Contains(text, want) {
			t.Fatalf("connection config lacks %q:\n%s", want, text)
		}
	}
	for _, secret := range []string{"sk-secret-123", "ghp-token-456", "sec-789", "pw-000", "bearer-111"} {
		if strings.Contains(text, secret) {
			t.Fatalf("connection config leaked %q:\n%s", secret, text)
		}
	}
}

// TestMCPConnectionConfigHTTPAndEdges covers HTTP rows (URL only), a server with
// no matching settings row (no block), malformed JSON (parse warning) and the
// English masking copy.
func TestMCPConnectionConfigHTTPAndEdges(t *testing.T) {
	t.Run("http shows URL only", func(t *testing.T) {
		_, tester := startPluginsUI(t, &pluginsServiceFake{mcp: []contracts.MCPPlugin{
			{Name: "web", Status: "已连接"},
		}}, map[string]string{
			contracts.SettingMCPServers: `[{"name":"web","url":"https://mcp.example.test/sse"}]`,
		})
		expandFirstPluginServer(t, tester)
		text := frameText(tester)
		if !strings.Contains(text, "https://mcp.example.test/sse") {
			t.Fatalf("HTTP URL missing:\n%s", text)
		}
		if strings.Contains(text, "选择") {
			t.Fatalf("unexpected detail row:\n%s", text)
		}
	})

	t.Run("no matching row hides the block", func(t *testing.T) {
		_, tester := startPluginsUI(t, &pluginsServiceFake{mcp: []contracts.MCPPlugin{
			{Name: "assembly-only", Status: "已连接"},
		}}, map[string]string{
			contracts.SettingMCPServers: `[{"name":"other","url":"https://mcp.example.test/sse"}]`,
		})
		expandFirstPluginServer(t, tester)
		requireNoText(t, tester, "连接配置")
	})

	t.Run("malformed settings row", func(t *testing.T) {
		_, tester := startPluginsUI(t, &pluginsServiceFake{mcp: []contracts.MCPPlugin{
			{Name: "srv", Status: "已连接"},
		}}, map[string]string{contracts.SettingMCPServers: `{"name":`})
		expandFirstPluginServer(t, tester)
		requireHasText(t, tester, "配置无法解析")
	})

	t.Run("english copy masks", func(t *testing.T) {
		h := newBareHarness(t)
		h.ui.Locale().SetLanguage("en")
		if err := h.settings.Set(contracts.SettingMCPServers,
			`[{"name":"srv","command":"npx","env":["API_KEY=sk-en-secret"]}]`); err != nil {
			t.Fatalf("set mcp.servers: %v", err)
		}
		provide(t, h.ctx, contracts.KeyPlugins, contracts.PluginsService(&pluginsServiceFake{mcp: []contracts.MCPPlugin{
			{Name: "srv", Status: "Connected"},
		}}))
		activate(t, h.ctx, pluginsui.Module(), pluginsui.Config{})
		h.push(pluginsui.ScreenID)
		tester := h.tester(1280, 800)
		tester.Frame()
		if err := tester.Click("Details"); err != nil {
			t.Fatalf("expand server (en): %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		requireHasTexts(t, tester, "Connection", "value hidden")
		if strings.Contains(frameText(tester), "sk-en-secret") {
			t.Fatalf("English config leaked the secret:\n%s", frameText(tester))
		}
	})
}
