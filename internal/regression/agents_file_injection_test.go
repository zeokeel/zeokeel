package regression

// Adversarial audit: AGENTS.md workspace-instruction injection into the agent's
// system prompt. Negative/boundary cases: default/custom file names, missing
// file ignored, empty setting disables, and over-limit content truncated with a
// warning (the truncated tail never reaches the provider).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
)

// startAgentsHarness activates the agent over a fake workspace root.
func startAgentsHarness(t *testing.T, settings map[string]string, root string) (*agentHarness, *fakeModel) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyWorkspace, contracts.WorkspaceService(&auditWorkspace{
		created: []contracts.Workspace{{ID: "ws-1", Name: "ws", Root: root}},
	}))
	for key, value := range settings {
		if err := h.settings.Set(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	model := newFakeModel(t, func([]byte) (int, string) { return 0, sseAnswer("ok") })
	harness := startAgentOn(t, h, agent.Config{}, model, approvals.ModeAuto)
	return harness, model
}

// systemPromptOf sends one prompt and returns the captured system prompt.
func systemPromptOf(t *testing.T, harness *agentHarness, model *fakeModel) string {
	t.Helper()
	sessionID := harness.newChatSession(t, "agents")
	harness.sendAndWait(t, sessionID, "hello")
	return decodeRequestContent(model.lastBody(), "system")
}

// writeAgentFile writes one instruction file below root.
func writeAgentFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestAgentsFileInjectionVariants covers default/custom/missing/disabled cases.
func TestAgentsFileInjectionVariants(t *testing.T) {
	t.Run("default AGENTS.md", func(t *testing.T) {
		root := t.TempDir()
		writeAgentFile(t, root, "AGENTS.md", "Follow the workspace rules.")
		harness, model := startAgentsHarness(t, nil, root)
		system := systemPromptOf(t, harness, model)
		if !strings.Contains(system, "Follow the workspace rules.") {
			t.Fatalf("AGENTS.md content missing from the system prompt: %q", system)
		}
		if !strings.Contains(system, "工作区指令") {
			t.Fatalf("AGENTS.md heading missing: %q", system)
		}
	})

	t.Run("custom name trimmed", func(t *testing.T) {
		root := t.TempDir()
		writeAgentFile(t, root, "CUSTOM.md", "custom instructions")
		harness, model := startAgentsHarness(t, map[string]string{
			contracts.SettingAgentsFile: "  CUSTOM.md  ",
		}, root)
		system := systemPromptOf(t, harness, model)
		if !strings.Contains(system, "custom instructions") {
			t.Fatalf("custom instruction file missing: %q", system)
		}
	})

	t.Run("missing file ignored", func(t *testing.T) {
		harness, model := startAgentsHarness(t, nil, t.TempDir())
		system := systemPromptOf(t, harness, model)
		// The agent always sends its environment/policy system block now, so
		// "missing" means no workspace-instruction block, not an empty prompt.
		if strings.Contains(system, "# 工作区指令") || strings.Contains(system, "AGENTS.md") {
			t.Fatalf("missing AGENTS.md injected workspace instructions: %q", system)
		}
	})

	t.Run("empty setting disables", func(t *testing.T) {
		root := t.TempDir()
		writeAgentFile(t, root, "AGENTS.md", "must not appear")
		harness, model := startAgentsHarness(t, map[string]string{
			contracts.SettingAgentsFile: "",
		}, root)
		if system := systemPromptOf(t, harness, model); strings.Contains(system, "must not appear") {
			t.Fatalf("empty setting still injected the file: %q", system)
		}
	})

	t.Run("over-limit content truncated", func(t *testing.T) {

		root := t.TempDir()
		head := "# rules\n" + strings.Repeat("A", 1000)
		tail := "\nTAIL-MUST-NOT-APPEAR"
		content := head + strings.Repeat("B", 40*1024) + tail
		writeAgentFile(t, root, "AGENTS.md", content)

		harness, model := startAgentsHarness(t, nil, root)
		system := systemPromptOf(t, harness, model)
		if !strings.Contains(system, "rules") {
			t.Fatalf("truncated AGENTS.md head missing: %q", system[:min(len(system), 80)])
		}
		if strings.Contains(system, "TAIL-MUST-NOT-APPEAR") {
			t.Fatal("AGENTS.md tail beyond the 32KiB cap reached the system prompt")
		}
		if !harness.h.logger.contains("truncated") {
			t.Fatalf("truncation was not logged; logs = %v", harness.h.logger.lines)
		}
	})
}
