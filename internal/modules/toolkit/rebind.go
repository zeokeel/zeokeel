package toolkit

import (
	"context"
	"fmt"
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
)

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
type rootScoped struct {
	root   string
	tools  []rig.Tool
	byName map[string]rig.Tool
	close  func()
}

// buildRootScoped builds the root-dependent slice of the Rig toolset for one
// workspace root. Empty root means "no workspace open": the sandbox policy then
// exposes no filesystem root and every write/edit/shell call is denied, which
// is the intended fail-closed behavior until the user opens a workspace.
func buildRootScoped(cfg Config, root string, timeout time.Duration) *rootScoped {
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

	tools := make([]rig.Tool, 0, 32)
	tools = append(tools, builtin.NewWithPolicyOptions(policy, builtin.WithBackgroundJobs(background))...)
	// pwsh is the only usable shell stack on Windows; registering it on every
	// platform matches full.All and keeps the tool surface stable.
	tools = append(tools, builtin.NewPwshWithPolicyOptions(policy, builtin.WithBackgroundJobs(background)))
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
		close: func() {
			_ = terminals.CloseAll()
			_ = background.CloseAll()
		},
	}
}

// rebindGroup keeps the root-scoped tool build for the current workspace root
// and rebuilds it when the root changes. Retired builds are only closed when
// the module unloads, so a workspace switch does not abort tool calls that are
// still running against the previous root.
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
// rebuilding and retiring the previous build when the root changed.
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

// close releases every root-scoped build that this module created.
func (g *rebindGroup) close() {
	g.mu.Lock()
	retired := append(g.retired, g.current)
	g.retired = nil
	g.current = nil
	g.mu.Unlock()
	for _, scoped := range retired {
		if scoped != nil && scoped.close != nil {
			scoped.close()
		}
	}
}

// boundTool is the stable registry entry for one root-scoped tool.
type boundTool struct {
	meta  rig.Tool
	group *rebindGroup
}

func (t *boundTool) Name() string           { return t.meta.Name() }
func (t *boundTool) Description() string    { return t.meta.Description() }
func (t *boundTool) Schema() map[string]any { return t.meta.Schema() }

func (t *boundTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	tool, ok := t.group.forRoot().byName[t.meta.Name()]
	if !ok {
		return rig.ToolResult{}, fmt.Errorf("%w: %q", rig.ErrToolNotFound, call.Name)
	}
	return tool.Invoke(ctx, call)
}
