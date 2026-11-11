package toolkit

// Tests for the mode-linked OS sandbox wiring:
//
//   - the agent permission preset (agent.permission) is translated into the
//     sandbox mode the root-scoped tools are built with (read-only ->
//     read-only, workspace -> workspace-write, full -> danger-full-access),
//     and cfg.ReadOnly still forces read-only;
//   - the optional contracts.SandboxService runner is injected into
//     bash/pwsh/jobs/terminal/worktree, while a nil runner keeps the exact old
//     behavior;
//   - the root-scoped tool build is rebuilt when the workspace root, the
//     permission mode or the runner-on state changes, without a restart.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeSandboxRunner is a sandbox.Runner test double: it records every Confine
// call, can rewrite the argv (proving the tools spawn the confined argv) or
// fail closed (proving the tools never spawn unconfined).
type fakeSandboxRunner struct {
	mu       sync.Mutex
	calls    [][]string
	policies []*rigsandbox.Policy
	rewrite  []string
	err      error
}

// Confine implements sandbox.Runner.
func (r *fakeSandboxRunner) Confine(argv []string, policy *rigsandbox.Policy) (rigsandbox.ConfinedCommand, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), argv...))
	r.policies = append(r.policies, policy)
	if r.err != nil {
		return rigsandbox.ConfinedCommand{}, r.err
	}
	confined := argv
	if r.rewrite != nil {
		confined = append([]string(nil), r.rewrite...)
	}
	return rigsandbox.ConfinedCommand{Argv: confined, Enforcement: rigsandbox.EnforcementFull}, nil
}

// recordedCalls returns a copy of every argv passed to Confine.
func (r *fakeSandboxRunner) recordedCalls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

// recordedPolicies returns a copy of every policy passed to Confine.
func (r *fakeSandboxRunner) recordedPolicies() []*rigsandbox.Policy {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*rigsandbox.Policy(nil), r.policies...)
}

// fakeSandboxService is the contracts.SandboxService test double. The runner is
// mutable so a test can observe the toolkit rebuilding without a restart.
type fakeSandboxService struct {
	mu        sync.Mutex
	runner    rigsandbox.Runner
	available bool
	status    contracts.SandboxStatus
}

// Runner implements contracts.SandboxService.
func (s *fakeSandboxService) Runner() rigsandbox.Runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runner
}

// Available implements contracts.SandboxService.
func (s *fakeSandboxService) Available() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.available
}

// Status implements contracts.SandboxService.
func (s *fakeSandboxService) Status() contracts.SandboxStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// setRunner switches the runner the service hands to the toolkit.
func (s *fakeSandboxService) setRunner(runner rigsandbox.Runner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner = runner
	s.available = runner != nil
}

// newSandboxBuild builds one root-scoped tool set through the injectable
// platform/PATH seam (no pwsh on the fake PATH, so the shell surface is bash).
func newSandboxBuild(t *testing.T, cfg Config, root string, mode rigsandbox.Mode, runner rigsandbox.Runner) *rootScoped {
	t.Helper()
	scoped := buildRootScopedFor(cfg, root, 5*time.Second, mode, runner, "linux",
		func(string) (string, error) { return "", os.ErrNotExist })
	t.Cleanup(scoped.close)
	return scoped
}

// invokeScopedTool invokes one tool of a root-scoped build by name.
func invokeScopedTool(t *testing.T, scoped *rootScoped, name, arguments string) rig.ToolResult {
	t.Helper()
	tool, ok := scoped.byName[name]
	if !ok {
		t.Fatalf("root-scoped build lacks tool %q", name)
	}
	result, err := tool.Invoke(context.Background(), rig.ToolCall{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("invoke %s: %v", name, err)
	}
	return result
}

// requireUnixShell skips tests that need Rig's /bin/sh bash tool.
func requireUnixShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Rig's bash tool needs /bin/sh, which is Unix-only")
	}
}

// TestPermissionSandboxMode pins the mode mapping of the three permission
// presets (plus the legacy spellings NormalizePermissionMode folds into them).
func TestPermissionSandboxMode(t *testing.T) {
	cases := []struct {
		name       string
		permission string
		want       rigsandbox.Mode
	}{
		{name: "read-only", permission: contracts.PermissionReadOnly, want: rigsandbox.ModeReadOnly},
		{name: "workspace", permission: contracts.PermissionWorkspace, want: rigsandbox.ModeWorkspaceWrite},
		{name: "full", permission: contracts.PermissionFull, want: rigsandbox.ModeDangerFullAccess},
		{name: "auto alias is full", permission: "auto", want: rigsandbox.ModeDangerFullAccess},
		{name: "legacy ask is read-only", permission: contracts.PermissionAsk, want: rigsandbox.ModeReadOnly},
		{name: "legacy never is read-only", permission: contracts.PermissionNever, want: rigsandbox.ModeReadOnly},
		{name: "empty defaults to workspace", permission: "", want: rigsandbox.ModeWorkspaceWrite},
		{name: "unknown defaults to workspace", permission: "definitely-not-a-mode", want: rigsandbox.ModeWorkspaceWrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := permissionSandboxMode(tc.permission); got != tc.want {
				t.Fatalf("permissionSandboxMode(%q) = %q, want %q", tc.permission, got, tc.want)
			}
		})
	}
}

// TestConfiguredSandboxModeReadsLiveSetting covers the resolver's two inputs:
// the live agent.permission setting and the assembly's read-only opt-out.
func TestConfiguredSandboxModeReadsLiveSetting(t *testing.T) {
	settings := newMutableSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionFull,
	})
	if got := configuredSandboxMode(Config{}, settings); got != rigsandbox.ModeDangerFullAccess {
		t.Fatalf("configuredSandboxMode(full) = %q, want danger-full-access", got)
	}
	settings.Set(contracts.SettingAgentPermission, contracts.PermissionReadOnly)
	if got := configuredSandboxMode(Config{}, settings); got != rigsandbox.ModeReadOnly {
		t.Fatalf("configuredSandboxMode(read-only) = %q, want read-only", got)
	}
	// cfg.ReadOnly is the assembly-level opt-out: it wins over the setting.
	if got := configuredSandboxMode(Config{ReadOnly: true}, settings); got != rigsandbox.ModeReadOnly {
		t.Fatalf("configuredSandboxMode(readOnly cfg, read-only setting) = %q, want read-only", got)
	}
	settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull)
	if got := configuredSandboxMode(Config{ReadOnly: true}, settings); got != rigsandbox.ModeReadOnly {
		t.Fatalf("configuredSandboxMode(readOnly cfg, full setting) = %q, want read-only", got)
	}
	// No settings service (or no stored value) keeps the workspace default.
	if got := configuredSandboxMode(Config{}, nil); got != rigsandbox.ModeWorkspaceWrite {
		t.Fatalf("configuredSandboxMode(no settings) = %q, want workspace-write", got)
	}
}

// TestNewPolicyModes pins the policy seam: the mode is installed with
// WithMode and cfg.ReadOnly still forces a read-only policy.
func TestNewPolicyModes(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		cfg  Config
		mode rigsandbox.Mode
		want rigsandbox.Mode
	}{
		{name: "workspace", mode: rigsandbox.ModeWorkspaceWrite, want: rigsandbox.ModeWorkspaceWrite},
		{name: "read-only", mode: rigsandbox.ModeReadOnly, want: rigsandbox.ModeReadOnly},
		{name: "danger", mode: rigsandbox.ModeDangerFullAccess, want: rigsandbox.ModeDangerFullAccess},
		{name: "cfg read-only wins", cfg: Config{ReadOnly: true}, mode: rigsandbox.ModeDangerFullAccess, want: rigsandbox.ModeReadOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPolicy(tc.cfg, root, time.Second, tc.mode)
			if got := policy.Mode(); got != tc.want {
				t.Fatalf("policy.Mode() = %q, want %q", got, tc.want)
			}
			if tc.cfg.ReadOnly && !policy.ReadOnly() {
				t.Fatal("cfg.ReadOnly did not force a read-only policy")
			}
		})
	}
}

// TestBuildRootScopedInjectsRunnerIntoBash is the injection proof: with a
// runner attached, invoking bash runs the runner's rewritten argv, not the
// policy-checked argv.
func TestBuildRootScopedInjectsRunnerIntoBash(t *testing.T) {
	requireUnixShell(t)
	root := t.TempDir()
	runner := &fakeSandboxRunner{rewrite: []string{"/bin/sh", "-c", "echo confined"}}
	scoped := newSandboxBuild(t, Config{}, root, rigsandbox.ModeWorkspaceWrite, runner)

	if !scoped.sandboxOn {
		t.Fatal("build with a runner reports sandboxOn = false")
	}
	if scoped.mode != rigsandbox.ModeWorkspaceWrite {
		t.Fatalf("build mode = %q, want workspace-write", scoped.mode)
	}

	result := invokeScopedTool(t, scoped, "bash", `{"command":"echo original"}`)
	if result.IsError {
		t.Fatalf("bash failed: %q", result.Content)
	}
	if !strings.Contains(result.Content, "confined") {
		t.Fatalf("bash output = %q, want the runner's rewritten command output", result.Content)
	}
	if strings.Contains(result.Content, "original") {
		t.Fatalf("bash ran the original command despite the runner: %q", result.Content)
	}

	calls := runner.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("runner Confine calls = %d, want 1", len(calls))
	}
	if got, want := strings.Join(calls[0], " "), "/bin/sh -c echo original"; got != want {
		t.Fatalf("runner confined argv = %q, want %q", got, want)
	}
	policies := runner.recordedPolicies()
	if len(policies) != 1 || policies[0] == nil {
		t.Fatalf("runner policies = %v, want one usable policy", policies)
	}
	if got := policies[0].Mode(); got != rigsandbox.ModeWorkspaceWrite {
		t.Fatalf("runner policy mode = %q, want workspace-write", got)
	}
	if roots := policies[0].Roots(); len(roots) != 1 || roots[0] != root {
		t.Fatalf("runner policy roots = %v, want [%q]", roots, root)
	}
}

// TestBuildRootScopedNilRunnerKeepsOldBehavior pins the default-off contract:
// a nil runner leaves the bash tool exactly as before.
func TestBuildRootScopedNilRunnerKeepsOldBehavior(t *testing.T) {
	requireUnixShell(t)
	scoped := newSandboxBuild(t, Config{}, t.TempDir(), rigsandbox.ModeWorkspaceWrite, nil)
	if scoped.sandboxOn {
		t.Fatal("build without a runner reports sandboxOn = true")
	}
	result := invokeScopedTool(t, scoped, "bash", `{"command":"echo unconfined"}`)
	if result.IsError {
		t.Fatalf("bash failed: %q", result.Content)
	}
	if !strings.Contains(result.Content, "unconfined") {
		t.Fatalf("bash output = %q, want the original command output", result.Content)
	}
}

// TestBuildRootScopedInjectsRunnerIntoJobTerminalAndWorktree proves the runner
// reaches every command-executing tool: a fail-closed runner makes jobs,
// terminals and worktree git calls fail before anything is spawned.
func TestBuildRootScopedInjectsRunnerIntoJobTerminalAndWorktree(t *testing.T) {
	requireUnixShell(t)
	root := t.TempDir()
	runner := &fakeSandboxRunner{err: rigsandbox.ErrSandboxUnavailable}
	scoped := newSandboxBuild(t, Config{}, root, rigsandbox.ModeWorkspaceWrite, runner)

	// Background jobs get the runner through jobs.WithRunner.
	started := invokeScopedTool(t, scoped, "job_start", `{"command":"echo never-spawned"}`)
	if !started.IsError {
		t.Fatalf("job_start succeeded with a fail-closed runner: %q", started.Content)
	}

	// Terminal sessions get it through terminal.WithRunner.
	opened := invokeScopedTool(t, scoped, "terminal_open", `{}`)
	if !opened.IsError {
		t.Fatalf("terminal_open succeeded with a fail-closed runner: %q", opened.Content)
	}

	// Worktree git calls get it through worktree.WithRunner: a fake git binary
	// on PATH would run (and touch a marker) if the runner were missing.
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "git-ran")
	script := "#!/bin/sh\ntouch " + marker + "\necho fake-toplevel\n"
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir)
	entered := invokeScopedTool(t, scoped, "enter_worktree", `{}`)
	t.Setenv("PATH", oldPath)
	if !entered.IsError {
		t.Fatalf("enter_worktree succeeded with a fail-closed runner: %q", entered.Content)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("enter_worktree spawned git despite the fail-closed runner")
	}
	if strings.Contains(strings.ToLower(entered.Content), "fake-toplevel") {
		t.Fatalf("enter_worktree used fake git output: %q", entered.Content)
	}

	if calls := runner.recordedCalls(); len(calls) < 3 {
		t.Fatalf("runner Confine calls = %d, want at least 3 (job, terminal, worktree)", len(calls))
	}
}

// TestRebindGroupRebuildsOnModeChange covers the no-restart requirement: a
// permission change between two forRoot calls produces a new build with the new
// mode and retires the previous one.
func TestRebindGroupRebuildsOnModeChange(t *testing.T) {
	root := t.TempDir()
	settings := newMutableSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionWorkspace,
	})
	sandboxOf := func() (rigsandbox.Mode, rigsandbox.Runner) {
		return configuredSandboxMode(Config{}, settings), nil
	}
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, sandboxOf, nil)
	defer group.close()

	first := group.initial()
	if first.mode != rigsandbox.ModeWorkspaceWrite {
		t.Fatalf("initial mode = %q, want workspace-write", first.mode)
	}
	if again := group.forRoot(); again != first {
		t.Fatal("unchanged mode rebuilt the same-root tool set")
	}

	settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull)
	second := group.forRoot()
	if second == first {
		t.Fatal("permission change reused the previous build")
	}
	if second.mode != rigsandbox.ModeDangerFullAccess {
		t.Fatalf("full-permission build mode = %q, want danger-full-access", second.mode)
	}
	if got := second.byName["write_file"]; got == nil {
		t.Fatal("danger-full-access build lost the file tools")
	}
	waitBuildClosed(t, first)

	settings.Set(contracts.SettingAgentPermission, contracts.PermissionReadOnly)
	third := group.forRoot()
	if third == second {
		t.Fatal("permission change to read-only reused the previous build")
	}
	if third.mode != rigsandbox.ModeReadOnly {
		t.Fatalf("read-only build mode = %q, want read-only", third.mode)
	}
	waitBuildClosed(t, second)
}

// TestRebindGroupRebuildsOnRunnerOnChange covers runner availability: when the
// service starts (or stops) returning a runner, the next forRoot call rebuilds
// the tools instead of keeping the previous injection state.
func TestRebindGroupRebuildsOnRunnerOnChange(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var runner rigsandbox.Runner = &fakeSandboxRunner{}
	sandboxOf := func() (rigsandbox.Mode, rigsandbox.Runner) {
		mu.Lock()
		defer mu.Unlock()
		return rigsandbox.ModeWorkspaceWrite, runner
	}
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, sandboxOf, nil)
	defer group.close()

	first := group.initial()
	if !first.sandboxOn {
		t.Fatal("initial build with a runner reports sandboxOn = false")
	}

	mu.Lock()
	runner = nil
	mu.Unlock()
	second := group.forRoot()
	if second == first {
		t.Fatal("runner removal reused the previous build")
	}
	if second.sandboxOn {
		t.Fatal("build without a runner reports sandboxOn = true")
	}
	waitBuildClosed(t, first)

	mu.Lock()
	runner = &fakeSandboxRunner{}
	mu.Unlock()
	third := group.forRoot()
	if third == second {
		t.Fatal("runner attachment reused the previous build")
	}
	if !third.sandboxOn {
		t.Fatal("build with a re-attached runner reports sandboxOn = false")
	}
	waitBuildClosed(t, second)
}

// TestRebindGroupOverrideCacheIncludesMode covers the per-run root cache: a
// cached override for a root must not survive a permission-mode change.
func TestRebindGroupOverrideCacheIncludesMode(t *testing.T) {
	runRoot := t.TempDir()
	settings := newMutableSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionWorkspace,
	})
	sandboxOf := func() (rigsandbox.Mode, rigsandbox.Runner) {
		return configuredSandboxMode(Config{}, settings), nil
	}
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return "" }, sandboxOf, nil)
	defer group.close()
	_ = group.initial()

	first := group.forRootValue(runRoot)
	if first.mode != rigsandbox.ModeWorkspaceWrite {
		t.Fatalf("override mode = %q, want workspace-write", first.mode)
	}
	if again := group.forRootValue(runRoot); again != first {
		t.Fatal("unchanged mode rebuilt the cached per-run override")
	}

	settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull)
	second := group.forRootValue(runRoot)
	if second == first {
		t.Fatal("mode change reused the cached per-run override")
	}
	if second.mode != rigsandbox.ModeDangerFullAccess {
		t.Fatalf("rebuilt override mode = %q, want danger-full-access", second.mode)
	}
}

// loadSandboxToolkit activates the toolkit module with a settings store and an
// optional fake sandbox service, returning the provided tool registry.
func loadSandboxToolkit(t *testing.T, settings contracts.SettingsService, service contracts.SandboxService, config map[string]any) *rigtool.Registry {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	if service != nil {
		if _, err := root.Provide(contracts.KeySandbox, contracts.SandboxService(service)); err != nil {
			t.Fatalf("provide sandbox service: %v", err)
		}
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "toolkit",
		Module: "zeokeel/toolkit",
		Config: config,
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	return tools
}

// invokeRegistryTool invokes one registry tool by name.
func invokeRegistryTool(t *testing.T, tools *rigtool.Registry, name, arguments string) rig.ToolResult {
	t.Helper()
	result, err := tools.Invoke(context.Background(), rig.ToolCall{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("invoke %s: %v", name, err)
	}
	return result
}

// TestModuleInjectsSandboxServiceRunnerIntoBash proves the module reads the
// optional contracts.KeySandbox service and injects its runner into the
// root-scoped bash tool.
func TestModuleInjectsSandboxServiceRunnerIntoBash(t *testing.T) {
	requireUnixShell(t)
	runner := &fakeSandboxRunner{rewrite: []string{"/bin/sh", "-c", "echo confined-by-service"}}
	service := &fakeSandboxService{runner: runner, available: true}
	tools := loadSandboxToolkit(t,
		newMutableSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionWorkspace}),
		service, map[string]any{"root": t.TempDir()})

	result := invokeRegistryTool(t, tools, "bash", `{"command":"echo original"}`)
	if result.IsError {
		t.Fatalf("bash failed: %q", result.Content)
	}
	if !strings.Contains(result.Content, "confined-by-service") {
		t.Fatalf("bash output = %q, want the sandbox service runner's output", result.Content)
	}
	calls := runner.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("runner Confine calls = %d, want 1", len(calls))
	}
	if got, want := strings.Join(calls[0], " "), "/bin/sh -c echo original"; got != want {
		t.Fatalf("runner confined argv = %q, want %q", got, want)
	}
}

// TestModuleWithoutSandboxServiceKeepsOldBehavior proves nil-safety: an
// assembly without the sandbox module still loads the toolkit and runs bash
// unchanged.
func TestModuleWithoutSandboxServiceKeepsOldBehavior(t *testing.T) {
	requireUnixShell(t)
	tools := loadSandboxToolkit(t, newMutableSettings(nil), nil, map[string]any{"root": t.TempDir()})
	result := invokeRegistryTool(t, tools, "bash", `{"command":"echo plain-old-behavior"}`)
	if result.IsError {
		t.Fatalf("bash failed: %q", result.Content)
	}
	if !strings.Contains(result.Content, "plain-old-behavior") {
		t.Fatalf("bash output = %q, want the unchanged command output", result.Content)
	}
}

// TestModuleRebuildsToolsOnSandboxAndModeChange exercises the whole no-restart
// path through the module: the runner appearing, a read-only permission change
// and a full-permission change each take effect on the next tool call.
func TestModuleRebuildsToolsOnSandboxAndModeChange(t *testing.T) {
	requireUnixShell(t)
	service := &fakeSandboxService{} // starts with no runner
	settings := newMutableSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionWorkspace,
	})
	tools := loadSandboxToolkit(t, settings, service, map[string]any{"root": t.TempDir()})

	// 1) No runner yet: the old, unconfined behavior.
	plain := invokeRegistryTool(t, tools, "bash", `{"command":"echo plain-one"}`)
	if plain.IsError || !strings.Contains(plain.Content, "plain-one") {
		t.Fatalf("bash before the runner appeared = %q (err %v)", plain.Content, plain.IsError)
	}

	// 2) The service starts returning a runner: the next call is confined
	// without any restart.
	runner := &fakeSandboxRunner{rewrite: []string{"/bin/sh", "-c", "echo confined-two"}}
	service.setRunner(runner)
	confined := invokeRegistryTool(t, tools, "bash", `{"command":"echo plain-two"}`)
	if confined.IsError || !strings.Contains(confined.Content, "confined-two") {
		t.Fatalf("bash after the runner appeared = %q (err %v), want confined output", confined.Content, confined.IsError)
	}
	if calls := runner.recordedCalls(); len(calls) != 1 {
		t.Fatalf("runner Confine calls = %d, want 1", len(calls))
	}

	// 3) Read-only permission is applied to the rebuilt policy: bash is denied.
	settings.Set(contracts.SettingAgentPermission, contracts.PermissionReadOnly)
	denied := invokeRegistryTool(t, tools, "bash", `{"command":"echo denied"}`)
	if !denied.IsError || !strings.Contains(denied.Content, "read-only") {
		t.Fatalf("bash under read-only permission = %q (err %v), want a read-only denial", denied.Content, denied.IsError)
	}

	// 4) Full permission switches the policy to danger-full-access (visible on
	// the policy the runner receives).
	settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull)
	full := invokeRegistryTool(t, tools, "bash", `{"command":"echo full-access"}`)
	if full.IsError {
		t.Fatalf("bash under full permission failed: %q", full.Content)
	}
	policies := runner.recordedPolicies()
	if len(policies) < 2 || policies[len(policies)-1] == nil {
		t.Fatalf("runner policies = %v, want a policy for the full-permission call", policies)
	}
	if got := policies[len(policies)-1].Mode(); got != rigsandbox.ModeDangerFullAccess {
		t.Fatalf("full-permission policy mode = %q, want danger-full-access", got)
	}
}
