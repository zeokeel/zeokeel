package toolkit

// User-configured MCP servers (settings mcp.servers) are merged with the
// assembly's mcpServers at activation. The assembly list wins on a URL
// conflict: it is the operator/product policy, and the Plugins screen already
// shows those servers; the user setting only adds endpoints.

import (
	"encoding/json"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// dedupeURLKey is the deduplication key for an MCP endpoint: the trimmed URL
// lowercased, so URLs that differ only by case are one endpoint. The stored
// server keeps its original spelling.
func dedupeURLKey(rawURL string) string {
	return strings.ToLower(strings.TrimSpace(rawURL))
}

// mcpServerSetting is one row of the JSON stored in contracts.SettingMCPServers.
type mcpServerSetting struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// mergeMCPServers returns the assembly servers plus the valid settings-defined
// servers, deduplicated by URL regardless of case (first occurrence wins, so the
// assembly entry is kept when a user row repeats its URL). Scheme and host are
// case-insensitive in URLs, and users type endpoints in mixed case; the dedupe
// key is the trimmed, lowercased URL so the same endpoint is registered once.
// Invalid JSON, invalid rows and duplicate URLs are logged and skipped; they
// never fail activation.
func mergeMCPServers(assembly []MCPServer, raw string, logf func(format string, args ...any)) []MCPServer {
	merged := append([]MCPServer(nil), assembly...)
	if strings.TrimSpace(raw) == "" {
		return merged
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var rows []mcpServerSetting
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		logf("[toolkit] parse %s: %v", contracts.SettingMCPServers, err)
		return merged
	}
	seen := make(map[string]bool, len(merged)+len(rows))
	for _, server := range merged {
		if url := strings.TrimSpace(server.URL); url != "" {
			seen[dedupeURLKey(url)] = true
		}
	}
	for index, row := range rows {
		name := strings.TrimSpace(row.Name)
		url := strings.TrimSpace(row.URL)
		if name == "" || url == "" || strings.ContainsRune(name, 0) || strings.ContainsRune(url, 0) {
			logf("[toolkit] skip %s[%d]: name and URL are required and must not contain NUL", contracts.SettingMCPServers, index)
			continue
		}
		key := dedupeURLKey(url)
		if seen[key] {
			logf("[toolkit] skip %s[%d]: duplicate URL %q (assembly wins)", contracts.SettingMCPServers, index, url)
			continue
		}
		seen[key] = true
		merged = append(merged, MCPServer{Name: name, URL: url})
	}
	return merged
}
