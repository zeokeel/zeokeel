package agent

// Role-scoped MCP servers (M3b): the role says which servers it may use, and the
// rule holds in both places (surface + gate). Skills are scoped through the same
// preset row and enforced by the toolkit's skill store, which asks this service
// for the session's allowed names.

import (
	"context"
	"strings"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func scopeService(t *testing.T, preset contracts.AgentPreset) (*service, string) {
	t.Helper()
	raw, err := contracts.EncodeAgentPresets([]contracts.AgentPreset{preset})
	if err != nil {
		t.Fatalf("encode presets: %v", err)
	}
	registry := rigtools.NewRegistry(
		presetTool{name: "bash"},
		presetTool{name: "mcp__files__read"},
		presetTool{name: "mcp__files__write"},
		presetTool{name: "mcp__browser__open"},
	)
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPresets: raw})
	svc := newService(Config{SystemPrompt: "base"}, registry, settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, preset.ID); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	return svc, sessionID
}

func TestMCPServerScopeFiltersTheSurface(t *testing.T) {
	svc, sessionID := scopeService(t, contracts.AgentPreset{
		ID: "files-only", Name: "只有文件 MCP", MCPServers: []string{"files"},
	})
	got := map[string]bool{}
	for _, tool := range svc.filterSessionTools(sessionID, svc.enabledRegistryTools()) {
		got[tool.Name()] = true
	}
	if !got["mcp__files__read"] || !got["mcp__files__write"] {
		t.Fatalf("surface = %v, want the allowed server's tools", got)
	}
	if got["mcp__browser__open"] {
		t.Fatalf("surface = %v, want the other server's tool removed", got)
	}
	if !got["bash"] {
		t.Fatalf("surface = %v, want non-MCP tools untouched", got)
	}
}

func TestMCPServerScopeRefusesAtTheGate(t *testing.T) {
	svc, sessionID := scopeService(t, contracts.AgentPreset{
		ID: "files-only", Name: "只有文件 MCP", MCPServers: []string{"files"},
	})
	preset, ok := svc.agentPresetForSession(sessionID)
	if !ok {
		t.Fatal("preset did not resolve")
	}
	if denial := presetToolsDenialForPreset(preset, "mcp__browser__open"); denial == "" {
		t.Fatal("gate allowed a tool from an unlisted MCP server")
	} else if !strings.Contains(denial, "browser") {
		t.Fatalf("denial = %q, want the server named", denial)
	}
	if denial := presetToolsDenialForPreset(preset, "mcp__files__read"); denial != "" {
		t.Fatalf("gate refused an allowed server's tool (%q)", denial)
	}
	if denial := presetToolsDenialForPreset(preset, "bash"); denial != "" {
		t.Fatalf("gate refused a non-MCP tool (%q)", denial)
	}
}

func TestMCPServerScopeEmptyMeansEveryServer(t *testing.T) {
	svc, sessionID := scopeService(t, contracts.AgentPreset{ID: "open", Name: "不限"})
	got := map[string]bool{}
	for _, tool := range svc.filterSessionTools(sessionID, svc.enabledRegistryTools()) {
		got[tool.Name()] = true
	}
	if !got["mcp__browser__open"] || !got["mcp__files__read"] {
		t.Fatalf("surface = %v, want every server when the role names none", got)
	}
}

// The skill scope the toolkit asks for: a session's role names its skills, and a
// role that names none answers "unrestricted" rather than "none allowed".
func TestSkillScopeFollowsTheSessionRole(t *testing.T) {
	svc, sessionID := scopeService(t, contracts.AgentPreset{
		ID: "deployer", Name: "发布", Skills: []string{"deploy", "rollback"},
	})
	ctx := contracts.WithAgentSession(context.Background(), sessionID)
	names, restricted := svc.AllowedSkills(ctx)
	if !restricted || strings.Join(names, ",") != "deploy,rollback" {
		t.Fatalf("scope = (%v, %v), want the role's two skills", names, restricted)
	}

	open, openSession := scopeService(t, contracts.AgentPreset{ID: "open", Name: "不限"})
	sessionCtx := contracts.WithAgentSession(context.Background(), openSession)
	if names, restricted := open.AllowedSkills(sessionCtx); restricted || len(names) != 0 {
		t.Fatalf("scope = (%v, %v), want unrestricted", names, restricted)
	}
}
