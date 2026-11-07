package agent

// Tests for the AGENTS.md workspace-instruction injection.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func newAgentsFileService(t *testing.T, settings *fakeSettings, workspace *fakeWorkspace, logger *fakeLogger) (*service, *captureProvider) {
	t.Helper()
	if settings == nil {
		settings = newFakeSettings(nil)
	}
	if logger == nil {
		logger = &fakeLogger{}
	}
	provider := &captureProvider{}
	svc := newService(Config{SystemPrompt: "base system prompt"}, rigtools.NewRegistry(),
		settings, logger, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	if workspace != nil {
		svc.workspace = workspace
	}
	return svc, provider
}

func runAndSystemPrompt(t *testing.T, svc *service, provider *captureProvider, workspace *fakeWorkspace, root string) string {
	t.Helper()
	if workspace != nil {
		workspace.setCurrent(contracts.Workspace{ID: "ws-1", Root: root}, true)
	}
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	request := provider.lastRequest()
	for _, message := range request.Messages {
		if message.Role == rig.RoleSystem {
			return message.Content[0].Text
		}
	}
	return ""
}

func TestAgentsFileInjectedIntoSystemPrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Follow the workspace rules."), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), workspace, nil)
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	if !strings.Contains(system, "base system prompt") {
		t.Fatalf("base prompt missing: %q", system)
	}
	if !strings.Contains(system, "# 工作区指令（AGENTS.md）") ||
		!strings.Contains(system, "Follow the workspace rules.") {
		t.Fatalf("workspace instructions missing: %q", system)
	}
	if !strings.HasPrefix(system, "base system prompt\n\n# 工作区指令（AGENTS.md）\n\n") {
		t.Fatalf("injection shape = %q", system)
	}
}

func TestAgentsFileMissingIsIgnored(t *testing.T) {
	workspace := &fakeWorkspace{}
	root := t.TempDir()
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), workspace, nil)
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	if strings.Contains(system, "# 工作区指令") {
		t.Fatalf("missing AGENTS.md injected: %q", system)
	}
	if system != "base system prompt" {
		t.Fatalf("system prompt = %q", system)
	}
}

func TestAgentsFileDisabledByEmptySetting(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("should not appear"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(map[string]string{
		contracts.SettingAgentsFile: "",
	}), workspace, nil)
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	if strings.Contains(system, "should not appear") || strings.Contains(system, "# 工作区指令") {
		t.Fatalf("empty setting still injected: %q", system)
	}
}

func TestAgentsFileCustomName(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CUSTOM.md"), []byte("custom instructions"), 0o644); err != nil {
		t.Fatalf("write CUSTOM.md: %v", err)
	}
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(map[string]string{
		contracts.SettingAgentsFile: " CUSTOM.md ",
	}), workspace, nil)
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	if !strings.Contains(system, "custom instructions") {
		t.Fatalf("custom file not injected: %q", system)
	}
}

func TestAgentsFileTruncatedAt32KiB(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("a", 40<<10)
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	logger := &fakeLogger{}
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), workspace, logger)
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	if !strings.Contains(system, "# 工作区指令（AGENTS.md）") {
		t.Fatalf("heading missing after truncation: %q", system)
	}
	content := strings.TrimPrefix(system, "base system prompt\n\n# 工作区指令（AGENTS.md）\n\n")
	if len(content) > 32<<10 {
		t.Fatalf("injected content length = %d, want <= %d", len(content), 32<<10)
	}
	if !strings.HasPrefix(content, strings.Repeat("a", 1024)) {
		t.Fatalf("content prefix lost: %q", content[:64])
	}
	if !logger.contains("truncated") {
		t.Fatalf("missing truncation warning: %v", logger.lines)
	}
}

func TestAgentsFileReadErrorIsLoggedAndIgnored(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "AGENTS.md"), 0o755); err != nil {
		t.Fatalf("mkdir AGENTS.md: %v", err)
	}
	logger := &fakeLogger{}
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), workspace, logger)
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	if strings.Contains(system, "# 工作区指令") {
		t.Fatalf("read error injected content: %q", system)
	}
	if !logger.contains("AGENTS.md") {
		t.Fatalf("read error not logged: %v", logger.lines)
	}
}

func TestAgentsFileWithoutWorkspaceIsIgnored(t *testing.T) {
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), nil, nil)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	request := provider.lastRequest()
	for _, message := range request.Messages {
		if message.Role == rig.RoleSystem && strings.Contains(message.Content[0].Text, "# 工作区指令") {
			t.Fatalf("workspace instructions injected without workspace: %q", message.Content[0].Text)
		}
	}
}
