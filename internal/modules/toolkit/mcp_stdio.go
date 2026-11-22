package toolkit

// MCP transport configuration: transport detection, child-process/HTTP client
// configuration and the OAuth/bearer header wiring. The connections themselves
// are supervised by the Rig SDK tools/mcp/manager.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel-rig/tools/mcp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mcpServerTransport infers the transport from an MCPServer row. A non-empty
// Command (or explicit "stdio") wins over URL, which is the documented
// stdio-priority rule when both are configured.
func mcpServerTransport(server MCPServer) string {
	transport := strings.ToLower(strings.TrimSpace(server.Transport))
	if strings.TrimSpace(server.Command) != "" || transport == mcp.TransportStdio {
		return mcp.TransportStdio
	}
	if strings.TrimSpace(server.URL) != "" || transport == mcp.TransportHTTP {
		return mcp.TransportHTTP
	}
	return transport
}

// mcpServerDedupeKey is the merge identity: URL for HTTP servers (case-folded)
// and command+args for stdio servers.
func mcpServerDedupeKey(server MCPServer) string {
	switch mcpServerTransport(server) {
	case mcp.TransportStdio:
		return "stdio:" + strings.TrimSpace(server.Command) + "\x00" + strings.Join(cleanMCPArgs(server.Args), "\x00")
	case mcp.TransportHTTP:
		return "http:" + dedupeURLKey(server.URL)
	default:
		return ""
	}
}

// mcpStartConfig builds the rig MCP client config for one stdio server.
func mcpStartConfig(server MCPServer) mcp.Config {
	cfg := mcp.Config{
		Transport:  mcp.TransportStdio,
		Command:    strings.TrimSpace(server.Command),
		Args:       cleanMCPArgs(server.Args),
		Env:        cleanMCPArgs(server.Env),
		ServerName: strings.TrimSpace(server.Name),
		ClientName: "ZeoKeel",
	}
	if server.TimeoutMs > 0 {
		cfg.Timeout = time.Duration(server.TimeoutMs) * time.Millisecond
	}
	return cfg
}

// mcpClientConfig builds the rig MCP client config handed to the manager for one
// configured server, including the HTTP bearer/OAuth wiring.
func mcpClientConfig(server MCPServer, auth *mcpAuthRuntime) (mcp.Config, error) {
	switch mcpServerTransport(server) {
	case mcp.TransportStdio:
		return mcpStartConfig(server), nil
	case mcp.TransportHTTP:
		headers := cloneMCPHeaders(server.Headers)
		var oauthClient *mcp.OAuthClient
		if server.Auth != nil {
			switch server.Auth.Mode {
			case contracts.MCPAuthBearer:
				if headers == nil {
					headers = make(map[string]string, 1)
				}
				// OAuth never coexists with this branch; a static token
				// overrides any assembly-provided Authorization header.
				headers["Authorization"] = "Bearer " + strings.TrimSpace(server.Auth.Token)
			case contracts.MCPAuthOAuth2:
				if auth == nil {
					return mcp.Config{}, fmt.Errorf("mcp: OAuth is not available in this composition")
				}
				client, err := auth.clientForServer(server)
				if err != nil {
					return mcp.Config{}, err
				}
				oauthClient = client
			}
		}
		cfg := mcp.Config{
			Transport:  mcp.TransportHTTP,
			URL:        server.URL,
			Headers:    headers,
			ServerName: server.Name,
			ClientName: "ZeoKeel",
			OAuth:      oauthClient,
		}
		if server.TimeoutMs > 0 {
			cfg.Timeout = time.Duration(server.TimeoutMs) * time.Millisecond
		}
		return cfg, nil
	default:
		return mcp.Config{}, fmt.Errorf("mcp: unsupported transport %q", mcpServerTransport(server))
	}
}

// startHTTPMCP connects one HTTP server directly. The manager builds its own
// clients; this helper remains for the OAuth integration tests and any caller
// that needs a single ad-hoc connection.
func startHTTPMCP(ctx context.Context, server MCPServer, auth *mcpAuthRuntime) (*mcp.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := mcpClientConfig(server, auth)
	if err != nil {
		return nil, err
	}
	if cfg.Transport != mcp.TransportHTTP {
		return nil, fmt.Errorf("mcp: server %q is not an HTTP server", server.Name)
	}
	return mcp.Start(ctx, cfg)
}
