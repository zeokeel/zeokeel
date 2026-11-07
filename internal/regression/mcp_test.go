package regression

// Adversarial audit: the toolkit's MCP settings merge (assembly servers +
// settings mcp.servers). The merge function is unexported, so these are
// black-box assertions through module activation and the public PluginsService
// read model: bad JSON, NUL bytes, empty rows, duplicate URLs (assembly wins)
// and a settings service that is absent entirely.

import (
	"context"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
)

// startToolkit activates the real toolkit module with the given assembly MCP
// servers and settings rows and returns the plugin read model.
func startToolkit(t *testing.T, h *harness, assemblyServers []toolkit.MCPServer, settingsRaw string) contracts.PluginsService {
	t.Helper()
	if settingsRaw != "" {
		if err := h.settings.Set(contracts.SettingMCPServers, settingsRaw); err != nil {
			t.Fatalf("set %s: %v", contracts.SettingMCPServers, err)
		}
	}
	activate(t, h.ctx, toolkit.Module(), toolkit.Config{MCPServers: assemblyServers})
	return requireService(t, h.ctx, contracts.KeyPlugins)
}

// pluginNames returns the display names of the plugin read model in order.
func pluginNames(plugins []contracts.MCPPlugin) []string {
	names := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		names = append(names, plugin.Name)
	}
	return names
}

// mcpServer builds one unreachable assembly MCP server: the handshake fails
// fast (connection refused), which is exactly the failure entry the Plugins
// screen must show.
func mcpServer(name, url string) toolkit.MCPServer {
	return toolkit.MCPServer{Name: name, URL: url, TimeoutMs: 50}
}

// TestMCPMergeBadJSONAndNUL covers hostile settings values: invalid JSON and
// rows carrying NUL bytes are logged and skipped, never failing activation.
func TestMCPMergeBadJSONAndNUL(t *testing.T) {
	h := newHarness(t)
	plugins := startToolkit(t, h, nil, "this is not json")
	if got := plugins.MCP(); len(got) != 0 {
		t.Fatalf("MCP() = %+v after bad JSON, want no plugins", got)
	}
	if !h.logger.contains("parse " + contracts.SettingMCPServers) {
		t.Fatalf("the bad JSON was not logged: %v", h.logger.lines)
	}

	for name, raw := range map[string]string{
		"nul-name": `[{"name":"bad\u0000name","url":"http://127.0.0.1:1/a"}]`,
		"nul-url":  `[{"name":"bad","url":"http://127.0.0.1:1/b\u0000"}]`,
		"empty":    `[{"name":"","url":"http://127.0.0.1:1/c"},{"name":"x","url":""},{"name":" ","url":" "}]`,
		"wrong":    `{"name":"not-an-array"}`,
	} {
		t.Run(name, func(t *testing.T) {
			sub := newHarness(t)
			plugins := startToolkit(t, sub, nil, raw)
			if got := plugins.MCP(); len(got) != 0 {
				t.Fatalf("MCP() = %+v for %s rows, want all rows skipped", got, name)
			}
			if !sub.logger.contains("skip "+contracts.SettingMCPServers) && !sub.logger.contains("parse "+contracts.SettingMCPServers) {
				t.Fatalf("the skipped rows were not logged: %v", sub.logger.lines)
			}
		})
	}
}

// TestMCPMergeAssemblyWinsDuplicateURL is the policy negative: a settings row
// repeating an assembly server's URL is dropped and the assembly entry is the
// one shown (operator policy wins), while a genuinely new user URL is appended.
func TestMCPMergeAssemblyWinsDuplicateURL(t *testing.T) {
	h := newHarness(t)
	assembly := []toolkit.MCPServer{mcpServer("operator", "http://127.0.0.1:1/shared")}
	raw := `[{"name":"user-dup","url":"http://127.0.0.1:1/shared"},` +
		`{"name":"user-extra","url":"http://127.0.0.1:1/extra"}]`
	plugins := startToolkit(t, h, assembly, raw)
	got := plugins.MCP()
	names := pluginNames(got)
	if len(got) != 2 || names[0] != "operator" || names[1] != "user-extra" {
		t.Fatalf("MCP() names = %v, want [operator user-extra] (assembly first, duplicate dropped)", names)
	}
	if !h.logger.contains("assembly wins") {
		t.Fatalf("the duplicate URL was not logged as deferred to the assembly: %v", h.logger.lines)
	}
	// Both entries are attempted once; unreachable servers stay readable failure
	// entries instead of failing activation.
	for _, plugin := range got {
		if !strings.HasPrefix(plugin.Status, "连接失败") {
			t.Fatalf("plugin %q status = %q, want a connection-failure status", plugin.Name, plugin.Status)
		}
	}
}

// TestMCPMergeDuplicateUserRows covers two user rows with the same URL: the
// first occurrence wins and the duplicate is skipped with a log line.
func TestMCPMergeDuplicateUserRows(t *testing.T) {
	h := newHarness(t)
	raw := `[{"name":"first","url":"http://127.0.0.1:1/one"},` +
		`{"name":"second","url":"http://127.0.0.1:1/one"}]`
	plugins := startToolkit(t, h, nil, raw)
	got := plugins.MCP()
	if len(got) != 1 || got[0].Name != "first" {
		t.Fatalf("MCP() = %+v, want only the first row for the duplicate URL", got)
	}
	if !h.logger.contains("duplicate URL") {
		t.Fatalf("the duplicate user URL was not logged: %v", h.logger.lines)
	}
}

// TestMCPMergeEmptySettingsAndAbsentService covers the two no-op inputs: an
// empty setting and a host without any settings service.
func TestMCPMergeEmptySettingsAndAbsentService(t *testing.T) {
	h := newHarness(t)
	assembly := []toolkit.MCPServer{mcpServer("operator", "http://127.0.0.1:1/shared")}
	plugins := startToolkit(t, h, assembly, "")
	got := plugins.MCP()
	if len(got) != 1 || got[0].Name != "operator" {
		t.Fatalf("MCP() with empty settings = %+v, want the assembly server only", got)
	}

	bare := newBareHarness(t) // no KeySettings: the toolkit keeps its assembly set
	plugins = startToolkit(t, bare, assembly, "")
	if got := plugins.MCP(); len(got) != 1 || got[0].Name != "operator" {
		t.Fatalf("MCP() without a settings service = %+v, want the assembly server only", got)
	}
}

// TestMCPMergeMissingURLKeepsFailureEntry covers a configured server without a
// URL: it must stay visible as a failed entry (misconfiguration shown to the
// user) instead of being silently dropped.
func TestMCPMergeMissingURLKeepsFailureEntry(t *testing.T) {
	h := newHarness(t)
	plugins := startToolkit(t, h, []toolkit.MCPServer{{Name: "半配置", URL: ""}}, "")
	got := plugins.MCP()
	if len(got) != 1 || got[0].Name != "半配置" {
		t.Fatalf("MCP() = %+v, want the misconfigured server entry", got)
	}
	if !strings.Contains(got[0].Status, "未配置 URL") {
		t.Fatalf("status = %q, want the missing-URL diagnostic", got[0].Status)
	}
}

// TestMCPRefreshToleratesCanceledContext covers PluginsService.Refresh with a
// nil/cancelled context: it must return the same read model without panicking.
func TestMCPRefreshToleratesCanceledContext(t *testing.T) {
	h := newHarness(t)
	plugins := startToolkit(t, h, []toolkit.MCPServer{mcpServer("operator", "http://127.0.0.1:1/shared")}, "")
	before := plugins.MCP()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	after := plugins.Refresh(canceled)
	if len(after) != len(before) {
		t.Fatalf("Refresh(cancelled) returned %d plugins, want %d", len(after), len(before))
	}
	if after[0].Name != before[0].Name {
		t.Fatalf("Refresh changed the plugin identity: %+v", after)
	}
	// A nil context is treated as Background by the service.
	if got := plugins.Refresh(nil); len(got) != len(before) {
		t.Fatalf("Refresh(nil) returned %d plugins, want %d", len(got), len(before))
	}
}

// TestMCPMergeURLCaseSensitivity documents a known limitation: deduplication is
// on the trimmed raw string, so the same endpoint written with a differently
// cased scheme/host is added twice. Recorded as AUDIT-P3-1 in the report.
func TestMCPMergeURLCaseSensitivity(t *testing.T) {
	h := newHarness(t)
	raw := `[{"name":"cased","url":"HTTP://127.0.0.1:1/shared"}]`
	plugins := startToolkit(t, h, []toolkit.MCPServer{mcpServer("operator", "http://127.0.0.1:1/shared")}, raw)
	got := plugins.MCP()
	if len(got) == 2 {
		t.Logf("AUDIT-P3-1: case-different duplicate URLs are not deduplicated: %v", pluginNames(got))
		return
	}
	if len(got) != 1 {
		t.Fatalf("MCP() = %+v, want one or two entries", got)
	}
}
