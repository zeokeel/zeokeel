package toolkit

// MCP tool-level switches: mcp.disabledTools stores model-visible tool names;
// activation parses it once and skips matching tools when registering an MCP
// client's toolset. Matching is trimmed and exact (case-sensitive) — MCP tool
// names are stable identifiers. Built-in tools and MCP resource tools are not
// affected by any other filtering.

import (
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
)

// shouldRegisterMCPTool reports whether a discovered MCP tool should be added
// to the product registry. disabled accepts the internal map or a plain name
// slice (either is convenient for table-driven checks); empty/nil registers.
func shouldRegisterMCPTool(name string, disabled any) bool {
	switch values := disabled.(type) {
	case nil:
		return true
	case map[string]bool:
		if len(values) == 0 {
			return true
		}
		return !values[strings.TrimSpace(name)]
	case []string:
		name = strings.TrimSpace(name)
		for _, value := range values {
			if strings.TrimSpace(value) == name {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// filterDisabledTools drops any registry tool whose trimmed name is disabled by
// tools.disabled or mcp.disabledTools and logs each skipped name.
func filterDisabledTools(tools []rig.Tool, disabled map[string]bool,
	logf func(format string, args ...any)) []rig.Tool {
	if len(disabled) == 0 || len(tools) == 0 {
		return tools
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	filtered := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name())
		if !shouldRegisterMCPTool(name, disabled) {
			logf("[toolkit] skip disabled tool %q", name)
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}
