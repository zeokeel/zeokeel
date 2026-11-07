package contracts

import (
	"context"
	"encoding/json"
	"strings"

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

// SettingMCPServers stores user-configured MCP servers as a JSON array of
// {"name":"...","url":"..."} rows. The toolkit merges them with the assembly's
// servers on startup (a restart applies changes).
const SettingMCPServers = "mcp.servers"

// KeyPlugins is provided by the toolkit module (which owns the MCP clients)
// and consumed by the Plugins screen.
var KeyPlugins = cordis.NewServiceKey[PluginsService]("zeokeel/plugins")

// MCPServerConfig is one user-configured MCP server row stored in settings.
// Rows with a Command (or Transport "stdio") start a stdio child process; rows
// with a URL use Streamable HTTP. Existing {name,url} JSON rows stay valid.
type MCPServerConfig struct {
	Name      string   `json:"name"`
	URL       string   `json:"url,omitempty"`
	Transport string   `json:"transport,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	Env       []string `json:"env,omitempty"`
}

// ParseMCPServersJSON decodes the settings mcp.servers raw value. An empty
// string is an empty list; malformed JSON returns an error.
func ParseMCPServersJSON(raw string) ([]MCPServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var rows []MCPServerConfig
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// EncodeMCPServersJSON renders rows for the settings mcp.servers value.
func EncodeMCPServersJSON(rows []MCPServerConfig) (string, error) {
	if rows == nil {
		rows = []MCPServerConfig{}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// SettingMCPWorkspaceOptIn enables reading <workspace>/mcp.json for extra MCP
// servers (the old shell's workspace opt-in). Applied on startup.
const SettingMCPWorkspaceOptIn = "mcp.workspaceOptIn"

// SettingMCPDisabledTools stores a JSON array of MCP tool names that should
// not be registered (per-tool switches in the plugins page; restart applies).
const SettingMCPDisabledTools = "mcp.disabledTools"

// SettingToolsDisabled stores a JSON array of any tool names (built-in or MCP)
// that should not be registered (the plugins page's per-tool switches; restart
// applies). It is merged with SettingMCPDisabledTools.
const SettingToolsDisabled = "tools.disabled"

// ParseStringListJSON decodes a JSON array of strings ("[]"/"" -> nil).
func ParseStringListJSON(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

// EncodeStringListJSON renders a string list for a settings row.
func EncodeStringListJSON(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
