package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Settings stores are the persistence seam behind the Creator mode
// settings_get/settings_set tools. *store.Store satisfies it.
type SettingsStore interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

// settingsKeyPattern restricts keys to the flat, lowercase, dotted namespace
// ZeoKeel uses for settings. It rejects whitespace, control characters,
// path-like values and empty keys before they reach the store.
var settingsKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// settingsSecretPattern matches setting names that hold credentials. Creator
// mode may inspect and tune behavior, but it must never read or rewrite a
// secret through a model-facing tool.
var settingsSecretPattern = regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|token|password|credential|authorization)`)

// SettingsAllowedPrefixes is the Creator-mode setting whitelist by namespace.
// It covers the UI, agent/preset, plugin, skill, approval, model/provider and
// web/MCP configuration the app exposes; secret names are still rejected by
// settingsSecretPattern.
var SettingsAllowedPrefixes = []string{
	"ui.",
	"agent.",
	"plugin.",
	"skills.",
	"approval.",
	"model.",
	"provider.",
	"websearch.",
	"web_search.",
	"mcp.",
}

// SettingsAllowedKeys is the exact-key part of the Creator-mode whitelist for
// well-known keys whose namespace would otherwise be too broad.
var SettingsAllowedKeys = map[string]bool{
	"model.default":              true,
	"provider.model":             true,
	"provider.base_url":          true,
	"provider.reasoning_effort":  true,
	"agent.preset":               true,
	"agent.collaboration_mode":   true,
	"skills.root":                true,
	"approval.tools":             true,
	"mcp.servers":                true,
	"websearch.endpoint":         true,
	"websearch.max_results":      true,
	"web_search.endpoint":        true,
	"web_search.max_results":     true,
	"ui.language":                true,
	"ui.theme":                   true,
	"ui.font_size":               true,
	"ui.permission":              true,
	"ui.workspace":               true,
	"ui.work_details":            true,
	"ui.show_coding_view":        true,
	"ui.collapse_work_details":   true,
	"ui.send_behavior":           true,
	"plugin.agent_loop.enabled":  true,
	"plugin.shell.enabled":       true,
	"plugin.skills.enabled":      true,
	"plugin.subagent.enabled":    true,
	"plugin.web_search.enabled":  true,
	"plugin.mcp.enabled":         true,
	"plugin.agent_teams.enabled": true,
}

// SettingsKeyAllowed reports whether Creator mode may read or write key. This
// is the single whitelist used by both settings tools.
func SettingsKeyAllowed(key string) bool {
	key = strings.TrimSpace(key)
	if !settingsKeyPattern.MatchString(key) {
		return false
	}
	if settingsSecretPattern.MatchString(key) {
		return false
	}
	if SettingsAllowedKeys[key] {
		return true
	}
	for _, prefix := range SettingsAllowedPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// settingsReadValue is the value settings_get exposes to the model. Most
// settings are returned verbatim, but MCP configuration can embed credentials:
// mcp.servers is reduced to its server names (like inspect's projection),
// because env, args and headers may all carry secrets, and any other mcp.* JSON
// value has secret-named fields redacted. This is the read half of the Creator
// contract; settings_set still writes the full configuration.
func settingsReadValue(key, value string) string {
	if !strings.HasPrefix(key, "mcp.") {
		return value
	}
	if key == "mcp.servers" {
		names, err := settingsMCPServerNames(value)
		if err != nil {
			return "[configured: invalid JSON]"
		}
		encoded, err := json.Marshal(names)
		if err != nil {
			return "[configured]"
		}
		return string(encoded)
	}
	return redactSettingsJSONSecrets(value)
}

// settingsMCPServerNames extracts the unique, non-empty server names from the
// mcp.servers JSON array without keeping the command, args, env or headers.
func settingsMCPServerNames(raw string) ([]string, error) {
	var servers []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(servers))
	seen := make(map[string]bool, len(servers))
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

// redactSettingsJSONSecrets redacts secret-named keys in a JSON object or array
// and secret-named environment entries in an "env" list. Plain scalar values
// are returned unchanged.
func redactSettingsJSONSecrets(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return raw
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return "[configured: invalid JSON]"
	}
	encoded, err := json.Marshal(redactSettingsJSONValue(value))
	if err != nil {
		return "[configured]"
	}
	return string(encoded)
}

// redactSettingsJSONValue walks decoded JSON and replaces secret object values.
func redactSettingsJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if settingsSecretPattern.MatchString(key) {
				out[key] = "[redacted]"
				continue
			}
			if strings.EqualFold(key, "env") {
				if entries, ok := item.([]any); ok {
					out[key] = redactSettingsEnvEntries(entries)
					continue
				}
			}
			out[key] = redactSettingsJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactSettingsJSONValue(item)
		}
		return out
	default:
		return value
	}
}

// redactSettingsEnvEntries redacts "KEY=VALUE" entries whose key names a
// credential, keeping the variable name so the model can still see the shape of
// the server's environment.
func redactSettingsEnvEntries(entries []any) []any {
	out := make([]any, len(entries))
	for i, entry := range entries {
		text, ok := entry.(string)
		if !ok {
			out[i] = redactSettingsJSONValue(entry)
			continue
		}
		name, _, hasValue := strings.Cut(text, "=")
		if hasValue && settingsSecretPattern.MatchString(name) {
			out[i] = name + "=[redacted]"
			continue
		}
		out[i] = text
	}
	return out
}

// settingsGetTool implements the "settings_get" tool.
type settingsGetTool struct {
	store SettingsStore
}

var _ Tool = (*settingsGetTool)(nil)

// NewSettingsGetTool returns the Creator-mode setting reader. A nil store is
// allowed: the tool then reports "settings store is not configured" when
// invoked instead of panicking.
func NewSettingsGetTool(store SettingsStore) Tool { return &settingsGetTool{store: store} }

func (t *settingsGetTool) Name() string { return "settings_get" }

const settingsGetDescription = `Read one whitelisted ZeoKeel setting.

Call it with a JSON object:
  {"key":"ui.language"}

Only Creator mode exposes this tool. The key whitelist covers the ui., agent., plugin., skills., approval., model., provider., websearch., web_search. and mcp. namespaces plus well-known exact keys. Secret names (API keys, tokens, passwords, credentials) are always rejected. mcp.servers is reduced to its server names because env, args and headers can carry credentials; other mcp.* JSON values have secret-named fields redacted. Every other value is returned as stored text.

Args JSON schema:
{"type":"object","properties":{"key":{"type":"string","description":"Setting key, for example ui.language."}},"required":["key"],"additionalProperties":false}`

func (t *settingsGetTool) Description() string { return settingsGetDescription }

func (t *settingsGetTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("settings_get: settings store is not configured")
	}
	var in struct {
		Key string `json:"key"`
	}
	if err := decodeSettingsArgs("settings_get", args, &in); err != nil {
		return "", err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return "", fmt.Errorf(`settings_get: "key" is required`)
	}
	if !SettingsKeyAllowed(key) {
		return "", fmt.Errorf("settings_get: key %q is not allowed by the Creator settings whitelist", key)
	}
	value, err := t.store.GetSetting(key)
	if err != nil {
		return "", fmt.Errorf("settings_get: %s: %w", key, err)
	}
	return settingsReadValue(key, value), nil
}

// settingsSetTool implements the "settings_set" tool.
type settingsSetTool struct {
	store SettingsStore
}

var _ Tool = (*settingsSetTool)(nil)

// NewSettingsSetTool returns the Creator-mode setting writer. A nil store is
// allowed: the tool then reports "settings store is not configured" when
// invoked instead of panicking.
func NewSettingsSetTool(store SettingsStore) Tool { return &settingsSetTool{store: store} }

func (t *settingsSetTool) Name() string { return "settings_set" }

const settingsSetDescription = `Write one whitelisted ZeoKeel setting.

Call it with a JSON object:
  {"key":"ui.language","value":"zh"}

Only Creator mode exposes this tool. The same whitelist as settings_get applies; secret names (API keys, tokens, passwords, credentials) are rejected. Values are stored as text; a JSON string, number, boolean or null is accepted and normalized to text (null/omitted stores an empty value). A setting change takes effect for the next agent rebuild or turn as the setting owner documents.

Args JSON schema:
{"type":"object","properties":{"key":{"type":"string","description":"Setting key, for example ui.language."},"value":{"description":"New value; strings, numbers and booleans are stored as text."}},"required":["key"],"additionalProperties":false}`

func (t *settingsSetTool) Description() string { return settingsSetDescription }

func (t *settingsSetTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("settings_set: settings store is not configured")
	}
	var in struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := decodeSettingsArgs("settings_set", args, &in); err != nil {
		return "", err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return "", fmt.Errorf(`settings_set: "key" is required`)
	}
	if !SettingsKeyAllowed(key) {
		return "", fmt.Errorf("settings_set: key %q is not allowed by the Creator settings whitelist", key)
	}
	value, err := settingsValueText(in.Value)
	if err != nil {
		return "", fmt.Errorf("settings_set: value: %w", err)
	}
	if err := t.store.SetSetting(key, value); err != nil {
		return "", fmt.Errorf("settings_set: %s: %w", key, err)
	}
	return fmt.Sprintf("stored %s", key), nil
}

// decodeSettingsArgs decodes one settings tool argument object strictly.
func decodeSettingsArgs(name string, args json.RawMessage, out any) error {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" {
		return fmt.Errorf(`%s: "key" is required`, name)
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: invalid arguments: %w", name, err)
	}
	return nil
}

// settingsValueText normalizes a JSON value to the text the settings table
// stores. Strings are unquoted; other scalars keep their JSON spelling; a
// missing or null value becomes "".
func settingsValueText(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		return text, nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("not valid JSON")
	}
	return trimmed, nil
}
