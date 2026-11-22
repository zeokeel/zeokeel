package toolkit

// MCP OAuth adapter: the protocol and refresh runtime live in rig's
// tools/mcp/auth package; this file keeps only the product wiring — the
// settings-backed mcp.TokenStore (stable settings key), the connector config
// normalization and the contracts.MCPAuthService status/flow mapping. The
// manager owns the OAuth clients (Server.OAuth) and the background refresh loop.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel-rig/tools/mcp"
	"github.com/zeokeel/zeokeel-rig/tools/mcp/auth"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mcpOAuthTokenStore persists one connector's versioned token record in the
// settings store. The record format is owned by rig; settings only carries the
// opaque bytes (and the diagnostics/model-facing surfaces redact the key).
type mcpOAuthTokenStore struct {
	settings contracts.SettingsService
	server   string
}

var _ mcp.TokenStore = mcpOAuthTokenStore{}

// serverName prefers the store's configured identity; rig passes the same name,
// but keeping the field makes the key independent of the caller.
func (s mcpOAuthTokenStore) serverName(server string) string {
	if strings.TrimSpace(s.server) != "" {
		return s.server
	}
	return server
}

// Load implements mcp.TokenStore.
func (s mcpOAuthTokenStore) Load(_ context.Context, server string) ([]byte, bool, error) {
	if s.settings == nil {
		return nil, false, errors.New("the settings store is unavailable")
	}
	raw := s.settings.Get(contracts.MCPAuthSettingKey(s.serverName(server)), "")
	if strings.TrimSpace(raw) == "" {
		return nil, false, nil
	}
	return []byte(raw), true, nil
}

// Save implements mcp.TokenStore.
func (s mcpOAuthTokenStore) Save(_ context.Context, server string, data []byte) error {
	if s.settings == nil {
		return errors.New("the settings store is unavailable")
	}
	return s.settings.Set(contracts.MCPAuthSettingKey(s.serverName(server)), string(data))
}

// Delete implements mcp.TokenStore.
func (s mcpOAuthTokenStore) Delete(_ context.Context, server string) error {
	if s.settings == nil {
		return errors.New("the settings store is unavailable")
	}
	return s.settings.Set(contracts.MCPAuthSettingKey(s.serverName(server)), "")
}

// authOAuth2 reports whether the row opts into the OAuth flow.
func authOAuth2(cfg contracts.MCPServerConfig) bool {
	return cfg.Auth != nil && cfg.Auth.Mode == contracts.MCPAuthOAuth2
}

// authResource is the canonical resource URI used for OAuth. The connector URL
// is authoritative: rig enforces URL == OAuthConfig.Resource when the client
// starts, so a separate override could only produce a connection that cannot
// start (the Auth.Resource field is kept for config compatibility but ignored).
func authResource(cfg contracts.MCPServerConfig) string {
	return strings.TrimSpace(cfg.URL)
}

// mcpAuthRuntime is the product adapter around auth.Runtime. It owns no client
// cache or refresh loop of its own: the runtime caches clients, and the manager
// (which receives this runtime through Config.Auth) builds the MCP clients from
// Server.OAuth and runs the background refresh.
type mcpAuthRuntime struct {
	settings contracts.SettingsService
	logf     func(string, ...any)
	runtime  *auth.Runtime
}

var _ contracts.MCPAuthService = (*mcpAuthRuntime)(nil)

// newMCPAuthRuntime creates the adapter for one toolkit activation. onAuthorized
// is invoked asynchronously when an interactive flow completes, so the product
// can reconnect the managed server.
func newMCPAuthRuntime(settings contracts.SettingsService, logf func(string, ...any),
	onAuthorized func(server string)) *mcpAuthRuntime {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &mcpAuthRuntime{
		settings: settings,
		logf:     logf,
		runtime: auth.New(
			auth.WithLogger(logf),
			auth.WithOnAuthorized(onAuthorized),
		),
	}
}

// Runtime returns the shared rig auth runtime handed to manager.Config.Auth.
func (a *mcpAuthRuntime) Runtime() *auth.Runtime {
	if a == nil {
		return nil
	}
	return a.runtime
}

// start launches the runtime's background refresh loop.
func (a *mcpAuthRuntime) start() {
	if a == nil {
		return
	}
	a.runtime.Start()
}

// close stops the loop and releases every cached OAuth client (module unload).
func (a *mcpAuthRuntime) close() {
	if a == nil {
		return
	}
	a.runtime.Stop()
	_ = a.runtime.Close()
}

// authConfig converts one connector config to the rig auth runtime config. The
// settings-backed token store keeps the historical settings key.
func (a *mcpAuthRuntime) authConfig(cfg contracts.MCPServerConfig) (auth.Config, error) {
	if a == nil || a.settings == nil {
		return auth.Config{}, errors.New("the settings store is unavailable; OAuth cannot persist tokens")
	}
	if !authOAuth2(cfg) {
		return auth.Config{}, fmt.Errorf("connector %q is not configured for OAuth", cfg.Name)
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return auth.Config{}, errors.New("the connector name is required for OAuth")
	}
	resource := authResource(cfg)
	if resource == "" {
		return auth.Config{}, fmt.Errorf("connector %q has no URL for OAuth", name)
	}
	oauthConfig := auth.Config{
		ServerName:          name,
		Resource:            resource,
		Scopes:              append([]string(nil), cfg.Auth.Scopes...),
		AuthorizationServer: strings.TrimSpace(cfg.Auth.AuthorizationServer),
		ClientID:            strings.TrimSpace(cfg.Auth.ClientID),
		TokenStore:          mcpOAuthTokenStore{settings: a.settings, server: name},
		Logf:                a.logf,
	}
	if cfg.TimeoutMs > 0 {
		oauthConfig.Timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	return oauthConfig, nil
}

// managerOAuthConfig returns the OAuth configuration to put on manager.Server,
// or nil for a connector that does not use OAuth.
func (a *mcpAuthRuntime) managerOAuthConfig(server MCPServer) (*auth.Config, error) {
	if !authOAuth2(server.config()) {
		return nil, nil
	}
	oauthConfig, err := a.authConfig(server.config())
	if err != nil {
		return nil, err
	}
	return &oauthConfig, nil
}

// authStatusFromRig folds rig's status onto the product's UI vocabulary.
func authStatusFromRig(status mcp.OAuthStatus) contracts.MCPAuthStatus {
	out := contracts.MCPAuthStatus{
		Issuer:    status.Issuer,
		Scopes:    append([]string(nil), status.Scopes...),
		ExpiresAt: status.ExpiresAt,
	}
	switch status.State {
	case mcp.OAuthStateRequired:
		out.State = contracts.MCPAuthStateRequired
	case mcp.OAuthStateAuthorizing:
		out.State = contracts.MCPAuthStateAuthorizing
	case mcp.OAuthStateAuthorized:
		out.State = contracts.MCPAuthStateAuthorized
	case mcp.OAuthStateExpired:
		out.State = contracts.MCPAuthStateExpired
	case mcp.OAuthStateError:
		out.State = contracts.MCPAuthStateError
	default:
		out.State = contracts.MCPAuthStateNone
	}
	if status.Err != nil {
		out.Code = string(status.Err.Code)
		out.Message = status.Err.Message
	}
	return out
}

// failureStatus maps a failed MCP connection to a product status line: an
// OAuth connector that needs (re)authorization says so instead of showing a
// generic connection error.
func (a *mcpAuthRuntime) failureStatus(server MCPServer) string {
	if a == nil || !authOAuth2(server.config()) {
		return ""
	}
	status := a.Status(server.config())
	switch status.State {
	case contracts.MCPAuthStateRequired, contracts.MCPAuthStateExpired, contracts.MCPAuthStateAuthorizing:
		return statusAuthRequired
	default:
		return ""
	}
}

// Status implements contracts.MCPAuthService. It is a pure read of the runtime's
// cached client (no discovery and no token I/O).
func (a *mcpAuthRuntime) Status(cfg contracts.MCPServerConfig) contracts.MCPAuthStatus {
	if a == nil || a.runtime == nil {
		return contracts.MCPAuthStatus{State: contracts.MCPAuthStateNone}
	}
	oauthConfig, err := a.authConfig(cfg)
	if err != nil {
		return contracts.MCPAuthStatus{State: contracts.MCPAuthStateNone}
	}
	return authStatusFromRig(a.runtime.Status(oauthConfig))
}

// Prepare implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Prepare(ctx context.Context, cfg contracts.MCPServerConfig) (contracts.MCPAuthFlow, error) {
	oauthConfig, err := a.authConfig(cfg)
	if err != nil {
		return contracts.MCPAuthFlow{}, err
	}
	flow, err := a.runtime.Prepare(ctx, oauthConfig)
	if err != nil {
		return contracts.MCPAuthFlow{}, err
	}
	return contracts.MCPAuthFlow{
		AuthURL:     flow.AuthURL,
		RedirectURI: flow.RedirectURI,
		Issuer:      flow.Issuer,
		Scopes:      append([]string(nil), flow.Scopes...),
		ExpiresAt:   flow.ExpiresAt,
	}, nil
}

// Wait implements contracts.MCPAuthService. The runtime invokes the authorized
// callback (reconnect) itself after a successful flow.
func (a *mcpAuthRuntime) Wait(ctx context.Context, cfg contracts.MCPServerConfig, flow contracts.MCPAuthFlow) (contracts.MCPAuthStatus, error) {
	oauthConfig, err := a.authConfig(cfg)
	if err != nil {
		return contracts.MCPAuthStatus{}, err
	}
	status, err := a.runtime.Wait(ctx, oauthConfig, mcp.OAuthFlow{
		AuthURL:     flow.AuthURL,
		RedirectURI: flow.RedirectURI,
		Issuer:      flow.Issuer,
		Scopes:      append([]string(nil), flow.Scopes...),
		ExpiresAt:   flow.ExpiresAt,
	})
	return authStatusFromRig(status), err
}

// Cancel implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Cancel(cfg contracts.MCPServerConfig, flow contracts.MCPAuthFlow) {
	oauthConfig, err := a.authConfig(cfg)
	if err != nil {
		return
	}
	a.runtime.Cancel(oauthConfig, mcp.OAuthFlow{
		AuthURL:     flow.AuthURL,
		RedirectURI: flow.RedirectURI,
		Issuer:      flow.Issuer,
		Scopes:      append([]string(nil), flow.Scopes...),
		ExpiresAt:   flow.ExpiresAt,
	})
}

// Revoke implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Revoke(ctx context.Context, cfg contracts.MCPServerConfig) error {
	oauthConfig, err := a.authConfig(cfg)
	if err != nil {
		return err
	}
	return a.runtime.Revoke(ctx, oauthConfig)
}

// config converts one merged server row back to the contract config used by
// the UI-facing OAuth service.
func (s MCPServer) config() contracts.MCPServerConfig {
	return contracts.NormalizeMCPAuthURL(contracts.MCPServerConfig{
		Name:      s.Name,
		URL:       s.URL,
		Transport: s.Transport,
		Command:   s.Command,
		Args:      append([]string(nil), s.Args...),
		Env:       append([]string(nil), s.Env...),
		TimeoutMs: s.TimeoutMs,
		Headers:   s.Headers,
		Auth:      s.Auth,
	})
}
