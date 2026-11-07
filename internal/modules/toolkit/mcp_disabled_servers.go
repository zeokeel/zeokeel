package toolkit

// MCP server-level switches: mcp.disabledServers stores a JSON array of server
// names that must not be started (their tools are not registered). Matching is
// trimmed and case-insensitive; bad JSON or empty rows are logged and ignored,
// never failing activation. The startup merge runs first, then this filter drops
// the disabled servers before any connection is attempted.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// filterDisabledMCPServers drops the servers named by mcp.disabledServers and
// reports how many were skipped. A malformed JSON value keeps every server and
// logs a parse warning.
func filterDisabledMCPServers(servers []MCPServer, raw string, logf func(format string, args ...any)) ([]MCPServer, int) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	names, err := contracts.ParseStringListJSON(raw)
	if err != nil {
		logf("[toolkit] parse %s: %v", contracts.SettingMCPDisabledServers, err)
		return servers, 0
	}
	if len(names) == 0 {
		return servers, 0
	}
	disabled := make(map[string]bool, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			disabled[strings.ToLower(trimmed)] = true
		}
	}
	if len(disabled) == 0 {
		return servers, 0
	}
	kept := make([]MCPServer, 0, len(servers))
	skipped := 0
	for _, server := range servers {
		if disabled[strings.ToLower(strings.TrimSpace(server.Name))] {
			logf("[toolkit] skip disabled MCP server %q", server.Name)
			skipped++
			continue
		}
		kept = append(kept, server)
	}
	return kept, skipped
}
