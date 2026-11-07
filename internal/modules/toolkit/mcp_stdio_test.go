package toolkit

// Tests for MCP stdio transports in the toolkit merge/startup logic.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// writeMCPStdioServer writes a tiny newline-delimited JSON-RPC MCP server that
// answers initialize/tools/list for one "stdio_echo" tool.
func writeMCPStdioServer(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      [ -n "$id" ] && printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"stdio-test","version":"1.0.0"}}}\n' "$id"
      ;;
    *'"method":"tools/list"'*)
      [ -n "$id" ] && printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"stdio_echo","description":"Echo text","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}}]}}\n' "$id"
      ;;
  esac
done
`
	path := filepath.Join(t.TempDir(), "mcp-stdio-server.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stdio server: %v", err)
	}
	return path
}

// stdioSettingsJSON encodes one stdio MCP row for settings.
func stdioSettingsJSON(t *testing.T, name, command string, args []string) string {
	t.Helper()
	encoded, err := contracts.EncodeMCPServersJSON([]contracts.MCPServerConfig{{
		Name: name, Transport: "stdio", Command: command, Args: args,
	}})
	if err != nil {
		t.Fatalf("encode MCP settings: %v", err)
	}
	return encoded
}

func TestToolkitMCPStdioStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	command := writeMCPStdioServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: stdioSettingsJSON(t, "local", command, nil),
	})
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	entries := plugins.MCP()
	if len(entries) != 1 || entries[0].Name != "local" || !strings.Contains(entries[0].Status, "已连接") || entries[0].Tools != 1 {
		t.Fatalf("stdio MCP entries = %+v", entries)
	}
	found := false
	for _, spec := range tools.Specs() {
		if strings.Contains(spec.Name, "stdio_echo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("stdio MCP tool not registered: %v", tools.Specs())
	}
}

func TestToolkitMCPStdioFailuresAndPriority(t *testing.T) {
	command := writeMCPStdioServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[
			{"name":"empty-stdio","transport":"stdio"},
			{"name":"missing-command","transport":"stdio","command":"/definitely/not/a/mcp-server"},
			{"name":"both","transport":"stdio","command":"` + command + `","url":"http://127.0.0.1:1/ignored"}
		]`,
	})
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	entries := plugins.MCP()
	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want three configured rows", entries)
	}
	byName := make(map[string]contracts.MCPPlugin, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	if !strings.Contains(byName["empty-stdio"].Status, "连接失败") {
		t.Fatalf("empty stdio status = %+v", byName["empty-stdio"])
	}
	if !strings.Contains(byName["missing-command"].Status, "连接失败") {
		t.Fatalf("missing command status = %+v", byName["missing-command"])
	}
	if !strings.Contains(byName["both"].Status, "已连接") || byName["both"].Tools != 1 {
		t.Fatalf("stdio priority status = %+v", byName["both"])
	}
	foundEcho := false
	for _, spec := range tools.Specs() {
		if strings.Contains(spec.Name, "stdio_echo") {
			foundEcho = true
		}
	}
	if !foundEcho {
		t.Fatalf("stdio priority tool missing: %v", tools.Specs())
	}
}

func TestMCPStdioConfigConstruction(t *testing.T) {
	server := MCPServer{Name: "local", Transport: "stdio", Command: "mcp-server", Args: []string{"--stdio"}, Env: []string{"A=1"}}
	if transport := mcpServerTransport(server); transport != "stdio" {
		t.Fatalf("transport = %q, want stdio", transport)
	}
	cfg := mcpStartConfig(server)
	if cfg.Transport != "stdio" || cfg.Command != "mcp-server" || len(cfg.Args) != 1 || len(cfg.Env) != 1 || cfg.ServerName != "local" {
		t.Fatalf("stdio start config = %+v", cfg)
	}
	// Command wins over URL when both are configured.
	both := MCPServer{Name: "both", URL: "http://127.0.0.1:1/mcp", Command: "mcp-server"}
	if transport := mcpServerTransport(both); transport != "stdio" {
		t.Fatalf("both transport = %q, want stdio priority", transport)
	}
	httpOnly := MCPServer{Name: "http", URL: "http://127.0.0.1:1/mcp"}
	if transport := mcpServerTransport(httpOnly); transport != "http" {
		t.Fatalf("http transport = %q", transport)
	}
}
