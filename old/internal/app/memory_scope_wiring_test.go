package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestMemoryPromptScopesAndPolicyWiring proves the production prompt carries
// both memory scopes (global first) and the mode-driven policy section, and
// that the memory tool resolves the configured write modes per call.
func TestMemoryPromptScopesAndPolicyWiring(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	ws, err := st.CreateWorkspace("memory scopes", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	other, err := st.CreateWorkspace("other workspace", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	if err := st.SetSetting(session.SettingWorkspace, ws.ID); err != nil {
		t.Fatalf("SetSetting workspace: %v", err)
	}
	if _, err := st.CreateMemoryScoped(store.Memory{
		Scope: store.MemoryScopeGlobal, Kind: store.MemoryKindUser,
		Status: store.MemoryStatusActive, Content: "Prefer concise answers.",
	}); err != nil {
		t.Fatalf("CreateMemoryScoped global: %v", err)
	}
	if _, err := st.CreateMemoryScoped(store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: ws.ID,
		Kind: store.MemoryKindProject, Status: store.MemoryStatusActive,
		Content: "Release branch is release/2026.",
	}); err != nil {
		t.Fatalf("CreateMemoryScoped workspace: %v", err)
	}

	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		WorkspaceID: ws.ID,
		Mode:        runtime.ModeStandard,
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions: %v", err)
	}
	if bundle == nil {
		t.Fatal("no agent bundle")
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})

	instruction := bundle.Agent.Instruction()
	for _, want := range []string{
		"## Global memory (user, cross-workspace)",
		"- Prefer concise answers.",
		"## Workspace memory",
		"- Release branch is release/2026.",
		"## Memory policy",
		"global",
		"workspace",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q:\n%s", want, instruction)
		}
	}
	global := strings.Index(instruction, "## Global memory (user, cross-workspace)")
	workspace := strings.Index(instruction, "## Workspace memory")
	policy := strings.Index(instruction, "## Memory policy")
	if !(global >= 0 && global < workspace && workspace < policy) {
		t.Fatalf("memory section order global=%d workspace=%d policy=%d, want global < workspace < policy:\n%s",
			global, workspace, policy, instruction)
	}
	// Default settings are workspace auto / global suggest, so the policy
	// explains the pending-review path.
	if !strings.Contains(instruction, "pending review") {
		t.Fatalf("default policy is missing the suggest-mode guidance:\n%s", instruction)
	}

	// Global memories are user-level: another workspace's bundle still sees
	// them but not this workspace's project notes.
	otherBundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		WorkspaceID: other.ID,
		Mode:        runtime.ModeStandard,
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions(other): %v", err)
	}
	t.Cleanup(func() {
		if otherBundle.Close != nil {
			_ = otherBundle.Close()
		}
	})
	otherInstruction := otherBundle.Agent.Instruction()
	if !strings.Contains(otherInstruction, "- Prefer concise answers.") {
		t.Fatalf("global memory missing from another workspace's instruction:\n%s", otherInstruction)
	}
	if strings.Contains(otherInstruction, "- Release branch is release/2026.") {
		t.Fatalf("workspace memory leaked into another workspace's instruction:\n%s", otherInstruction)
	}

	// The tool path reads the same settings: global defaults to suggest, so a
	// model-written global note becomes a pending suggestion.
	tool, ok := bundle.Agent.ToolByName("memory")
	if !ok {
		t.Fatalf("memory tool is not registered; tools: %v", bundle.Agent.ToolNames())
	}
	out, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"action":"add","scope":"global","content":"User works in UTC+8."}`))
	if err != nil {
		t.Fatalf("memory add global: %v", err)
	}
	var result toolsMemoryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode memory result: %v", err)
	}
	if result.Count != 1 || len(result.Memories) != 1 {
		t.Fatalf("global add result = %s", out)
	}
	if got := result.Memories[0]; got.Scope != store.MemoryScopeGlobal ||
		got.Status != store.MemoryStatusPending || got.Source != store.MemorySourceAuto {
		t.Fatalf("global add = %+v, want global/pending/auto", got)
	}

	// Switching both modes to off removes the policy section on the next
	// recomposition.
	for _, key := range []string{session.SettingMemoryWorkspaceMode, session.SettingMemoryGlobalMode} {
		if err := st.SetSetting(key, "off"); err != nil {
			t.Fatalf("SetSetting(%s): %v", key, err)
		}
	}
	if err := bundle.Agent.SetMode(runtime.ModeStandard); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	instruction = bundle.Agent.Instruction()
	if strings.Contains(instruction, "## Memory policy") {
		t.Fatalf("policy section survived when both memory modes are off:\n%s", instruction)
	}
	// Existing memories are still injected as data; mode off only stops writes.
	if !strings.Contains(instruction, "- Prefer concise answers.") {
		t.Fatalf("global memory disappeared when writes were disabled:\n%s", instruction)
	}
}

// toolsMemoryResult mirrors the memory tool's JSON result for assertions
// without importing the tools package's unexported fields.
type toolsMemoryResult struct {
	Kind     string `json:"kind"`
	Action   string `json:"action"`
	Scope    string `json:"scope"`
	Status   string `json:"status"`
	Count    int    `json:"count"`
	Memories []struct {
		ID        string `json:"id"`
		Workspace string `json:"workspace_id"`
		Scope     string `json:"scope"`
		Kind      string `json:"kind"`
		Source    string `json:"source"`
		Status    string `json:"status"`
		Content   string `json:"content"`
	} `json:"memories"`
}
