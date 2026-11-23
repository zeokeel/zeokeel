package toolkit

// User-configured MCP servers (settings mcp.servers) are merged with the
// assembly's mcpServers at activation. The assembly list wins on a URL
// conflict: it is the operator/product policy, and the Plugins screen already
// shows those servers; the user setting only adds endpoints.

import (
	"net/url"
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
	return mergeMCPServersForWorkspace(assembly, raw, "", logf)
}

// mergeMCPServersForWorkspace merges the assembly servers with the settings
// rows that belong to workspaceID (a legacy-global row with an empty
// WorkspaceID stays visible everywhere until the connectors page migrates it).
// Rows scoped to another workspace are ignored, so one workspace's connectors
// never leak into another. Assembly rows are product policy and always apply.
func mergeMCPServersForWorkspace(assembly []MCPServer, raw, workspaceID string,
	logf func(format string, args ...any)) []MCPServer {
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
	scoped := rows[:0]
	for _, row := range rows {
		if contracts.MCPServerVisibleInWorkspace(row, workspaceID) {
			scoped = append(scoped, row)
		}
	}
	return mergeMCPServerRows(merged, scoped, contracts.SettingMCPServers, true, logf)
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
		auth := cloneMCPAuthConfig(row.Auth)
		if source == "workspace mcp.json" && auth != nil {
			// A repository-provided mcp.json must not be able to start an
			// interactive authorization against a user's account.
			logf("[toolkit] ignore OAuth auth from %s[%d]: authentication is only configurable in settings", source, index)
			auth = nil
		}
		server := MCPServer{
			Name:      name,
			URL:       url,
			Transport: transport,
			Command:   command,
			Args:      cleanMCPArgs(row.Args),
			Env:       cleanMCPArgs(row.Env),
			Headers:   cloneMCPHeaders(row.Headers),
			TimeoutMs: row.TimeoutMs,
			Auth:      auth,
		}
		if server.Auth != nil {
			normalized, authErr := normalizeMCPAuth(server.Auth, server.URL)
			if authErr != "" {
				logf("[toolkit] skip %s[%d]: %s", source, index, authErr)
				continue
			}
			server.Auth = normalized
			if server.Auth != nil && server.Auth.Mode == contracts.MCPAuthOAuth2 {
				server.URL = contracts.NormalizeMCPURL(server.URL)
			}
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

// mergeMCPServerConfigs merges already-parsed settings rows after the assembly
// servers (the reconcile step has already migrated/GC'd the rows).
func mergeMCPServerConfigs(assembly []MCPServer, rows []contracts.MCPServerConfig,
	logf func(format string, args ...any)) []MCPServer {
	return mergeMCPServerRows(append([]MCPServer(nil), assembly...), rows, contracts.SettingMCPServers, true, logf)
}

// reconcileMCPServerRows migrates legacy-global connector rows (empty
// WorkspaceID) to the current workspace and drops rows scoped to a workspace
// that no longer exists (valid != nil; a nil set means the registry could not
// be read and every row is kept). The rewritten list is persisted immediately
// on the toolkit load/reload path, so migration never waits for a page visit.
func reconcileMCPServerRows(settings contracts.SettingsService, hasSettings bool, currentID string,
	valid map[string]bool, logf func(format string, args ...any)) []contracts.MCPServerConfig {
	if !hasSettings || settings == nil {
		return nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil {
		logf("[toolkit] parse %s: %v", contracts.SettingMCPServers, err)
		return nil
	}
	currentID = strings.TrimSpace(currentID)
	changed := false
	kept := make([]contracts.MCPServerConfig, 0, len(rows))
	for _, row := range rows {
		scope := strings.TrimSpace(row.WorkspaceID)
		if scope != "" && valid != nil && !valid[scope] {
			logf("[toolkit] drop MCP connector %q scoped to deleted workspace %q", row.Name, scope)
			changed = true
			continue
		}
		if scope == "" && currentID != "" {
			// Eager migration: bind the pre-upgrade global row to the workspace
			// the toolkit is loading for, before any page reads it.
			row.WorkspaceID = currentID
			changed = true
		}
		kept = append(kept, row)
	}
	if changed {
		encoded, encodeErr := contracts.EncodeMCPServersJSON(kept)
		if encodeErr != nil {
			logf("[toolkit] encode migrated %s: %v", contracts.SettingMCPServers, encodeErr)
			return kept
		}
		if writeErr := settings.Set(contracts.SettingMCPServers, encoded); writeErr != nil {
			logf("[toolkit] persist migrated %s: %v", contracts.SettingMCPServers, writeErr)
		}
	}
	return kept
}

// gcMCPDisabledSettings clears the scoped disabled-server/tool keys of deleted
// workspaces. A nil valid set (registry List failed) keeps every key.
func gcMCPDisabledSettings(settings contracts.SettingsService, hasSettings bool,
	valid map[string]bool, logf func(format string, args ...any)) {
	if !hasSettings || settings == nil || valid == nil {
		return
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	for _, base := range []string{contracts.SettingMCPDisabledServers, contracts.SettingMCPDisabledTools} {
		prefix := base + "."
		for key := range settings.All() {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			scope := strings.TrimPrefix(key, prefix)
			if scope == "" || valid[scope] {
				continue
			}
			if err := settings.Set(key, ""); err != nil {
				logf("[toolkit] clear %s for deleted workspace %q: %v", key, scope, err)
				continue
			}
			logf("[toolkit] cleared %s for deleted workspace %q", key, scope)
		}
	}
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

// cloneMCPHeaders copies one header map so merge results never alias settings.
func cloneMCPHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cloneMCPAuthConfig copies one auth config and its slice fields.
func cloneMCPAuthConfig(auth *contracts.MCPAuthConfig) *contracts.MCPAuthConfig {
	if auth == nil {
		return nil
	}
	out := *auth
	out.Scopes = append([]string(nil), auth.Scopes...)
	return &out
}

// normalizeMCPAuth validates one connector's auth mode. An empty bearer token
// degrades to no authentication (the historical row semantics); OAuth requires
// an HTTP URL.
func normalizeMCPAuth(auth *contracts.MCPAuthConfig, url string) (*contracts.MCPAuthConfig, string) {
	if auth == nil {
		return nil, ""
	}
	auth.Mode = contracts.MCPAuthMode(strings.ToLower(strings.TrimSpace(string(auth.Mode))))
	switch auth.Mode {
	case contracts.MCPAuthNone:
		return nil, ""
	case contracts.MCPAuthBearer:
		auth.Token = strings.TrimSpace(auth.Token)
		if auth.Token == "" {
			return nil, ""
		}
		if strings.TrimSpace(url) == "" {
			return nil, "bearer auth requires an HTTP URL"
		}
		return auth, ""
	case contracts.MCPAuthOAuth2:
		if strings.TrimSpace(url) == "" {
			return nil, "OAuth requires an HTTP URL"
		}
		if !validOAuthEndpointURL(url) {
			return nil, "OAuth requires an https URL (loopback http is allowed for local servers)"
		}
		auth.Scopes = cleanMCPArgs(auth.Scopes)
		// The connector URL is the canonical resource (rig enforces URL ==
		// resource); the legacy override field is ignored.
		auth.Resource = ""
		return auth, ""
	default:
		return nil, "unsupported auth mode " + string(auth.Mode)
	}
}

// validOAuthEndpointURL accepts https, or plain http only for loopback hosts.
func validOAuthEndpointURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return true
	case "http":
		host := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
		return host == "127.0.0.1" || host == "::1" || host == "localhost"
	default:
		return false
	}
}
