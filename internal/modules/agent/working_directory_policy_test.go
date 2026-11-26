package agent

// (b) / RG-07 (2026-11 audit): the working-directory line of the system prompt
// used to hardcode "File policy: workspace-write" regardless of the actual OS
// sandbox mode — overstating the model's rights under read-only and
// understating them under full. The line now follows the effective OS mode
// (the GLOBAL agent.permission preset — a session approval override never
// changes the OS wall), and the full + no-workspace combination discloses the
// process-cwd exception instead of the fail-closed promise (RG-07).

import (
	"context"
	"strings"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestWorkingDirectoryLineThreeStates(t *testing.T) {
	root := "/work/project"
	workspaceLine := "Working directory: /work/project. File policy: workspace-write; the file tools may read files anywhere on this machine, and may modify files inside the working directory only."
	cases := []struct {
		mode     string
		contains []string
		absent   []string
	}{
		{
			mode:     contracts.PermissionWorkspace,
			contains: []string{"File policy: workspace-write", "read files anywhere", "modify files inside the working directory only"},
		},
		{
			mode:     contracts.PermissionReadOnly,
			contains: []string{"File policy: read-only", "cannot modify", "shell commands run only while an OS sandbox confines their writes"},
			absent:   []string{"workspace-write", "full-access"},
		},
		{
			mode:     contracts.PermissionFull,
			contains: []string{"File policy: full-access", "not confined to this directory"},
			absent:   []string{"workspace-write", "working directory only"},
		},
		{
			mode:     "",
			contains: []string{"File policy: workspace-write"},
		},
	}
	for _, tc := range cases {
		got := workingDirectoryLine(root, tc.mode)
		if tc.mode == contracts.PermissionWorkspace && got != workspaceLine {
			t.Fatalf("workspace line drifted from the historical wording: %q", got)
		}
		for _, want := range tc.contains {
			if !strings.Contains(got, want) {
				t.Fatalf("mode %q line lacks %q: %q", tc.mode, want, got)
			}
		}
		for _, absent := range tc.absent {
			if strings.Contains(got, absent) {
				t.Fatalf("mode %q line must not contain %q: %q", tc.mode, absent, got)
			}
		}
	}
}

func TestWorkingDirectoryLineNoWorkspaceCombinations(t *testing.T) {
	failClosed := "Working directory: none (no workspace is open). File writes and shell commands fail until the user opens a workspace."
	for _, mode := range []string{contracts.PermissionReadOnly, contracts.PermissionWorkspace, ""} {
		if got := workingDirectoryLine("", mode); got != failClosed {
			t.Fatalf("no-workspace %q line = %q, want the fail-closed wording", mode, got)
		}
	}
	// RG-07: under full access an empty root does NOT fail closed — relative
	// paths resolve against the process working directory. The prompt must
	// disclose that exception instead of promising a failure.
	got := workingDirectoryLine("  ", contracts.PermissionFull)
	for _, want := range []string{"no workspace is open", "full-access", "process working directory"} {
		if !strings.Contains(got, want) {
			t.Fatalf("full + no-workspace line lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "fail until the user opens a workspace") {
		t.Fatalf("full + no-workspace line keeps the false fail-closed promise: %q", got)
	}
}

// The file policy (OS mode) and the approval policy are independent axes: a
// session-level approval override never rewrites the OS file policy.
func TestSystemPromptSandboxModeIndependentOfApprovalOverride(t *testing.T) {
	prompt := systemPromptForRun("linux", "amd64", "/work",
		contracts.PermissionWorkspace, contracts.PermissionFull, "", "", nil)
	if !strings.Contains(prompt, "File policy: workspace-write") || !strings.Contains(prompt, "Approval policy: full") {
		t.Fatalf("OS workspace + approval full prompt = %q", prompt)
	}
	prompt = systemPromptForRun("linux", "amd64", "/work",
		contracts.PermissionFull, contracts.PermissionReadOnly, "", "", nil)
	if !strings.Contains(prompt, "File policy: full-access") || !strings.Contains(prompt, "Approval policy: read-only") {
		t.Fatalf("OS full + approval read-only prompt = %q", prompt)
	}
}

// The service path resolves the OS mode from the GLOBAL permission setting
// (the same source the toolkit build reads), never from the session override.
func TestBuildRunSystemPromptUsesGlobalSandboxMode(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:      "m",
		contracts.SettingAgentPermission: contracts.PermissionFull,
	})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{mode: "workspace"})
	id, err := svc.NewSession("prompt")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(id, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	prompt := svc.buildRunSystemPrompt(context.Background(), id, svc.Config(), nil, "/work")
	if !strings.Contains(prompt, "File policy: full-access") {
		t.Fatalf("OS mode must follow the global full preset: %q", prompt)
	}
	if !strings.Contains(prompt, "Approval policy: read-only") {
		t.Fatalf("approval policy must follow the session override: %q", prompt)
	}
	// Reverse: global workspace + session full override keeps the OS wall.
	if err := settings.Set(contracts.SettingAgentPermission, contracts.PermissionWorkspace); err != nil {
		t.Fatalf("set permission: %v", err)
	}
	if err := svc.SetSessionPermission(id, contracts.PermissionFull); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	prompt = svc.buildRunSystemPrompt(context.Background(), id, svc.Config(), nil, "/work")
	if !strings.Contains(prompt, "File policy: workspace-write") || !strings.Contains(prompt, "Approval policy: full") {
		t.Fatalf("global workspace + session full prompt = %q", prompt)
	}
}
