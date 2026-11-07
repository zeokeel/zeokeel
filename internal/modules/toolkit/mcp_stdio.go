package toolkit

// MCP stdio support: transport detection, child-process configuration and the
// shared tool/resource registration used by both HTTP and stdio servers.

import (
	"context"
	"strings"
	"time"

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

// startHTTPMCP is the HTTP half of the startup loop, kept separate so the loop
// reads as transport dispatch.
func startHTTPMCP(server MCPServer) (*mcp.Client, error) {
	return mcp.StartHTTP(context.Background(), mcp.HTTPConfig{
		URL:        server.URL,
		Headers:    server.Headers,
		ServerName: server.Name,
		Timeout:    time.Duration(server.TimeoutMs) * time.Millisecond,
	})
}
