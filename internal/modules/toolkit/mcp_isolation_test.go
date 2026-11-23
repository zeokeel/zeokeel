package toolkit

// Isolation-boundary tests: OAuth token records are per workspace (with legacy
// migration), the toolkit eagerly migrates legacy connector rows on load and
// reload, and it drops rows plus scoped disabled keys of deleted workspaces
// while keeping data when the registry read fails.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestMCPOAuthTokenRecordsAreWorkspaceScoped(t *testing.T) {
	settings := newMutableSettings(nil)
	scope := "ws-a"
	store := mcpOAuthTokenStore{
		settings:    settings,
		server:      "notion",
		workspaceID: func() string { return scope },
	}
	ctx := context.Background()
	if err := store.Save(ctx, "notion", []byte("token-a")); err != nil {
		t.Fatalf("Save ws-a: %v", err)
	}
	if got := settings.Get(contracts.MCPAuthSettingKeyForWorkspace("ws-a", "notion"), ""); got != "token-a" {
		t.Fatalf("ws-a token key = %q", got)
	}

	// Another workspace must not read ws-a's token.
	scope = "ws-b"
	if _, ok, err := store.Load(ctx, "notion"); err != nil || ok {
		t.Fatalf("ws-b Load = ok %v err %v, want no token", ok, err)
	}
	if err := store.Save(ctx, "notion", []byte("token-b")); err != nil {
		t.Fatalf("Save ws-b: %v", err)
	}

	// Back to ws-a: its own record is intact.
	scope = "ws-a"
	data, ok, err := store.Load(ctx, "notion")
	if err != nil || !ok || string(data) != "token-a" {
		t.Fatalf("ws-a Load = %q/%v/%v", data, ok, err)
	}
	scope = "ws-b"
	data, ok, err = store.Load(ctx, "notion")
	if err != nil || !ok || string(data) != "token-b" {
		t.Fatalf("ws-b Load = %q/%v/%v", data, ok, err)
	}
}

func TestMCPOAuthLegacyTokenMovesToFirstWorkspace(t *testing.T) {
	legacyKey := contracts.MCPAuthSettingKey("notion")
	settings := newMutableSettings(map[string]string{legacyKey: `{"version":1,"access":"legacy"}`})
	scope := "ws-a"
	store := mcpOAuthTokenStore{
		settings:    settings,
		server:      "notion",
		workspaceID: func() string { return scope },
	}
	data, ok, err := store.Load(context.Background(), "notion")
	if err != nil || !ok || !strings.Contains(string(data), "legacy") {
		t.Fatalf("legacy Load = %q/%v/%v", data, ok, err)
	}
	if got := settings.Get(contracts.MCPAuthSettingKeyForWorkspace("ws-a", "notion"), ""); got == "" {
		t.Fatal("legacy token was not moved into ws-a")
	}
	if got := settings.Get(legacyKey, ""); got != "" {
		t.Fatalf("legacy key still holds the token: %q", got)
	}

	// The legacy record is moved, not copied: another workspace starts clean.
	scope = "ws-b"
	if _, ok, err := store.Load(context.Background(), "notion"); err != nil || ok {
		t.Fatalf("ws-b inherited the legacy token: ok %v err %v", ok, err)
	}
}

func TestMCPAuthConfigUsesPerWorkspaceClient(t *testing.T) {
	settings := newMutableSettings(nil)
	scope := "ws-a"
	runtime := newMCPAuthRuntime(settings, nil, nil, func() string { return scope })
	cfg := contracts.MCPServerConfig{
		Name: "notion", URL: "https://mcp.example.com/mcp",
		Auth: &contracts.MCPAuthConfig{Mode: contracts.MCPAuthOAuth2},
	}
	first, err := runtime.authConfig(cfg)
	if err != nil {
		t.Fatalf("authConfig ws-a: %v", err)
	}
	again, err := runtime.authConfig(cfg)
	if err != nil || again.HTTPClient != first.HTTPClient {
		t.Fatalf("same workspace changed the OAuth client sentinel: %v", err)
	}
	scope = "ws-b"
	switched, err := runtime.authConfig(cfg)
	if err != nil {
		t.Fatalf("authConfig ws-b: %v", err)
	}
	if switched.HTTPClient == first.HTTPClient {
		t.Fatal("workspace switch reused the cached OAuth client (token could leak)")
	}
}

func TestToolkitLoadEagerlyMigratesLegacyConnectorRows(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()
	settings := newMutableSettings(map[string]string{
		// No WorkspaceID: an upgrade row that used to be visible everywhere.
		contracts.SettingMCPServers: `[{"name":"alpha","url":"` + alpha.URL + `"}]`,
	})
	workspace := &fakeMCPWorkspace{current: contracts.Workspace{ID: "ws-a"}, has: true}
	registry, _, _ := activateScopedToolkit(t, settings, workspace)

	if !registryHasTool(registry, "mcp__alpha__alpha_tool") {
		t.Fatal("legacy connector is not live after the toolkit load")
	}
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil || len(rows) != 1 {
		t.Fatalf("migrated rows = %+v/%v", rows, err)
	}
	if rows[0].WorkspaceID != "ws-a" {
		t.Fatalf("legacy row WorkspaceID = %q, want ws-a (eager migration)", rows[0].WorkspaceID)
	}
}

func TestMCPReloadGCsDeletedWorkspaceState(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()
	goneServer := testMCPToolListServer(t, []map[string]any{oneTool("gone_tool")})
	defer goneServer.Close()

	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"alpha","url":"` + alpha.URL + `","workspaceId":"ws-a"},` +
			`{"name":"gone","url":"` + goneServer.URL + `","workspaceId":"ws-gone"}]`,
		contracts.MCPDisabledServersSettingKey("ws-gone"): `["gone"]`,
		contracts.MCPDisabledServersSettingKey("ws-a"):    `["other"]`,
	})
	workspace := &fakeMCPWorkspace{current: contracts.Workspace{ID: "ws-a"}, has: true}
	registry, plugins, _ := activateScopedToolkit(t, settings, workspace)

	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil || len(rows) != 1 || rows[0].WorkspaceID != "ws-a" {
		t.Fatalf("rows after GC = %+v/%v, want only ws-a's connector", rows, err)
	}
	if got := settings.Get(contracts.MCPDisabledServersSettingKey("ws-gone"), ""); got != "" {
		t.Fatalf("deleted workspace's disabled key survived: %q", got)
	}
	if got := settings.Get(contracts.MCPDisabledServersSettingKey("ws-a"), ""); got != `["other"]` {
		t.Fatalf("live workspace's disabled key changed: %q", got)
	}
	if registryHasTool(registry, "mcp__gone__gone_tool") {
		t.Fatal("deleted workspace's connector is still live")
	}
	if len(plugins.MCP()) != 1 || plugins.MCP()[0].Name != "alpha" {
		t.Fatalf("plugins = %+v, want only alpha", plugins.MCP())
	}
}

func TestMCPReloadKeepsStateWhenWorkspaceListFails(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"alpha","url":"` + alpha.URL + `","workspaceId":"ws-a"},` +
			`{"name":"orphan","url":"` + alpha.URL + `/2","workspaceId":"ws-orphan"}]`,
		contracts.MCPDisabledServersSettingKey("ws-orphan"): `["x"]`,
	})
	workspace := &fakeMCPWorkspace{
		current: contracts.Workspace{ID: "ws-a"}, has: true,
		listErr: errors.New("workspace registry unavailable"),
	}
	activateScopedToolkit(t, settings, workspace)

	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows after failed List = %+v/%v, want both kept", rows, err)
	}
	if got := settings.Get(contracts.MCPDisabledServersSettingKey("ws-orphan"), ""); got == "" {
		t.Fatal("disabled key for the unreadable registry was cleared")
	}
}
