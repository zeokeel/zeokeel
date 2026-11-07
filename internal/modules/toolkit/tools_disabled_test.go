package toolkit

// Tests for the generic tools.disabled switch (built-in tools) merged with the
// legacy mcp.disabledTools setting.

import (
	"runtime"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func toolkitSpecs(t *testing.T, settings map[string]string) map[string]bool {
	t.Helper()
	reg, _ := activateMCPToolkit(t, map[string]any{"root": "."}, newMutableSettings(settings), &captureLogger{})
	names := make(map[string]bool)
	for _, spec := range reg.Specs() {
		names[spec.Name] = true
	}
	return names
}

func TestToolsDisabledFiltersBuiltInTools(t *testing.T) {
	logger := &captureLogger{}
	tools, _ := activateMCPToolkit(t, map[string]any{"root": "."},
		newMutableSettings(map[string]string{contracts.SettingToolsDisabled: `["read_file","bash"]`}), logger)
	names := make(map[string]bool)
	for _, spec := range tools.Specs() {
		names[spec.Name] = true
	}
	if names["read_file"] || names["bash"] {
		t.Fatalf("disabled built-ins are registered: %v", names)
	}
	if !names["glob"] && !names["list_dir"] {
		t.Fatalf("other built-ins disappeared: %v", names)
	}
	if !logger.contains("disabled") {
		t.Fatalf("missing disabled-tool warning: %v", logger.lines)
	}
}

func TestToolsDisabledBadJSONRegistersAll(t *testing.T) {
	names := toolkitSpecs(t, map[string]string{contracts.SettingToolsDisabled: "not-json"})
	if !names["read_file"] {
		t.Fatalf("bad JSON filtered read_file: %v", names)
	}
}

func TestToolsDisabledUnionWithMCPDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	command := writeMCPStdioTwoToolsServer(t)
	settings := map[string]string{
		contracts.SettingMCPServers:       stdioToolSettings(t, command),
		contracts.SettingToolsDisabled:    `["read_file","mcp__local__echo"]`,
		contracts.SettingMCPDisabledTools: `["mcp__local__second"]`,
	}
	names := toolkitSpecs(t, settings)
	for _, want := range []string{"read_file", "mcp__local__echo", "mcp__local__second"} {
		if names[want] {
			t.Fatalf("union disabled tool %q is registered: %v", want, names)
		}
	}
	if !names["glob"] {
		t.Fatalf("glob should remain registered: %v", names)
	}
}
