package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/store"
)

// openInspectStore opens a fresh index and closes it at test cleanup.
func openInspectStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	return st
}

// seedInspectFixture builds a store, a skills catalog and an mcp.json.
func seedInspectFixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := openInspectStore(t)
	root := t.TempDir()
	workspace, err := st.CreateWorkspace("demo", root)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	for _, title := range []string{"First session", "Second session"} {
		if _, err := st.CreateSession(workspace.ID, title); err != nil {
			t.Fatalf("CreateSession(%q): %v", title, err)
		}
	}

	settings := map[string]string{
		"plugin.web_search.enabled": "false",
		"plugin.skills.enabled":     "true",
		"provider.api_key":          "sk-provider-secret",
		"provider.model":            "deepseek-chat",
		"provider.base_url":         "https://api.example.test",
		"websearch.api_key":         "ws-websearch-secret",
		"approval.tools":            "bash,write_file",
		"ui.theme":                  "dark",
		"model.demo-model.name":     "Demo Model",
		"model.demo-model.base_url": "https://models.example.test",
		"model.demo-model.model":    "demo-1",
		"model.demo-model.api_key":  "sk-model-secret",
		"model.default":             "demo-model",
	}
	for key, value := range settings {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%q): %v", key, err)
		}
	}

	writeTestFile(t, filepath.Join(root, "skills", "demo", "SKILL.md"), `---
name: demo
description: Demo skill.
---
# Demo

Body.
`)
	writeTestFile(t, filepath.Join(root, "mcp.json"), `{"servers":[{"name":"alpha","command":"alpha-cmd"},{"name":"beta","command":"beta-cmd"}]}`)
	return st, root
}

// inspectTestSnapshot is the JSON shape shared by the assertions below.
type inspectTestSnapshot struct {
	Kind    string `json:"kind"`
	Root    string `json:"root"`
	Section string `json:"section"`
	Plugins []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Source  string `json:"source"`
	} `json:"plugins"`
	Settings map[string]string `json:"settings"`
	Sessions struct {
		Count  int `json:"count"`
		Recent []struct {
			ID        string    `json:"id"`
			Title     string    `json:"title"`
			Status    string    `json:"status"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"recent"`
	} `json:"sessions"`
	Skills struct {
		Root   string `json:"root"`
		Count  int    `json:"count"`
		Error  string `json:"error"`
		Skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
		} `json:"skills"`
	} `json:"skills"`
	MCP struct {
		Source              string   `json:"source"`
		File                string   `json:"file"`
		WorkspaceConfig     string   `json:"workspace_config"`
		WorkspaceConfigNote string   `json:"workspace_config_note"`
		Count               int      `json:"count"`
		Servers             []string `json:"servers"`
		Error               string   `json:"error"`
	} `json:"mcp"`
}

func decodeInspectJSON(t *testing.T, out string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), dst); err != nil {
		t.Fatalf("decode inspect result: %v\n%s", err, out)
	}
}

func pluginByName(t *testing.T, snap inspectTestSnapshot, name string) (bool, bool, string) {
	t.Helper()
	for _, plugin := range snap.Plugins {
		if plugin.Name == name {
			return true, plugin.Enabled, plugin.Source
		}
	}
	return false, false, ""
}

func TestInspectSnapshot(t *testing.T) {
	st, root := seedInspectFixture(t)
	tool := NewInspectTool(InspectConfig{Store: st, Root: root})

	out := mustInvoke(t, tool, map[string]any{})
	var snap inspectTestSnapshot
	decodeInspectJSON(t, out, &snap)

	if snap.Kind != "inspect" {
		t.Errorf("kind = %q, want inspect", snap.Kind)
	}
	if canonicalPath(snap.Root) != canonicalPath(root) {
		t.Errorf("root = %q, want %q", snap.Root, root)
	}
	if snap.Section != "all" {
		t.Errorf("section = %q, want all", snap.Section)
	}

	// Plugin toggles: stored values win, unset values report their default.
	if found, enabled, source := pluginByName(t, snap, "web_search"); !found || enabled || source != "setting" {
		t.Errorf("web_search = found:%v enabled:%v source:%q, want true/false/setting", found, enabled, source)
	}
	if found, enabled, source := pluginByName(t, snap, "skills"); !found || !enabled || source != "setting" {
		t.Errorf("skills = found:%v enabled:%v source:%q, want true/true/setting", found, enabled, source)
	}
	if found, enabled, source := pluginByName(t, snap, "subagent"); !found || !enabled || source != "default" {
		t.Errorf("subagent = found:%v enabled:%v source:%q, want true/true/default", found, enabled, source)
	}
	if found, enabled, source := pluginByName(t, snap, "agent_teams"); !found || enabled || source != "default" {
		t.Errorf("agent_teams = found:%v enabled:%v source:%q, want true/false/default", found, enabled, source)
	}

	// Secrets are redacted by key, including per-model API keys.
	for _, key := range []string{"provider.api_key", "websearch.api_key", "model.demo-model.api_key"} {
		if got := snap.Settings[key]; got != redactedValue {
			t.Errorf("settings[%q] = %q, want %q", key, got, redactedValue)
		}
	}
	for _, secret := range []string{"sk-provider-secret", "ws-websearch-secret", "sk-model-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("inspect output leaked %q: %s", secret, out)
		}
	}
	if got := snap.Settings["provider.model"]; got != "deepseek-chat" {
		t.Errorf("provider.model = %q", got)
	}
	if got := snap.Settings["approval.tools"]; got != "bash,write_file" {
		t.Errorf("approval.tools = %q", got)
	}
	if got := snap.Settings["ui.theme"]; got != "dark" {
		t.Errorf("ui.theme = %q", got)
	}
	if got := snap.Settings["model.demo-model.model"]; got != "demo-1" {
		t.Errorf("model.demo-model.model = %q", got)
	}
	if got := snap.Settings["model.default"]; got != "demo-model" {
		t.Errorf("model.default = %q", got)
	}

	// Sessions: count plus the recent titles.
	if snap.Sessions.Count != 2 || len(snap.Sessions.Recent) != 2 {
		t.Fatalf("sessions = %+v, want 2", snap.Sessions)
	}
	titles := make(map[string]bool, 2)
	for _, session := range snap.Sessions.Recent {
		titles[session.Title] = true
		if session.ID == "" || session.Status == "" || session.UpdatedAt.IsZero() {
			t.Errorf("incomplete recent session: %+v", session)
		}
	}
	if !titles["First session"] || !titles["Second session"] {
		t.Errorf("recent titles = %v", titles)
	}

	// Skill catalog: names and descriptions only, never bodies.
	if snap.Skills.Error != "" {
		t.Fatalf("skills error = %q", snap.Skills.Error)
	}
	if snap.Skills.Root != "skills" || snap.Skills.Count != 1 || len(snap.Skills.Skills) != 1 {
		t.Fatalf("skills = %+v, want one skill under skills/", snap.Skills)
	}
	if got := snap.Skills.Skills[0]; got.Name != "demo" || got.Description != "Demo skill." || got.Path != "skills/demo/SKILL.md" {
		t.Errorf("skill = %+v", got)
	}
	if strings.Contains(out, "Body.") {
		t.Errorf("inspect output contains a skill body")
	}

	// MCP: names from mcp.json, no command or environment details. The
	// workspace trust gate is reported explicitly: no setting means the
	// default "off" and the file is marked ignored, not active.
	if snap.MCP.Error != "" {
		t.Fatalf("mcp error = %q", snap.MCP.Error)
	}
	if snap.MCP.Source != "mcp.json" || snap.MCP.File != "mcp.json" || snap.MCP.Count != 2 {
		t.Errorf("mcp = %+v", snap.MCP)
	}
	if snap.MCP.WorkspaceConfig != "off" {
		t.Errorf("mcp.workspace_config = %q, want the effective default off", snap.MCP.WorkspaceConfig)
	}
	if !strings.Contains(snap.MCP.WorkspaceConfigNote, "ignored") {
		t.Errorf("workspace_config_note = %q, want the ignored status", snap.MCP.WorkspaceConfigNote)
	}
	if strings.Join(snap.MCP.Servers, ",") != "alpha,beta" {
		t.Errorf("mcp servers = %v", snap.MCP.Servers)
	}
	if strings.Contains(out, "alpha-cmd") {
		t.Errorf("inspect output contains an MCP command")
	}
}

// TestInspectMCPWorkspaceGateStatus covers the RT-01 diagnostics follow-up:
// inspect must distinguish an ignored workspace mcp.json (default off) from an
// explicitly enabled one, and must mark the enabled source as untrusted.
func TestInspectMCPWorkspaceGateStatus(t *testing.T) {
	st := openInspectStore(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "mcp.json"), `{"servers":[{"name":"alpha","command":"alpha-cmd"}]}`)
	tool := NewInspectTool(InspectConfig{Store: st, Root: root})

	var snap inspectTestSnapshot
	decodeInspectJSON(t, mustInvoke(t, tool, map[string]any{"section": "mcp"}), &snap)
	if snap.MCP.Source != "mcp.json" || snap.MCP.WorkspaceConfig != "off" {
		t.Fatalf("default gate status = %+v, want source mcp.json + off", snap.MCP)
	}
	if !strings.Contains(snap.MCP.WorkspaceConfigNote, "ignored") {
		t.Errorf("default note = %q, want ignored", snap.MCP.WorkspaceConfigNote)
	}

	if err := st.SetSetting("mcp.workspace_config", "allow"); err != nil {
		t.Fatal(err)
	}
	decodeInspectJSON(t, mustInvoke(t, tool, map[string]any{"section": "mcp"}), &snap)
	if snap.MCP.WorkspaceConfig != "allow" {
		t.Fatalf("allowed gate status = %+v", snap.MCP)
	}
	if !strings.Contains(snap.MCP.WorkspaceConfigNote, "untrusted") || !strings.Contains(snap.MCP.WorkspaceConfigNote, "allow") {
		t.Errorf("allowed note = %q, want untrusted + allow", snap.MCP.WorkspaceConfigNote)
	}
	if strings.Contains(mustInvoke(t, tool, map[string]any{"section": "mcp"}), "alpha-cmd") {
		t.Error("inspect output leaked the MCP command")
	}

	// An explicit mcp.servers value wins; the workspace file is reported as
	// not read so the note cannot suggest two active sources.
	if err := st.SetSetting("mcp.servers", `[{"name":"beta","command":"beta-cmd"}]`); err != nil {
		t.Fatal(err)
	}
	decodeInspectJSON(t, mustInvoke(t, tool, map[string]any{"section": "mcp"}), &snap)
	if snap.MCP.Source != "settings" || snap.MCP.Count != 1 || snap.MCP.Servers[0] != "beta" {
		t.Fatalf("settings source = %+v", snap.MCP)
	}
	if !strings.Contains(snap.MCP.WorkspaceConfigNote, "wins") {
		t.Errorf("settings note = %q, want it to say the setting wins", snap.MCP.WorkspaceConfigNote)
	}
}

func TestInspectSectionFiltering(t *testing.T) {
	st, root := seedInspectFixture(t)
	tool := NewInspectTool(InspectConfig{Store: st, Root: root})

	sections := []string{"plugins", "settings", "sessions", "skills", "mcp"}
	for _, section := range sections {
		out := mustInvoke(t, tool, map[string]any{"section": section})
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			t.Fatalf("%s: invalid JSON: %v", section, err)
		}
		if _, ok := raw[section]; !ok {
			t.Errorf("%s: section is missing from %s", section, out)
		}
		for _, other := range sections {
			if other == section {
				continue
			}
			if _, ok := raw[other]; ok {
				t.Errorf("%s: unexpected section %q in %s", section, other, out)
			}
		}
		if string(raw["kind"]) != `"inspect"` {
			t.Errorf("%s: kind = %s", section, raw["kind"])
		}
	}

	// Case-insensitive selection and an explicit all.
	out := mustInvoke(t, tool, map[string]any{"section": "PLUGINS"})
	var snap inspectTestSnapshot
	decodeInspectJSON(t, out, &snap)
	if snap.Section != "plugins" || len(snap.Plugins) == 0 || snap.Settings != nil {
		t.Errorf("uppercase section result = %+v", snap)
	}

	out = mustInvoke(t, tool, map[string]any{"section": "all"})
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	for _, section := range append(sections, "kind", "root", "section") {
		if _, ok := raw[section]; !ok {
			t.Errorf("all: missing %q", section)
		}
	}

	if _, err := invokeTool(t, tool, map[string]any{"section": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown section") {
		t.Errorf("unknown section error = %v", err)
	}
	if _, err := invokeTool(t, tool, json.RawMessage(`[]`)); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Errorf("non-object args error = %v", err)
	}
}

func TestInspectEmptyStore(t *testing.T) {
	st := openInspectStore(t)
	root := t.TempDir()
	tool := NewInspectTool(InspectConfig{Store: st, Root: root})

	out := mustInvoke(t, tool, map[string]any{})
	var snap inspectTestSnapshot
	decodeInspectJSON(t, out, &snap)
	if snap.Kind != "inspect" || snap.Section != "all" {
		t.Errorf("snapshot header = %+v", snap)
	}
	if len(snap.Plugins) != len(inspectPluginCatalog) {
		t.Errorf("plugins = %d, want %d defaults", len(snap.Plugins), len(inspectPluginCatalog))
	}
	if found, enabled, source := pluginByName(t, snap, "web_search"); !found || !enabled || source != "default" {
		t.Errorf("default web_search = found:%v enabled:%v source:%q", found, enabled, source)
	}
	if len(snap.Settings) != 0 {
		t.Errorf("empty store settings = %v, want none", snap.Settings)
	}
	if snap.Sessions.Count != 0 || len(snap.Sessions.Recent) != 0 {
		t.Errorf("empty store sessions = %+v", snap.Sessions)
	}
	if snap.Skills.Error != "" || snap.Skills.Count != 0 || len(snap.Skills.Skills) != 0 {
		t.Errorf("empty store skills = %+v", snap.Skills)
	}
	if snap.MCP.Error != "" || snap.MCP.Source != "none" || snap.MCP.Count != 0 || len(snap.MCP.Servers) != 0 {
		t.Errorf("empty store mcp = %+v", snap.MCP)
	}
	var check map[string]any
	if err := json.Unmarshal([]byte(out), &check); err != nil {
		t.Errorf("empty-store snapshot is not valid JSON: %v", err)
	}
}

func TestInspectRedactionAndOutputCap(t *testing.T) {
	st := openInspectStore(t)
	root := t.TempDir()
	// MCP configuration with secrets in arguments and environment. The
	// snapshot must expose only the server name.
	mcpConfig := `[{"name":"s1","command":"cmd","args":["--token","sk-cli-secret"],"env":["API_KEY=sk-env-secret"]}]`
	for key, value := range map[string]string{
		"provider.api_key": "sk-provider-secret",
		"mcp.servers":      mcpConfig,
		"approval.tools":   strings.Repeat("tool,", 40*1024), // > 64 KiB when expanded
	} {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%q): %v", key, err)
		}
	}
	tool := NewInspectTool(InspectConfig{Store: st, Root: root})

	out := mustInvoke(t, tool, map[string]any{})
	if len(out) > inspectMaxOutput {
		t.Errorf("output is %d bytes, cap is %d", len(out), inspectMaxOutput)
	}
	var check map[string]any
	if err := json.Unmarshal([]byte(out), &check); err != nil {
		t.Errorf("capped snapshot is not valid JSON: %v", err)
	}
	var snap inspectTestSnapshot
	decodeInspectJSON(t, out, &snap)
	for _, secret := range []string{"sk-provider-secret", "sk-cli-secret", "sk-env-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("output leaked %q", secret)
		}
	}
	if got := snap.Settings["provider.api_key"]; got != redactedValue {
		t.Errorf("provider.api_key = %q, want %q", got, redactedValue)
	}
	if got := snap.Settings["mcp.servers"]; got != `["s1"]` {
		t.Errorf("settings mcp.servers = %q, want [\"s1\"]", got)
	}
	if !strings.Contains(snap.Settings["approval.tools"], "value truncated") {
		t.Errorf("large setting was not truncated: %q", snap.Settings["approval.tools"])
	}
	if snap.MCP.Source != "settings" || snap.MCP.Count != 1 || len(snap.MCP.Servers) != 1 || snap.MCP.Servers[0] != "s1" {
		t.Errorf("mcp = %+v", snap.MCP)
	}
	if strings.Contains(out, "cmd") || strings.Contains(out, "API_KEY=") {
		t.Errorf("output contains MCP command/environment details")
	}

	// Nested JSON redaction is applied to non-secret setting keys too.
	redacted := redactJSONSecrets(`{"api_key":"sk-a","nested":{"token":"t","keep":"v"},"list":[{"secret":"s","ok":1}]}`)
	for _, secret := range []string{"sk-a", `"t"`, `"s"`} {
		if strings.Contains(redacted, secret) {
			t.Errorf("redactJSONSecrets leaked %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, `"keep":"v"`) || !strings.Contains(redacted, `"ok":1`) {
		t.Errorf("redactJSONSecrets removed non-secret data: %s", redacted)
	}
}

func TestInspectMalformedSources(t *testing.T) {
	st := openInspectStore(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "skills", "bad", "SKILL.md"), "no front matter here\n")
	writeTestFile(t, filepath.Join(root, "mcp.json"), `{"servers":`)
	tool := NewInspectTool(InspectConfig{Store: st, Root: root})

	out := mustInvoke(t, tool, map[string]any{})
	var snap inspectTestSnapshot
	decodeInspectJSON(t, out, &snap)
	if snap.Skills.Error == "" || snap.Skills.Count != 0 {
		t.Errorf("malformed skill was not reported: %+v", snap.Skills)
	}
	if snap.MCP.Error == "" || snap.MCP.Source != "mcp.json" || snap.MCP.Count != 0 {
		t.Errorf("malformed mcp.json was not reported: %+v", snap.MCP)
	}

	// The mcp.servers setting wins over mcp.json; a bad value is reported and
	// never leaks raw configuration.
	if err := st.SetSetting("mcp.servers", `{"not":"an array"}`); err != nil {
		t.Fatal(err)
	}
	out = mustInvoke(t, tool, map[string]any{"section": "mcp"})
	decodeInspectJSON(t, out, &snap)
	if snap.MCP.Error == "" || snap.MCP.Source != "settings" || snap.MCP.Count != 0 {
		t.Errorf("malformed mcp.servers = %+v", snap.MCP)
	}
	if strings.Contains(out, "not") && strings.Contains(out, "an array") {
		t.Errorf("raw malformed mcp.servers leaked: %s", out)
	}

	out = mustInvoke(t, tool, map[string]any{"section": "settings"})
	decodeInspectJSON(t, out, &snap)
	if got := snap.Settings["mcp.servers"]; got != "[configured: invalid JSON]" {
		t.Errorf("settings mcp.servers = %q", got)
	}
}

func TestInspectConfigurationErrors(t *testing.T) {
	if _, err := invokeTool(t, NewInspectTool(InspectConfig{Root: t.TempDir()}), map[string]any{}); err == nil || !strings.Contains(err.Error(), "store is not configured") {
		t.Errorf("nil-store error = %v", err)
	}
	st := openInspectStore(t)
	if _, err := invokeTool(t, NewInspectTool(InspectConfig{Store: st}), map[string]any{}); err == nil || !strings.Contains(err.Error(), "workspace root is not configured") {
		t.Errorf("empty-root error = %v", err)
	}

	var nilTool *inspectTool
	if _, err := nilTool.Invoke(nil, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("nil tool error = %v", err)
	}
}
