package toolkit

// MCP OAuth 2.1 adapter: the protocol lives in rig's tools/mcp package; this
// file only wires it to the product (settings-backed TokenStore, the connector
// config model, status mapping and reconnect-after-authorization).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel-rig/tools/mcp"

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

// mcpAuthClientEntry is one cached rig OAuth client plus the configuration it
// was built from (a changed URL/scope/client id rebuilds it).
type mcpAuthClientEntry struct {
	client      *mcp.OAuthClient
	fingerprint string
}

// mcpAuthRuntime implements contracts.MCPAuthService and builds the OAuth
// clients used by startHTTPMCP.
type mcpAuthRuntime struct {
	settings contracts.SettingsService
	logf     func(string, ...any)

	mu      sync.Mutex
	clients map[string]*mcpAuthClientEntry
	// onAuthorized reconnects the managed connection after a completed flow.
	onAuthorized func(server string)
}

var _ contracts.MCPAuthService = (*mcpAuthRuntime)(nil)

// newMCPAuthRuntime creates the adapter for one toolkit activation.
func newMCPAuthRuntime(settings contracts.SettingsService, logf func(string, ...any)) *mcpAuthRuntime {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &mcpAuthRuntime{settings: settings, logf: logf, clients: make(map[string]*mcpAuthClientEntry)}
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

// authFingerprint changes whenever a cached client must be rebuilt.
func authFingerprint(cfg contracts.MCPServerConfig) string {
	if cfg.Auth == nil {
		return cfg.Name + "\x00" + cfg.URL
	}
	return strings.Join([]string{
		cfg.Name,
		cfg.URL,
		authResource(cfg),
		cfg.Auth.ClientID,
		cfg.Auth.AuthorizationServer,
		strings.Join(cfg.Auth.Scopes, "\x00"),
	}, "\x01")
}

// clientForServer returns (building when needed) the rig OAuth client of one
// configured server.
func (a *mcpAuthRuntime) clientForServer(server MCPServer) (*mcp.OAuthClient, error) {
	return a.clientForConfig(server.config())
}

// clientForConfig returns (building when needed) the rig OAuth client for one
// connector config.
func (a *mcpAuthRuntime) clientForConfig(cfg contracts.MCPServerConfig) (*mcp.OAuthClient, error) {
	if a == nil || a.settings == nil {
		return nil, errors.New("the settings store is unavailable; OAuth cannot persist tokens")
	}
	if !authOAuth2(cfg) {
		return nil, fmt.Errorf("connector %q is not configured for OAuth", cfg.Name)
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, errors.New("the connector name is required for OAuth")
	}
	resource := authResource(cfg)
	if resource == "" {
		return nil, fmt.Errorf("connector %q has no URL for OAuth", name)
	}
	fingerprint := authFingerprint(cfg)
	a.mu.Lock()
	existing := a.clients[name]
	a.mu.Unlock()
	if existing != nil && existing.fingerprint == fingerprint {
		return existing.client, nil
	}

	oauthConfig := mcp.OAuthConfig{
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
	created, err := mcp.NewOAuthClient(oauthConfig)
	if err != nil {
		return nil, fmt.Errorf("connector %q OAuth: %w", name, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if current := a.clients[name]; current != nil && current.fingerprint == fingerprint {
		_ = created.Close()
		return current.client, nil
	}
	old := a.clients[name]
	a.clients[name] = &mcpAuthClientEntry{client: created, fingerprint: fingerprint}
	if old != nil {
		_ = old.client.Close()
	}
	return created, nil
}

// existingClient returns a cached client without building one (Cancel/cleanup).
func (a *mcpAuthRuntime) existingClient(server string) *mcp.OAuthClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.clients[strings.TrimSpace(server)]
	if entry == nil {
		return nil
	}
	return entry.client
}

// close releases every cached client (module unload).
func (a *mcpAuthRuntime) close() {
	a.mu.Lock()
	clients := a.clients
	a.clients = make(map[string]*mcpAuthClientEntry)
	a.mu.Unlock()
	for _, entry := range clients {
		if entry != nil && entry.client != nil {
			_ = entry.client.Close()
		}
	}
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

// Status implements contracts.MCPAuthService. It is a pure read: it only
// reports the cached client (built at activation or by Prepare/Wait). Building
// or replacing a client here would close a live connection and violate the
// UI-thread contract.
func (a *mcpAuthRuntime) Status(cfg contracts.MCPServerConfig) contracts.MCPAuthStatus {
	if a == nil {
		return contracts.MCPAuthStatus{State: contracts.MCPAuthStateNone}
	}
	name := strings.TrimSpace(cfg.Name)
	a.mu.Lock()
	entry := a.clients[name]
	a.mu.Unlock()
	if entry == nil || entry.fingerprint != authFingerprint(cfg) {
		return contracts.MCPAuthStatus{State: contracts.MCPAuthStateNone}
	}
	return authStatusFromRig(entry.client.Status())
}

// Prepare implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Prepare(ctx context.Context, cfg contracts.MCPServerConfig) (contracts.MCPAuthFlow, error) {
	client, err := a.clientForConfig(cfg)
	if err != nil {
		return contracts.MCPAuthFlow{}, err
	}
	flow, err := client.Prepare(ctx)
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

// Wait implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Wait(ctx context.Context, cfg contracts.MCPServerConfig, flow contracts.MCPAuthFlow) (contracts.MCPAuthStatus, error) {
	client, err := a.clientForConfig(cfg)
	if err != nil {
		return contracts.MCPAuthStatus{}, err
	}
	status, err := client.Wait(ctx, mcp.OAuthFlow{
		AuthURL:     flow.AuthURL,
		RedirectURI: flow.RedirectURI,
		Issuer:      flow.Issuer,
		Scopes:      append([]string(nil), flow.Scopes...),
		ExpiresAt:   flow.ExpiresAt,
	})
	if err != nil {
		return authStatusFromRig(status), err
	}
	if a.onAuthorized != nil {
		name := cfg.Name
		go a.onAuthorized(name)
	}
	return authStatusFromRig(status), nil
}

// Cancel implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Cancel(cfg contracts.MCPServerConfig, flow contracts.MCPAuthFlow) {
	client := a.existingClient(cfg.Name)
	if client == nil {
		return
	}
	client.Cancel(mcp.OAuthFlow{
		AuthURL:     flow.AuthURL,
		RedirectURI: flow.RedirectURI,
		Issuer:      flow.Issuer,
		Scopes:      append([]string(nil), flow.Scopes...),
		ExpiresAt:   flow.ExpiresAt,
	})
}

// Refresh implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Refresh(ctx context.Context, cfg contracts.MCPServerConfig) (contracts.MCPAuthStatus, error) {
	client, err := a.clientForConfig(cfg)
	if err != nil {
		return contracts.MCPAuthStatus{}, err
	}
	status, err := client.Refresh(ctx)
	return authStatusFromRig(status), err
}

// Revoke implements contracts.MCPAuthService.
func (a *mcpAuthRuntime) Revoke(ctx context.Context, cfg contracts.MCPServerConfig) error {
	client, err := a.clientForConfig(cfg)
	if err != nil {
		return err
	}
	return client.Revoke(ctx)
}

// config converts one merged server row back to the contract config used by
// the UI-facing OAuth service.
func (s MCPServer) config() contracts.MCPServerConfig {
	return contracts.MCPServerConfig{
		Name:      s.Name,
		URL:       s.URL,
		Transport: s.Transport,
		Command:   s.Command,
		Args:      append([]string(nil), s.Args...),
		Env:       append([]string(nil), s.Env...),
		TimeoutMs: s.TimeoutMs,
		Headers:   s.Headers,
		Auth:      s.Auth,
	}
}
