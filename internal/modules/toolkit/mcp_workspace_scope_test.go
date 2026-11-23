package toolkit

// Workspace-scoped MCP connectors: settings rows carry WorkspaceID; the loader
// merges only the current workspace's rows (assembly rows stay product policy)
// and the disabled server/tool switches are scoped by settings key.

import (
	"context"
	"fmt"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
)

// activateScopedToolkit mounts the toolkit with a mutable current workspace and
// returns the registry, the plugins read model and the reloader.
func activateScopedToolkit(t *testing.T, settings contracts.SettingsService,
	workspace contracts.WorkspaceService) (*rigtool.Registry, contracts.PluginsService, contracts.MCPReloader) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(settings)); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	if _, err := root.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: map[string]any{"root": "."},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(hosttools.KeyRegistry)
	if err != nil {
		t.Fatalf("require tool registry: %v", err)
	}
	plugins, err := root.Require(contracts.KeyPlugins)
	if err != nil {
		t.Fatalf("require plugins: %v", err)
	}
	reloader, err := root.Require(contracts.KeyMCPReloader)
	if err != nil {
		t.Fatalf("require reloader: %v", err)
	}
	return tools, plugins, reloader
}

// scopedRow renders one settings connector row.
func scopedRow(name, url, workspaceID string) string {
	return fmt.Sprintf(`{"name":%q,"url":%q,"workspaceId":%q}`, name, url, workspaceID)
}

func TestMCPConnectorsAreScopedToTheCurrentWorkspace(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()
	beta := testMCPToolListServer(t, []map[string]any{oneTool("beta_tool")})
	defer beta.Close()

	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: "[" + scopedRow("alpha", alpha.URL, "ws-a") + "," +
			scopedRow("beta", beta.URL, "ws-b") + "]",
	})
	workspace := &fakeMCPWorkspace{
		current: contracts.Workspace{ID: "ws-a"}, has: true,
		all: []contracts.Workspace{{ID: "ws-a"}, {ID: "ws-b"}},
	}
	registry, plugins, reloader := activateScopedToolkit(t, settings, workspace)

	assertMCPTools := func(want map[string]bool) {
		t.Helper()
		if got := registryHasTool(registry, "mcp__alpha__alpha_tool"); got != want["alpha"] {
			t.Fatalf("alpha tool registered = %v, want %v", got, want["alpha"])
		}
		if got := registryHasTool(registry, "mcp__beta__beta_tool"); got != want["beta"] {
			t.Fatalf("beta tool registered = %v, want %v", got, want["beta"])
		}
	}
	assertMCPTools(map[string]bool{"alpha": true})
	if got := len(plugins.MCP()); got != 1 || plugins.MCP()[0].Name != "alpha" {
		t.Fatalf("ws-a plugins = %+v, want only alpha", plugins.MCP())
	}

	// Switching the current workspace and reloading must swap the connectors.
	workspace.current = contracts.Workspace{ID: "ws-b"}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload for ws-b: %v", err)
	}
	assertMCPTools(map[string]bool{"beta": true})
	if got := len(plugins.MCP()); got != 1 || plugins.MCP()[0].Name != "beta" {
		t.Fatalf("ws-b plugins = %+v, want only beta", plugins.MCP())
	}
}

func TestMCPDisabledServersAreScoped(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()
	beta := testMCPToolListServer(t, []map[string]any{oneTool("beta_tool")})
	defer beta.Close()

	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: "[" + scopedRow("alpha", alpha.URL, "ws-a") + "," +
			scopedRow("beta", beta.URL, "ws-b") + "]",
		// ws-a disables its own connector; ws-b must stay untouched.
		contracts.MCPDisabledServersSettingKey("ws-a"): `["alpha"]`,
	})
	workspace := &fakeMCPWorkspace{
		current: contracts.Workspace{ID: "ws-a"}, has: true,
		all: []contracts.Workspace{{ID: "ws-a"}, {ID: "ws-b"}},
	}
	registry, plugins, reloader := activateScopedToolkit(t, settings, workspace)
	if registryHasTool(registry, "mcp__alpha__alpha_tool") {
		t.Fatal("disabled connector's tool is registered in ws-a")
	}
	if len(plugins.MCP()) != 0 {
		t.Fatalf("ws-a plugins = %+v, want none", plugins.MCP())
	}

	workspace.current = contracts.Workspace{ID: "ws-b"}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload for ws-b: %v", err)
	}
	if !registryHasTool(registry, "mcp__beta__beta_tool") {
		t.Fatal("ws-a's disable switch leaked into ws-b")
	}
}

func TestMCPDisabledToolsAreScoped(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()

	settings := newMutableSettings(map[string]string{
		// The same connector is configured in both workspaces.
		contracts.SettingMCPServers: "[" + scopedRow("alpha", alpha.URL, "ws-a") + "," +
			scopedRow("alpha", alpha.URL, "ws-b") + "]",
		// Only ws-a hides the tool.
		contracts.MCPDisabledToolsSettingKey("ws-a"): `["mcp__alpha__alpha_tool"]`,
	})
	workspace := &fakeMCPWorkspace{
		current: contracts.Workspace{ID: "ws-a"}, has: true,
		all: []contracts.Workspace{{ID: "ws-a"}, {ID: "ws-b"}},
	}
	registry, _, reloader := activateScopedToolkit(t, settings, workspace)
	if registryHasTool(registry, "mcp__alpha__alpha_tool") {
		t.Fatal("ws-a's disabled tool is still registered")
	}

	workspace.current = contracts.Workspace{ID: "ws-b"}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload for ws-b: %v", err)
	}
	if !registryHasTool(registry, "mcp__alpha__alpha_tool") {
		t.Fatal("ws-a's disabled tool leaked into ws-b")
	}
}

func TestMCPLegacyGlobalRowFollowsEveryWorkspace(t *testing.T) {
	legacy := testMCPToolListServer(t, []map[string]any{oneTool("legacy_tool")})
	defer legacy.Close()
	settings := newMutableSettings(map[string]string{
		// No WorkspaceID: a pre-upgrade row. It stays visible until the
		// connectors page migrates it to the workspace it is viewed from.
		contracts.SettingMCPServers: "[" + scopedRow("legacy", legacy.URL, "") + "]",
	})
	workspace := &fakeMCPWorkspace{
		current: contracts.Workspace{ID: "ws-a"}, has: true,
		all: []contracts.Workspace{{ID: "ws-a"}, {ID: "ws-b"}},
	}
	registry, _, reloader := activateScopedToolkit(t, settings, workspace)
	if !registryHasTool(registry, "mcp__legacy__legacy_tool") {
		t.Fatal("legacy-global connector is not visible in a workspace")
	}
	// The toolkit load migrates the row eagerly to the current workspace.
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil || len(rows) != 1 || rows[0].WorkspaceID != "ws-a" {
		t.Fatalf("legacy row after eager migration = %+v/%v", rows, err)
	}
	workspace.current = contracts.Workspace{ID: "ws-b"}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload for ws-b: %v", err)
	}
	if registryHasTool(registry, "mcp__legacy__legacy_tool") {
		t.Fatal("migrated connector leaked into another workspace")
	}
}

func TestMCPAssemblyRowsRemainProductPolicy(t *testing.T) {
	assembly := testMCPToolListServer(t, []map[string]any{oneTool("assembly_tool")})
	defer assembly.Close()
	other := testMCPToolListServer(t, []map[string]any{oneTool("other_tool")})
	defer other.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: "[" + scopedRow("other", other.URL, "ws-a") + "]",
	})
	workspace := &fakeMCPWorkspace{current: contracts.Workspace{ID: "ws-b"}, has: true}
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(settings)); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	if _, err := root.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit",
		Config: map[string]any{
			"root":       ".",
			"mcpServers": []map[string]any{{"name": "assembly", "url": assembly.URL}},
		},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(hosttools.KeyRegistry)
	if err != nil {
		t.Fatalf("require registry: %v", err)
	}
	if !registryHasTool(tools, "mcp__assembly__assembly_tool") {
		t.Fatal("assembly-provided MCP server disappeared with a workspace scope")
	}
	if registryHasTool(tools, "mcp__other__other_tool") {
		t.Fatal("another workspace's settings connector leaked into ws-b")
	}
}
