package agent

// Tests for the AGENTS.md workspace-instruction injection.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !strings.HasPrefix(system, "You are ZeoKeel") {
		t.Fatalf("system prompt does not start with the identity/environment block: %q", system)
	}
	if !strings.Contains(system, "base system prompt") {
		t.Fatalf("base prompt missing: %q", system)
	}
	if !strings.Contains(system, "# 工作区指令（AGENTS.md）") ||
		!strings.Contains(system, "Follow the workspace rules.") {
		t.Fatalf("workspace instructions missing: %q", system)
	}
	baseIndex := strings.Index(system, "base system prompt")
	headingIndex := strings.Index(system, "# 工作区指令（AGENTS.md）")
	if baseIndex < 0 || headingIndex < 0 || baseIndex > headingIndex {
		t.Fatalf("workspace instructions are not appended after the base prompt: %q", system)
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
	// The environment/identity block is always present now; only the workspace
	// instruction injection is optional.
	if !strings.Contains(system, "base system prompt") || !strings.Contains(system, "You are ZeoKeel") {
		t.Fatalf("base prompt or environment block missing: %q", system)
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
	headingIndex := strings.Index(system, workspaceInstructionsHeading)
	if headingIndex < 0 {
		t.Fatalf("heading missing after truncation: %q", system)
	}
	fenceStart := strings.Index(system[headingIndex:], workspaceInstructionsOpen)
	fenceEnd := strings.Index(system[headingIndex:], workspaceInstructionsClose)
	if fenceStart < 0 || fenceEnd <= fenceStart {
		t.Fatalf("workspace-instruction fence missing after truncation: %q", system[headingIndex:])
	}
	injected := system[headingIndex+fenceStart+len(workspaceInstructionsOpen) : headingIndex+fenceEnd]
	if !strings.Contains(injected, strings.Repeat("a", 1024)) {
		t.Fatalf("content prefix lost: %q", injected[:64])
	}
	if !strings.Contains(injected, workspaceInstructionsTruncatedMarker) {
		t.Fatalf("truncation marker missing from the injected block: %q", injected[len(injected)-80:])
	}
	if len(injected) > 32<<10+len(workspaceInstructionsTruncatedMarker)+8 {
		t.Fatalf("injected content length = %d, want <= %d", len(injected), 32<<10+len(workspaceInstructionsTruncatedMarker)+8)
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

// TestRunPromptCarriesTheDateThroughSystemPromptFunc pins the SDK-side render:
// the spec no longer carries a static SystemPrompt, so the provider's system
// message can only come from SystemPromptFunc — and it carries today's date.
func TestRunPromptCarriesTheDateThroughSystemPromptFunc(t *testing.T) {
	root := t.TempDir()
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), workspace, nil)
	// Two different dates prove which render wins: the snapshot build reads one
	// day, the SDK-side SystemPromptFunc reads the next.
	snapshotDay := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	sdkDay := snapshotDay.AddDate(0, 0, 1)
	calls := 0
	svc.now = func() time.Time {
		calls++
		if calls == 1 {
			return snapshotDay
		}
		return sdkDay
	}
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	if !strings.Contains(system, "Today's date is 2026-11-22 (Sunday).") {
		t.Fatalf("system prompt does not carry the SDK-side date: %q", system)
	}
	if strings.Contains(system, "Today's date is 2026-11-21") {
		t.Fatalf("system prompt carries the snapshot build's date: %q", system)
	}
	if !strings.HasPrefix(system, "You are ZeoKeel") {
		t.Fatalf("SystemPromptFunc did not render the base prompt: %q", system)
	}
}

// TestPromptOmitsTheDateWithoutAClock keeps the legacy assembly (no clock, e.g.
// tests and tooling that only need the static part) free of a guessed date.
func TestPromptOmitsTheDateWithoutAClock(t *testing.T) {
	prompt := systemPromptForRun("linux", "amd64", "/work", contracts.PermissionWorkspace,
		contracts.PermissionWorkspace, "", "base", map[string]bool{"bash": true})
	if strings.Contains(prompt, "Today's date") {
		t.Fatalf("legacy assembly invented a date: %q", prompt)
	}
}

// TestRunPromptAlwaysCarriesTheProcessDate covers the production path: no clock
// override, so the prompt must still carry today's local date (a nil clock must
// not silently drop the line).
func TestRunPromptAlwaysCarriesTheProcessDate(t *testing.T) {
	root := t.TempDir()
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), workspace, nil)
	system := runAndSystemPrompt(t, svc, provider, workspace, root)
	want := "Today's date is " + time.Now().Format("2006-01-02")
	if !strings.Contains(system, want) {
		t.Fatalf("system prompt lacks %q: %q", want, system)
	}
}

// TestPromptDateUsesTheClockLocation pins the date as the clock's own location,
// not UTC: 00:30 in UTC+8 is still the previous day in UTC, so formatting the
// instant in UTC would print the wrong date.
func TestPromptDateUsesTheClockLocation(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*3600)
	clock := time.Date(2026, 11, 21, 0, 30, 0, 0, zone)
	sections := promptSectionsForRun("linux", "amd64", "/work", contracts.PermissionWorkspace,
		approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true, SandboxAssembled: true},
		"", clock, nil, map[string]bool{"bash": true})
	rendered := renderPromptSections(sections)
	if !strings.Contains(rendered, "Today's date is 2026-11-21 (Saturday).") {
		t.Fatalf("date does not follow the clock location: %q", rendered)
	}
}

// TestPromptSnapshotCarriesTheDateSection pins the inspector side: the recorded
// snapshot has the date section and its byte total equals the prompt the model
// actually received.
func TestPromptSnapshotCarriesTheDateSection(t *testing.T) {
	root := t.TempDir()
	workspace := &fakeWorkspace{}
	svc, provider := newAgentsFileService(t, newFakeSettings(nil), workspace, nil)
	workspace.setCurrent(contracts.Workspace{ID: "ws-1", Root: root}, true)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("no context snapshot")
	}
	hasDate := false
	for _, section := range folded.Snapshot.Sections {
		if section.ID == PromptSectionDate {
			hasDate = true
		}
	}
	if !hasDate {
		t.Fatalf("snapshot sections lack the date: %+v", folded.Snapshot.Sections)
	}
	system := ""
	for _, message := range provider.lastRequest().Messages {
		if message.Role == rig.RoleSystem {
			for _, chunk := range message.Content {
				system += chunk.Text
			}
		}
	}
	if folded.Snapshot.SystemPromptBytes != len(system) {
		t.Fatalf("snapshot prompt bytes = %d, received system prompt = %d", folded.Snapshot.SystemPromptBytes, len(system))
	}
}
