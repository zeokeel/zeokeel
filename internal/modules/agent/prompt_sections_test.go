package agent

// Tests for the section-ized system prompt: the rendered string must stay
// byte-identical to the historical assembly while the sections carry measured
// per-part sizes and fill the context snapshot.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// legacySystemPrompt is the pre-sections assembly, kept here as the byte-level
// compatibility oracle.
func legacySystemPrompt(goos, goarch, workspaceRoot, approvalMode, mode, configuredPrompt string, toolNames map[string]bool) string {
	parts := []string{
		environmentBlockFor(goos, goarch, workspaceRoot, toolNames),
		approvalPolicyLine(approvalMode),
	}
	if modeLine := modePolicyLine(mode, toolNames); modeLine != "" {
		parts = append(parts, modeLine)
	}
	if prompt := strings.TrimSpace(configuredPrompt); prompt != "" {
		parts = append(parts, prompt)
	}
	if guidance := toolGuidanceSection(toolNames); guidance != "" {
		parts = append(parts, guidance)
	}
	return strings.Join(parts, "\n\n")
}

// TestPromptSectionsRenderMatchesLegacyAssembly pins the byte-level invariant
// across the mode/platform/tool-surface branches.
func TestPromptSectionsRenderMatchesLegacyAssembly(t *testing.T) {
	toolSurfaces := []map[string]bool{
		nil,
		{"bash": true, "read_file": true, "write_file": true, "glob": true, "grep": true, "web_fetch": true},
		{"pwsh": true, "read_file": true, "ask_user": true, "present": true},
		{"read_file": true, "memory": true, "skill": true},
	}
	modes := []string{"", contracts.AgentModePlan, contracts.ChatModeMinimal, contracts.ChatModeCreator}
	configuredPrompts := []string{"", "base system prompt", "line one\nline two\n"}
	for _, toolNames := range toolSurfaces {
		for _, mode := range modes {
			for _, configured := range configuredPrompts {
				want := legacySystemPrompt("linux", "amd64", "/work/project", contracts.PermissionWorkspace,
					mode, configured, toolNames)
				got := systemPromptForRun("linux", "amd64", "/work/project", contracts.PermissionWorkspace,
					mode, configured, toolNames)
				if got != want {
					t.Fatalf("sections render drifted for mode=%q configured=%q tools=%v:\n got: %q\nwant: %q",
						mode, configured, toolNames, got, want)
				}
			}
		}
	}
}

// TestPromptSectionsShape pins the ordered section IDs and the measured fields.
func TestPromptSectionsShape(t *testing.T) {
	toolNames := map[string]bool{"bash": true, "read_file": true, "ask_user": true}
	configured := []contracts.PromptSection{{
		ID: PromptSectionConfig, Source: "config", Text: "base system prompt",
	}}
	sections := promptSectionsForRun("linux", "amd64", "/work", contracts.PermissionWorkspace,
		"", configured, toolNames)
	ids := make([]string, 0, len(sections))
	for _, section := range sections {
		ids = append(ids, section.ID)
		if section.Bytes != len(section.Text) {
			t.Fatalf("section %q Bytes = %d, want len(Text) = %d", section.ID, section.Bytes, len(section.Text))
		}
		if section.Tokens != rig.EstimateTextTokens(section.Text) {
			t.Fatalf("section %q Tokens = %d, want the estimate", section.ID, section.Tokens)
		}
		if section.Source == "" {
			t.Fatalf("section %q has no source", section.ID)
		}
	}
	wantIDs := []string{PromptSectionIdentity, PromptSectionEnvironment, PromptSectionWorkingDirectory,
		PromptSectionApproval, PromptSectionConfig, PromptSectionToolGuidance}
	if strings.Join(ids, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("section order = %v, want %v", ids, wantIDs)
	}
	rendered := renderPromptSections(sections)
	if want := systemPromptForRun("linux", "amd64", "/work", contracts.PermissionWorkspace, "", "base system prompt", toolNames); rendered != want {
		t.Fatalf("renderPromptSections = %q, want %q", rendered, want)
	}
}

// TestBuildRunSystemPromptSectionsMatchesLegacy covers the real service path:
// base prompt + AGENTS.md + preset produce the same bytes as the historical
// assembly, and their sections carry the injection sources.
func TestBuildRunSystemPromptSectionsMatchesLegacy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Follow the workspace rules."), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "m",
		contracts.SettingAgentPresets: `[{"id":"p1","name":"P1","persona":"You are terse.","prompt":"Prefer tables."}]`,
	})
	workspace := &fakeWorkspace{}
	svc, _ := newAgentsFileService(t, settings, workspace, nil)
	workspace.setCurrent(contracts.Workspace{ID: "ws-1", Root: root}, true)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, "p1"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	toolNames := map[string]bool{"bash": true, "read_file": true}
	cfg := svc.Config()

	prompt, sections := svc.buildRunSystemPromptSections(context.Background(), sessionID, cfg, toolNames, root)

	// The historical chain: base → AGENTS.md → preset, then the old assembly.
	legacyCfg := svc.withWorkspaceInstructions(cfg, root)
	legacyCfg = svc.withPresetInstructions(sessionID, legacyCfg)
	want := legacySystemPrompt("linux", "amd64", root, svc.effectiveApprovalMode(sessionID),
		svc.runtimeMode(), legacyCfg.SystemPrompt, toolNames)
	if prompt != want {
		t.Fatalf("service prompt drifted from the legacy assembly:\n got: %q\nwant: %q", prompt, want)
	}
	if rendered := renderPromptSections(sections); rendered != prompt {
		t.Fatalf("prompt sections do not render back to the prompt")
	}

	sources := make(map[string]string, len(sections))
	for _, section := range sections {
		sources[section.ID] = section.Source
	}
	if sources[PromptSectionConfig] != "config" || sources[PromptSectionAgentsMD] != "workspace" ||
		sources[PromptSectionPresetPersona] != "preset" || sources[PromptSectionPresetPrompt] != "preset" {
		t.Fatalf("section sources = %v, want config/workspace/preset", sources)
	}
	if !strings.Contains(prompt, "Follow the workspace rules.") || !strings.Contains(prompt, "You are terse.") {
		t.Fatalf("rendered prompt lacks the injected sections: %q", prompt)
	}
}

// TestPromptSectionHashIsStable pins the snapshot hash helper.
func TestPromptSectionHashIsStable(t *testing.T) {
	first := promptSectionHash("same text")
	second := promptSectionHash("same text")
	if first == "" || first != second {
		t.Fatalf("promptSectionHash = %q/%q, want a stable non-empty hash", first, second)
	}
	if promptSectionHash("other text") == first {
		t.Fatal("promptSectionHash collided for different text")
	}
}
