package toolkit

// Tests for the MCP OAuth adapter's product-side wiring: settings merge
// (headers/auth copied, workspace OAuth rejected), token-store persistence and
// the read-model mapping.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel-rig/tools/mcp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type authTestSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newAuthTestSettings() *authTestSettings {
	return &authTestSettings{values: make(map[string]string)}
}

func (s *authTestSettings) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

func (s *authTestSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *authTestSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func TestMergeMCPServersKeepsAuthAndHeaders(t *testing.T) {
	raw := `[{"name":"notion","url":"https://mcp.example.com/mcp",` +
		`"headers":{"X-Gateway":"edge"},` +
		`"auth":{"mode":"oauth2","scopes":["files:read"],"clientId":"pre-client"}}]`
	merged := mergeMCPServers(nil, raw, nil)
	if len(merged) != 1 {
		t.Fatalf("merged = %+v, want one row", merged)
	}
	server := merged[0]
	if server.Headers["X-Gateway"] != "edge" {
		t.Fatalf("headers = %v", server.Headers)
	}
	if server.Auth == nil || server.Auth.Mode != contracts.MCPAuthOAuth2 {
		t.Fatalf("auth = %+v", server.Auth)
	}
	if server.Auth.ClientID != "pre-client" || len(server.Auth.Scopes) != 1 || server.Auth.Scopes[0] != "files:read" {
		t.Fatalf("auth = %+v", server.Auth)
	}
}

func TestMergeMCPServersDropsBlankBearer(t *testing.T) {
	raw := `[{"name":"files","url":"https://mcp.example.com/mcp","auth":{"mode":"bearer"}}]`
	merged := mergeMCPServers(nil, raw, nil)
	if len(merged) != 1 || merged[0].Auth != nil {
		t.Fatalf("blank bearer = %+v, want no auth", merged)
	}
}

func TestWorkspaceMCPJSONCannotStartOAuth(t *testing.T) {
	var warnings []string
	logf := func(format string, args ...any) {
		warnings = append(warnings, format)
	}
	rows := []contracts.MCPServerConfig{{
		Name: "repo", URL: "https://mcp.example.com/mcp",
		Auth: &contracts.MCPAuthConfig{Mode: contracts.MCPAuthOAuth2},
	}}
	merged := mergeMCPServerRows(nil, rows, "workspace mcp.json", false, logf)
	if len(merged) != 1 {
		t.Fatalf("merged = %+v, want one row", merged)
	}
	if merged[0].Auth != nil {
		t.Fatalf("workspace OAuth was not stripped: %+v", merged[0].Auth)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "OAuth") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestMCPAuthSettingKeyStableAndDistinct(t *testing.T) {
	first := contracts.MCPAuthSettingKey("Notion MCP")
	second := contracts.MCPAuthSettingKey("notion mcp")
	if first != second {
		t.Fatalf("key is case-sensitive: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, contracts.MCPAuthSettingPrefix) {
		t.Fatalf("key %q lacks prefix", first)
	}
	if other := contracts.MCPAuthSettingKey("notion-mcp"); other == first {
		t.Fatalf("different names collided on %q", first)
	}
}

func TestMCPOAuthTokenStoreUsesSettings(t *testing.T) {
	settings := newAuthTestSettings()
	store := mcpOAuthTokenStore{settings: settings, server: "notion"}
	data := []byte(`{"version":1}`)
	if err := store.Save(context.Background(), "ignored", data); err != nil {
		t.Fatalf("Save: %v", err)
	}
	key := contracts.MCPAuthSettingKey("notion")
	if got := settings.Get(key, ""); string(got) != string(data) {
		t.Fatalf("settings[%s] = %q", key, got)
	}
	loaded, found, err := store.Load(context.Background(), "ignored")
	if err != nil || !found || string(loaded) != string(data) {
		t.Fatalf("Load = %q found=%v err=%v", loaded, found, err)
	}
	if err := store.Delete(context.Background(), "ignored"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := store.Load(context.Background(), "ignored"); found {
		t.Fatal("token was not deleted")
	}
}

func TestMCPAuthStatusMapping(t *testing.T) {
	status := authStatusFromRig(mcp.OAuthStatus{
		State:  mcp.OAuthStateExpired,
		Issuer: "https://auth.example.com",
		Err:    &mcp.OAuthError{Code: mcp.OAuthErrRefresh, Message: "invalid_grant"},
	})
	if status.State != contracts.MCPAuthStateExpired {
		t.Fatalf("state = %q", status.State)
	}
	if status.Code != string(mcp.OAuthErrRefresh) || status.Issuer != "https://auth.example.com" {
		t.Fatalf("status = %+v", status)
	}
	// Ensure the adapter's fingerprint changes when the server moves.
	base := contracts.MCPServerConfig{Name: "files", URL: "https://a.example/mcp",
		Auth: &contracts.MCPAuthConfig{Mode: contracts.MCPAuthOAuth2}}
	if authFingerprint(base) == authFingerprint(contracts.MCPServerConfig{
		Name: "files", URL: "https://b.example/mcp",
		Auth: &contracts.MCPAuthConfig{Mode: contracts.MCPAuthOAuth2},
	}) {
		t.Fatal("fingerprint ignores the URL")
	}
	// The config conversion keeps the auth pointer (startup path).
	server := MCPServer{Name: "files", URL: "https://a.example/mcp", Headers: map[string]string{"X": "1"}, Auth: base.Auth}
	converted := server.config()
	if converted.Auth != base.Auth || converted.Headers["X"] != "1" {
		t.Fatalf("config = %+v", converted)
	}
	_, _ = json.Marshal(converted)
}
