package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// MCPPlugin describes one configured MCP server (the old Plugins screen's
// remote plugin entry).
type MCPPlugin struct {
	// Name is the server identity used in tool names (config name).
	Name string
	// URL is the endpoint ("" for stdio transports).
	URL string
	// ServerName/Version/Protocol are reported by the MCP server after the
	// initialize handshake.
	ServerName string
	Version    string
	Protocol   string
	// Tools is how many tools the server exposes.
	Tools int
	// Status is a short human-readable state ("已连接", "连接失败", ...).
	Status string
}

// PluginsService exposes the product's MCP plugin connections to the Plugins
// screen.
type PluginsService interface {
	// MCP returns the configured MCP servers and their last known state.
	MCP() []MCPPlugin
	// Refresh pings every server, refreshes the tool counts and returns the
	// updated list.
	Refresh(ctx context.Context) []MCPPlugin
}

// KeyPlugins is provided by the toolkit module (which owns the MCP clients)
// and consumed by the Plugins screen.
var KeyPlugins = cordis.NewServiceKey[PluginsService]("zeokeel/plugins")
