package toolkit

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/tools/builtin"
	"github.com/zeokeel/zeokeel-rig/tools/editor"
	"github.com/zeokeel/zeokeel-rig/tools/image"
	"github.com/zeokeel/zeokeel-rig/tools/jobs"
	"github.com/zeokeel/zeokeel-rig/tools/misc"
	"github.com/zeokeel/zeokeel-rig/tools/patch"
	"github.com/zeokeel/zeokeel-rig/tools/sandboxgrant"
	"github.com/zeokeel/zeokeel-rig/tools/sandboxstatus"
	"github.com/zeokeel/zeokeel-rig/tools/search"
	"github.com/zeokeel/zeokeel-rig/tools/worktree"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/textenc"
)

// shellTools reports which shell tools one root-scoped build registers on goos:
//
//   - bash everywhere except Windows: Rig's bash tool always runs /bin/sh and
//     can never work on Windows, so registering it there only wastes rounds with
//     a tool that must fail.
//   - pwsh on Windows always (powershell.exe ships with the OS; the tool probes
//     pwsh.exe/powershell.exe itself at call time), and elsewhere only when a
//     PowerShell binary is actually on PATH.
//
// lookPath is injected so both platform branches are unit-testable without the
// test host's real PATH (exec.LookPath in production).
func shellTools(goos string, lookPath func(string) (string, error)) (bash, pwsh bool) {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return false, true
	}
	if lookPath == nil {
		return true, false
	}
	for _, name := range []string{"pwsh", "powershell"} {
		if _, err := lookPath(name); err == nil {
			return true, true
		}
	}
	return true, false
}

// workspaceRootSource returns a function that reports the root the agent tools
// must be bound to. An explicit assembly root other than "." pins the tools to
// that directory (deployments can still opt out of workspace tracking); the
// default "." follows the workspace currently open in the UI.
//
// The root is read at tool-call time on purpose: the desktop product launches
// before any workspace is opened, and a workspace may be added or switched
// while the process keeps running. On Windows a double-clicked executable
// starts with its working directory set to the install folder, so a static "."
// would silently sandbox the agent to the program directory instead of the
// user's workspace.
func workspaceRootSource(ctx cordis.Context, configured string) func() string {
	configured = strings.TrimSpace(configured)
	if configured != "" && configured != "." {
		return func() string { return configured }
	}
	workspace, _ := ctx.Get(contracts.KeyWorkspace)
	return func() string {
		if workspace == nil {
			return ""
		}
		current, ok, err := workspace.Current(context.Background())
		if err != nil || !ok {
			return ""
		}
		return strings.TrimSpace(current.Root)
	}
}

// rootScoped is one build of the tools whose filesystem/shell behavior depends
// on the workspace root. The build owns its terminal and background-job
// managers. Builds are RETAINED for the lifetime of the module (see
// rebindGroup): switching the workspace root or the permission/sandbox policy
// creates a new build with the new policy, but the previous build keeps its
// managers alive so its running jobs stay controllable by ID.
// closeNow releases the managers; it runs on module unload only.
//
// A build also records the linked OS sandbox mode and whether a runner was
// injected: both are part of the rebuild identity, so switching the permission
// preset or a change in OS-runner availability rebuilds the tools instead of
// serving a build with a stale policy or a stale runner.
type rootScoped struct {
	root string
	// policy is the sandbox policy every tool in this build shares. The agent
	// gate reads it (contracts.KeyToolPolicy) and hands it to the SDK's
	// gate.AllowSandboxedCommands, so the "already confined, skip the prompt"
	// decision uses the same policy the tools execute under.
	policy *sandbox.Policy
	// mode is the effective sandbox mode of this build (cfg.ReadOnly already
	// folded in), and sandboxOn reports whether an OS runner was injected.
	mode      sandbox.Mode
	sandboxOn bool
	// jobs is this build's background-job manager: the typed read model behind
	// contracts.JobService.Jobs (manager.Snapshot) and the stale-permission
	// report. Control still goes through the job_* tools.
	jobs   *jobs.Manager
	tools  []rig.Tool
	byName map[string]rig.Tool
	close  func()
	// drainJobNotices drains the root's pending background-job completion
	// notices (the toolkit turns them into model-facing lines and durable
	// execution events).
	drainJobNotices func() []jobs.Notice

	mu     sync.Mutex
	closed bool
	// done closes after the release hook ran; tests wait on it to observe that
	// the build's managers were actually torn down.
	done     chan struct{}
	doneOnce sync.Once
}

// closeNow releases the build synchronously (module unload). It is idempotent.
func (s *rootScoped) closeNow() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.runClose()
}

// isClosed reports whether the build has been released (test observability).
func (s *rootScoped) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// runClose invokes the build's release hook exactly once; callers marshal the
// closed flag.
func (s *rootScoped) runClose() {
	if s.close != nil {
		s.close()
	}
	s.doneOnce.Do(func() {
		if s.done != nil {
			close(s.done)
		}
	})
}

// closedChan returns a channel that is closed once the build's release hook ran.
func (s *rootScoped) closedChan() <-chan struct{} { return s.done }

// waitClosed blocks until the build was released or timeout elapses (tests).
func (s *rootScoped) waitClosed(timeout time.Duration) bool {
	if s == nil || s.done == nil {
		return true
	}
	select {
	case <-s.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// buildRootScoped builds the root-dependent slice of the Rig toolset for one
// workspace root. Empty root means "no workspace open": under the wrapping
// modes (read-only / workspace-write) the sandbox policy then exposes no
// filesystem root and every write/edit/shell call is denied — the intended
// fail-closed behavior until the user opens a workspace.
//
// Full access is the documented exception (2026-11 audit RG-07): the rig
// policy admits an empty root list in full mode, and relative paths then
// resolve against the process working directory, so file/shell tools run
// unconfined from the process cwd even with no workspace open. The system
// prompt's working-directory line and the settings sandbox note disclose the
// same exchange; under full this is a disclosure, not a fail-closed guarantee.
//
// mode is the linked OS sandbox mode and runner the optional OS runner to
// inject; a nil runner keeps the exact pre-sandbox behavior (no wrapping).
func buildRootScoped(cfg Config, root string, timeout time.Duration, mode sandbox.Mode, runner sandbox.Runner) *rootScoped {
	return buildRootScopedFor(cfg, root, timeout, mode, runner, runtime.GOOS, exec.LookPath)
}

// buildRootScopedFor is buildRootScoped with the platform and PATH probe
// injectable for tests.
func buildRootScopedFor(cfg Config, root string, timeout time.Duration, mode sandbox.Mode, runner sandbox.Runner, goos string, lookPath func(string) (string, error)) *rootScoped {
	policy := newPolicy(cfg, root, timeout, mode)
	// The runner is injected into every command-executing surface: the built-in
	// bash/pwsh tools, the background job manager and the worktree git calls. A
	// nil runner adds no option at all, so the old behavior is preserved exactly.
	jobsOpts := []jobs.Option{jobs.WithPolicy(policy)}
	var worktreeOpts []worktree.Option
	if runner != nil {
		jobsOpts = append(jobsOpts, jobs.WithRunner(runner))
		worktreeOpts = append(worktreeOpts, worktree.WithRunner(runner))
	}
	background := jobs.NewManager(jobsOpts...)
	// The working-directory tool and the file tools share one session cwd:
	// after working_directory switches, relative file paths follow it.
	workingDirectory := misc.NewWorkingDirectoryState(root)
	builtinOpts := []builtin.Option{
		builtin.WithBackgroundJobs(background),
		builtin.WithWorkingDirectory(workingDirectory.Current),
	}
	// rig v0.26 owns the read-only semantics: with an OS runner attached a
	// read-only policy may execute commands (the runner confines every write),
	// without one the policy itself denies them. Under rig v0.19 the builtin
	// shell refused commands outright in that mode ("sandbox policy is
	// read-only"), so ordinary read-only work (`ls`, `git status`) could not run
	// even on hosts that do have a sandbox. Personal-client stance: use the
	// SDK's rule as-is and always inject the runner.
	if runner != nil {
		builtinOpts = append(builtinOpts, builtin.WithRunner(runner))
	}
	bash, pwsh := shellTools(goos, lookPath)
	tools := make([]rig.Tool, 0, 32)
	// The built-in policy set is the six file tools plus the bash tool: keep the
	// file tools on every platform, drop only the shell tool that cannot work
	// there.
	for _, tool := range builtin.NewWithPolicyOptions(policy, builtinOpts...) {
		if !bash && tool.Name() == builtin.ToolBash {
			continue
		}
		tools = append(tools, tool)
	}
	if pwsh {
		tools = append(tools, builtin.NewPwshWithPolicyOptions(policy, builtinOpts...))
	}
	tools = append(tools,
		editor.New(policy, editor.WithWorkingDirectory(workingDirectory.Current)),
		image.New(policy, image.WithWorkingDirectory(workingDirectory.Current)))
	// DSH's bash tool owns background execution (run_in_background); the
	// product keeps only the job observer half (output/kill/list). job_start and
	// job_wait are deliberately not model-facing.
	for _, tool := range background.Tools() {
		switch tool.Name() {
		case "job_start", "job_wait":
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, worktree.Tools(policy, worktreeOpts...)...)
	tools = append(tools, workingDirectory)
	// SDK sandbox affordances (personal harness): the model can read the policy
	// it is under (sandbox_status, read-only and auto-allowed) and ask for one
	// writable directory when a tool insists on a fixed path
	// (request_sandbox_access; the approval gate decides, since it is not in the
	// read-only set). Both are bound to this build's policy, so a workspace or
	// permission change rebuilds them.
	statusOpts := []sandboxstatus.Option{}
	if runner != nil {
		statusOpts = append(statusOpts, sandboxstatus.WithRunnerInfo(func() (string, sandbox.Enforcement) {
			name, enforcement, _ := sandbox.RunnerStatus(runner)
			return name, enforcement
		}))
	}
	tools = append(tools,
		sandboxstatus.New(policy, statusOpts...),
		sandboxgrant.New(policy),
		// Multi-file atomic patches (SDK): the single-file editors cannot add,
		// delete and move files in one call. A policy is mandatory — the tool
		// refuses to run without one.
		patch.New(root, patch.WithPolicy(policy)),
		// Ranked retrieval (SDK): BM25 over content plus fuzzy file names. Its
		// traversal does not go through Policy.OpenPath, so the hidden-path
		// predicate is wired explicitly — otherwise search would be the one
		// surface that still reads hidden files.
		search.New(root, search.WithHiddenFunc(func(abs string) bool {
			return policy.Hidden(abs)
		})),
	)

	byName := make(map[string]rig.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	return &rootScoped{
		root:      root,
		policy:    policy,
		mode:      policyMode(cfg, mode),
		sandboxOn: runner != nil,
		jobs:      background,
		tools:     tools,
		byName:    byName,
		done:      make(chan struct{}),
		close: func() {
			_ = background.CloseAll()
		},
		drainJobNotices: func() []jobs.Notice {
			return background.PendingNotices()
		},
	}
}

// backgroundJobNoticeText renders one settled-job completion notice as the
// model-facing line the agent loop appends (Rig's documented in-session job
// notification contract).
func backgroundJobNoticeText(notice jobs.Notice) string {
	text := strings.TrimSpace(notice.Text)
	if text == "" {
		text = notice.JobID + ": " + string(notice.Status)
	}
	return text
}

// rebindGroup keeps one tool build per (root, mode, runner-on) identity for the
// whole module lifetime. A build is never retired by a workspace/permission
// switch: the new identity gets a new build (so new work uses the new policy),
// while the old build keeps its background jobs and terminal sessions alive and
// reachable by ID through the execution-tool routing. Everything is released on
// module unload.
type rebindGroup struct {
	cfg     Config
	timeout time.Duration
	rootOf  func() string
	// sandboxOf resolves the (mode, runner) pair the next build must use. The
	// toolkit reads both from live services on every call, so switching the
	// permission preset or a change in OS-runner availability rebuilds the tools
	// without a restart. A nil resolver keeps the historical behavior:
	// workspace-write mode with no runner injection.
	sandboxOf func() (sandbox.Mode, sandbox.Runner)
	logf      func(string, ...any)

	mu      sync.Mutex
	current *rootScoped
	// builds keeps every build ever created in this module, keyed by identity.
	builds map[rebindKey]*rootScoped
	// order preserves creation order for deterministic merging/draining.
	order []*rootScoped
	// jobOwners/terminalOwners route follow-up execution calls to the build that
	// created the job/terminal, even after a workspace/permission rebuild.
	jobOwners        map[string]*rootScoped
	terminalOwners   map[string]*rootScoped
	jobSessions      map[string]string
	terminalSessions map[string]string
	// events records durable execution lifecycle events (nil = no store wired).
	events *executionEvents
}

// rebindKey is the rebuild identity of a root-scoped build: a change to any
// field must produce a new build.
type rebindKey struct {
	root      string
	mode      sandbox.Mode
	sandboxOn bool
}

func newRebindGroup(cfg Config, timeout time.Duration, rootOf func() string, sandboxOf func() (sandbox.Mode, sandbox.Runner), logf func(string, ...any)) *rebindGroup {
	return &rebindGroup{
		cfg:              cfg,
		timeout:          timeout,
		rootOf:           rootOf,
		sandboxOf:        sandboxOf,
		logf:             logf,
		builds:           make(map[rebindKey]*rootScoped),
		jobOwners:        make(map[string]*rootScoped),
		terminalOwners:   make(map[string]*rootScoped),
		jobSessions:      make(map[string]string),
		terminalSessions: make(map[string]string),
	}
}

// sandboxState resolves the sandbox identity of the next build.
func (g *rebindGroup) sandboxState() (sandbox.Mode, sandbox.Runner) {
	if g.sandboxOf == nil {
		return sandbox.ModeWorkspaceWrite, nil
	}
	return g.sandboxOf()
}

// policyForRoot returns the policy of the tool build that dispatches calls for
// root (empty = the current workspace), building/reusing that retained build
// when needed. It is the value behind contracts.ToolPolicy.
func (g *rebindGroup) policyForRoot(root string) *sandbox.Policy {
	if g == nil {
		return nil
	}
	scoped := g.forRootValue(root)
	if scoped == nil {
		return nil
	}
	return scoped.policy
}

// policySourceAdapter exposes the group's live policies to the agent gate.
type policySourceAdapter struct{ group *rebindGroup }

// PolicyFor implements contracts.ToolPolicy.
func (a policySourceAdapter) PolicyFor(root string) *sandbox.Policy {
	if a.group == nil {
		return nil
	}
	return a.group.policyForRoot(root)
}

// buildKey folds cfg.ReadOnly and the runner-on state into the rebuild identity.
func (g *rebindGroup) buildKey(root string, mode sandbox.Mode, runner sandbox.Runner) rebindKey {
	return rebindKey{root: root, mode: policyMode(g.cfg, mode), sandboxOn: runner != nil}
}

// matches reports whether a build was made for the given identity.
func (s *rootScoped) matches(key rebindKey) bool {
	return s != nil && s.root == key.root && s.mode == key.mode && s.sandboxOn == key.sandboxOn
}

// addBuildLocked registers one build in the retained set.
func (g *rebindGroup) addBuildLocked(key rebindKey, scoped *rootScoped) {
	if scoped == nil {
		return
	}
	if g.builds == nil {
		g.builds = make(map[rebindKey]*rootScoped)
	}
	if g.builds[key] == nil {
		g.builds[key] = scoped
		g.order = append(g.order, scoped)
	}
}

// findBuildLocked returns the retained build for an identity.
func (g *rebindGroup) findBuildLocked(key rebindKey) *rootScoped {
	if g.builds == nil {
		return nil
	}
	return g.builds[key]
}

// initial builds the tool set for the root that is active at activation time.
// The tools double as the stable metadata templates of the registry proxies.
func (g *rebindGroup) initial() *rootScoped {
	mode, runner := g.sandboxState()
	root := strings.TrimSpace(g.rootOf())
	key := g.buildKey(root, mode, runner)
	scoped := buildRootScoped(g.cfg, root, g.timeout, mode, runner)
	g.mu.Lock()
	g.current = scoped
	g.addBuildLocked(key, scoped)
	g.mu.Unlock()
	return scoped
}

// forRoot returns the build for the workspace root that is current now. The
// same (root, mode, runner-on) identity reuses its retained build; a changed
// identity adds a new build and leaves the previous one alive for its jobs and
// terminals.
func (g *rebindGroup) forRoot() *rootScoped {
	root := strings.TrimSpace(g.rootOf())
	mode, runner := g.sandboxState()
	key := g.buildKey(root, mode, runner)
	g.mu.Lock()
	if current := g.current; current.matches(key) {
		g.mu.Unlock()
		return current
	}
	if retained := g.findBuildLocked(key); retained != nil {
		g.current = retained
		g.mu.Unlock()
		return retained
	}
	next := buildRootScoped(g.cfg, root, g.timeout, mode, runner)
	g.current = next
	g.addBuildLocked(key, next)
	g.mu.Unlock()
	if g.logf != nil {
		g.logf("[toolkit] tool root follows workspace: %q (mode %q, os sandbox %t)", root, key.mode, key.sandboxOn)
	}
	return next
}

// forRootValue returns the build for one explicit workspace root: the current
// build when the identities match, a retained per-run build otherwise. Empty
// root falls back to the current-workspace behavior.
func (g *rebindGroup) forRootValue(root string) *rootScoped {
	root = strings.TrimSpace(root)
	if root == "" {
		return g.forRoot()
	}
	mode, runner := g.sandboxState()
	key := g.buildKey(root, mode, runner)
	g.mu.Lock()
	if current := g.current; current.matches(key) {
		g.mu.Unlock()
		return current
	}
	if retained := g.findBuildLocked(key); retained != nil {
		g.mu.Unlock()
		return retained
	}
	scoped := buildRootScoped(g.cfg, root, g.timeout, mode, runner)
	g.addBuildLocked(key, scoped)
	g.mu.Unlock()
	if g.logf != nil {
		g.logf("[toolkit] tool root bound to run workspace: %q (mode %q, os sandbox %t)", root, key.mode, key.sandboxOn)
	}
	return scoped
}

func (g *rebindGroup) proxies(scoped *rootScoped) []rig.Tool {
	proxies := make([]rig.Tool, 0, len(scoped.tools))
	for _, tool := range scoped.tools {
		proxies = append(proxies, &boundTool{meta: tool, group: g})
	}
	return proxies
}

// buildsSnapshot returns a stable copy of every retained build.
func (g *rebindGroup) buildsSnapshot() []*rootScoped {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]*rootScoped, 0, len(g.order)+1)
	out = append(out, g.order...)
	if g.current != nil {
		found := false
		for _, scoped := range out {
			if scoped == g.current {
				found = true
				break
			}
		}
		if !found {
			out = append(out, g.current)
		}
	}
	return out
}

// close releases every root-scoped build this module created (module unload).
// It is idempotent and synchronous.
func (g *rebindGroup) close() {
	g.mu.Lock()
	builds := make([]*rootScoped, 0, len(g.builds)+1)
	seen := make(map[*rootScoped]bool, len(g.builds)+1)
	for _, scoped := range g.order {
		if scoped != nil && !seen[scoped] {
			seen[scoped] = true
			builds = append(builds, scoped)
		}
	}
	for _, scoped := range g.builds {
		if scoped != nil && !seen[scoped] {
			seen[scoped] = true
			builds = append(builds, scoped)
		}
	}
	if g.current != nil && !seen[g.current] {
		builds = append(builds, g.current)
	}
	g.current = nil
	g.builds = nil
	g.order = nil
	g.jobOwners = nil
	g.terminalOwners = nil
	g.jobSessions = nil
	g.terminalSessions = nil
	g.mu.Unlock()
	for _, scoped := range builds {
		scoped.closeNow()
	}
}

// drainNotices pulls pending background-job completion notices from EVERY
// retained build, not just the current one: a job that finished in a previous
// workspace/permission build must still reach the model (and its durable
// job_finished audit event) instead of being dropped by the switch. The drain is
// consuming, so repeated per-round proxy calls do not duplicate notices.
func (g *rebindGroup) drainNotices() []string {
	builds := g.buildsSnapshot()
	var notices []string
	for _, scoped := range builds {
		if scoped == nil || scoped.drainJobNotices == nil {
			continue
		}
		pending := scoped.drainJobNotices()
		for _, notice := range pending {
			if text := backgroundJobNoticeText(notice); text != "" {
				notices = append(notices, text)
			}
			g.recordJobFinished(notice)
		}
	}
	return notices
}

// stalePermissionJobs reports the RUNNING jobs retained in builds whose
// permission identity (mode + runner-on) differs from the identity resolved for
// the current workspace root right now (2026-11 audit RG-09). A permission
// downgrade does not retroactively confine jobs spawned under the old mode —
// confinement is applied at spawn, an OS fact this read model does not change.
// It exists so the UI can disclose "N running jobs still use the old permission
// mode" and offer the kill entry (KillJob routes by id to the owner build).
//
// The read is side-effect free: it resolves the current identity WITHOUT
// creating a build, scopes to the caller's current workspace root (the same
// scope as the job chip's merged list) and lists each older build's jobs through
// that build's own manager. Finished jobs are excluded.
func (g *rebindGroup) stalePermissionJobs() []contracts.BackgroundJob {
	root := g.contextRoot(nil)
	mode, runner := g.sandboxState()
	key := g.buildKey(root, mode, runner)
	var stale []contracts.BackgroundJob
	for _, scoped := range g.buildsForRoot(root) {
		if scoped == nil || scoped.matches(key) {
			continue
		}
		if scoped.jobs == nil {
			continue
		}
		for _, row := range backgroundJobsFromSnapshots(scoped.jobs.Snapshot(), time.Now()) {
			if row.Running {
				stale = append(stale, row)
			}
		}
	}
	return stale
}

// listJobs implements the toolkit's typed job read model: the retained jobs of
// the build that currently dispatches calls for the current workspace root.
func (g *rebindGroup) listJobs() []contracts.BackgroundJob {
	if g == nil {
		return nil
	}
	scoped := g.forRoot()
	if scoped == nil || scoped.jobs == nil {
		return nil
	}
	return backgroundJobsFromSnapshots(scoped.jobs.Snapshot(), time.Now())
}

// runningJob reports whether one job id is running in any build retained for the
// caller's current workspace root (a job spawned before a permission rebuild
// still belongs to its older build).
func (g *rebindGroup) runningJob(ctx context.Context, id string) bool {
	if g == nil {
		return false
	}
	for _, scoped := range g.buildsForRoot(g.contextRoot(ctx)) {
		if scoped == nil || scoped.jobs == nil {
			continue
		}
		for _, snap := range scoped.jobs.Snapshot() {
			if snap.ID == id {
				return snap.State == "running"
			}
		}
	}
	return false
}

// boundTool is the stable registry entry for one root-scoped tool.
type boundTool struct {
	meta  rig.Tool
	group *rebindGroup
}

func (t *boundTool) Name() string           { return t.meta.Name() }
func (t *boundTool) Description() string    { return t.meta.Description() }
func (t *boundTool) Schema() map[string]any { return t.meta.Schema() }

// PendingNotices implements rig.NoticeSource. Every root-scoped proxy reports
// (empty) notices because Rig only asks the tools a round actually called, and
// a job that finished must reach the model even in a round that used only
// file tools; the first drain empties the queues, so the per-round repeats are
// harmless.
func (t *boundTool) PendingNotices() []string {
	return t.group.drainNotices()
}

func (t *boundTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	// Execution tools (jobs/terminals) route by their resource ID across builds;
	// every other tool uses the build for the root of this call.
	if t.group.isExecutionTool(call.Name) {
		return t.group.invokeExecutionTool(ctx, call)
	}
	if root := contracts.WorkspaceRootFromContext(ctx); root != "" {
		return invokeRootScoped(ctx, t.group.forRootValue(root), call)
	}
	return invokeRootScoped(ctx, t.group.forRoot(), call)
}

// invokeRootScoped dispatches one call to a specific root-scoped build. It is
// the single dispatch seam for both the registry proxies and host-facing
// callers such as the job service; output goes through one repair point.
func invokeRootScoped(ctx context.Context, scoped *rootScoped, call rig.ToolCall) (rig.ToolResult, error) {
	if scoped == nil {
		return rig.ToolResult{}, fmt.Errorf("%w: %q", rig.ErrToolNotFound, call.Name)
	}
	tool, ok := scoped.byName[call.Name]
	if !ok {
		return rig.ToolResult{}, fmt.Errorf("%w: %q", rig.ErrToolNotFound, call.Name)
	}
	result, err := tool.Invoke(ctx, call)
	// One central repair point for every root-scoped tool (pwsh, bash,
	// terminal_*, job_*, file tools, ...): Windows console programs emit
	// console-code-page bytes (GBK/cp936) and PowerShell may emit UTF-16LE, so
	// raw captured output can be invalid UTF-8. Well-formed UTF-8 is returned
	// unchanged; the wrapper keeps the inner tool's error and IsError semantics.
	result.Content = textenc.RepairOutput(result.Content)
	return result, err
}

// invokeJobTool implements jobToolInvoker: host-facing job reads/controls use
// the same ID routing as the model-facing tools, so the header's job chip can
// still list and kill jobs created in another (retained) build.
func (g *rebindGroup) invokeJobTool(ctx context.Context, name, arguments string) (rig.ToolResult, error) {
	return g.invokeExecutionTool(ctx, rig.ToolCall{Name: name, Arguments: arguments})
}
