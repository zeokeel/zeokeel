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
	"github.com/zeokeel/zeokeel-rig/tools/builtin"
	"github.com/zeokeel/zeokeel-rig/tools/editor"
	"github.com/zeokeel/zeokeel-rig/tools/image"
	"github.com/zeokeel/zeokeel-rig/tools/jobs"
	"github.com/zeokeel/zeokeel-rig/tools/misc"
	"github.com/zeokeel/zeokeel-rig/tools/terminal"
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
// managers; close releases them when the build is retired.
//
// A retired build is closed as soon as it has no in-flight tool call: after a
// workspace switch its background jobs and terminal sessions are no longer
// reachable from the model (the job_*/terminal_* proxies dispatch to the new
// build), so leaving them running would leak processes and ports forever.
// enter/leave count in-flight calls so a switch never aborts a call that is
// still running against the old root.
type rootScoped struct {
	root   string
	tools  []rig.Tool
	byName map[string]rig.Tool
	close  func()
	// drainNotices returns the root's pending background-job completion
	// notices for the agent loop (rig.NoticeSource on the tool proxies).
	drainNotices func() []string

	mu       sync.Mutex
	inflight int
	retired  bool
	closed   bool
	// done closes after the release hook ran; tests wait on it to observe that
	// the build's managers were actually torn down.
	done     chan struct{}
	doneOnce sync.Once
}

// enter registers one in-flight tool call against this build.
func (s *rootScoped) enter() {
	s.mu.Lock()
	s.inflight++
	s.mu.Unlock()
}

// leave releases one in-flight call. A retired build closes itself as soon as
// nothing is running against it anymore.
func (s *rootScoped) leave() {
	s.mu.Lock()
	s.inflight--
	closeNow := s.inflight <= 0 && s.retired && !s.closed
	if closeNow {
		s.closed = true
	}
	s.mu.Unlock()
	if closeNow {
		s.runClose()
	}
}

// retire marks the build as retired: an idle build is closed immediately (in
// the background, so a workspace switch does not wait on process teardown); a
// busy build closes on its last leave.
func (s *rootScoped) retire() {
	s.mu.Lock()
	s.retired = true
	closeNow := s.inflight <= 0 && !s.closed
	if closeNow {
		s.closed = true
	}
	s.mu.Unlock()
	if closeNow {
		go s.runClose()
	}
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

// closed returns a channel that is closed once the build's release hook ran.
func (s *rootScoped) closedChan() <-chan struct{} { return s.done }

// buildRootScoped builds the root-dependent slice of the Rig toolset for one
// workspace root. Empty root means "no workspace open": the sandbox policy then
// exposes no filesystem root and every write/edit/shell call is denied, which
// is the intended fail-closed behavior until the user opens a workspace.
func buildRootScoped(cfg Config, root string, timeout time.Duration) *rootScoped {
	return buildRootScopedFor(cfg, root, timeout, runtime.GOOS, exec.LookPath)
}

// buildRootScopedFor is buildRootScoped with the platform and PATH probe
// injectable for tests.
func buildRootScopedFor(cfg Config, root string, timeout time.Duration, goos string, lookPath func(string) (string, error)) *rootScoped {
	policy := newPolicy(cfg, root, timeout)
	background := jobs.NewManager(jobs.WithPolicy(policy))
	terminalOpts := []terminal.Option{terminal.WithPolicy(policy)}
	switch strings.ToLower(strings.TrimSpace(cfg.TerminalTransport)) {
	case "pty":
		terminalOpts = append(terminalOpts, terminal.WithTransport(terminal.TransportPTY))
	case "pipe":
		terminalOpts = append(terminalOpts, terminal.WithTransport(terminal.TransportPipe))
	}
	terminals := terminal.NewManager(terminalOpts...)

	bash, pwsh := shellTools(goos, lookPath)
	tools := make([]rig.Tool, 0, 32)
	// The built-in policy set is the six file tools plus the bash tool: keep the
	// file tools on every platform, drop only the shell tool that cannot work
	// there.
	for _, tool := range builtin.NewWithPolicyOptions(policy, builtin.WithBackgroundJobs(background)) {
		if !bash && tool.Name() == builtin.ToolBash {
			continue
		}
		tools = append(tools, tool)
	}
	if pwsh {
		tools = append(tools, builtin.NewPwshWithPolicyOptions(policy, builtin.WithBackgroundJobs(background)))
	}
	tools = append(tools, editor.New(policy), image.New(policy))
	tools = append(tools, terminals.ToolsWithList()...)
	tools = append(tools, background.Tools()...)
	tools = append(tools, worktree.Tools(policy)...)
	tools = append(tools, misc.NewWorkingDirectory(root))

	byName := make(map[string]rig.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	return &rootScoped{
		root:   root,
		tools:  tools,
		byName: byName,
		done:   make(chan struct{}),
		close: func() {
			_ = terminals.CloseAll()
			_ = background.CloseAll()
		},
		drainNotices: func() []string {
			return backgroundJobNotices(background)
		},
	}
}

// backgroundJobNotices drains the manager's settled-job completion notices as
// model-facing lines. Rig's job prompt ("You are notified in-session when a job
// finishes") only holds when the host hands these notices to the agent loop;
// the loop appends them as a user message after a tool round, mirroring DSH's
// in-session job notifications.
func backgroundJobNotices(manager *jobs.Manager) []string {
	if manager == nil {
		return nil
	}
	pending := manager.PendingNotices()
	if len(pending) == 0 {
		return nil
	}
	notices := make([]string, 0, len(pending))
	for _, notice := range pending {
		text := strings.TrimSpace(notice.Text)
		if text == "" {
			text = notice.JobID + ": " + string(notice.Status)
		}
		notices = append(notices, text)
	}
	return notices
}

// rebindGroup keeps the root-scoped tool build for the current workspace root
// and rebuilds it when the root changes. A retired build closes as soon as it is
// idle, so a workspace switch does not abort a tool call that is still running
// against the previous root but it also does not orphan that build's background
// jobs and terminals (they are unreachable from the model after the switch).
type rebindGroup struct {
	cfg     Config
	timeout time.Duration
	rootOf  func() string
	logf    func(string, ...any)

	mu      sync.Mutex
	current *rootScoped
	retired []*rootScoped
}

func newRebindGroup(cfg Config, timeout time.Duration, rootOf func() string, logf func(string, ...any)) *rebindGroup {
	return &rebindGroup{cfg: cfg, timeout: timeout, rootOf: rootOf, logf: logf}
}

// initial builds the tool set for the root that is active at activation time.
// The tools double as the stable metadata templates of the registry proxies.
func (g *rebindGroup) initial() *rootScoped {
	scoped := buildRootScoped(g.cfg, strings.TrimSpace(g.rootOf()), g.timeout)
	g.mu.Lock()
	g.current = scoped
	g.mu.Unlock()
	return scoped
}

// forRoot returns the build for the workspace root that is current now,
// rebuilding and retiring the previous build when the root changed. The retired
// build releases its background jobs and terminals once its in-flight calls end.
func (g *rebindGroup) forRoot() *rootScoped {
	root := strings.TrimSpace(g.rootOf())
	g.mu.Lock()
	current := g.current
	if current != nil && current.root == root {
		g.mu.Unlock()
		return current
	}
	next := buildRootScoped(g.cfg, root, g.timeout)
	g.current = next
	if current != nil {
		g.retired = append(g.retired, current)
	}
	g.mu.Unlock()
	if current != nil {
		current.retire()
	}
	if g.logf != nil {
		g.logf("[toolkit] tool root follows workspace: %q", root)
	}
	return next
}

// proxies wraps every root-scoped tool in a registry-visible proxy: the model
// sees a stable tool name/description/schema, while invoke dispatches to the
// build belonging to the workspace that is current at call time.
func (g *rebindGroup) proxies(scoped *rootScoped) []rig.Tool {
	proxies := make([]rig.Tool, 0, len(scoped.tools))
	for _, tool := range scoped.tools {
		proxies = append(proxies, &boundTool{meta: tool, group: g})
	}
	return proxies
}

// close releases every root-scoped build that this module created. It runs on
// module unload, so it closes synchronously; a build that already closed
// itself is skipped.
func (g *rebindGroup) close() {
	g.mu.Lock()
	retired := append(g.retired, g.current)
	g.retired = nil
	g.current = nil
	g.mu.Unlock()
	for _, scoped := range retired {
		if scoped != nil {
			scoped.closeNow()
		}
	}
}

// drainNotices pulls the current build's pending background-job completion
// notices. It never rebuilds for a changed root: notice draining must not have
// the side effect of retiring the active build.
func (g *rebindGroup) drainNotices() []string {
	g.mu.Lock()
	current := g.current
	g.mu.Unlock()
	if current == nil || current.drainNotices == nil {
		return nil
	}
	return current.drainNotices()
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
// file tools; the first drain empties the queue, so the per-round repeats are
// harmless.
func (t *boundTool) PendingNotices() []string {
	return t.group.drainNotices()
}

func (t *boundTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	scoped := t.group.forRoot()
	// Count the call against the build so a workspace switch retires the old
	// build without aborting a call that is still running against it.
	scoped.enter()
	defer scoped.leave()
	tool, ok := scoped.byName[t.meta.Name()]
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
