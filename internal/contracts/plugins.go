package contracts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"

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
// servers and hot-reloads add/edit/remove/enable changes (no restart).
const SettingMCPServers = "mcp.servers"

// KeyPlugins is provided by the toolkit module (which owns the MCP clients)
// and consumed by the Plugins screen.
var KeyPlugins = cordis.NewServiceKey[PluginsService]("zeokeel/plugins")

// MCPReloader rebuilds the MCP connections from the current settings (add,
// edit, remove, enable/disable and workspace opt-in) without restarting the
// app. It returns the fresh read model.
type MCPReloader interface {
	Reload(ctx context.Context) ([]MCPPlugin, error)
}

// KeyMCPReloader is provided by the toolkit and consumed by the connectors tab.
var KeyMCPReloader = cordis.NewServiceKey[MCPReloader]("zeokeel/mcp-reloader")

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
	// TimeoutMs bounds one request/handshake for this server (0 = the toolkit
	// default). A hung endpoint then fails at this budget instead of the 30s
	// rig request default, so it cannot stall product startup.
	TimeoutMs int `json:"timeoutMs,omitempty"`
	// Headers are extra HTTP request headers (assembly-provided or advanced
	// settings); the OAuth bearer header is injected separately.
	Headers map[string]string `json:"headers,omitempty"`
	// Auth configures the HTTP authentication mode. A nil Auth is the
	// historical unauthenticated row.
	Auth *MCPAuthConfig `json:"auth,omitempty"`
}

// MCPAuthMode is the HTTP authentication mode of one connector.
type MCPAuthMode string

const (
	// MCPAuthNone means no authentication (the zero value).
	MCPAuthNone MCPAuthMode = ""
	// MCPAuthBearer sends a static Bearer token from MCPAuthConfig.Token.
	MCPAuthBearer MCPAuthMode = "bearer"
	// MCPAuthOAuth2 runs the OAuth 2.1 authorization-code flow (rig owns the
	// protocol; the toolkit owns the browser/token wiring).
	MCPAuthOAuth2 MCPAuthMode = "oauth2"
)

// MCPAuthConfig is the authentication half of one connector row. OAuth tokens
// are never stored here: they live in a separate mcp.oauth.<server> settings
// row owned by the toolkit's TokenStore.
type MCPAuthConfig struct {
	Mode MCPAuthMode `json:"mode,omitempty"`
	// Token is the static Bearer token (Mode=bearer).
	Token string `json:"token,omitempty"`
	// Scopes optionally overrides the OAuth scopes; empty uses the server's
	// 401 challenge / Protected Resource Metadata.
	Scopes []string `json:"scopes,omitempty"`
	// AuthorizationServer optionally overrides the discovered authorization
	// server identifier.
	AuthorizationServer string `json:"authorizationServer,omitempty"`
	// ClientID is an optional pre-registered OAuth client; empty uses dynamic
	// client registration when the AS supports it.
	ClientID string `json:"clientId,omitempty"`
	// Resource optionally overrides the canonical OAuth resource URI; empty
	// uses the connector URL.
	Resource string `json:"resource,omitempty"`
}

// MCPAuthState is the connector authorization state surfaced to the UI.
type MCPAuthState string

const (
	MCPAuthStateNone        MCPAuthState = "none"
	MCPAuthStateRequired    MCPAuthState = "required"
	MCPAuthStateAuthorizing MCPAuthState = "authorizing"
	MCPAuthStateAuthorized  MCPAuthState = "authorized"
	MCPAuthStateExpired     MCPAuthState = "expired"
	MCPAuthStateError       MCPAuthState = "error"
)

// MCPAuthStatus is a read-only authorization snapshot for one connector.
type MCPAuthStatus struct {
	State     MCPAuthState
	Issuer    string
	Scopes    []string
	ExpiresAt time.Time
	// Code is a stable machine-readable cause (rig OAuthErrorCode when
	// available); Message is diagnostic text the UI shows in details only.
	Code    string
	Message string
}

// MCPAuthFlow is one prepared interactive authorization. The UI opens AuthURL
// on the main thread and then waits for the callback through MCPAuthService.
type MCPAuthFlow struct {
	AuthURL     string
	RedirectURI string
	Issuer      string
	Scopes      []string
	ExpiresAt   time.Time
}

// MCPAuthService owns the product side of MCP OAuth: it builds a rig OAuth
// client per connector, persists tokens in settings, and reconnects a
// connector after a successful authorization. The protocol itself lives in
// rig; this service only adapts it to the product.
type MCPAuthService interface {
	// Status is read-only (no I/O) and may be called from the UI thread.
	Status(cfg MCPServerConfig) MCPAuthStatus
	// Prepare discovers the authorization server, registers a client, creates
	// PKCE state and starts the loopback listener. It performs network I/O and
	// must run off the UI thread.
	Prepare(ctx context.Context, cfg MCPServerConfig) (MCPAuthFlow, error)
	// Wait blocks for the loopback callback, exchanges the code and persists
	// the token; on success it reconnects the connector. Off the UI thread.
	Wait(ctx context.Context, cfg MCPServerConfig, flow MCPAuthFlow) (MCPAuthStatus, error)
	// Cancel drops a pending flow (safe on the UI thread; it only closes local
	// resources).
	Cancel(cfg MCPServerConfig, flow MCPAuthFlow)
	// Refresh refreshes the access token (off the UI thread).
	Refresh(ctx context.Context, cfg MCPServerConfig) (MCPAuthStatus, error)
	// Revoke calls the revocation endpoint when available and deletes the local
	// token (off the UI thread; confirm in the UI first).
	Revoke(ctx context.Context, cfg MCPServerConfig) error
}

// KeyMCPAuth is provided by the toolkit module and consumed by the
// Capabilities screen's connectors tab.
var KeyMCPAuth = cordis.NewServiceKey[MCPAuthService]("zeokeel/mcp/auth")

// MCPAuthSettingPrefix is the settings key prefix of one connector's persisted
// OAuth token record. The suffix is a stable slug + name hash so two servers
// that normalize to the same slug cannot overwrite each other.
const MCPAuthSettingPrefix = "mcp.oauth."

// MCPAuthSettingKey returns the settings key holding one server's OAuth token
// record. The record format is owned by rig (versioned JSON).
func MCPAuthSettingKey(server string) string {
	name := strings.ToLower(strings.TrimSpace(server))
	sum := sha256.Sum256([]byte(name))
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	slug := strings.Trim(builder.String(), "_")
	if slug == "" {
		slug = "server"
	}
	if runes := []rune(slug); len(runes) > 40 {
		slug = string(runes[:40])
	}
	return MCPAuthSettingPrefix + slug + "." + hex.EncodeToString(sum[:4])
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
// not be registered (per-tool switches in the plugins page; the next reload applies).
const SettingMCPDisabledTools = "mcp.disabledTools"

// SettingMCPDisabledServers stores a JSON array of MCP server names that are
// not started (their tools are not registered; the next reload applies).
const SettingMCPDisabledServers = "mcp.disabledServers"

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

// NormalizeMCPURL trims a trailing slash from a connector URL path (the root
// "/" is preserved). OAuth protected-resource metadata commonly advertises
// "https://host/mcp" while users paste "https://host/mcp/"; canonicalizing the
// connector URL avoids a spurious resource_mismatch. Other connectors keep the
// URL exactly as entered.
func NormalizeMCPURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.Path == "" || parsed.Path == "/" {
		return trimmed
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

// NormalizeMCPAuthURL applies NormalizeMCPURL only to OAuth rows.
func NormalizeMCPAuthURL(row MCPServerConfig) MCPServerConfig {
	if row.Auth != nil && row.Auth.Mode == MCPAuthOAuth2 {
		row.URL = NormalizeMCPURL(row.URL)
	}
	return row
}
