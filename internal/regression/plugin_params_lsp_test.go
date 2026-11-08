package regression

// Adversarial audit: the plugins page's parameter section and the LSP workspace
// config opt-in. The section only exposes wired settings (sensitive values are
// masked, compile-time constants read-only), edits persist to the settings
// store, and a workspace .zeokeel/lsp.json is only read after the explicit
// lsp.workspace_config="allow" opt-in; settings servers win over workspace ones.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	_ "unsafe"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	pluginsui "github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// startPluginParamsUI opens the plugins page with room for the params section.
func startPluginParamsUI(t *testing.T, values map[string]string) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	if values == nil {
		values = map[string]string{}
	}
	if err := h.settings.Set(contracts.SettingWebSearchEndpoint, "https://search.example.test/api"); err != nil {
		t.Fatalf("set search endpoint: %v", err)
	}
	if err := h.settings.Set(contracts.SettingWebSearchAPIKey, "secret-search-key-123"); err != nil {
		t.Fatalf("set search api key: %v", err)
	}
	for key, value := range values {
		if err := h.settings.Set(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	provide(t, h.ctx, contracts.KeyPlugins, contracts.PluginsService(&pluginsServiceFake{}))
	activate(t, h.ctx, pluginsui.Module(), pluginsui.Config{})
	h.push(pluginsui.ScreenID)
	tester := h.tester(1280, 1500)
	tester.Frame()
	return h, tester
}

// TestPluginParamsSection covers the curated parameter inventory: masked
// sensitive value, read-only compile-time constants, no dead keys, and edits
// persisting to the settings store.
func TestPluginParamsSection(t *testing.T) {
	h, tester := startPluginParamsUI(t, map[string]string{contracts.SettingMCPWorkspaceOptIn: "false"})
	requireHasTexts(t, tester, "插件参数", "已接线")
	requireNoText(t, tester, "web.search.endpoint")
	if err := tester.Click("展开参数"); err != nil {
		t.Fatalf("expand params: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester,
		"搜索端点", "web.search.endpoint", "https://search.example.test/api",
		"API Key", "API Key 已设置",
		"工作区 mcp.json", "未启用", "需重启",
		"子代理深度", "编译期常量", "子代理并发",
		"只展示已被产品实际消费的设置；敏感值仅显示是否已设置。")
	// Sensitive values never appear in the frame.
	for _, text := range tester.Texts() {
		if strings.Contains(text, "secret-search-key-123") {
			t.Fatalf("the plugins page leaked the search API key: %q", text)
		}
	}
	// Dead/unwired keys are deliberately absent (no editor for compile-time
	// constants, no invented shell/agent-loop settings).
	requireNoText(t, tester, "plugin.agent_loop")
	requireNoText(t, tester, "plugin.shell")
	requireNoText(t, tester, "编辑 子代理深度")

	// Editing the endpoint writes the settings row and shows the new value.
	if err := tester.Click("编辑 搜索端点"); err != nil {
		t.Fatalf("edit endpoint: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "编辑参数", "参数：web.search.endpoint", "新值：搜索端点", "保存")
	if err := tester.Click("新值：搜索端点"); err != nil {
		t.Fatalf("focus endpoint input: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("https://search2.example.test/v2")
	tester.Frame()
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("save endpoint: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "已保存")
	if got := h.settings.Get(contracts.SettingWebSearchEndpoint, ""); got != "https://search2.example.test/v2" {
		t.Fatalf("saved search endpoint = %q", got)
	}

	// The sensitive editor starts blank (never echoes), and a blank save keeps
	// the existing key.
	if err := tester.Click("编辑 API Key"); err != nil {
		t.Fatalf("edit API key: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "编辑参数", "参数：web.search.apiKey", "留空保持不变")
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("blank save API key: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := h.settings.Get(contracts.SettingWebSearchAPIKey, ""); got != "secret-search-key-123" {
		t.Fatalf("blank save changed the API key to %q", got)
	}
	for _, text := range tester.Texts() {
		if strings.Contains(text, "secret-search-key-123") {
			t.Fatalf("the plugins page echoed the API key after saving: %q", text)
		}
	}

	// Editing with a new secret persists it and still never echoes it.
	if err := tester.Click("编辑 API Key"); err != nil {
		t.Fatalf("re-edit API key: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("新值：API Key"); err != nil {
		t.Fatalf("focus API key input: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Type("rotated-secret-456")
	tester.Frame()
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("save rotated key: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := h.settings.Get(contracts.SettingWebSearchAPIKey, ""); got != "rotated-secret-456" {
		t.Fatalf("rotated API key = %q", got)
	}
	for _, text := range tester.Texts() {
		if strings.Contains(text, "rotated-secret-456") {
			t.Fatalf("the plugins page echoed the rotated API key: %q", text)
		}
	}
}

// toolkit internals: the workspace LSP loader and the settings-first merge are
// unexported; the audit reaches them via test-only linknames.
//
//go:linkname auditLoadWorkspaceLSPServers github.com/zeokeel/zeokeel/internal/modules/toolkit.loadWorkspaceLSPServers
func auditLoadWorkspaceLSPServers(kctx cordis.Context, settings contracts.SettingsService,
	logf func(string, ...any)) []contracts.LSPServerConfig

//go:linkname auditMergeLSPServers github.com/zeokeel/zeokeel/internal/modules/toolkit.mergeLSPServers
func auditMergeLSPServers(primary, secondary []contracts.LSPServerConfig) []contracts.LSPServerConfig

// TestLSPWorkspaceOptIn covers the workspace config contract: only "allow"
// enables reading the repository file, malformed configs are warnings, settings
// servers win duplicate names, and no workspace backend is safe.
func TestLSPWorkspaceOptIn(t *testing.T) {
	// Contract parsing: array and wrapped-object shapes, invalid JSON, empty.
	for _, raw := range []string{
		`[{"name":"go","command":"gopls"}]`,
		`{"servers":[{"name":"go","command":"gopls"}]}`,
	} {
		servers, err := contracts.ParseLSPWorkspaceConfigFile([]byte(raw))
		if err != nil || len(servers) != 1 || servers[0].Name != "go" || servers[0].Command != "gopls" {
			t.Fatalf("ParseLSPWorkspaceConfigFile(%q) = %+v, %v", raw, servers, err)
		}
	}
	if _, err := contracts.ParseLSPWorkspaceConfigFile([]byte(`{"broken":`)); err == nil {
		t.Fatal("ParseLSPWorkspaceConfigFile accepted malformed JSON")
	}
	if servers, err := contracts.ParseLSPWorkspaceConfigFile(nil); err != nil || len(servers) != 0 {
		t.Fatalf("empty workspace config = %+v, %v", servers, err)
	}

	h := newBareHarness(t)
	workspace := &auditWorkspace{}
	root := t.TempDir()
	if _, err := workspace.Create(context.Background(), "lsp", root); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	configPath := filepath.Join(root, filepath.FromSlash(contracts.LSPWorkspaceConfigFile))
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir lsp config dir: %v", err)
	}
	fileBody := `[{"name":"workspace-server","command":"ws-lsp","args":["--stdio"]}]`
	if err := os.WriteFile(configPath, []byte(fileBody), 0o644); err != nil {
		t.Fatalf("write workspace lsp config: %v", err)
	}
	provide(t, h.ctx, contracts.KeyWorkspace, contracts.WorkspaceService(workspace))

	// Opt-in off: no read, no log, nil result.
	logged := make([]string, 0)
	logf := func(format string, args ...any) { logged = append(logged, format) }
	if got := auditLoadWorkspaceLSPServers(h.ctx, h.settings, logf); len(got) != 0 {
		t.Fatalf("loader read the workspace config without the opt-in: %+v", got)
	}
	if len(logged) != 0 {
		t.Fatalf("loader touched the workspace config without the opt-in: %v", logged)
	}

	// Opt-in allow: the workspace file is read.
	if err := h.settings.Set(contracts.SettingLSPWorkspaceConfig, "allow"); err != nil {
		t.Fatalf("enable LSP workspace config: %v", err)
	}
	servers := auditLoadWorkspaceLSPServers(h.ctx, h.settings, logf)
	if len(servers) != 1 || servers[0].Name != "workspace-server" || servers[0].Command != "ws-lsp" {
		t.Fatalf("workspace servers = %+v", servers)
	}

	// Malformed workspace JSON is ignored with a warning.
	if err := os.WriteFile(configPath, []byte(`{"broken":`), 0o644); err != nil {
		t.Fatalf("rewrite malformed config: %v", err)
	}
	logged = logged[:0]
	if got := auditLoadWorkspaceLSPServers(h.ctx, h.settings, logf); len(got) != 0 || len(logged) == 0 {
		t.Fatalf("malformed workspace config = %+v (logs %v), want nil + warning", got, logged)
	}

	// Without a workspace backend the loader is safe.
	bare := newBareHarness(t)
	if err := bare.settings.Set(contracts.SettingLSPWorkspaceConfig, "allow"); err != nil {
		t.Fatalf("enable opt-in without workspace: %v", err)
	}
	if got := auditLoadWorkspaceLSPServers(bare.ctx, bare.settings, logf); len(got) != 0 {
		t.Fatalf("loader returned servers without a workspace backend: %+v", got)
	}

	// Settings servers win duplicate names; unique workspace servers append.
	merged := auditMergeLSPServers(
		[]contracts.LSPServerConfig{{Name: "shared", Command: "primary-lsp"}},
		[]contracts.LSPServerConfig{
			{Name: "SHARED", Command: "secondary-lsp"},
			{Name: "extra", Command: "extra-lsp"},
		})
	if len(merged) != 2 || merged[0].Name != "shared" || merged[0].Command != "primary-lsp" ||
		merged[1].Name != "extra" || merged[1].Command != "extra-lsp" {
		t.Fatalf("merged servers = %+v", merged)
	}
}
