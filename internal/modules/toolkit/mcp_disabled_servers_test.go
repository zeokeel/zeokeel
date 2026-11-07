package toolkit

// Tests for the MCP server-level switch (mcp.disabledServers): the parser/
// filter helper and activation behavior (disabled servers are not started, bad
// JSON starts everything, matching is trimmed/case-insensitive).

import (
	"runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestFilterDisabledMCPServers(t *testing.T) {
	servers := []MCPServer{
		{Name: "local", Command: "/bin/true"},
		{Name: "remote", URL: "http://127.0.0.1:1/mcp"},
	}
	tests := []struct {
		name    string
		raw     string
		want    int
		skipped int
		warn    string
	}{
		{name: "empty keeps all", raw: "", want: 2},
		{name: "exact match", raw: `["local"]`, want: 1, skipped: 1},
		{name: "case and trim match", raw: `[" LOCAL "]`, want: 1, skipped: 1},
		{name: "unknown keeps all", raw: `["other"]`, want: 2},
		{name: "bad json keeps all", raw: "not-json", want: 2, warn: "parse"},
		{name: "empty strings ignored", raw: `["","local"]`, want: 1, skipped: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var warnings []string
			logf := func(format string, args ...any) {
				warnings = append(warnings, format)
			}
			filtered, skipped := filterDisabledMCPServers(servers, test.raw, logf)
			if len(filtered) != test.want || skipped != test.skipped {
				t.Fatalf("filtered/skipped = %d/%d, want %d/%d (names %v)",
					len(filtered), skipped, test.want, test.skipped, serverNames(filtered))
			}
			if test.warn != "" {
				joined := strings.Join(warnings, "\n")
				if !strings.Contains(joined, test.warn) {
					t.Fatalf("warnings = %v, want one mentioning %q", warnings, test.warn)
				}
			}
		})
	}
}

func serverNames(servers []MCPServer) []string {
	out := make([]string, 0, len(servers))
	for _, server := range servers {
		out = append(out, server.Name)
	}
	return out
}

func TestToolkitSkipsDisabledMCPServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	command := writeMCPStdioServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers:         stdioSettingsJSON(t, "local", command, nil),
		contracts.SettingMCPDisabledServers: `["  LOCAL "]`,
	})
	logger := &captureLogger{}
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, logger)
	if entries := plugins.MCP(); len(entries) != 0 {
		t.Fatalf("disabled MCP server still reported: %+v", entries)
	}
	for _, spec := range tools.Specs() {
		if strings.Contains(spec.Name, "stdio_echo") {
			t.Fatalf("disabled MCP server registered its tool: %v", spec.Name)
		}
	}
	if !logger.contains("disabled MCP server") {
		t.Fatalf("logger lines = %v, want a disabled-server skip", logger.lines)
	}
}

func TestToolkitStartsAllServersOnBadDisabledJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	command := writeMCPStdioServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers:         stdioSettingsJSON(t, "local", command, nil),
		contracts.SettingMCPDisabledServers: `{"not":"an array"}`,
	})
	logger := &captureLogger{}
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, logger)
	if entries := plugins.MCP(); len(entries) != 1 {
		t.Fatalf("bad disabled JSON suppressed the server: %+v", entries)
	}
	found := false
	for _, spec := range tools.Specs() {
		if strings.Contains(spec.Name, "stdio_echo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bad disabled JSON suppressed the MCP tool: %v", tools.Specs())
	}
	if !logger.contains("parse") {
		t.Fatalf("logger lines = %v, want a parse warning", logger.lines)
	}
}
