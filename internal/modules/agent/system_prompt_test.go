package agent

// Tests for the model-facing system prompt: identity + platform environment are
// present from the very first request, the configured/workspace/preset prompt
// keeps its order after them, and only the tools a run actually receives get a
// DSH-style guidance line. The prompt must never be persisted.

import (
	"context"
	"runtime"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// requestSystemText returns the concatenated text of a request's system message.
func requestSystemText(t *testing.T, request rig.Request) string {
	t.Helper()
	for _, message := range request.Messages {
		if message.Role != rig.RoleSystem {
			continue
		}
		var text strings.Builder
		for _, part := range message.Content {
			text.WriteString(part.Text)
		}
		return text.String()
	}
	t.Fatalf("request has no system message: %+v", request.Messages)
	return ""
}

// TestShellToolsPlatformChoice pins the platform decision the environment line
// is built from: pwsh exists only on Windows, bash everywhere else.
func TestShellToolsPlatformChoice(t *testing.T) {
	cases := []struct {
		goos        string
		wantShell   string
		wantMissing string
	}{
		{"windows", "pwsh", "bash"},
		{"WINDOWS", "pwsh", "bash"},
		{"linux", "bash", "pwsh"},
		{"darwin", "bash", "pwsh"},
		{"", "bash", "pwsh"},
	}
	for _, tc := range cases {
		shell, unavailable := shellTools(tc.goos)
		if shell != tc.wantShell || unavailable != tc.wantMissing {
			t.Fatalf("shellTools(%q) = %q/%q, want %q/%q",
				tc.goos, shell, unavailable, tc.wantShell, tc.wantMissing)
		}
		sentence := shellEnvironmentSentence(tc.goos)
		if !strings.Contains(sentence, "Use the "+tc.wantShell+" tool for shell commands.") {
			t.Fatalf("shell sentence for %q = %q", tc.goos, sentence)
		}
		if !strings.Contains(sentence, "The "+tc.wantMissing+" tool is not available on this machine.") {
			t.Fatalf("shell sentence for %q does not forbid the other shell: %q", tc.goos, sentence)
		}
		if tc.wantShell == "pwsh" && !strings.Contains(sentence, "backslashes") {
			t.Fatalf("windows shell sentence should mention backslash paths: %q", sentence)
		}
	}
}

// TestEnvironmentBlock starts with the identity line and carries the OS, shell
// choice, working directory and file policy.
func TestEnvironmentBlock(t *testing.T) {
	block := environmentBlock("windows", "amd64", `D:\work\demo`, contracts.PermissionWorkspace)
	if !strings.HasPrefix(block, agentIdentity+"\n") {
		t.Fatalf("environment block does not start with the identity line: %q", block)
	}
	for _, want := range []string{
		"Environment: windows/amd64.",
		"Use the pwsh tool for shell commands.",
		"The bash tool is not available on this machine.",
		`Working directory: D:\work\demo.`,
		"File policy: workspace-write",
		"modify files inside the working directory only",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("environment block lacks %q: %q", want, block)
		}
	}
	if strings.Contains(block, "Use the bash tool for shell commands") {
		t.Fatalf("windows environment block suggests bash: %q", block)
	}

	empty := environmentBlock("linux", "arm64", "  ", contracts.PermissionWorkspace)
	if !strings.Contains(empty, "no workspace is open") {
		t.Fatalf("empty root block = %q, want a no-workspace line", empty)
	}
	if !strings.Contains(empty, "Environment: linux/arm64.") {
		t.Fatalf("empty root block lacks the OS: %q", empty)
	}
}

// TestApprovalPolicyLines pins what the model-facing sentence must still
// guarantee now that it is rendered from the claim table: the tier label every
// preset (and every legacy spelling) normalizes to, the anti-caution clause where
// a dialog is possible, and the absence of the "sandboxed shell" phrasing.
//
// It no longer pins the prose itself. The literal sentences it used to pin were
// the reason a false claim could stay green: they were written from the preset
// alone and ignored whether an approvals service, a deny-all policy or a command
// policy existed. What the sentence says about each family is checked against the
// REAL gate in permission_policy_test.go (TestApprovalSentenceAgreesWithTheGate);
// this test only keeps the wording's contract (label, caution, no sandbox lie).
func TestApprovalPolicyLines(t *testing.T) {
	// The shipped assembly's tier: approvals service, sandbox module, the command
	// policy commands are dispatched under, and no deny-all policy.
	shipped := func(mode string) approvalPolicy {
		return approvalPolicy{Mode: mode, ApprovalsAvailable: true, SandboxAssembled: true,
			CommandPolicyAvailable: true, Confinement: contracts.SandboxConfinementOS}
	}
	cases := []struct {
		mode      string
		wantLabel string
	}{
		{contracts.PermissionWorkspace, "workspace"},
		{contracts.PermissionFull, "full"},
		{"auto", "full"},
		{contracts.PermissionReadOnly, "read-only"},
		// Retired ask/never spellings (legacy stored rows and headless assembly
		// policies) normalize to the read-only preset.
		{contracts.PermissionAsk, "read-only"},
		{contracts.PermissionNever, "read-only"},
		{"", "workspace"},
		{"bogus", "workspace"},
	}
	for _, tc := range cases {
		policy := shipped(tc.mode)
		sentence := approvalPolicyLine(policy)
		if !strings.HasPrefix(sentence, "Approval policy: "+tc.wantLabel+" — ") {
			t.Fatalf("approvalPolicyLine(%q) = %q, want the %q label", tc.mode, sentence, tc.wantLabel)
		}
		claims := approvalPolicyClaims(policy)
		if len(familiesWith(claims, dispositionAllowed)) == len(claims) {
			// Nothing to prompt about: only the full preset reaches this form.
			if want := "Approval policy: " + tc.wantLabel + " — no approval prompts are shown."; sentence != want {
				t.Fatalf("approvalPolicyLine(%q) = %q, want %q", tc.mode, sentence, want)
			}
			continue
		}
		// Otherwise the sentence must state the claim table's own dispositions
		// rather than a hand-written claim about them.
		if want := claimSentence(claims); !strings.Contains(sentence, want) {
			t.Fatalf("approvalPolicyLine(%q) = %q, want it to state %q", tc.mode, sentence, want)
		}
	}
	// The workspace sentence must not make the model abandon needed work.
	workspace := approvalPolicyLine(shipped(contracts.PermissionWorkspace))
	if !strings.Contains(workspace, "Do not abandon a needed operation") ||
		!strings.Contains(workspace, "follow the dialog guidance") {
		t.Fatalf("workspace approval sentence lost its anti-caution clause: %q", workspace)
	}
	// The phrase "sandboxed shell commands" was true only while a command had to
	// be confined to skip the prompt. Commands are now exempt either way, so on a
	// host with no runner that wording claims a fence nothing enforces — the
	// exact lie the 2026-11-26 audit found (README-style honesty rule).
	for _, mode := range []string{contracts.PermissionWorkspace, contracts.PermissionReadOnly, contracts.PermissionFull} {
		if strings.Contains(approvalPolicyLine(shipped(mode)), "sandboxed shell") {
			t.Fatalf("approvalPolicyLine(%q) still claims every command is sandboxed: %q", mode, approvalPolicyLine(shipped(mode)))
		}
	}
}

// TestUnconfinedCommandsLineDisclosesTheMissingFence pins the host-specific half:
// the model is told the working-directory rule is not OS-enforced when this host
// has no runner, and is told nothing extra when commands ARE confined.
func TestUnconfinedCommandsLineDisclosesTheMissingFence(t *testing.T) {
	// The shipped assembly: the sandbox module AND the command policy the
	// exemption is judged under (without the policy the command family keeps the
	// approval flow, and "unconfined" would describe nothing).
	unconfined := unconfinedCommandsLine(approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true,
		SandboxAssembled: true, CommandPolicyAvailable: true, Confinement: contracts.SandboxConfinementNone})
	if unconfined == "" {
		t.Fatal("no disclosure for a host without an OS sandbox runner")
	}
	for _, want := range []string{"OS confinement: none", "no OS sandbox runner", "not a boundary the OS enforces", "sandbox_status"} {
		if !strings.Contains(unconfined, want) {
			t.Fatalf("disclosure %q lacks %q", unconfined, want)
		}
	}
	// Confined hosts, assemblies without a sandbox module ("" = unknown, commands
	// stay behind the approval gate) and full access (already disclosed) get no
	// second sentence.
	for _, confinement := range []string{contracts.SandboxConfinementOS, ""} {
		policy := approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true,
			SandboxAssembled: true, CommandPolicyAvailable: true, Confinement: confinement}
		if got := unconfinedCommandsLine(policy); got != "" {
			t.Fatalf("confinement %q produced a disclosure: %q", confinement, got)
		}
	}
	full := approvalPolicy{Mode: contracts.PermissionFull, ApprovalsAvailable: true,
		SandboxAssembled: true, CommandPolicyAvailable: true, Confinement: contracts.SandboxConfinementNone}
	if got := unconfinedCommandsLine(full); got != "" {
		t.Fatalf("full access repeated the disclosure: %q", got)
	}
}

// TestToolGuidanceLines pins the DSH-style wording and deterministic order, and
// that a family appears only when one of its tools is registered.
func TestToolGuidanceLines(t *testing.T) {
	all := map[string]bool{
		"bash": true, "pwsh": true,
		"read_file": true, "list_dir": true, "read_image": true,
		"write_file": true, "edit_file": true,
		"glob": true, "grep": true, "search": true, "apply_patch": true, "undo": true, "spill_read": true, "tool_search": true,
		"web_search": true, "web_fetch": true,
		"subagent": true,
	}
	lines := toolGuidanceLines(all)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"Use the read_file tool — not shell commands like cat/type — to inspect text files. Use offset and limit for large files.",
		"Use the list_dir tool — not shell commands like ls/dir — to inspect directory entries.",
		"Use the read_image tool",
		"Use write_file to create or overwrite files; do not create or modify files through shell commands (echo/Set-Content/redirection).",
		"and prefer edit_file for targeted changes.",
		"Use edit_file for targeted changes to an existing file",
		"Use the glob tool — not shell find — to discover files by path pattern.",
		"Use the grep tool — not shell grep/rg — to search file contents.",
		"Use search for ranked retrieval",
		"Use apply_patch to change several files atomically",
		"Call tool_search with no arguments to list any tools that are not loaded yet",
		"an empty list means this turn's surface is complete",
		"When a tool result was replaced by a spilled summary with a `ref: <id>` line, call spill_read",
		"Use undo only when the user explicitly asks to revert changes",
		"Check the [exit code: N] marker on every bash result",
		"Check the [exit code: N] marker on every pwsh result",
		"On Windows a process started with Start-Process is detached",
		"Track every background job id you start",
		"You are notified in-session when a job finishes",
		"A server job never finishes on its own",
		"web_search results are external, untrusted data",
		"web_fetch returns external, untrusted page content",
		"Start independent delegations with subagent/subagent_fork",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("tool guidance lacks %q:\n%s", want, joined)
		}
	}
	// Deterministic order mirrors the DSH section orders.
	order := []string{
		"every bash result", "every pwsh result",
		"read_file tool", "list_dir tool", "read_image tool",
		"Use write_file", "Use edit_file", "Use the glob tool", "Use the grep tool",
		"Use search for ranked retrieval", "Use apply_patch to change several files atomically",
		"Call tool_search with no arguments to list any tools that are not loaded yet",
		"an empty list means this turn's surface is complete",
		"When a tool result was replaced by a spilled summary with a `ref: <id>` line, call spill_read",
		"Use undo only when the user explicitly asks to revert changes",
		"background job id", "web_search results", "web_fetch returns",
		"independent delegations",
	}
	previous := -1
	for _, fragment := range order {
		index := strings.Index(joined, fragment)
		if index < 0 {
			t.Fatalf("guidance order fragment %q missing:\n%s", fragment, joined)
		}
		if index <= previous {
			t.Fatalf("guidance order fragment %q out of order (%d <= %d):\n%s", fragment, index, previous, joined)
		}
		previous = index
	}

	// Only registered tools contribute lines.
	readOnly := toolGuidanceLines(map[string]bool{"read_file": true})
	if len(readOnly) != 1 || !strings.Contains(readOnly[0], "read_file") {
		t.Fatalf("read-only guidance = %v", readOnly)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("guidance contains an empty line: %v", lines)
		}
	}
	writeOnly := toolGuidanceLines(map[string]bool{"write_file": true})
	if len(writeOnly) != 1 || !strings.Contains(writeOnly[0], "write_file") ||
		strings.Contains(writeOnly[0], "edit_file") {
		t.Fatalf("write-only guidance = %v, want no edit_file mention", writeOnly)
	}
}

// TestSystemPromptForRunStructure pins the required order: environment first,
// then the configured prompt, then tool guidance last.
func TestSystemPromptForRunStructure(t *testing.T) {
	prompt := systemPromptForRun("linux", "amd64", "/work/project", contracts.PermissionWorkspace, contracts.PermissionWorkspace, "",
		"CONFIGURED-PROMPT", map[string]bool{"read_file": true, "write_file": true})
	if !strings.HasPrefix(prompt, agentIdentity) {
		t.Fatalf("prompt does not start with the identity: %q", prompt)
	}
	environmentAt := strings.Index(prompt, "Environment: linux/amd64.")
	policyAt := strings.Index(prompt, "Approval policy: workspace —")
	configuredAt := strings.Index(prompt, "CONFIGURED-PROMPT")
	guidanceAt := strings.Index(prompt, "## Tool guidance")
	if environmentAt < 0 || policyAt < 0 || configuredAt < 0 || guidanceAt < 0 {
		t.Fatalf("prompt sections missing (env:%d policy:%d configured:%d guidance:%d):\n%s",
			environmentAt, policyAt, configuredAt, guidanceAt, prompt)
	}
	if !(environmentAt < policyAt && policyAt < configuredAt && configuredAt < guidanceAt) {
		t.Fatalf("prompt order = env:%d policy:%d configured:%d guidance:%d:\n%s",
			environmentAt, policyAt, configuredAt, guidanceAt, prompt)
	}
	// edit_file is not registered here, so the write line carries only its own
	// guidance (and no "prefer edit_file" clause).
	if !strings.HasSuffix(strings.TrimSpace(prompt), "Use write_file to create or overwrite files; do not create or modify files through shell commands (echo/Set-Content/redirection). Read an existing file before overwriting it.") {
		t.Fatalf("tool guidance is not the last section: %q", prompt)
	}

	// No configured prompt: environment and guidance are still present.
	bare := systemPromptForRun("windows", "arm64", "", contracts.PermissionReadOnly, contracts.PermissionReadOnly, "", "", map[string]bool{"pwsh": true})
	if !strings.Contains(bare, "You are ZeoKeel") || !strings.Contains(bare, "Use the pwsh tool for shell commands.") {
		t.Fatalf("bare prompt lacks environment: %q", bare)
	}
	if !strings.Contains(bare, "Approval policy: read-only") || !strings.Contains(bare, "pwsh result") {
		t.Fatalf("bare prompt lacks policy/guidance: %q", bare)
	}
}

// newPromptService builds a service over the given tools and settings with a
// scripted provider.
func newPromptService(t *testing.T, settings *fakeSettings, approvals contracts.Approvals, tools ...rig.Tool) (*service, *captureProvider, *fakeStore) {
	t.Helper()
	if settings == nil {
		settings = newFakeSettings(nil)
	}
	provider := &captureProvider{}
	store := newFakeStore()
	svc := newService(Config{SystemPrompt: "base system prompt"}, rigtools.NewRegistry(tools...),
		contracts.SettingsService(settings), &fakeLogger{}, store, approvals)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc, provider, store
}

// TestRunSystemPromptHasEnvironmentAndGuidance proves the first provider request
// already carries the platform, shell choice, approval policy and guidance for
// exactly the registered tools.
func TestRunSystemPromptHasEnvironmentAndGuidance(t *testing.T) {
	svc, provider, store := newPromptService(t, nil, fakeApprovals{},
		presetTool{name: "read_file"}, presetTool{name: "write_file"})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	system := requestSystemText(t, provider.lastRequest())
	// The identity is pinned by its own tests; the string must not be duplicated
	// here, or updating the product description turns into a two-file edit that
	// silently leaves one copy stale.
	if !strings.HasPrefix(system, agentIdentity) {
		t.Fatalf("system prompt does not start with the identity: %q", system)
	}
	if !strings.Contains(system, "Environment: "+runtime.GOOS+"/"+runtime.GOARCH+".") {
		t.Fatalf("system prompt lacks the runtime OS/arch: %q", system)
	}
	if !strings.Contains(system, "base system prompt") {
		t.Fatalf("configured system prompt lost: %q", system)
	}
	if !strings.Contains(system, "Use the read_file tool") || !strings.Contains(system, "Use write_file") {
		t.Fatalf("registered file tools lack guidance: %q", system)
	}
	guidanceAt := strings.Index(system, "## Tool guidance")
	if guidanceAt < 0 {
		t.Fatalf("system prompt lacks the tool guidance section: %q", system)
	}
	if guidance := system[guidanceAt:]; strings.Contains(guidance, "bash result") || strings.Contains(guidance, "pwsh result") {
		t.Fatalf("unregistered shell tool got guidance: %q", guidance)
	}

	// The generated prompt is never persisted: no settings row and no session
	// event carries it, and the read model stays message-only.
	for key, value := range svc.settings.All() {
		if strings.Contains(value, "You are ZeoKeel") || strings.Contains(value, "## Tool guidance") {
			t.Fatalf("generated prompt persisted in settings %q: %q", key, value)
		}
	}
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), "You are ZeoKeel") {
			t.Fatalf("generated prompt persisted in event %q: %s", event.Type, event.Payload)
		}
	}
	session, _ := svc.Session(sessionID)
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleSystem {
			t.Fatalf("generated prompt persisted as a session message: %+v", message)
		}
	}
}

// TestRunSystemPromptReflectsApprovalModeAndSessionOverride covers the runtime
// approval line against the approvals service and the per-session override.
func TestRunSystemPromptReflectsApprovalModeAndSessionOverride(t *testing.T) {
	svc, _, _ := newPromptService(t, nil, fakeApprovals{mode: contracts.PermissionWorkspace},
		presetTool{name: "read_file"})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	prompt := svc.buildRunSystemPrompt(context.Background(), sessionID, contracts.ChatConfig{}, toolNameSet(nil), "")
	if !strings.Contains(prompt, "Approval policy: workspace —") {
		t.Fatalf("approval line does not follow the approvals service: %q", prompt)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionFull); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	prompt = svc.buildRunSystemPrompt(context.Background(), sessionID, contracts.ChatConfig{}, toolNameSet(nil), "")
	if !strings.Contains(prompt, "Approval policy: full — no approval prompts are shown.") {
		t.Fatalf("session full override not reflected: %q", prompt)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	prompt = svc.buildRunSystemPrompt(context.Background(), sessionID, contracts.ChatConfig{}, toolNameSet(nil), "")
	if !strings.Contains(prompt, "Approval policy: read-only —") {
		t.Fatalf("session read-only override not reflected: %q", prompt)
	}
}

// TestSubagentPromptHasEnvironmentAndFilteredGuidance proves delegated children
// inherit the full prompt with guidance for their filtered tool set.
func TestSubagentPromptHasEnvironmentAndFilteredGuidance(t *testing.T) {
	svc, provider, _ := newPromptService(t, nil, fakeApprovals{},
		presetTool{name: "read_file"}, presetTool{name: "write_file"}, presetTool{name: "subagent"})
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "research"}); err != nil {
		t.Fatalf("Run subagent: %v", err)
	}
	system := requestSystemText(t, provider.lastRequest())
	if !strings.HasPrefix(system, "You are ZeoKeel") || !strings.Contains(system, "Environment: "+runtime.GOOS+"/") {
		t.Fatalf("subagent prompt lacks identity/environment: %q", system)
	}
	if !strings.Contains(system, "Use the read_file tool") || !strings.Contains(system, "Use write_file") {
		t.Fatalf("subagent prompt lacks its file-tool guidance: %q", system)
	}
	if strings.Contains(system, "Start independent delegations") {
		t.Fatalf("subagent prompt describes the excluded delegation tool: %q", system)
	}
}

// TestTeamMemberPromptHasEnvironment proves team members get the same builder.
func TestTeamMemberPromptHasEnvironment(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	member, err := teamService.Spawn(context.Background(), "researcher", "summarize", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := teamService.Wait(context.Background(), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	system := requestSystemText(t, provider.lastRequest())
	if !strings.HasPrefix(system, "You are ZeoKeel") ||
		!strings.Contains(system, "Environment: "+runtime.GOOS+"/"+runtime.GOARCH) ||
		!strings.Contains(system, "Approval policy:") {
		t.Fatalf("team member prompt lacks the shared environment block: %q", system)
	}
}
