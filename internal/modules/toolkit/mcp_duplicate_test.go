package toolkit

// Regression tests for FR-01: two MCP servers (or one server whose tools
// sanitize to the same model-visible name) must never panic the toolkit
// activation. The MCP resource/prompt tools are namespaced per server and
// duplicate registrations are skipped with a warning.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// testMCPToolListServer is a minimal Streamable HTTP MCP server that lists the
// given tools and answers tools/call with "pong".
func testMCPToolListServer(t *testing.T, tools []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("server: bad request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test", "version": "1.0.0"},
			}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": tools}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong"}}}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
}

// oneTool builds a tools/list entry.
func oneTool(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": "Tool " + name,
		"inputSchema": map[string]any{"type": "object"},
	}
}

// TestToolkitMCPTwoStdioServersNoDuplicatePanic is the FR-01 regression: two
// successfully connecting MCP servers contribute the same rig resource/prompt
// tool names, which used to panic rig's NewRegistry and fail activation.
func TestToolkitMCPTwoStdioServersNoDuplicatePanic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	first := writeMCPStdioServer(t)
	second := writeMCPStdioServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"alpha","transport":"stdio","command":"` + first + `"},` +
			`{"name":"beta","transport":"stdio","command":"` + second + `"}]`,
	})
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)

	entries := plugins.MCP()
	if len(entries) != 2 {
		t.Fatalf("MCP entries = %+v, want two connected servers", entries)
	}
	for _, entry := range entries {
		if !strings.Contains(entry.Status, "已连接") || entry.Tools != 1 {
			t.Fatalf("MCP entry = %+v, want connected with one tools/list tool", entry)
		}
	}

	names := map[string]bool{}
	for _, spec := range tools.Specs() {
		if names[spec.Name] {
			t.Fatalf("duplicate tool name %q", spec.Name)
		}
		names[spec.Name] = true
	}
	for _, want := range []string{
		"mcp__alpha__stdio_echo", "mcp__beta__stdio_echo",
		"mcp__alpha__mcp_list_resources", "mcp__alpha__mcp_read_resource",
		"mcp__alpha__mcp_list_prompts", "mcp__alpha__mcp_get_prompt",
		"mcp__beta__mcp_list_resources", "mcp__beta__mcp_read_resource",
		"mcp__beta__mcp_list_prompts", "mcp__beta__mcp_get_prompt",
	} {
		if !names[want] {
			t.Fatalf("registry lacks %q (have %v)", want, names)
		}
	}
	// The unprefixed rig resource names must not appear anymore: they are the
	// collision source between servers.
	for _, old := range []string{"mcp_list_resources", "mcp_read_resource", "mcp_list_prompts", "mcp_get_prompt"} {
		if names[old] {
			t.Fatalf("registry still has the unprefixed MCP resource tool %q", old)
		}
	}
}

// TestToolkitMCPDuplicateSanitizedNameSkipped covers one hostile/buggy server
// whose two tool names sanitize to the same model-visible name: activation
// succeeds and the duplicate is skipped with a warning.
func TestToolkitMCPDuplicateSanitizedNameSkipped(t *testing.T) {
	server := testMCPToolListServer(t, []map[string]any{oneTool("run.sh"), oneTool("run/sh")})
	defer server.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"evil","url":"` + server.URL + `"}]`,
	})
	logger := &captureLogger{}
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, logger)
	if len(plugins.MCP()) != 1 || !strings.Contains(plugins.MCP()[0].Status, "已连接") {
		t.Fatalf("MCP entries = %+v, want one connected server", plugins.MCP())
	}
	registered := 0
	for _, spec := range tools.Specs() {
		if spec.Name == "mcp__evil__run_sh" {
			registered++
		}
	}
	if registered != 1 {
		t.Fatalf("mcp__evil__run_sh registered %d times, want exactly once", registered)
	}
	if !logger.contains("skip duplicate tool") {
		t.Fatalf("duplicate registration was not logged: %v", logger.lines)
	}
}

// TestToolkitMCPTwoServersSameNameSkipped covers two different endpoints that
// share one configured name: both connect, but the second server's tool set is
// skipped (duplicate names) instead of panicking activation.
func TestToolkitMCPTwoServersSameNameSkipped(t *testing.T) {
	first := testMCPToolListServer(t, []map[string]any{oneTool("shared")})
	defer first.Close()
	second := testMCPToolListServer(t, []map[string]any{oneTool("shared")})
	defer second.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"dup","url":"` + first.URL + `"},` +
			`{"name":"dup","url":"` + second.URL + `"}]`,
	})
	logger := &captureLogger{}
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, logger)
	if got := len(plugins.MCP()); got != 2 {
		t.Fatalf("MCP entries = %d, want both configured servers", got)
	}
	seen := 0
	for _, spec := range tools.Specs() {
		if spec.Name == "mcp__dup__shared" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("mcp__dup__shared registered %d times, want exactly once", seen)
	}
	if !logger.contains("skip duplicate tool") {
		t.Fatalf("duplicate registration was not logged: %v", logger.lines)
	}
}

// TestMCPDisabledToolsFilterPrefixedResourceTools is the toolkit-level FR-15
// assertion: the disabled-tool switches apply to the per-server MCP tool names,
// including the prefixed resource/prompt helpers, before anything reaches the
// registry.
func TestMCPDisabledToolsFilterPrefixedResourceTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	command := writeMCPStdioTwoToolsServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers:       stdioToolSettings(t, command),
		contracts.SettingMCPDisabledTools: `["mcp__local__echo","mcp__local__mcp_list_resources"]`,
	})
	tools, _ := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	names := make(map[string]bool)
	for _, spec := range tools.Specs() {
		names[spec.Name] = true
	}
	for _, disabled := range []string{"mcp__local__echo", "mcp__local__mcp_list_resources"} {
		if names[disabled] {
			t.Fatalf("disabled MCP tool %q is registered: %v", disabled, names)
		}
	}
	for _, kept := range []string{"mcp__local__second", "mcp__local__mcp_read_resource"} {
		if !names[kept] {
			t.Fatalf("enabled MCP tool %q missing: %v", kept, names)
		}
	}
}
