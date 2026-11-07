package toolkit

// User-configured MCP servers (settings mcp.servers) are merged with the
// assembly's mcpServers at activation. The assembly list wins on a URL
// conflict: it is the operator/product policy, and the Plugins screen already
// shows those servers; the user setting only adds endpoints.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// dedupeURLKey is the deduplication key for an MCP endpoint: the trimmed URL
// lowercased, so URLs that differ only by case are one endpoint. The stored
// server keeps its original spelling.
func dedupeURLKey(rawURL string) string {
	return strings.ToLower(strings.TrimSpace(rawURL))
}

// mergeMCPServers returns the assembly servers plus the valid settings-defined
// servers, deduplicated by URL regardless of case (first occurrence wins, so the
// assembly entry is kept when a user row repeats its URL) or by stdio
// command+args. Invalid JSON, invalid rows and duplicate entries are logged and
// skipped; they never fail activation. Explicitly malformed stdio/http settings
// rows keep a failure entry so the Plugins screen can show the misconfiguration.
func mergeMCPServers(assembly []MCPServer, raw string, logf func(format string, args ...any)) []MCPServer {
	merged := append([]MCPServer(nil), assembly...)
	if strings.TrimSpace(raw) == "" {
		return merged
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	rows, err := contracts.ParseMCPServersJSON(raw)
	if err != nil {
		logf("[toolkit] parse %s: %v", contracts.SettingMCPServers, err)
		return merged
	}
	return mergeMCPServerRows(merged, rows, contracts.SettingMCPServers, true, logf)
}

// mergeMCPServerRows merges parsed rows after base. keepFailures controls whether
// explicitly malformed stdio/http rows become diagnostic plugin entries (settings)
// or are only warned and skipped (workspace mcp.json).
func mergeMCPServerRows(base []MCPServer, rows []contracts.MCPServerConfig, source string, keepFailures bool,
	logf func(format string, args ...any)) []MCPServer {
	merged := append([]MCPServer(nil), base...)
	if logf == nil {
		logf = func(string, ...any) {}
	}
	seen := make(map[string]bool, len(merged)+len(rows))
	for _, server := range merged {
		if key := mcpServerDedupeKey(server); key != "" {
			seen[key] = true
		}
	}
	for index, row := range rows {
		name := strings.TrimSpace(row.Name)
		transport := strings.ToLower(strings.TrimSpace(row.Transport))
		command := strings.TrimSpace(row.Command)
		url := strings.TrimSpace(row.URL)
		if name == "" || strings.ContainsRune(name, 0) {
			logf("[toolkit] skip %s[%d]: name is required and must not contain NUL", source, index)
			continue
		}
		if strings.ContainsRune(command, 0) || strings.ContainsRune(url, 0) {
			logf("[toolkit] skip %s[%d]: command/URL must not contain NUL", source, index)
			continue
		}
		server := MCPServer{
			Name:      name,
			URL:       url,
			Transport: transport,
			Command:   command,
			Args:      cleanMCPArgs(row.Args),
			Env:       cleanMCPArgs(row.Env),
		}
		switch {
		case command != "" || transport == "stdio":
			if command == "" {
				if !keepFailures {
					logf("[toolkit] skip %s[%d]: stdio command is required", source, index)
					continue
				}
				// Explicit stdio row with no command: keep a failed diagnostic
				// entry instead of silently dropping the user's configuration.
				server.URL = ""
				merged = append(merged, server)
				continue
			}
		case url != "" || transport == "http":
			if url == "" {
				if !keepFailures {
					logf("[toolkit] skip %s[%d]: HTTP URL is required", source, index)
					continue
				}
				server.Transport = "http"
				merged = append(merged, server)
				continue
			}
		default:
			logf("[toolkit] skip %s[%d]: name and URL/command are required", source, index)
			continue
		}
		key := mcpServerDedupeKey(server)
		if key != "" && seen[key] {
			winner := "earlier source wins"
			if source == contracts.SettingMCPServers {
				winner = "assembly wins"
			}
			if mcpServerTransport(server) == "http" {
				logf("[toolkit] skip %s[%d]: duplicate URL %q (%s)", source, index, server.URL, winner)
			} else {
				logf("[toolkit] skip %s[%d]: duplicate stdio MCP server (%s)", source, index, winner)
			}
			continue
		}
		seen[key] = true
		if mcpServerTransport(server) == "stdio" {
			server.URL = ""
		}
		merged = append(merged, server)
	}
	return merged
}

// cleanMCPArgs trims whitespace and drops empty strings, returning nil for an
// empty input so stdio env inheritance and argument lists stay exact.
func cleanMCPArgs(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
