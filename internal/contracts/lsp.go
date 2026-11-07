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
