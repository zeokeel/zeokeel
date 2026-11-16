package toolkit

// Hot-reload tests: changing settings.mcp.servers / mcp.disabledServers must
// rebuild the MCP connections and their tool registrations in place, without
// reloading the toolkit module or restarting the app.

import (
	"context"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func activateMCPToolkitWithRoot(t *testing.T, config map[string]any,
	settings contracts.SettingsService) (cordis.Context, *rigtool.Registry, contracts.PluginsService, contracts.MCPReloader) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: config,
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	plugins, err := root.Require(contracts.KeyPlugins)
	if err != nil {
		t.Fatalf("require plugins: %v", err)
	}
	reloader, err := root.Require(contracts.KeyMCPReloader)
	if err != nil {
		t.Fatalf("require reloader: %v", err)
	}
	return root, tools, plugins, reloader
}

func registryHasTool(registry *rigtool.Registry, name string) bool {
	for _, spec := range registry.Specs() {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func TestMCPHotReloadAddsRemovesAndDisablesServers(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()
	beta := testMCPToolListServer(t, []map[string]any{oneTool("beta_tool")})
	defer beta.Close()

	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"alpha","url":"` + alpha.URL + `"}]`,
	})
	_, registry, plugins, reloader := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)
	if !registryHasTool(registry, "mcp__alpha__alpha_tool") {
		t.Fatal("initial alpha tool is not registered")
	}

	// Add beta without restarting; both servers' tools must be live.
	if err := settings.Set(contracts.SettingMCPServers,
		`[{"name":"alpha","url":"`+alpha.URL+`"},{"name":"beta","url":"`+beta.URL+`"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload add: %v", err)
	}
	if got := len(plugins.MCP()); got != 2 {
		t.Fatalf("plugins after add = %d, want 2", got)
	}
	for _, name := range []string{"mcp__alpha__alpha_tool", "mcp__beta__beta_tool"} {
		if !registryHasTool(registry, name) {
			t.Fatalf("tool %q missing after add reload", name)
		}
	}

	// Remove alpha: its tools must disappear from the shared registry.
	if err := settings.Set(contracts.SettingMCPServers, `[{"name":"beta","url":"`+beta.URL+`"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload remove: %v", err)
	}
	if got := len(plugins.MCP()); got != 1 || !strings.EqualFold(plugins.MCP()[0].Name, "beta") {
		t.Fatalf("plugins after remove = %+v, want only beta", plugins.MCP())
	}
	if registryHasTool(registry, "mcp__alpha__alpha_tool") {
		t.Fatal("removed server's tool is still registered")
	}
	if !registryHasTool(registry, "mcp__beta__beta_tool") {
		t.Fatal("kept server's tool disappeared after reload")
	}

	// Disable beta through mcp.disabledServers: the live tool is unregistered.
	if err := settings.Set(contracts.SettingMCPDisabledServers, `["beta"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload disable: %v", err)
	}
	if got := len(plugins.MCP()); got != 0 {
		t.Fatalf("plugins after disable = %+v, want none", plugins.MCP())
	}
	if registryHasTool(registry, "mcp__beta__beta_tool") {
		t.Fatal("disabled server's tool is still registered")
	}
}

func TestMCPToolCatalogListsAndTogglesTools(t *testing.T) {
	server := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool"), oneTool("beta_tool")})
	defer server.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"srv","url":"` + server.URL + `"}]`,
	})
	root, registry, plugins, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)
	catalog, err := root.Require(contracts.KeyMCPToolCatalog)
	if err != nil {
		t.Fatalf("require tool catalog: %v", err)
	}
	tools := catalog.Tools("srv")
	if len(tools) < 2 {
		t.Fatalf("tools = %+v, want at least the two regular tools", tools)
	}
	for _, tool := range tools {
		if !tool.Enabled {
			t.Fatalf("tool %q starts disabled", tool.Name)
		}
	}
	target := ""
	for _, tool := range tools {
		if strings.Contains(tool.Name, "alpha") {
			target = tool.Name
		}
	}
	if target == "" {
		t.Fatalf("alpha tool not found in %+v", tools)
	}
	if err := catalog.SetToolEnabled(context.Background(), "srv", target, false); err != nil {
		t.Fatalf("disable tool: %v", err)
	}
	if registryHasTool(registry, target) {
		t.Fatalf("disabled tool %q is still registered", target)
	}
	if got := len(plugins.MCP()); got != 1 || plugins.MCP()[0].Tools != 1 {
		t.Fatalf("plugins after disable = %+v, want one tool", plugins.MCP())
	}
	if err := catalog.SetToolEnabled(context.Background(), "srv", target, true); err != nil {
		t.Fatalf("enable tool: %v", err)
	}
	if !registryHasTool(registry, target) {
		t.Fatalf("re-enabled tool %q is not registered", target)
	}
}
