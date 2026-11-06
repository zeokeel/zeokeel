package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Language-server configuration.
//
// lsp.servers is a JSON object keyed by language id ("go", "python", ...):
//
//	{"go": {"command": "gopls", "args": ["serve"], "rootURI": "file:///repo"}}
//
// A workspace file <root>/.zeokeel/lsp.json may declare the same shape, but it
// arrives with cloned repositories, and starting the commands it declares is
// third-party code execution. It is therefore untrusted data: only read after
// an explicit lsp.workspace_config=allow opt-in, exactly like mcp.workspace_config.
// An explicit lsp.servers setting always wins and the workspace file is not
// read at all.
const (
	// SettingKey is the settings key holding the JSON server map.
	SettingKey = "lsp.servers"
	// WorkspaceConfigKey is the setting that gates the workspace file.
	WorkspaceConfigKey = "lsp.workspace_config"
	// WorkspaceConfigOff is the default: the workspace file is ignored.
	WorkspaceConfigOff = "off"
	// WorkspaceConfigAllow enables the workspace file after an explicit opt-in.
	WorkspaceConfigAllow = "allow"
	// WorkspaceConfigFile is the workspace-relative path of the untrusted file.
	WorkspaceConfigFile = ".zeokeel/lsp.json"
)

// ServerConfig describes one configured stdio language server.
type ServerConfig struct {
	// Command is the executable to start. Required.
	Command string `json:"command"`
	// Args are passed to Command verbatim.
	Args []string `json:"args,omitempty"`
	// RootURI overrides the workspace root sent as rootUri. Optional.
	RootURI string `json:"rootURI,omitempty"`
	// Env entries are "KEY=VALUE" overrides appended to the scrubbed child
	// environment. Optional.
	Env []string `json:"env,omitempty"`
}

// WorkspaceConfigAllowed reports whether value opts into reading the workspace
// file. Only the exact value "allow" (case-insensitive, surrounding space
// ignored) enables it: ambiguous truthy aliases fail closed.
func WorkspaceConfigAllowed(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), WorkspaceConfigAllow)
}

// ParseServersConfig parses an lsp.servers JSON object. Empty input means "no
// servers". Errors are stable and name the offending language, never echoing
// the raw configuration (arguments can carry credentials).
func ParseServersConfig(raw string) (map[string]ServerConfig, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return map[string]ServerConfig{}, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		if strings.HasPrefix(trimmed, "[") {
			return nil, errors.New(`lsp.servers must be a JSON object keyed by language id (for example {"go": {"command": "gopls"}})`)
		}
		return nil, fmt.Errorf("lsp.servers is not valid JSON: %w", err)
	}
	servers := make(map[string]ServerConfig, len(entries))
	for language, rawServer := range entries {
		language = strings.TrimSpace(language)
		if language == "" {
			return nil, errors.New("lsp.servers: language ids must not be empty")
		}
		var server ServerConfig
		if err := json.Unmarshal(rawServer, &server); err != nil {
			return nil, fmt.Errorf("lsp.servers: language %q: invalid server object: %w", language, err)
		}
		if err := validateServerConfig(language, server); err != nil {
			return nil, err
		}
		servers[language] = server
	}
	return servers, nil
}

// LoadServers resolves the effective server map for one workspace build. An
// explicit rawSetting wins; otherwise, when the workspace file is explicitly
// allowed, <root>/.zeokeel/lsp.json is read (a missing file means no servers).
func LoadServers(rawSetting, workspaceRoot, workspaceConfig string) (map[string]ServerConfig, error) {
	if strings.TrimSpace(rawSetting) != "" {
		return ParseServersConfig(rawSetting)
	}
	if !WorkspaceConfigAllowed(workspaceConfig) {
		return map[string]ServerConfig{}, nil
	}
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return map[string]ServerConfig{}, nil
	}
	path := filepath.Join(root, filepath.FromSlash(WorkspaceConfigFile))
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]ServerConfig{}, nil
		}
		return nil, fmt.Errorf("lsp: read workspace config %s: %w", WorkspaceConfigFile, err)
	}
	servers, err := ParseServersConfig(string(data))
	if err != nil {
		return nil, fmt.Errorf("lsp: %s: %w", WorkspaceConfigFile, err)
	}
	return servers, nil
}

// validateServerConfig checks one parsed server entry.
func validateServerConfig(language string, server ServerConfig) error {
	if strings.TrimSpace(server.Command) == "" {
		return fmt.Errorf("lsp.servers: language %q: \"command\" is required", language)
	}
	return nil
}
