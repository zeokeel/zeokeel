package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/mcp"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/skills"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// The inspect tool is ZeoKeel's substitute for DSH's Cordis inspect tools,
// which expose the DSH host runtime. Our runtime is ZeoKeel, so inspect
// reports the pieces an agent needs to configure a workspace: plugin toggles,
// relevant settings, sessions, the skill catalog and MCP server names.
//
// It is deliberately read-only and secret-free. Settings values whose key
// contains api_key/secret/token/password/credential/authorization are replaced
// with "[redacted]", and JSON-shaped values are redacted key by key before they
// are returned. MCP configuration is reduced to server names so command
// arguments and environment entries can never leak.
const (
	// inspectMaxOutput caps the whole snapshot (64 KiB).
	inspectMaxOutput = DefaultMaxOutputBytes

	// inspectRecentSessions is how many recent session titles are returned.
	inspectRecentSessions = 10

	// inspectSettingValueBytes bounds one setting value in the snapshot.
	inspectSettingValueBytes = 2 * 1024

	// inspectTextBytes bounds free-form text such as titles and descriptions.
	inspectTextBytes = 300

	// redactedValue replaces a secret value.
	redactedValue = "[redacted]"
)

// InspectConfig configures the inspect tool.
type InspectConfig struct {
	// Store is the workspace index. It is required when a tool is invoked;
	// a nil store reports a clear error instead of panicking.
	Store *store.Store

	// Root is the workspace root. The skill catalog is read from
	// <Root>/skills and mcp.json from <Root>/mcp.json.
	Root string

	// WorkspaceID scopes the sessions section to one workspace. Empty keeps
	// the historical global listing (direct tool use and tests).
	WorkspaceID string
}

// NewInspectTool returns the inspect runtime.Tool. Construction never fails;
// invalid configuration is reported by Invoke.
func NewInspectTool(cfg InspectConfig) runtime.Tool {
	root := strings.TrimSpace(cfg.Root)
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	return &inspectTool{st: cfg.Store, root: root, workspaceID: strings.TrimSpace(cfg.WorkspaceID)}
}

const inspectDescription = `Inspect this ZeoKeel workspace: plugins, settings, sessions, skills and MCP servers.

Call it with an empty object for a full snapshot, or select one section with
{"section":"plugins|settings|sessions|skills|mcp|all"}.

The result is one read-only JSON document:
  kind, root, section
  plugins:  [{name,enabled,source}] where source is "setting" or "default"
  settings: {key:value} for provider, web search, approval, skills, language
            servers (lsp.servers, lsp.workspace_config), UI and model settings.
            Values whose key matches api_key/secret/token/password/credential/
            authorization are "[redacted]"; mcp.servers is reduced to its
            server-name array. Values are capped at 2 KiB.
  sessions: {count,recent:[{id,title,status,updated_at}]} (10 most recent)
  skills:   {root,count,skills:[{name,description,path}],error?}
  mcp:      {source,file?,workspace_config,workspace_config_note?,count,
             servers:[name],error?} where source is "settings", "mcp.json" or
             "none". workspace_config is the effective mcp.workspace_config
             value ("off" by default), and workspace_config_note explains
             whether a workspace mcp.json would be ignored or is untrusted
             repository content that only runs after an explicit opt-in.

The whole document is capped at 64 KiB. The tool uses only read-only store
and filesystem APIs: it never changes settings, sessions or workspace files,
and never returns API keys, tokens or MCP environment entries.

Args JSON schema:
{"type":"object","properties":{"section":{"type":"string","enum":["plugins","settings","sessions","skills","mcp","all"],"description":"Section to inspect; defaults to all."}},"additionalProperties":false}`

// inspectPluginDef is one built-in plugin toggle. The catalog mirrors the
// shell's built-in plugins; inspect must not depend on the UI package (that
// would pull the widget toolkit into the agent runtime), and the store has no
// settings-enumeration API, so the known catalog is the source of truth here.
type inspectPluginDef struct {
	name           string
	defaultEnabled bool
}

var inspectPluginCatalog = []inspectPluginDef{
	{name: "web_search", defaultEnabled: true},
	{name: "skills", defaultEnabled: true},
	{name: "mcp", defaultEnabled: true},
	{name: "shell", defaultEnabled: true},
	{name: "subagent", defaultEnabled: true},
	{name: "lsp", defaultEnabled: true},
	{name: "agent_teams", defaultEnabled: false},
	{name: "auto_review", defaultEnabled: false},
	{name: "developer_tools", defaultEnabled: false},
}

// inspectSettingKeys is the curated set of settings an agent needs to
// understand the workspace. The store exposes GetSetting but no enumeration,
// so unknown keys are deliberately not reached for through the SQLite file.
var inspectSettingKeys = []string{
	store.ProviderAPIKeyKey,
	store.ProviderBaseURLKey,
	store.ProviderModelKey,
	store.ProviderReasoningEffortKey,
	"websearch.endpoint",
	"websearch.api_key",
	"websearch.max_results",
	"web_search.endpoint",
	"web_search.api_key",
	"approval.tools",
	"skills.root",
	"mcp.servers",
	"lsp.servers",
	"lsp.workspace_config",
	store.ModelDefaultKey,
	"ui.language",
	"ui.theme",
	"ui.font_size",
	"ui.permission",
	"ui.workspace",
	"ui.work_details",
	"ui.show_coding_view",
	"ui.collapse_work_details",
	"ui.send_behavior",
}

// inspectSecretNamePattern matches setting and JSON keys that hold secrets.
var inspectSecretNamePattern = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|credential|authorization)`)

// isSecretName reports whether a setting or JSON object key names a secret.
func isSecretName(key string) bool {
	return inspectSecretNamePattern.MatchString(key)
}

// inspectTool implements runtime.Tool.
type inspectTool struct {
	st          *store.Store
	root        string
	workspaceID string
}

var _ runtime.Tool = (*inspectTool)(nil)

// Name implements runtime.Tool.
func (t *inspectTool) Name() string { return "inspect" }

// Description implements runtime.Tool.
func (t *inspectTool) Description() string { return inspectDescription }

// Invoke implements runtime.Tool.
func (t *inspectTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("inspect: tool is not initialized")
	}
	if t.st == nil {
		return "", errors.New("inspect: store is not configured")
	}
	if strings.TrimSpace(t.root) == "" {
		return "", errors.New("inspect: workspace root is not configured")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("inspect: %w", err)
		}
	}
	var in struct {
		Section string `json:"section"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	section := strings.ToLower(strings.TrimSpace(in.Section))
	if section == "" {
		section = "all"
	}
	switch section {
	case "plugins", "settings", "sessions", "skills", "mcp", "all":
	default:
		return "", fmt.Errorf("inspect: unknown section %q (want plugins, settings, sessions, skills, mcp or all)", in.Section)
	}

	snapshot := map[string]any{
		"kind":    "inspect",
		"root":    t.root,
		"section": section,
	}
	if section == "all" || section == "plugins" {
		plugins, err := t.plugins()
		if err != nil {
			return "", fmt.Errorf("inspect: plugins: %w", err)
		}
		snapshot["plugins"] = plugins
	}
	if section == "all" || section == "settings" {
		settings, err := t.settings()
		if err != nil {
			return "", fmt.Errorf("inspect: settings: %w", err)
		}
		snapshot["settings"] = settings
	}
	if section == "all" || section == "sessions" {
		sessions, err := t.sessions()
		if err != nil {
			return "", fmt.Errorf("inspect: sessions: %w", err)
		}
		snapshot["sessions"] = sessions
	}
	if section == "all" || section == "skills" {
		snapshot["skills"] = t.skills()
	}
	if section == "all" || section == "mcp" {
		snapshot["mcp"] = t.mcp()
	}
	return boundedToolJSON(snapshot, inspectMaxOutput, "inspect")
}

// inspectPluginView is one plugin toggle in the snapshot.
type inspectPluginView struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
}

// plugins reads plugin.<name>.enabled for the known catalog.
func (t *inspectTool) plugins() ([]inspectPluginView, error) {
	views := make([]inspectPluginView, 0, len(inspectPluginCatalog))
	for _, plugin := range inspectPluginCatalog {
		view := inspectPluginView{
			Name:    plugin.name,
			Enabled: plugin.defaultEnabled,
			Source:  "default",
		}
		key := "plugin." + plugin.name + ".enabled"
		value, err := t.st.GetSetting(key)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// Keep the documented default.
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", key, err)
		case strings.TrimSpace(value) != "":
			view.Source = "setting"
			if parsed, perr := strconv.ParseBool(strings.TrimSpace(value)); perr == nil {
				view.Enabled = parsed
			}
		}
		views = append(views, view)
	}
	return views, nil
}

// settings returns the curated settings snapshot, including per-model fields.
func (t *inspectTool) settings() (map[string]string, error) {
	out := make(map[string]string, len(inspectSettingKeys)+8)
	for _, key := range inspectSettingKeys {
		value, err := t.st.GetSetting(key)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", key, err)
		}
		out[key] = projectSettingValue(key, value)
	}

	models, err := t.st.ListModels()
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	for _, model := range models {
		prefix := store.ModelSettingPrefix + model.ID + "."
		if model.Name != "" {
			out[prefix+"name"] = truncateBytes(model.Name, inspectSettingValueBytes, "value truncated")
		}
		if model.BaseURL != "" {
			out[prefix+"base_url"] = truncateBytes(model.BaseURL, inspectSettingValueBytes, "value truncated")
		}
		if model.Model != "" {
			out[prefix+"model"] = truncateBytes(model.Model, inspectSettingValueBytes, "value truncated")
		}
		if model.ReasoningEffort != "" {
			out[prefix+"reasoning_effort"] = truncateBytes(model.ReasoningEffort, inspectSettingValueBytes, "value truncated")
		}
		if model.ContextWindow > 0 {
			out[prefix+"context_window"] = strconv.Itoa(model.ContextWindow)
		}
		if model.APIKey != "" {
			out[prefix+"api_key"] = redactedValue
		}
		if model.Default {
			if _, ok := out[store.ModelDefaultKey]; !ok {
				out[store.ModelDefaultKey] = model.ID
			}
		}
	}
	return out, nil
}

// projectSettingValue redacts and bounds one setting value. MCP configuration
// is reduced to names so arguments and environment entries never leak.
func projectSettingValue(key, value string) string {
	if key == "mcp.servers" {
		names, err := parseMCPServerNames([]byte(value), false)
		if err != nil {
			return "[configured: invalid JSON]"
		}
		encoded, err := json.Marshal(names)
		if err != nil {
			return "[configured]"
		}
		return truncateBytes(string(encoded), inspectSettingValueBytes, "value truncated")
	}
	if isSecretName(key) {
		return redactedValue
	}
	return truncateBytes(redactJSONSecrets(value), inspectSettingValueBytes, "value truncated")
}

// redactJSONSecrets redacts secret keys in a JSON object or array value. Plain
// scalar values are returned unchanged (their key was checked by the caller).
func redactJSONSecrets(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return raw
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return raw
	}
	value = redactJSONValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

// redactJSONValue walks decoded JSON and replaces secret object values.
func redactJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if isSecretName(key) {
				out[key] = redactedValue
				continue
			}
			out[key] = redactJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactJSONValue(item)
		}
		return out
	default:
		return value
	}
}

// inspectSessionsSection is the sessions snapshot.
type inspectSessionsSection struct {
	Count  int                  `json:"count"`
	Recent []inspectSessionView `json:"recent"`
}

// inspectSessionView is one recent session.
type inspectSessionView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// sessions returns the total session count and a bounded recent list.
func (t *inspectTool) sessions() (inspectSessionsSection, error) {
	sessions, err := t.st.ListSessions(t.workspaceID)
	if err != nil {
		return inspectSessionsSection{}, err
	}
	recent := sessions
	if len(recent) > inspectRecentSessions {
		recent = recent[:inspectRecentSessions]
	}
	views := make([]inspectSessionView, 0, len(recent))
	for _, session := range recent {
		views = append(views, inspectSessionView{
			ID:        session.ID,
			Title:     truncateBytes(session.Title, inspectTextBytes, "title truncated"),
			Status:    string(session.Status),
			UpdatedAt: session.UpdatedAt,
		})
	}
	return inspectSessionsSection{Count: len(sessions), Recent: views}, nil
}

// inspectSkillsSection is the skill catalog snapshot.
type inspectSkillsSection struct {
	Root   string             `json:"root"`
	Count  int                `json:"count"`
	Skills []inspectSkillView `json:"skills"`
	Error  string             `json:"error,omitempty"`
}

// inspectSkillView is one skill catalog entry (the body is never included).
type inspectSkillView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

// skills loads <root>/skills. A missing directory is an empty catalog; a
// malformed catalog is reported in "error" so the rest of the snapshot stays
// usable.
func (t *inspectTool) skills() inspectSkillsSection {
	dir := filepath.Join(t.root, "skills")
	view := inspectSkillsSection{
		Root:   inspectedRelativePath(t.root, dir),
		Skills: []inspectSkillView{},
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return view
	}
	if err != nil {
		view.Error = truncateBytes(fmt.Sprintf("stat %s: %v", view.Root, err), inspectTextBytes, "error truncated")
		return view
	}
	if !info.IsDir() {
		view.Error = fmt.Sprintf("%s is not a directory", view.Root)
		return view
	}
	loaded, err := skills.Load(dir)
	if err != nil {
		view.Error = truncateBytes(err.Error(), inspectTextBytes, "error truncated")
		return view
	}
	for _, skill := range loaded {
		view.Skills = append(view.Skills, inspectSkillView{
			Name:        skill.Name,
			Description: truncateBytes(skill.Description, inspectTextBytes, "description truncated"),
			Path:        inspectedRelativePath(t.root, skill.Path),
		})
	}
	view.Count = len(view.Skills)
	return view
}

// inspectMCPSection is the MCP server-name snapshot plus the workspace trust
// gate status. It carries no command, argument or environment value, so a
// secret in the configuration can never leak through diagnostics.
type inspectMCPSection struct {
	Source string `json:"source"`
	// File is the workspace-relative config file when source is "mcp.json".
	File string `json:"file,omitempty"`
	// WorkspaceConfig is the effective mcp.workspace_config value ("off" when
	// unset or invalid): "allow" means <root>/mcp.json is executed.
	WorkspaceConfig string `json:"workspace_config"`
	// WorkspaceConfigNote explains what the gate means for this workspace, so
	// diagnostics do not present an ignored file as active configuration.
	WorkspaceConfigNote string   `json:"workspace_config_note,omitempty"`
	Count               int      `json:"count"`
	Servers             []string `json:"servers"`
	Error               string   `json:"error,omitempty"`
}

// mcp reports the configured MCP server names. It mirrors the app wiring: the
// mcp.servers setting wins and mcp.json is read only when that setting is
// absent or blank; when the file exists it is reported together with the
// mcp.workspace_config gate that decides whether the app may execute it.
func (t *inspectTool) mcp() inspectMCPSection {
	view := inspectMCPSection{
		Source:          "none",
		WorkspaceConfig: mcp.WorkspaceConfigOff,
		Servers:         []string{},
	}
	if t.st != nil {
		rawConfig, err := t.st.GetSetting(mcp.WorkspaceConfigKey)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			view.Error = truncateBytes(fmt.Sprintf("read %s: %v", mcp.WorkspaceConfigKey, err), inspectTextBytes, "error truncated")
			return view
		}
		view.WorkspaceConfig = mcp.NormalizeWorkspaceConfig(rawConfig)
	}
	raw, err := t.st.GetSetting("mcp.servers")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		view.Error = truncateBytes(fmt.Sprintf("read mcp.servers: %v", err), inspectTextBytes, "error truncated")
		return view
	}
	path := filepath.Join(t.root, "mcp.json")
	_, statErr := os.Lstat(path)
	fileExists := statErr == nil
	if err == nil && strings.TrimSpace(raw) != "" {
		names, perr := parseMCPServerNames([]byte(raw), false)
		view.Source = "settings"
		if fileExists {
			view.WorkspaceConfigNote = "the mcp.servers setting wins; workspace mcp.json is not read"
		}
		if perr != nil {
			view.Error = truncateBytes(fmt.Sprintf("parse mcp.servers: %v", perr), inspectTextBytes, "error truncated")
			return view
		}
		view.Servers = names
		view.Count = len(names)
		return view
	}

	if !fileExists {
		return view
	}
	view.Source = "mcp.json"
	view.File = "mcp.json"
	if mcp.WorkspaceConfigAllowed(view.WorkspaceConfig) {
		view.WorkspaceConfigNote = "untrusted workspace repository content; enabled by mcp.workspace_config=allow"
	} else {
		view.WorkspaceConfigNote = "ignored: mcp.workspace_config is off (set it to \"allow\" to run workspace-declared MCP servers); the names below are not started"
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		view.Error = truncateBytes(fmt.Sprintf("read mcp.json: %v", rerr), inspectTextBytes, "error truncated")
		return view
	}
	names, perr := parseMCPServerNames(data, true)
	if perr != nil {
		view.Error = truncateBytes(fmt.Sprintf("parse mcp.json: %v", perr), inspectTextBytes, "error truncated")
		return view
	}
	view.Servers = names
	view.Count = len(names)
	return view
}

// parseMCPServerNames extracts the server names from mcp.servers (a JSON
// array) or mcp.json ({"servers":[...]}). Everything except the name is
// intentionally discarded so secrets in command arguments or env cannot leak.
func parseMCPServerNames(data []byte, wrapped bool) ([]string, error) {
	type serverConfig struct {
		Name string `json:"name"`
	}
	var servers []serverConfig
	if wrapped {
		var cfg struct {
			Servers []serverConfig `json:"servers"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		servers = cfg.Servers
	} else {
		if err := json.Unmarshal(data, &servers); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(servers))
	seen := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

// inspectedRelativePath renders path relative to root with forward slashes.
func inspectedRelativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "" {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
