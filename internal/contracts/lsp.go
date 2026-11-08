package contracts

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SettingLSPServer configures the optional LSP client the toolkit exposes as
// lsp_* tools (single server; empty disables it, restart applies).
const SettingLSPServer = "lsp.server"

// LSPServerConfig is the stored configuration of the one optional language
// server. The toolkit starts it at activation and registers its rig lsp_* tools.
type LSPServerConfig struct {
	// Name is a human label for the server (optional).
	Name string `json:"name,omitempty"`
	// Command is the language-server executable (required; empty disables it).
	Command string `json:"command"`
	// Args are passed to Command verbatim.
	Args []string `json:"args,omitempty"`
	// Dir is the server process working directory (optional).
	Dir string `json:"dir,omitempty"`
	// RootURI is the initialize workspace root (optional; derived from Dir).
	RootURI string `json:"rootUri,omitempty"`
	// LanguageID is the didOpen language id (optional; defaults to plaintext).
	LanguageID string `json:"languageId,omitempty"`
	// Env entries ("KEY=VALUE") are appended to the server environment.
	Env []string `json:"env,omitempty"`
}

// ParseLSPServerConfig decodes the lsp.server settings row. An empty value
// returns the zero config (LSP disabled) without error; a JSON row without a
// usable Command is an error.
func ParseLSPServerConfig(raw string) (LSPServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return LSPServerConfig{}, nil
	}
	var cfg LSPServerConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return LSPServerConfig{}, fmt.Errorf("%s: parse: %w", SettingLSPServer, err)
	}
	cfg.normalize()
	if cfg.Command == "" {
		return LSPServerConfig{}, fmt.Errorf("%s: command is required", SettingLSPServer)
	}
	return cfg, nil
}

// EncodeLSPServerConfig renders the lsp.server settings row, normalizing the
// values exactly like ParseLSPServerConfig does.
func EncodeLSPServerConfig(cfg LSPServerConfig) (string, error) {
	cfg.normalize()
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("%s: encode: %w", SettingLSPServer, err)
	}
	return string(encoded), nil
}

// normalize trims the scalar fields and drops blank Args/Env entries.
func (c *LSPServerConfig) normalize() {
	c.Name = strings.TrimSpace(c.Name)
	c.Command = strings.TrimSpace(c.Command)
	c.Dir = strings.TrimSpace(c.Dir)
	c.RootURI = strings.TrimSpace(c.RootURI)
	c.LanguageID = strings.TrimSpace(c.LanguageID)
	c.Args = trimStrings(c.Args)
	c.Env = trimStrings(c.Env)
}

// trimStrings trims each entry and drops the empty ones; nil stays nil.
func trimStrings(values []string) []string {
	if values == nil {
		return nil
	}
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

// SettingLSPServers stores the multi-server LSP configuration as a JSON array
// of LSPServerConfig (name required, unique). SettingLSPActive names the active
// server; when empty/unknown the first configured server is used.
const (
	SettingLSPServers = "lsp.servers"
	SettingLSPActive  = "lsp.active"
)

// ParseLSPServerConfigList decodes the lsp.servers settings row. An empty value
// returns nil without error; every entry requires a command and a unique name.
func ParseLSPServerConfigList(raw string) ([]LSPServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var rows []LSPServerConfig
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("%s: parse: %w", SettingLSPServers, err)
	}
	return normalizeLSPServerList(rows, SettingLSPServers)
}

// normalizeLSPServerList normalizes and validates a server list (shared by the
// settings rows and the workspace config file): command and name are required,
// names must be unique case-insensitively.
func normalizeLSPServerList(rows []LSPServerConfig, source string) ([]LSPServerConfig, error) {
	servers := make([]LSPServerConfig, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for index, cfg := range rows {
		cfg.normalize()
		if cfg.Command == "" {
			return nil, fmt.Errorf("%s: servers[%d]: command is required", source, index)
		}
		if cfg.Name == "" {
			return nil, fmt.Errorf("%s: servers[%d]: name is required", source, index)
		}
		key := strings.ToLower(cfg.Name)
		if seen[key] {
			return nil, fmt.Errorf("%s: servers[%d]: duplicate name %q", source, index, cfg.Name)
		}
		seen[key] = true
		servers = append(servers, cfg)
	}
	return servers, nil
}

// EncodeLSPServerConfigList renders the lsp.servers settings row, normalizing
// every entry exactly like ParseLSPServerConfig does.
func EncodeLSPServerConfigList(servers []LSPServerConfig) (string, error) {
	normalized := make([]LSPServerConfig, len(servers))
	for index, cfg := range servers {
		cfg.normalize()
		normalized[index] = cfg
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("%s: encode: %w", SettingLSPServers, err)
	}
	return string(encoded), nil
}

// ResolveLSPServerConfigs merges the multi-server list with the legacy single
// lsp.server row: a non-empty list wins; otherwise a configured legacy server
// becomes the single "default" entry (empty/absent means LSP disabled).
func ResolveLSPServerConfigs(serversRaw, legacyRaw string) ([]LSPServerConfig, error) {
	servers, err := ParseLSPServerConfigList(serversRaw)
	if err != nil {
		return nil, err
	}
	if len(servers) > 0 {
		return servers, nil
	}
	legacy, err := ParseLSPServerConfig(legacyRaw)
	if err != nil {
		return nil, err
	}
	if legacy.Command == "" {
		return nil, nil
	}
	if legacy.Name == "" {
		legacy.Name = "default"
	}
	return []LSPServerConfig{legacy}, nil
}

// ActiveLSPServer returns the config named by active, falling back to the first
// configured server when the name is empty or unknown.
func ActiveLSPServer(servers []LSPServerConfig, active string) (LSPServerConfig, bool) {
	if len(servers) == 0 {
		return LSPServerConfig{}, false
	}
	name := strings.TrimSpace(active)
	if name != "" {
		for _, cfg := range servers {
			if strings.EqualFold(cfg.Name, name) {
				return cfg, true
			}
		}
	}
	return servers[0], true
}

// SettingLSPWorkspaceConfig opts into reading the repository's LSP config file
// (.zeokeel/lsp.json): only the exact value "allow" enables it (default off).
const SettingLSPWorkspaceConfig = "lsp.workspace_config"

// LSPWorkspaceConfigFile is the workspace-relative path of the opt-in LSP
// configuration file (a JSON object with "servers", or a top-level array).
const LSPWorkspaceConfigFile = ".zeokeel/lsp.json"

// ParseLSPWorkspaceConfigFile decodes a workspace LSP config file: either a
// {"servers":[...]} object or a top-level array of servers. The entries follow
// the same rules as lsp.servers (normalize; command/name required; unique
// names); empty data is an empty list and malformed JSON is an error.
func ParseLSPWorkspaceConfigFile(data []byte) ([]LSPServerConfig, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var document struct {
			Servers []LSPServerConfig `json:"servers"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("%s: parse: %w", LSPWorkspaceConfigFile, err)
		}
		return normalizeLSPServerList(document.Servers, LSPWorkspaceConfigFile)
	}
	var rows []LSPServerConfig
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("%s: parse: %w", LSPWorkspaceConfigFile, err)
	}
	return normalizeLSPServerList(rows, LSPWorkspaceConfigFile)
}
