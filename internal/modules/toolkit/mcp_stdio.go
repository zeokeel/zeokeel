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
func startMCPClient(ctx context.Context, server MCPServer) (*mcp.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch mcpServerTransport(server) {
	case mcp.TransportStdio:
		return mcp.Start(ctx, mcpStartConfig(server))
	case mcp.TransportHTTP:
		return startHTTPMCP(ctx, server)
	default:
		return nil, fmt.Errorf("mcp: unsupported transport %q", mcpServerTransport(server))
	}
}

// startHTTPMCP is the HTTP half of the startup loop, kept separate so the loop
// reads as transport dispatch.
func startHTTPMCP(ctx context.Context, server MCPServer) (*mcp.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := mcp.HTTPConfig{
		URL:        server.URL,
		Headers:    server.Headers,
		ServerName: server.Name,
	}
	if server.TimeoutMs > 0 {
		cfg.Timeout = time.Duration(server.TimeoutMs) * time.Millisecond
	}
	return mcp.StartHTTP(ctx, cfg)
}

// mcpResourceTools adapts the resource/prompt tool subset of one MCP server.
// rig names those five tools without any server identity, so a second server
// would register the same names and panic the tool registry; the toolkit gives
// every server its own "mcp__<server>__" prefix instead.
func mcpResourceTools(client *mcp.Client) []rig.Tool {
	serverName := ""
	if client != nil {
		serverName = client.ServerName()
	}
	return mcp.ResourceTools(client, "mcp__"+sanitizeMCPName(serverName)+"__")
}

// sanitizeMCPName maps an MCP server name onto the model-visible tool namespace
// characters ([A-Za-z0-9_-]). It mirrors rig's tools/mcp sanitizeName so the
// resource tools share the exact namespace of the tools/list tools
// ("mcp__<server>__<tool>").
func sanitizeMCPName(name string) string {
	var builder strings.Builder
	builder.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	out := strings.Trim(builder.String(), "_")
	if out == "" {
		return "tool"
	}
	return out
}
