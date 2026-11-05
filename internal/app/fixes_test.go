package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/mcp"
	"github.com/zeokeel/zeokeel/internal/session"
)

// configuredProvider makes buildAgent's provider path reachable without a
// network call: only construction reads these keys.
func configuredProvider(t *testing.T, st interface{ SetSetting(string, string) error }) {
	t.Helper()
	for k, v := range map[string]string{
		"provider.api_key": "test-key",
		"provider.model":   "test-model",
		"ui.language":      "en",
	} {
		if err := st.SetSetting(k, v); err != nil {
			t.Fatalf("SetSetting(%s): %v", k, err)
		}
	}
}

func toolNames(reg interface {
	List() []interface{ Name() string }
}) []string {
	return nil
}

func registryHas(bundle *AgentBundle, name string) bool {
	for _, tool := range bundle.Agent.ToolNames() {
		if tool == name {
			return true
		}
	}
	return false
}

// TestDefaultApprovalGatesMutatingTools covers the tools the default patterns
// used to miss: process, network, editor and worktree tools.
func TestDefaultApprovalGatesMutatingTools(t *testing.T) {
	st, _ := newToolStore(t)
	patterns := approvalToolPatterns(st)
	for _, name := range []string{
		"bash", "write_file", "edit_file", "str_replace_editor",
		"terminal_open", "terminal_send", "terminal_signal",
		"job_start", "job_kill", "pwsh", "enter_worktree", "exit_worktree",
		"web_search", "web_fetch", "mcp__demo__echo",
	} {
		if !toolNeedsApproval(name, patterns) {
			t.Errorf("tool %q is not gated under Workspace Write", name)
		}
	}
	// Read-only polling and listing must stay ungated: the prompt tells the
	// model to poll job_output, and repeated approval prompts broke normal
	// background-job use.
	for _, name := range []string{
		"read_file", "glob", "grep", "todo", "goal",
		"terminal_read", "terminal_list", "job_output", "job_list",
	} {
		if toolNeedsApproval(name, patterns) {
			t.Errorf("read-only tool %q unexpectedly requires approval", name)
		}
	}
}

// TestBuildAgentUsesSessionPermission proves the per-session override reaches
// tool registration: Read Only omits bash/write_file while Full access keeps
// them and bypasses the approval gate.
func TestBuildAgentUsesSessionPermission(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		wantBash bool
	}{
		{session.PermissionAsk, false},
		{session.PermissionWorkspace, true},
		{session.PermissionFull, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			st, dir := newToolStore(t)
			configuredProvider(t, st)
			bundle, err := buildAgentWithPermission(st, dir, "instruction", tc.mode)
			if err != nil {
				t.Fatalf("buildAgentWithPermission: %v", err)
			}
			t.Cleanup(func() {
				if bundle.Close != nil {
					_ = bundle.Close()
				}
			})
			if got := registryHas(bundle, "bash"); got != tc.wantBash {
				t.Fatalf("%s: has bash = %v, want %v (tools: %v)", tc.mode, got, tc.wantBash, bundle.Agent.ToolNames())
			}
			require := bundle.Gate.RequireApproval("bash", nil)
			if tc.mode == session.PermissionFull && require {
				t.Fatalf("Full access still asks for bash approval")
			}
			if tc.mode != session.PermissionFull && !require {
				t.Fatalf("%s: bash does not require approval", tc.mode)
			}
		})
	}
}

// TestPromptModeIsReachable proves the mode-gated sections actually render in
// the production prompt, not only in prompt package unit tests.
func TestPromptModeIsReachable(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, st interface{ SetSetting(string, string) error })
		expect string
	}{
		{"team", func(t *testing.T, st interface{ SetSetting(string, string) error }) {
			if err := st.SetSetting("plugin.agent_teams.enabled", "true"); err != nil {
				t.Fatal(err)
			}
		}, "Agent Teams is active"},
		{"plan", func(t *testing.T, st interface{ SetSetting(string, string) error }) {
			if err := st.SetSetting("agent.collaboration_mode", "plan"); err != nil {
				t.Fatal(err)
			}
		}, "Plan mode is active"},
		{"subagent", func(t *testing.T, st interface{ SetSetting(string, string) error }) {
			if err := st.SetSetting("agent.collaboration_mode", "subagent"); err != nil {
				t.Fatal(err)
			}
		}, "You are a delegated subagent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, dir := newToolStore(t)
			configuredProvider(t, st)
			tc.setup(t, st)
			bundle, err := buildAgent(st, dir, "instruction")
			if err != nil {
				t.Fatalf("buildAgent: %v", err)
			}
			t.Cleanup(func() {
				if bundle.Close != nil {
					_ = bundle.Close()
				}
			})
			if !strings.Contains(bundle.Agent.Instruction(), tc.expect) {
				t.Fatalf("prompt for mode %s missing %q:\n%s", tc.name, tc.expect, bundle.Agent.Instruction())
			}
		})
	}
}

// TestMalformedSkillKeepsSkillTool proves one bad SKILL.md no longer removes
// the whole skill capability.
func TestMalformedSkillKeepsSkillTool(t *testing.T) {
	st, dir := newToolStore(t)
	root := filepath.Join(dir, "skills")
	if err := os.MkdirAll(filepath.Join(root, "good"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "good", "SKILL.md"),
		[]byte("---\nname: good\ndescription: Good skill.\n---\nBody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad", "SKILL.md"),
		[]byte("---\nname: wrong-name\n---\nBody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("skills.root", root); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("malformed skill failed the whole registry: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	found := false
	for _, tool := range reg.List() {
		if tool.Name() == "skill" {
			found = true
		}
	}
	if !found {
		var names []string
		for _, tool := range reg.List() {
			names = append(names, tool.Name())
		}
		t.Fatalf("skill tool was dropped because one SKILL.md was malformed: %v", names)
	}
}

func toolNamesOf(reg interface {
	List() []interface{ Name() string }
}) []string {
	return nil
}

// TestBrokenMCPServerKeepsBuiltins proves a failed MCP connect no longer
// aborts the whole tool registry.
func TestBrokenMCPServerKeepsBuiltins(t *testing.T) {
	st, dir := newToolStore(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := mcp.Config{Servers: []mcp.ServerConfig{
		{Name: "broken", Command: filepath.Join(t.TempDir(), "definitely-missing-mcp")},
		{Name: "good", Command: exe, Args: []string{"-test.run=^$"},
			Env: []string{appMCPHelperEnv + "=server"}},
	}}
	raw, err := json.Marshal(cfg.Servers)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("mcp.servers", string(raw)); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("broken MCP server failed the registry: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	names := make(map[string]bool)
	for _, tool := range reg.List() {
		names[tool.Name()] = true
	}
	if !names["bash"] || !names["read_file"] {
		t.Fatalf("built-in tools lost with a broken MCP server: %v", names)
	}
	if !names["mcp__good__echo"] {
		t.Fatalf("good MCP server's tool missing: %v", names)
	}
}

// TestFailedMCPDoesNotExposeResourceTools covers the always-failing MCP
// resource/prompt tools: when every configured server fails to connect, the
// manager is non-nil but no MCP tools should be registered.
func TestFailedMCPDoesNotExposeResourceTools(t *testing.T) {
	st, dir := newToolStore(t)
	cfg := mcp.Config{Servers: []mcp.ServerConfig{
		{Name: "broken", Command: filepath.Join(t.TempDir(), "definitely-missing-mcp")},
	}}
	raw, err := json.Marshal(cfg.Servers)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("mcp.servers", string(raw)); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	for _, tool := range reg.List() {
		switch tool.Name() {
		case "mcp_list_resources", "mcp_read_resource", "mcp_list_prompts", "mcp_get_prompt":
			t.Fatalf("registered always-failing MCP resource tool %q with no connected server", tool.Name())
		}
	}
}

// TestAgentTeamsWithoutSubagentHasToolsAndPrompt covers the toggle
// combination that used to tell the model to use team tools that were never
// registered: Agent Teams on, Subagent off.
func TestAgentTeamsWithoutSubagentHasToolsAndPrompt(t *testing.T) {
	st, dir := newToolStore(t)
	for key, value := range map[string]string{
		"plugin.subagent.enabled":    "false",
		"plugin.agent_teams.enabled": "true",
		"ui.language":                "en",
	} {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	have := make(map[string]bool)
	for _, tool := range reg.List() {
		have[tool.Name()] = true
	}
	if have["subagent"] {
		t.Fatal("Subagent plugin off still registered the subagent tool")
	}
	for _, name := range []string{"spawn_team", "list_subagent_models", "send_message"} {
		if !have[name] {
			t.Errorf("Agent Teams on without Subagent did not register %q", name)
		}
	}
	mode := collaborationMode(st)
	if mode != "team" {
		t.Fatalf("collaborationMode = %q, want team", mode)
	}
	prompt := composeInstruction(st, dir, reg, "", session.PermissionWorkspace, mode)
	if !strings.Contains(prompt, "Agent Teams is active") {
		t.Fatalf("prompt missing the team section with the team tools registered:\n%s", prompt)
	}
}

// TestShellOffPromptIsReadOnly covers the prompt claiming workspace-write
// while the Shell plugin removed every mutation tool.
func TestShellOffPromptIsReadOnly(t *testing.T) {
	st, dir := newToolStore(t)
	if err := st.SetSetting("plugin.shell.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("ui.language", "en"); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	have := make(map[string]bool)
	for _, tool := range reg.List() {
		have[tool.Name()] = true
	}
	for _, name := range []string{"write_file", "edit_file", "bash"} {
		if have[name] {
			t.Errorf("Shell plugin off still registered %q", name)
		}
	}
	prompt := composeInstruction(st, dir, reg, "", session.PermissionWorkspace, "")
	if !strings.Contains(prompt, "Current ZeoKeel file policy: read-only") {
		t.Fatalf("shell-off prompt does not say read-only:\n%s", prompt)
	}
	if strings.Contains(prompt, "Current ZeoKeel file policy: workspace-write") {
		t.Fatalf("shell-off prompt still claims workspace-write:\n%s", prompt)
	}
}
