package toolkit

// Regression tests for MCP-05: the Plugins read model's tool count reflects the
// tools actually registered (disabled tools excluded) and stays consistent
// after a Refresh.

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestPluginToolCountExcludesDisabledTools covers activation.
func TestPluginToolCountExcludesDisabledTools(t *testing.T) {
	server := testMCPToolListServer(t, []map[string]any{oneTool("echo"), oneTool("second")})
	defer server.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers:    `[{"name":"srv","url":"` + server.URL + `"}]`,
		contracts.SettingToolsDisabled: `["mcp__srv__echo"]`,
	})
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	if got := plugins.MCP()[0].Tools; got != 1 {
		t.Fatalf("plugin Tools = %d, want 1 (echo disabled)", got)
	}
	for _, spec := range tools.Specs() {
		if spec.Name == "mcp__srv__echo" {
			t.Fatal("disabled MCP tool is registered")
		}
	}
}

// TestPluginRefreshCountMatchesRegistry covers the Refresh path: the count must
// stay filtered (a disabled tool is not registered and must not be counted).
func TestPluginRefreshCountMatchesRegistry(t *testing.T) {
	server := testMCPToolListServer(t, []map[string]any{oneTool("echo"), oneTool("second")})
	defer server.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers:       `[{"name":"srv","url":"` + server.URL + `"}]`,
		contracts.SettingMCPDisabledTools: `["mcp__srv__second"]`,
	})
	_, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	if got := plugins.MCP()[0].Tools; got != 1 {
		t.Fatalf("plugin Tools after activation = %d, want 1", got)
	}
	refreshed := plugins.Refresh(context.Background())
	if len(refreshed) != 1 {
		t.Fatalf("Refresh returned %+v", refreshed)
	}
	if refreshed[0].Tools != 1 {
		t.Fatalf("plugin Tools after Refresh = %d, want 1 (registered tools, disabled excluded)", refreshed[0].Tools)
	}
}
