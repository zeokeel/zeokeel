package toolkit

// Tests for the MCP per-tool disable filter (settings mcp.disabledTools).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// writeMCPStdioTwoToolsServer writes a tiny MCP stdio server exposing echo and
// second tools.
func writeMCPStdioTwoToolsServer(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      [ -n "$id" ] && printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"stdio-test","version":"1.0.0"}}}\n' "$id"
      ;;
    *'"method":"tools/list"'*)
      [ -n "$id" ] && printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}},{"name":"second","description":"Second","inputSchema":{"type":"object"}}]}}\n' "$id"
      ;;
  esac
done
`
	path := filepath.Join(t.TempDir(), "mcp-stdio-two-tools.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stdio server: %v", err)
	}
	return path
}

func stdioToolSettings(t *testing.T, command string) string {
	t.Helper()
	encoded, err := contracts.EncodeMCPServersJSON([]contracts.MCPServerConfig{{
		Name: "local", Transport: "stdio", Command: command,
	}})
	if err != nil {
		t.Fatalf("encode MCP settings: %v", err)
	}
	return encoded
}

func TestShouldRegisterMCPTool(t *testing.T) {
	disabled := map[string]bool{"mcp__srv__echo": true, "mcp_list_resources": true}
	cases := []struct {
		name string
		want bool
	}{
		{"mcp__srv__echo", false},
		{"mcp__srv__second", true},
		{"mcp_list_resources", false},
		{"read_file", true},
		{"", true},
	}
	for _, tc := range cases {
		if got := shouldRegisterMCPTool(tc.name, disabled); got != tc.want {
			t.Fatalf("shouldRegisterMCPTool(%q) = %t, want %t", tc.name, got, tc.want)
		}
	}
	if !shouldRegisterMCPTool("mcp__srv__echo", nil) {
		t.Fatal("empty disabled list must register MCP tools")
	}
	if shouldRegisterMCPTool("mcp__srv__echo", []string{"mcp__srv__echo", " "}) {
		t.Fatal("slice-form disabled list must filter MCP tools")
	}
	if !shouldRegisterMCPTool("mcp__srv__second", []string{"mcp__srv__echo"}) {
		t.Fatal("slice-form disabled list must keep other MCP tools")
	}
}

func TestMCPServerDisabledToolsFilterRegistration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	command := writeMCPStdioTwoToolsServer(t)
	baseSettings := map[string]string{contracts.SettingMCPServers: stdioToolSettings(t, command)}
	for _, tc := range []struct {
		name     string
		disabled string
		wantEcho bool
		wantNext bool
	}{
		{name: "one disabled", disabled: `["mcp__local__echo"]`, wantEcho: false, wantNext: true},
		{name: "none disabled", disabled: `[]`, wantEcho: true, wantNext: true},
		{name: "bad JSON registers all", disabled: `not-json`, wantEcho: true, wantNext: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := make(map[string]string, len(baseSettings)+1)
			for key, value := range baseSettings {
				values[key] = value
			}
			values[contracts.SettingMCPDisabledTools] = tc.disabled
			logger := &captureLogger{}
			tools, _ := activateMCPToolkit(t, map[string]any{"root": "."}, newMutableSettings(values), logger)
			names := make(map[string]bool)
			for _, spec := range tools.Specs() {
				names[spec.Name] = true
			}
			if names["mcp__local__echo"] != tc.wantEcho || names["mcp__local__second"] != tc.wantNext {
				t.Fatalf("registered MCP tools = %v (echo=%t second=%t)", names, names["mcp__local__echo"], names["mcp__local__second"])
			}
			// Built-in tools are never filtered by the MCP setting.
			if !names["read_file"] && !names["bash"] && !names["glob"] {
				t.Fatalf("built-in tools disappeared: %v", names)
			}
			if tc.disabled == "not-json" && !logger.contains("disabledTools") {
				t.Fatalf("bad JSON warning missing: %v", logger.lines)
			}
		})
	}
}

func TestDisabledToolsSettingParsing(t *testing.T) {
	values, err := contracts.ParseStringListJSON(`[" a ","","b"]`)
	if err != nil || len(values) != 3 {
		t.Fatalf("ParseStringListJSON = %v/%v", values, err)
	}
	if _, err := contracts.ParseStringListJSON(`{"nope":1}`); err == nil {
		t.Fatal("ParseStringListJSON accepted an object")
	}
	encoded, err := contracts.EncodeStringListJSON([]string{"a", "b"})
	if err != nil || encoded != `["a","b"]` {
		t.Fatalf("EncodeStringListJSON = %q/%v", encoded, err)
	}
	var decoded []string
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil || strings.Join(decoded, ",") != "a,b" {
		t.Fatalf("decoded list = %v/%v", decoded, err)
	}
}
