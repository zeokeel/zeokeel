package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServerConfigTransports pins the FP-13 config parsing: stdio keeps working
// from a command, http transports need a URL (and reject command lines), and an
// unknown transport is reported instead of being silently treated as stdio.
func TestServerConfigTransports(t *testing.T) {
	cases := []struct {
		name   string
		server ServerConfig
		want   string // "" means valid
	}{
		{name: "stdio default", server: ServerConfig{Name: "s", Command: "/bin/echo"}, want: ""},
		{name: "stdio explicit", server: ServerConfig{Name: "s", Transport: "stdio", Command: "/bin/echo"}, want: ""},
		{name: "http explicit", server: ServerConfig{Name: "r", Transport: "http", URL: "https://example.test/mcp"}, want: ""},
		{name: "streamable-http alias", server: ServerConfig{Name: "r", Transport: "streamable-http", URL: "http://127.0.0.1:1234/mcp"}, want: ""},
		{name: "url implies http", server: ServerConfig{Name: "r", URL: "https://example.test/mcp"}, want: ""},
		{name: "http headers", server: ServerConfig{Name: "r", Transport: "http", URL: "https://example.test/mcp",
			Headers: map[string]string{"Authorization": "Bearer x"}}, want: ""},
		{name: "http missing url", server: ServerConfig{Name: "r", Transport: "http"}, want: "url is required"},
		{name: "http rejects command", server: ServerConfig{Name: "r", Transport: "http", URL: "https://example.test/mcp", Command: "evil"}, want: "command is only valid"},
		{name: "http rejects bad scheme", server: ServerConfig{Name: "r", Transport: "http", URL: "ftp://example.test/mcp"}, want: "invalid url"},
		{name: "stdio missing command", server: ServerConfig{Name: "s"}, want: "command is required"},
		{name: "stdio rejects url", server: ServerConfig{Name: "s", Command: "/bin/echo", URL: "https://example.test/mcp"}, want: "url is only valid"},
		{name: "unknown transport", server: ServerConfig{Name: "s", Transport: "websocket", Command: "/bin/echo"}, want: "unknown transport"},
	}
	for _, tc := range cases {
		server := tc.server
		server.Transport = normalizeTransport(server)
		err := validateServerConfig(server)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: validateServerConfig = %v, want valid", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: validateServerConfig = %v, want error containing %q", tc.name, err, tc.want)
		}
	}
	// IsStreamableHTTP drives the diagnostics branch in the app log.
	if !IsStreamableHTTP(ServerConfig{Name: "r", URL: "https://example.test/mcp"}) {
		t.Error("URL-only config was not recognized as streamable-http")
	}
	if IsStreamableHTTP(ServerConfig{Name: "s", Command: "/bin/echo"}) {
		t.Error("stdio config was reported as streamable-http")
	}
}

// TestManagerConnectsStreamableHTTP is the end-to-end FP-13 test: a real
// streamable-HTTP MCP server is reached over HTTP, its tool is listed, invoked,
// and the configured Authorization header reaches the server.
func TestManagerConnectsStreamableHTTP(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "remote-fake", Version: "0.1.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Echo text."},
		func(_ context.Context, _ *sdk.CallToolRequest, in struct {
			Text string `json:"text,omitempty"`
		}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: "remote: " + in.Text},
			}}, nil, nil
		})
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	manager := NewManager(context.Background())
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, Config{Servers: []ServerConfig{{
		Name:      "remote",
		Transport: TransportStreamableHTTP,
		URL:       srv.URL,
		Headers:   map[string]string{"Authorization": "Bearer test-token"},
	}}}); err != nil {
		t.Fatalf("Connect over streamable-http: %v", err)
	}
	if got := manager.Servers(); len(got) != 1 || got[0] != "remote" {
		t.Fatalf("Servers() = %v, want [remote]", got)
	}
	echo := findTool(t, manager.Tools(), "mcp__remote__echo")
	out, err := echo.Invoke(ctx, json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("remote tool invoke: %v", err)
	}
	if !strings.Contains(out, "remote: hi") {
		t.Fatalf("remote tool result = %q, want it to include the echo", out)
	}
}

// TestManagerStreamableHTTPAuthFailure surfaces a misconfigured header as a
// connect error instead of a silent empty server.
func TestManagerStreamableHTTPAuthFailure(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "remote-fake", Version: "0.1.0"}, nil)
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	manager := NewManager(context.Background())
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, Config{Servers: []ServerConfig{{
		Name: "remote", Transport: TransportStreamableHTTP, URL: srv.URL,
	}}}); err == nil {
		t.Fatal("Connect succeeded against a 401 endpoint")
	}
	_ = mcpHandler
}
