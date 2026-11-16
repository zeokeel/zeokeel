package toolkit

// End-to-end product wiring test for MCP OAuth 2.1: a fake authorization
// server + a fake MCP resource server drive the real rig OAuth client through
// the toolkit adapter (settings TokenStore, Prepare/Wait, token persistence)
// and then start the MCP client with the persisted token.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type oauthFakeAS struct {
	t            *testing.T
	base         string
	server       *httptest.Server
	mu           sync.Mutex
	challenge    string
	refreshCalls int
}

func newOAuthFakeAS(t *testing.T) *oauthFakeAS {
	t.Helper()
	as := &oauthFakeAS{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeToolkitJSON(t, w, map[string]any{
			"issuer":                                         as.base,
			"authorization_endpoint":                         as.base + "/authorize",
			"token_endpoint":                                 as.base + "/token",
			"registration_endpoint":                          as.base + "/register",
			"revocation_endpoint":                            as.base + "/revoke",
			"scopes_supported":                               []string{"files:read"},
			"code_challenge_methods_supported":               []string{"S256"},
			"authorization_response_iss_parameter_supported": true,
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		writeToolkitJSON(t, w, map[string]any{"client_id": "dcr-client"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		as.mu.Lock()
		as.challenge = query.Get("code_challenge")
		as.mu.Unlock()
		target, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad redirect", http.StatusBadRequest)
			return
		}
		values := target.Query()
		values.Set("code", "auth-code")
		values.Set("state", query.Get("state"))
		values.Set("iss", as.base)
		target.RawQuery = values.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("grant_type") == "refresh_token" {
			as.mu.Lock()
			as.refreshCalls++
			as.mu.Unlock()
			if r.PostForm.Get("refresh_token") != "refresh-1" {
				w.WriteHeader(http.StatusBadRequest)
				writeToolkitJSON(t, w, map[string]any{"error": "invalid_grant"})
				return
			}
			writeToolkitJSON(t, w, map[string]any{
				"access_token": "access-2", "token_type": "Bearer",
				"expires_in": 3600, "refresh_token": "refresh-2", "scope": "files:read",
			})
			return
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			writeToolkitJSON(t, w, map[string]any{"error": "unsupported_grant_type"})
			return
		}
		as.mu.Lock()
		expected := as.challenge
		as.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if expected == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != expected {
			w.WriteHeader(http.StatusBadRequest)
			writeToolkitJSON(t, w, map[string]any{"error": "invalid_grant"})
			return
		}
		writeToolkitJSON(t, w, map[string]any{
			"access_token": "access-1", "token_type": "Bearer",
			"expires_in": 3600, "refresh_token": "refresh-1", "scope": "files:read",
		})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	as.server = httptest.NewServer(mux)
	as.base = as.server.URL
	t.Cleanup(as.server.Close)
	return as
}

type oauthFakeMCP struct {
	t        *testing.T
	base     string
	asURL    string
	server   *httptest.Server
	mu       sync.Mutex
	seenAuth []string
}

func newOAuthFakeMCP(t *testing.T, asURL string) *oauthFakeMCP {
	t.Helper()
	mcpServer := &oauthFakeMCP{t: t, asURL: asURL}
	mcpServer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-protected-resource":
			writeToolkitJSON(t, w, map[string]any{
				"resource":              mcpServer.base,
				"authorization_servers": []string{mcpServer.asURL},
				"scopes_supported":      []string{"files:read"},
			})
			return
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
			return
		case r.Method != http.MethodPost:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		authHeader := r.Header.Get("Authorization")
		mcpServer.mu.Lock()
		mcpServer.seenAuth = append(mcpServer.seenAuth, authHeader)
		mcpServer.mu.Unlock()
		if authHeader != "Bearer access-1" && authHeader != "Bearer access-2" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource", scope="files:read"`, mcpServer.base))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		switch message.Method {
		case "initialize":
			writeToolkitJSON(t, w, map[string]any{
				"jsonrpc": "2.0", "id": message.ID,
				"result": map[string]any{
					"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
					"serverInfo": map[string]any{"name": "fake", "version": "1.0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeToolkitJSON(t, w, map[string]any{
				"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"tools": []any{}},
			})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	mcpServer.base = mcpServer.server.URL
	t.Cleanup(mcpServer.server.Close)
	return mcpServer
}

func (m *oauthFakeMCP) authorizations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.seenAuth...)
}

func writeToolkitJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode JSON: %v", err)
	}
}

func TestMCPAuthRuntimeAuthorizesPersistsAndConnects(t *testing.T) {
	as := newOAuthFakeAS(t)
	resource := newOAuthFakeMCP(t, as.server.URL)
	settings := newAuthTestSettings()
	runtime := newMCPAuthRuntime(settings, t.Logf)
	defer runtime.close()

	authorized := make(chan string, 1)
	runtime.onAuthorized = func(name string) { authorized <- name }

	cfg := contracts.MCPServerConfig{
		Name: "files",
		URL:  resource.server.URL,
		Auth: &contracts.MCPAuthConfig{Mode: contracts.MCPAuthOAuth2},
	}
	flow, err := runtime.Prepare(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.Contains(flow.AuthURL, "code_challenge_method=S256") {
		t.Fatalf("authorization URL lacks S256: %s", flow.AuthURL)
	}

	// Simulate the browser: follow the authorization redirect into the loopback
	// listener that Prepare opened.
	response, err := http.Get(flow.AuthURL)
	if err != nil {
		t.Fatalf("browser GET: %v", err)
	}
	_ = response.Body.Close()

	status, err := runtime.Wait(context.Background(), cfg, flow)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if status.State != contracts.MCPAuthStateAuthorized {
		t.Fatalf("status = %+v, want authorized", status)
	}
	select {
	case name := <-authorized:
		if name != cfg.Name {
			t.Fatalf("onAuthorized(%q), want %q", name, cfg.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authorization did not trigger reconnect")
	}
	if raw := settings.Get(contracts.MCPAuthSettingKey(cfg.Name), ""); strings.TrimSpace(raw) == "" {
		t.Fatal("token record was not persisted in settings")
	}

	// The persisted token now lets the real MCP client connect through the same
	// toolkit start path (OAuth client injected into rig).
	client, err := startHTTPMCP(context.Background(), MCPServer{
		Name: cfg.Name, URL: cfg.URL, Auth: cfg.Auth,
	}, runtime)
	if err != nil {
		t.Fatalf("startHTTPMCP with saved token: %v", err)
	}
	_ = client.Close()
	if got := resource.authorizations(); len(got) == 0 || got[len(got)-1] != "Bearer access-1" {
		t.Fatalf("MCP authorizations = %v, want Bearer access-1", got)
	}
}

func TestMCPAuthRefreshPersistsRotatedTokens(t *testing.T) {
	as := newOAuthFakeAS(t)
	resource := newOAuthFakeMCP(t, as.server.URL)
	settings := newAuthTestSettings()
	runtime := newMCPAuthRuntime(settings, t.Logf)
	defer runtime.close()

	cfg := contracts.MCPServerConfig{
		Name: "files",
		URL:  resource.server.URL,
		Auth: &contracts.MCPAuthConfig{Mode: contracts.MCPAuthOAuth2},
	}
	flow, err := runtime.Prepare(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	response, err := http.Get(flow.AuthURL)
	if err != nil {
		t.Fatalf("browser GET: %v", err)
	}
	_ = response.Body.Close()
	if _, err := runtime.Wait(context.Background(), cfg, flow); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Force a refresh (the transport also does this automatically on near-expiry
	// and on a 401); the rotated refresh token must be persisted.
	status, err := runtime.Refresh(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if status.State != contracts.MCPAuthStateAuthorized {
		t.Fatalf("status after refresh = %+v, want authorized", status)
	}
	if status.ExpiresAt.IsZero() {
		t.Fatal("refresh did not report a new expiry")
	}
	record := settings.Get(contracts.MCPAuthSettingKey(cfg.Name), "")
	if !strings.Contains(record, `"accessToken":"access-2"`) {
		t.Fatalf("token record has no rotated access token: %s", record)
	}
	if !strings.Contains(record, `"refreshToken":"refresh-2"`) {
		t.Fatalf("token record has no rotated refresh token: %s", record)
	}
	as.mu.Lock()
	refreshCalls := as.refreshCalls
	as.mu.Unlock()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}

	client, err := startHTTPMCP(context.Background(), MCPServer{
		Name: cfg.Name, URL: cfg.URL, Auth: cfg.Auth,
	}, runtime)
	if err != nil {
		t.Fatalf("startHTTPMCP after refresh: %v", err)
	}
	_ = client.Close()
	if got := resource.authorizations(); len(got) == 0 || got[len(got)-1] != "Bearer access-2" {
		t.Fatalf("MCP authorizations = %v, want the rotated access token", got)
	}
}
