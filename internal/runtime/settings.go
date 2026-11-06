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
	"model.default":                 true,
	"provider.model":                true,
	"provider.base_url":             true,
	"provider.reasoning_effort":     true,
	"agent.preset":                  true,
	"agent.collaboration_mode":      true,
	"skills.root":                   true,
	"approval.tools":                true,
	"mcp.servers":                   true,
	"websearch.endpoint":            true,
	"websearch.max_results":         true,
	"web_search.endpoint":           true,
	"web_search.max_results":        true,
	"ui.language":                   true,
	"ui.theme":                      true,
	"ui.font_size":                  true,
	"ui.permission":                 true,
	"ui.workspace":                  true,
	"ui.work_details":               true,
	"ui.show_coding_view":           true,
	"ui.collapse_work_details":      true,
	"ui.send_behavior":              true,
	"ui.memory_workspace_mode":      true,
	"ui.memory_global_mode":         true,
	"ui.memory_extractor":           true,
	"ui.memory_extraction_interval": true,
	"plugin.agent_loop.enabled":     true,
	"plugin.shell.enabled":          true,
	"plugin.skills.enabled":         true,
	"plugin.subagent.enabled":       true,
	"plugin.web_search.enabled":     true,
	"plugin.mcp.enabled":            true,
	"plugin.agent_teams.enabled":    true,
}

// SettingsWriteAllowedKeys is the settings_set allowlist. It is deliberately
// narrower than the read whitelist: Creator mode may tune benign behavior, but
// it must not be able to change a setting whose next value starts a process,
// moves credentials to a different destination, or weakens an approval/sandbox
// control. Writing those through a model-facing tool would be a second
// execution/authorization channel outside the approval gate (the model — or
// prompt-injected workspace content — could point MCP at an attacker command,
// redirect provider traffic, or empty the approval list).
//
// Everything absent here is rejected even when settings_get may read it:
//   - mcp.*            (server command/args/env; including any workspace MCP
//     enable switch) — process execution on next rebuild
//   - provider.base_url — sends the API key and conversation to another host
//   - websearch.endpoint / web_search.endpoint — sends the stored key to
//     another host
//   - approval.tools   — would disable/weaken the approval gate
//   - ui.permission    — would weaken the default access mode
//   - plugin.shell.*, plugin.mcp.enabled — re-enable a process-capable plugin
//     or its configuration path
var SettingsWriteAllowedKeys = map[string]bool{
	// Model behavior (not endpoints or credentials).
	"model.default":             true,
	"provider.model":            true,
	"provider.reasoning_effort": true,
	// Agent preset selection.
	"agent.preset":             true,
	"agent.collaboration_mode": true,
	// Skill catalog root: only changes which SKILL.md files are read as
	// untrusted data, not what can execute.
	"skills.root": true,
	// Appearance and composer behavior.
	"ui.language":              true,
	"ui.theme":                 true,
	"ui.font_size":             true,
	"ui.work_details":          true,
	"ui.show_coding_view":      true,
	"ui.collapse_work_details": true,
	"ui.send_behavior":         true,
	// Auto-memory modes are benign preferences (not execution paths); the
	// extractor only learns from text already in the session history.
	"ui.memory_workspace_mode":      true,
	"ui.memory_global_mode":         true,
	"ui.memory_extractor":           true,
	"ui.memory_extraction_interval": true,
	// Safe plugin tuning: iteration/timeout/output knobs and feature switches
	// that cannot start arbitrary processes by themselves.
	"plugin.agent_loop.enabled":            true,
	"plugin.agent_loop.max_iterations":     true,
	"plugin.agent_loop.budget_warn_steps":  true,
	"plugin.agent_loop.budget_warn_tokens": true,
	"plugin.skills.enabled":                true,
	"plugin.subagent.enabled":              true,
	"plugin.subagent.depth":                true,
	"plugin.subagent.model":                true,
	"plugin.web_search.enabled":            true,
	"plugin.agent_teams.enabled":           true,
	// Search result cap (the endpoint itself is not writable).
	"websearch.max_results":  true,
	"web_search.max_results": true,
}

// SettingsWriteDeniedKeys are the read-whitelisted keys the Creator writer must
// never change. They are listed explicitly (in addition to the exact-key
// allowlist) so the intent survives a future widening of the allowlist: a key
// here can never become writable by mistake.
var SettingsWriteDeniedKeys = map[string]bool{
	"mcp.servers":          true,
	"mcp.workspace_config": true,
	"provider.base_url":    true,
	"websearch.endpoint":   true,
	"web_search.endpoint":  true,
	"approval.tools":       true,
	"ui.permission":        true,
	"plugin.shell.enabled": true,
	"plugin.mcp.enabled":   true,
}

// SettingsWriteDeniedPrefixes are namespaces where every key could re-open an
// execution or credential path. Denying the whole namespace is simpler and
// safer than enumerating future server/transport/endpoint keys.
var SettingsWriteDeniedPrefixes = []string{
	"mcp.",
	"plugin.shell.",
	"plugin.mcp.",
}

// SettingsKeyWritable reports whether Creator mode's settings_set tool may
// write key. Reads use SettingsKeyAllowed; writes additionally require the
// minimized write allowlist and reject every execution, credential or
// authorization surface.
func SettingsKeyWritable(key string) bool {
	key = strings.TrimSpace(key)
	if !SettingsKeyAllowed(key) {
		return false
	}
	if SettingsWriteDeniedKeys[key] {
		return false
	}
	for _, prefix := range SettingsWriteDeniedPrefixes {
		if strings.HasPrefix(key, prefix) {
			return false
		}
	}
	return SettingsWriteAllowedKeys[key]
}

// SettingsKeyAllowed reports whether Creator mode may read or write key. This
// is the single read whitelist used by settings_get; settings_set applies the
// narrower SettingsKeyWritable on top.
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

Only Creator mode exposes this tool. The write whitelist is narrower than settings_get's read whitelist: secret names (API keys, tokens, passwords, credentials) are rejected, and so is every setting that could start a process, move credentials or weaken the approval/sandbox controls (MCP server configuration, provider/web-search endpoints, approval.tools, ui.permission, shell/MCP plugin switches). Values are stored as text; a JSON string, number, boolean or null is accepted and normalized to text (null/omitted stores an empty value). A setting change takes effect for the next agent rebuild or turn as the setting owner documents.

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
	if !SettingsKeyWritable(key) {
		return "", fmt.Errorf("settings_set: key %q is not writable by the Creator settings tool (execution, credential and authorization settings are read-only)", key)
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
