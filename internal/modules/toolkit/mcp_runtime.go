package toolkit

// MCP startup planning: the product validates each merged server row and freezes
// the Plugins read-model base entry (name/URL/status). The connection lifecycle,
// tool binding and reconnect supervision itself live in the Rig SDK
// github.com/zeokeel/zeokeel-rig/tools/mcp/manager; this file only decides which
// rows are runnable and what their diagnostic row says before they connect.

import (
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel-rig/tools/mcp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mcpStartupOverallTimeout bounds the whole MCP startup phase (the manager uses
// it as its Start budget). MCP servers start concurrently and every handshake is
// context-bounded by this budget, so a hung endpoint cannot hold the product's
// first frame for 30s per server (the rig per-request default), let alone
// minutes for several servers.
const mcpStartupOverallTimeout = 5 * time.Second

// mcpStartPlan is one configured server plus its validation outcome. Invalid
// rows keep a diagnostic Plugins entry and never reach the start phase.
type mcpStartPlan struct {
	server  MCPServer
	entry   pluginEntry
	invalid bool
}

// prepareMCPServerPlans validates every merged server row and freezes the
// Plugins read-model base entry (name/URL/status) for it.
func prepareMCPServerPlans(servers []MCPServer, logf func(string, ...any)) []mcpStartPlan {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	plans := make([]mcpStartPlan, 0, len(servers))
	for _, server := range servers {
		transport := mcpServerTransport(server)
		plan := mcpStartPlan{
			server: server,
			entry: pluginEntry{plugin: contracts.MCPPlugin{
				Name:   pluginDisplayName(server.Name, nil),
				URL:    strings.TrimSpace(server.URL),
				Status: statusConnectFailed + "未配置 URL",
			}},
		}
		switch transport {
		case mcp.TransportStdio:
			plan.entry.plugin.URL = ""
			if strings.TrimSpace(server.Command) == "" {
				plan.invalid = true
				plan.entry.plugin.Status = statusConnectFailed + "stdio command is required"
				logf("[toolkit] mcp %q: stdio command is required", plan.entry.plugin.Name)
			}
		case mcp.TransportHTTP:
			if plan.entry.plugin.URL == "" {
				// Keep a failed entry so the Plugins screen can show the
				// misconfiguration instead of silently dropping the server.
				plan.invalid = true
				logf("[toolkit] mcp %q: url is required for the http transport", plan.entry.plugin.Name)
			}
		default:
			plan.invalid = true
			// Legacy assembly rows (and unclassified rows) with no URL keep the
			// original missing-URL diagnostic.
			if plan.entry.plugin.URL == "" && strings.TrimSpace(server.Command) == "" {
				plan.entry.plugin.Status = statusConnectFailed + "未配置 URL"
			} else {
				plan.entry.plugin.Status = statusConnectFailed + "unsupported transport"
			}
			logf("[toolkit] mcp %q: unsupported transport %q", plan.entry.plugin.Name, transport)
		}
		plans = append(plans, plan)
	}
	return plans
}

// mcpManagerKey resolves the manager identity of one configured row. Duplicate
// configured names are legal product input (two endpoints sharing a display
// name); the manager requires unique identities, so later duplicates get a
// stable "#n" suffix while the Plugins row keeps the user's name.
func mcpManagerKey(server MCPServer, index int, used map[string]int) string {
	base := mcpBaseName(server, index)
	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	return fmt.Sprintf("%s#%d", base, count+1)
}

// mcpBaseName is the unsuffixed manager identity of one configured row: the
// configured name, or a generated fallback for an unnamed row.
func mcpBaseName(server MCPServer, index int) string {
	if base := strings.TrimSpace(server.Name); base != "" {
		return base
	}
	return fmt.Sprintf("server-%d", index+1)
}
