package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

func newToolStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, dir
}

// TestPluginTogglesRemoveTools proves every built-in and experimental switch
// is read at build time: turning a plugin off removes its tools instead of
// only persisting a preference.
func TestPluginTogglesRemoveTools(t *testing.T) {
	cases := []struct {
		key   string
		value string
		gone  []string
		kept  []string
	}{
		{"plugin.shell.enabled", "false", []string{"bash", "write_file", "edit_file", "terminal_open", "str_replace_editor"}, []string{"read_file", "glob", "grep"}},
		{"plugin.subagent.enabled", "false", []string{"subagent", "workflow", "ralph"}, []string{"goal"}},
		{"plugin.agent_teams.enabled", "false", []string{"list_subagent_models", "spawn_team", "send_message"}, []string{"goal"}},
		{"plugin.developer_tools.enabled", "false", []string{"inspect"}, []string{"read_file"}},
		{"plugin.skills.enabled", "false", nil, []string{"read_file"}},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			st, dir := newToolStore(t)
			if err := st.SetSetting(tc.key, tc.value); err != nil {
				t.Fatal(err)
			}
			reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
			if err != nil {
				t.Fatalf("register: %v", err)
			}
			if closeFn != nil {
				t.Cleanup(func() { _ = closeFn() })
			}
			have := make(map[string]bool)
			for _, tl := range reg.List() {
				have[tl.Name()] = true
			}
			for _, name := range tc.gone {
				if have[name] {
					t.Errorf("%s=%s kept %q", tc.key, tc.value, name)
				}
			}
			for _, name := range tc.kept {
				if !have[name] {
					t.Errorf("%s=%s dropped %q", tc.key, tc.value, name)
				}
			}
		})
	}
}

// TestExperimentalTogglesAddTools is the complement: enabling Agent Teams and
// Developer Tools registers their tools.
func TestExperimentalTogglesAddTools(t *testing.T) {
	st, dir := newToolStore(t)
	for _, key := range []string{"plugin.agent_teams.enabled", "plugin.developer_tools.enabled"} {
		if err := st.SetSetting(key, "true"); err != nil {
			t.Fatal(err)
		}
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	have := make(map[string]bool)
	for _, tl := range reg.List() {
		have[tl.Name()] = true
	}
	for _, name := range []string{"spawn_team", "list_subagent_models", "inspect"} {
		if !have[name] {
			t.Errorf("enabled toggle did not register %q", name)
		}
	}
}

// TestWebSearchUIKeysRegisterTool proves the keys the plugin editor writes
// (plugin.web_search.*) are the keys the runtime reads.
func TestWebSearchUIKeysRegisterTool(t *testing.T) {
	st, dir := newToolStore(t)
	if err := st.SetSetting("plugin.web_search.endpoint", "http://127.0.0.1:9/search"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("plugin.web_search.max_results", "3"); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	have := false
	for _, tl := range reg.List() {
		if tl.Name() == "web_search" {
			have = true
		}
	}
	if !have {
		t.Fatal("plugin.web_search.endpoint did not register web_search")
	}
}

// TestMCPGating covers the disabled, no-server and malformed cases.
func TestMCPGating(t *testing.T) {
	t.Run("disabled does not parse config", func(t *testing.T) {
		st, dir := newToolStore(t)
		if err := st.SetSetting("plugin.mcp.enabled", "false"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetSetting("mcp.servers", "{not json"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := registerAllTools(st, dir, func(k, e, d string) string { return d }); err != nil {
			t.Fatalf("disabled MCP read the config: %v", err)
		}
	})
	t.Run("no servers registers no resource tools", func(t *testing.T) {
		st, dir := newToolStore(t)
		reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
		if err != nil {
			t.Fatal(err)
		}
		if closeFn != nil {
			t.Cleanup(func() { _ = closeFn() })
		}
		for _, tl := range reg.List() {
			if strings.HasPrefix(tl.Name(), "mcp_") {
				t.Errorf("no-server registry has %q", tl.Name())
			}
		}
	})
	t.Run("malformed config surfaces", func(t *testing.T) {
		st, dir := newToolStore(t)
		if err := st.SetSetting("mcp.servers", "{not json"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := registerAllTools(st, dir, func(k, e, d string) string { return d }); err == nil {
			t.Fatal("malformed mcp.servers was swallowed")
		}
	})
}

// TestSkillsPromptMatchesTool proves the prompt catalog comes from the same
// resolved directory and plugin state as the registered skill tool.
func TestSkillsPromptMatchesTool(t *testing.T) {
	st, dir := newToolStore(t)
	custom := filepath.Join(dir, "custom-skills")
	if err := os.MkdirAll(filepath.Join(custom, "custom"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: custom\ndescription: A custom skill.\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(custom, "custom", "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("skills.root", custom); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string {
		if k == "skills.root" {
			return custom
		}
		return d
	})
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	out := composeInstruction(st, dir, reg, custom, session.PermissionWorkspace, "")
	if !strings.Contains(out, "custom") {
		t.Fatalf("prompt does not list the custom skill: %.400s", out)
	}
	if strings.Contains(out, "- demo") {
		t.Fatal("prompt still lists the default skills directory")
	}
	// Disabled plugin: no catalog even though the directory exists.
	off := composeInstruction(st, dir, reg, "", session.PermissionWorkspace, "")
	if strings.Contains(off, "custom") && strings.Contains(off, "A custom skill") {
		t.Fatalf("disabled skills plugin still listed the catalog: %.400s", off)
	}
}

// TestBuildAgentReadsRuntimeSettings checks the values the plugin editors
// persist reach the runtime configuration.
func TestBuildAgentReadsRuntimeSettings(t *testing.T) {
	st, dir := newToolStore(t)
	settings := map[string]string{
		"provider.api_key":                 "test-key",
		"provider.model":                   "test-model",
		"plugin.agent_loop.max_iterations": "7",
		"plugin.subagent.depth":            "4",
		"plugin.subagent.model":            "child-model",
		"plugin.shell.timeout_seconds":     "45",
		"plugin.shell.output_cap_kb":       "32",
	}
	for k, v := range settings {
		if err := st.SetSetting(k, v); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := buildAgent(st, dir, "instruction")
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	if bundle == nil || bundle.Agent == nil {
		t.Fatal("buildAgent returned no agent")
	}
	defer func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	}()
	if got := bundle.Agent.MaxIterations(); got != 7 {
		t.Errorf("MaxIterations = %d, want 7", got)
	}
	if got := bundle.Agent.MaxSubagentDepth(); got != 4 {
		t.Errorf("MaxSubagentDepth = %d, want 4", got)
	}
	if got := bundle.Agent.SubagentModel(); got != "child-model" {
		t.Errorf("SubagentModel = %q", got)
	}
	// Agent loop disabled: the cap collapses to a single round trip.
	if err := st.SetSetting("plugin.agent_loop.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	bundle2, err := buildAgent(st, dir, "instruction")
	if err != nil {
		t.Fatal(err)
	}
	if bundle2 != nil && bundle2.Agent != nil && bundle2.Agent.MaxIterations() != 1 {
		t.Errorf("disabled agent loop MaxIterations = %d, want 1", bundle2.Agent.MaxIterations())
	}
}

// TestPermissionModePolicy covers Read Only tool availability and Full access
// bypassing approval.
func TestPermissionModePolicy(t *testing.T) {
	st, dir := newToolStore(t)
	if err := st.SetSetting(session.SettingPermission, session.PermissionAsk); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	have := make(map[string]bool)
	for _, tl := range reg.List() {
		have[tl.Name()] = true
	}
	for _, name := range []string{"write_file", "edit_file", "bash", "str_replace_editor"} {
		if have[name] {
			t.Errorf("Read Only registered mutating tool %q", name)
		}
	}
	if !have["read_file"] || !have["glob"] || !have["grep"] {
		t.Errorf("Read Only dropped read tools: %v", have)
	}

	// Full access bypasses the approval gate.
	if err := st.SetSetting(session.SettingPermission, session.PermissionFull); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("provider.api_key", "test-key"); err != nil {
		t.Fatal(err)
	}
	bundle, err := buildAgent(st, dir, "instruction")
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	if bundle == nil || bundle.Gate == nil {
		t.Fatal("no approval gate")
	}
	if bundle.Gate.RequireApproval("bash", nil) {
		t.Error("Full access still requires approval for bash")
	}
	// Workspace Write still asks.
	if err := st.SetSetting(session.SettingPermission, session.PermissionWorkspace); err != nil {
		t.Fatal(err)
	}
	ws, err := buildAgent(st, dir, "instruction")
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil || ws.Gate == nil || !ws.Gate.RequireApproval("bash", nil) {
		t.Error("Workspace Write does not ask for bash approval")
	}
	if ws.Agent != nil && !strings.Contains(ws.Agent.Instruction(), "workspace-write") {
		t.Error("prompt does not describe the workspace-write policy")
	}
	if err := st.SetSetting(session.SettingPermission, session.PermissionAsk); err != nil {
		t.Fatal(err)
	}
	ro, err := buildAgent(st, dir, "instruction")
	if err != nil {
		t.Fatal(err)
	}
	if ro.Agent != nil {
		if !strings.Contains(ro.Agent.Instruction(), "read-only") {
			t.Error("prompt does not describe the read-only policy")
		}
		for _, name := range ro.Agent.ToolNames() {
			if name == "write_file" || name == "bash" {
				t.Errorf("Read Only agent registered %q", name)
			}
		}
	}
}

// TestProductionToolsUseWorkspaceRoot proves the production registry is built
// against a user workspace, not the data directory.
func TestProductionToolsUseWorkspaceRoot(t *testing.T) {
	st, dataDir := newToolStore(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "user-file.txt"), []byte("from workspace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "user-file.txt"), []byte("from data dir\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, workspace, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	for _, tl := range reg.List() {
		if tl.Name() != "read_file" {
			continue
		}
		out, err := tl.Invoke(context.Background(), []byte(`{"path":"user-file.txt"}`))
		if err != nil {
			t.Fatalf("read_file: %v", err)
		}
		if strings.TrimSpace(out) != "from workspace" {
			t.Fatalf("read_file returned %q, want the workspace file", out)
		}
		return
	}
	t.Fatal("read_file not registered")
}

// TestWorkingDirectorySharedWithFSTools covers the working_directory contract:
// the fs and bash tools follow the shared current directory.
func TestWorkingDirectorySharedWithFSTools(t *testing.T) {
	st, dir := newToolStore(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "hello.txt"), []byte("inside sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	invoke := func(name string, args string) string {
		for _, tl := range reg.List() {
			if tl.Name() == name {
				out, err := tl.Invoke(context.Background(), []byte(args))
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				return out
			}
		}
		t.Fatalf("%s not registered", name)
		return ""
	}
	invoke("working_directory", `{"subdir":"sub"}`)
	if out := invoke("read_file", `{"path":"hello.txt"}`); !strings.Contains(out, "inside sub") {
		t.Fatalf("read_file after cd read %q", out)
	}
	if out := invoke("bash", `{"command":"cat hello.txt"}`); !strings.Contains(out, "inside sub") {
		t.Fatalf("bash after cd printed %q", out)
	}
	if out := invoke("glob", `{"pattern":"*.txt"}`); !strings.Contains(out, "hello.txt") {
		t.Fatalf("glob after cd returned %q", out)
	}
}

// TestPromptToolParity guards the two prompt sections that described tools
// this build does not have: the goal section now names the real "goal" tool
// and its actions, and workflow describes the steps list instead of a
// JavaScript runner.
func TestPromptToolParity(t *testing.T) {
	st, dir := newToolStore(t)
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	out := composeInstruction(st, dir, reg, "", session.PermissionWorkspace, "")
	for _, bad := range []string{"JavaScript script", "create_goal", "update_goal", "blocked_reason"} {
		if strings.Contains(out, bad) {
			t.Errorf("prompt mentions %q, which no registered tool supports", bad)
		}
	}
	for _, want := range []string{"goal tool", "steps list"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	// The goal section is gated on the registered name, not the aliases.
	haveGoal := false
	for _, tl := range reg.List() {
		if tl.Name() == "goal" {
			haveGoal = true
		}
	}
	if !haveGoal {
		t.Fatal("goal tool not registered")
	}
}

// TestWorkspaceRootForStoreResolvesSelectedRoot covers the startup build:
// before a Controller exists the agent must use the selected workspace's root,
// not the data directory.
func TestWorkspaceRootForStoreResolvesSelectedRoot(t *testing.T) {
	st, dataDir := newToolStore(t)
	root := t.TempDir()
	ws, err := st.CreateWorkspace("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(session.SettingWorkspace, ws.ID); err != nil {
		t.Fatal(err)
	}
	if got := workspaceRootForStore(st, dataDir); got != root {
		t.Fatalf("workspaceRootForStore = %q, want %q", got, root)
	}
	// A default workspace with an empty root falls back to the data dir.
	empty, err := st.CreateWorkspace("default", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(session.SettingWorkspace, empty.ID); err != nil {
		t.Fatal(err)
	}
	if got := workspaceRootForStore(st, dataDir); got != dataDir {
		t.Fatalf("empty-root fallback = %q, want %q", got, dataDir)
	}
}

// TestWebSearchStubEndpointServesTool is the end-to-end setup check: the keys
// the editor now saves (adapter URL plus websearch.api_key) register a
// web_search tool that actually queries the configured adapter with the
// Bearer token and returns its results.
func TestWebSearchStubEndpointServesTool(t *testing.T) {
	var gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("stub decode request: %v", err)
		}
		gotQuery = req.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"title":"ZeoKeel","url":"https://example.com/zeokeel","snippet":"a Go harness"}]}`)
	}))
	defer srv.Close()

	st, dir := newToolStore(t)
	for key, value := range map[string]string{
		"plugin.web_search.endpoint":    srv.URL,
		"websearch.api_key":             "stub-secret",
		"plugin.web_search.max_results": "3",
	} {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%s): %v", key, err)
		}
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	var web runtime.Tool
	for _, tool := range reg.List() {
		if tool.Name() == "web_search" {
			web = tool
		}
	}
	if web == nil {
		t.Fatalf("configured adapter did not register web_search: %v", reg.List())
	}
	out, err := web.Invoke(context.Background(), json.RawMessage(`{"query":"zeokeel"}`))
	if err != nil {
		t.Fatalf("web_search invoke: %v", err)
	}
	if gotQuery != "zeokeel" {
		t.Fatalf("adapter query = %q, want zeokeel", gotQuery)
	}
	if gotAuth != "Bearer stub-secret" {
		t.Fatalf("adapter Authorization = %q, want Bearer stub-secret", gotAuth)
	}
	if !strings.Contains(out, "ZeoKeel") || !strings.Contains(out, "https://example.com/zeokeel") {
		t.Fatalf("web_search result = %q, want the stub hit", out)
	}
}
