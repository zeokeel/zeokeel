package toolkit

// MCP stdio support: transport detection, child-process configuration and the
// shared tool/resource registration used by both HTTP and stdio servers.

import (
	"context"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
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

// mcpStartConfig builds the rig MCP client config for one server. The caller
// only uses it for stdio rows.
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

// startMCPClient connects one server over its configured transport. ctx bounds
// the handshake (the MCP startup phase passes one overall deadline).
func startMCPClient(ctx context.Context, server MCPServer, auth *mcpAuthRuntime) (*mcp.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch mcpServerTransport(server) {
	case mcp.TransportStdio:
		return mcp.Start(ctx, mcpStartConfig(server))
	case mcp.TransportHTTP:
		return startHTTPMCP(ctx, server, auth)
	default:
		return nil, fmt.Errorf("mcp: unsupported transport %q", mcpServerTransport(server))
	}
}

// startHTTPMCP is the HTTP half of the startup loop, kept separate so the loop
// reads as transport dispatch.
func startHTTPMCP(ctx context.Context, server MCPServer, auth *mcpAuthRuntime) (*mcp.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	headers := cloneMCPHeaders(server.Headers)
	var oauthClient *mcp.OAuthClient
	if server.Auth != nil {
		switch server.Auth.Mode {
		case contracts.MCPAuthBearer:
			if headers == nil {
				headers = make(map[string]string, 1)
			}
			// OAuth never coexists with this branch; a static token overrides
			// any assembly-provided Authorization header.
			headers["Authorization"] = "Bearer " + strings.TrimSpace(server.Auth.Token)
		case contracts.MCPAuthOAuth2:
			if auth == nil {
				return nil, fmt.Errorf("mcp: OAuth is not available in this composition")
			}
			client, err := auth.clientForServer(server)
			if err != nil {
				return nil, err
			}
			oauthClient = client
		}
	}
	cfg := mcp.HTTPConfig{
		URL:        server.URL,
		Headers:    headers,
		ServerName: server.Name,
		OAuth:      oauthClient,
	}
	if server.TimeoutMs > 0 {
		cfg.Timeout = time.Duration(server.TimeoutMs) * time.Millisecond
	}
	return mcp.StartHTTP(ctx, cfg)
}

// mcpResourceTools adapts the resource/prompt tool subset of one MCP server.
//
// rig v0.24.0 names those five tools "mcp__<server>__<base>" itself (the server
// segment comes from client.ServerName(), sanitized by rig), so the toolkit only
// supplies the "mcp__" namespace prefix. Before v0.24.0 the rig names carried no
// server identity and the toolkit had to add one; passing the server again would
// now produce "mcp__<server>__<server>__<base>".
func mcpResourceTools(client *mcp.Client) []rig.Tool {
	return mcp.ResourceTools(client, mcp.DefaultPrefix)
}
