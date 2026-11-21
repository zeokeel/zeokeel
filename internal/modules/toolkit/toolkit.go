// Package toolkit assembles the ZeoKeel Rig toolset into the product's kernel
// tree. The product owns the policy and the wiring; Rig supplies the tool
// implementations:
//
//   - sandbox policy (roots, read-only, shell allowlist, network, caps)
//   - built-in file/shell/pwsh tools, editor and image tools
//   - background jobs (the stateful terminal tools were removed from the
//     product; their ID routing is kept defensively, see execution_routing.go)
//   - web fetch/search, and remote MCP servers over Streamable HTTP
//
// The resulting *tools.Registry is provided as hosttools.KeyRegistry (the
// backend seam) and, as the narrow rig-free contracts.ToolCatalog, as
// contracts.KeyToolCatalog;
// the host-tools module adds product-only tools to the same registry.
package toolkit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/tools/misc"
	"github.com/zeokeel/zeokeel-rig/tools/schedule"
	"github.com/zeokeel/zeokeel-rig/tools/skill"
	"github.com/zeokeel/zeokeel-rig/tools/web"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
	"github.com/zeokeel/zeokeel/internal/memorytools"
	"github.com/zeokeel/zeokeel/internal/websearch"
)

// MCPServer configures one MCP server: a Streamable HTTP endpoint or a stdio
// child process. A non-empty Command (or Transport "stdio") selects stdio and
// takes priority over URL.
type MCPServer struct {
	// Name is the server identity used in tool names (default: "remote").
	Name string `json:"name,omitempty"`
	// URL is the MCP endpoint for HTTP transports.
	URL string `json:"url,omitempty"`
	// Transport is "", "http" or "stdio"; empty infers from URL/Command.
	Transport string `json:"transport,omitempty"`
	// Command is the stdio MCP server executable (required for stdio).
	Command string `json:"command,omitempty"`
	// Args and Env are the stdio child process arguments/environment.
	Args []string `json:"args,omitempty"`
	Env  []string `json:"env,omitempty"`
	// Headers are extra request headers (for example Authorization).
	Headers map[string]string `json:"headers,omitempty"`
	// TimeoutMs bounds one request (0 = 30s).
	TimeoutMs int `json:"timeoutMs,omitempty"`
	// Auth is the HTTP authentication mode for this row (nil = none).
	Auth *contracts.MCPAuthConfig `json:"auth,omitempty"`
}

// Config is the module's typed assembly config.
type Config struct {
	// Root is the workspace root the file tools are sandboxed to. Empty or "."
	// means "follow the workspace currently open in the UI" (the desktop tool
	// root then moves with the workspace and stays fail-closed while no
	// workspace is open). Any other value pins the tools to that directory.
	Root string `json:"root"`
	// ShellTimeoutMs bounds shell tool executions (0 = 30s).
	ShellTimeoutMs int `json:"shellTimeoutMs"`
	// ReadOnly disables file writes and shell execution.
	ReadOnly bool `json:"readOnly"`
	// Network allows web_fetch and outbound connections from shell tools.
	Network bool `json:"network"`
	// CommandAllowlist optionally restricts which command names shell tools may
	// run. nil/empty is the personal-client default: any command is allowed and
	// the approval gate decides per call (Rig's documented recommendation). A
	// non-empty list keeps shell execution to those bare names.
	CommandAllowlist []string `json:"commandAllowlist"`
	// AllowShellOperators is kept for assembly compatibility and is ignored:
	// shell grammar (pipes, lists, &&, ||, redirections, leading env
	// assignments) is always allowed now. Restricting it only produced
	// "operator not allowed" loops on ordinary development commands, which is
	// the opposite of what a personal harness needs.
	AllowShellOperators bool `json:"allowShellOperators"`
	// DeniedGlobs hides files (e.g. "**/.env") from every file tool. The
	// opt-in settings row toolkit.deniedGlobs overrides this list (default
	// empty = nothing hidden). Shell tools can still read denied files — this
	// is file-surface defense in depth, not a security boundary.
	DeniedGlobs []string `json:"deniedGlobs"`
	// MaxOutputBytes caps one tool result (0 = tool default).
	MaxOutputBytes int `json:"maxOutputBytes"`
	// IgnorePermissionSetting pins the OS sandbox mode to the assembly default
	// (workspace-write, or read-only with ReadOnly) instead of following the
	// stored agent.permission preset. Headless/ACP products set it so a desktop
	// preset left in a shared settings store cannot change their OS confinement
	// — the same contract the approvals module's ignorePermissionSetting pins
	// for the approval gate (2026-11 audit).
	IgnorePermissionSetting bool `json:"ignorePermissionSetting"`
	// MCPServers are remote MCP servers exposed as tools/resources/prompts.
	MCPServers []MCPServer `json:"mcpServers"`
}

// Module provides the tool registry built from the Rig toolset.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel/toolkit", func(ctx cordis.Context, cfg Config) error {
		logger, _ := ctx.Get(contracts.KeyLogger)
		logf := func(format string, args ...any) {
			if logger != nil {
				logger.Printf(format, args...)
			}
		}
		// The data directory is optional: a headless/unit-test composition
		// without it keeps the task board in memory.
		dataDir, _ := ctx.Get(contracts.KeyDataDir)
		settingsService, hasSettings := ctx.Get(contracts.KeySettings)
		// The capability settings override the module config for the shell
		// tool: the timeout (toolkit.shellTimeoutSeconds, 5–600s) replaces
		// Config.ShellTimeoutMs and the output cap (toolkit.shellMaxOutputKiB,
		// 0 = the rig default) replaces Config.MaxOutputBytes for the
		// root-scoped tools (files, shell, editor). Both are resolved
		// here, at activation: the settings page says a restart applies them.
		// policyCfg carries only those values; the MCP runtime below keeps
		// the assembly config's own output cap. The opt-in denied-globs row
		// (toolkit.deniedGlobs, default empty) joins the same policy config so
		// every file-tool build and the LSP path checks hide the same files.
		timeout := shellTimeout(cfg, settingsService, hasSettings)
		policyCfg := cfg
		policyCfg.MaxOutputBytes = shellMaxOutputBytes(cfg, settingsService, hasSettings)
		policyCfg.DeniedGlobs = resolveDeniedGlobs(cfg, settingsService, hasSettings, logf)
		// The OS sandbox is optional: an assembly without the sandbox module
		// keeps the pre-sandbox behavior (nil runner, approval-only boundary).
		sandboxService, hasSandbox := ctx.Get(contracts.KeySandbox)
		// sandboxOf is read on every root-scoped build, so an agent.permission
		// preset change or OS-runner availability takes effect on the next tool
		// call without a restart.
		sandboxOf := func() (sandbox.Mode, sandbox.Runner) {
			mode := configuredSandboxMode(cfg, settingsService)
			if !hasSandbox || sandboxService == nil {
				return mode, nil
			}
			return mode, sandboxService.Runner()
		}
		// Root-dependent tools (files, shell, editor, image, jobs, worktree,
		// working_directory) follow the workspace that is current at call time;
		// tools without a filesystem root are registered once below.
		rootOf := workspaceRootSource(ctx, cfg.Root)
		group := newRebindGroup(policyCfg, timeout, rootOf, sandboxOf, logf)
		if dataDir, ok := ctx.Get(contracts.KeyDataDir); ok {
			group.dataDir = strings.TrimSpace(dataDir)
		}
		// One store instance is shared with the agent (SpillPolicy.Store) and the
		// model-facing spill_read tool, so handles written by the runtime are the
		// ones the model can read back.
		if store, err := newSpillStore(group.dataDir); err == nil {
			group.spillStore = store
			if _, provideErr := ctx.Provide(contracts.KeySpillStore, store); provideErr != nil {
				// Optional capability: a failed provide degrades BOTH sides to the
				// file backend (the tools must not read a store the agent cannot
				// see) instead of failing the whole toolkit.
				group.spillStore = nil
				if logf != nil {
					logf("[toolkit] spill store not provided: %v", provideErr)
				}
			}
		} else if logf != nil {
			logf("[toolkit] spill store unavailable: %v", err)
		}
		// The agent gate asks the SDK whether a shell call is already confined; that
		// needs the live policy, which this module owns (one per root/mode build).
		if _, err := ctx.Provide(contracts.KeyToolPolicy, contracts.ToolPolicy(policySourceAdapter{group: group})); err != nil {
			return err
		}
		// The agent's turn hook asks for a snapshot before each run so the undo
		// tool can revert it; the toolkit owns the per-workspace trackers.
		if _, err := ctx.Provide(contracts.KeyUndo, contracts.UndoService(group)); err != nil {
			return err
		}
		// Optional durable store: execution lifecycle events (job/terminal
		// start/finish/close) are recorded for the agent session that invoked the
		// tool. A product without a store keeps the tools working, event-free.
		executionStore, _ := ctx.Get(contracts.KeyStore)
		group.events = &executionEvents{store: executionStore, logf: logf}
		rootScoped := group.initial()
		tools := append([]rig.Tool(nil), group.proxies(rootScoped)...)
		// Keep the todo store the model-facing tool writes to: the session's Todo
		// dock reads the same store through contracts.KeyTodo. The store is
		// process-level in this build (one list for the whole product), not
		// per-session; the contract and the UI say so instead of faking scoping.
		todoStore := misc.NewTodoStore()
		tools = append(tools, misc.NewTodoTool(todoStore))
		if memoryStore, ok := ctx.Get(contracts.KeyMemory); ok {
			// The memory backend provides the durable rig.MemoryStore; without
			// it the memory tools fall back to their in-memory default. The
			// product wrapper keeps the stable "memory" tool name and adds the
			// update action over the store's MemoryUpdater half.
			tools = append(tools, memorytools.Tools(memoryStore)...)
		} else {
			tools = append(tools, memorytools.Tools(nil)...)
		}
		tools = append(tools, schedule.Tools(nil)...)
		if skillStore, ok := ctx.Get(contracts.KeySkills); ok {
			// The skills backend discovers SKILL.md files from the data dir and
			// the current workspace; the skill tool lists/searches/loads them.
			// Workspace-origin skills are repository content, so the wrapper
			// marks their rendered output as untrusted. skills.disabled is
			// applied at the agent-facing store (the UI still lists disabled
			// skills so the user can re-enable them).
			var visibleSkills contracts.SkillsService = skillStore
			if hasSettings {
				visibleSkills = filteredSkillStore{inner: skillStore, settings: settingsService}
			}
			for _, tool := range skill.Tools(visibleSkills) {
				tools = append(tools, newTrustAwareSkillTool(tool, visibleSkills, rootOf))
			}
		} else {
			tools = append(tools, skill.Tools(nil)...)
		}
		// The shared task board is per session (DSH's Lead-session board): the
		// task tools dispatch on the run-scoped session ID from their context,
		// while KeyTaskBoard exposes the aggregate view for host surfaces that
		// have no session context.
		taskBoard := newTaskBoardService(dataDir, logf)
		tools = append(tools, taskBoard.tools()...)
		if hasSettings {
			tools = append(tools, maskedSettingsTools(settingsService)...)
		}

		if fetcher := webFetcher(cfg); fetcher != nil {
			tools = append(tools, fetcher)
			// The search backend is read from settings on every call so an
			// endpoint/key change applies to the next search without a restart.
			// Without a settings service there is nowhere to configure it, so
			// web_search stays unregistered (except for the fetcher above).
			if hasSettings {
				tools = append(tools, web.NewSearcher(web.SearcherFunc(func(ctx context.Context, query string, limit int) ([]web.SearchResult, error) {
					endpoint := strings.TrimSpace(settingsService.Get(contracts.SettingWebSearchEndpoint, ""))
					if endpoint == "" {
						return nil, fmt.Errorf("web search endpoint 未配置（%s）", contracts.SettingWebSearchEndpoint)
					}
					apiKey := strings.TrimSpace(settingsService.Get(contracts.SettingWebSearchAPIKey, ""))
					searcher, err := websearch.NewSearcher(websearch.Config{Endpoint: endpoint, APIKey: apiKey})
					if err != nil {
						return nil, fmt.Errorf("web search: %w", err)
					}
					found, err := searcher.Search(ctx, query, limit)
					if err != nil {
						return nil, err
					}
					results := make([]web.SearchResult, 0, len(found))
					for _, result := range found {
						results = append(results, web.SearchResult{
							Title:   result.Title,
							URL:     result.URL,
							Snippet: result.Snippet,
						})
					}
					return results, nil
				})))
			}
		}

		// tools.disabled applies to every registry tool; mcp.disabledTools is
		// kept as a legacy MCP-only alias. MCP connections are rebuilt by the
		// controller below, which applies the same set to their tools.
		disabledTools := loadDisabledTools(settingsService, hasSettings, logf)

		// Optional single LSP server: its tools join the same registry and the
		// server is closed when the module unloads.
		lspPolicy := newPolicy(policyCfg, rootScoped.root, timeout, rootScoped.mode)
		lspTools, closeLSP := startLSPTools(context.Background(), ctx, logf, settingsService, hasSettings, tools, lspPolicy)
		tools = append(tools, lspTools...)
		tools = filterDisabledTools(tools, disabledTools, logf)

		registry := newToolRegistry(tools, logf)
		// MCP starts after the built-in/LSP registry exists so a hot reload can
		// register/unregister its tools against the same registry the agent uses.
		plugins := newPluginsService(nil)
		authRuntime := newMCPAuthRuntime(settingsService, logf)
		controller := newMCPController(logf, registry, plugins, authRuntime, settingsService, cfg.MaxOutputBytes,
			func() ([]MCPServer, map[string]bool) {
				return loadMCPServers(ctx, cfg, settingsService, hasSettings, logf)
			})
		if _, err := controller.Load(context.Background()); err != nil {
			controller.close()
			closeLSP()
			group.close()
			return err
		}
		if _, err := ctx.Provide(hosttools.KeyRegistry, registry); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		// The narrow rig-free catalog over the SAME registry (2026-11 audit
		// AR-06): diagnostics consumers read names/count through it instead of
		// the concrete rig type. The registry itself is provided under
		// hosttools.KeyRegistry for backend tool wiring.
		if _, err := ctx.Provide(contracts.KeyToolCatalog, contracts.ToolCatalog(toolCatalog{registry: registry})); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		_, err := ctx.Provide(contracts.KeyPlugins, contracts.PluginsService(plugins))
		if err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		if _, err := ctx.Provide(contracts.KeyMCPAuth, contracts.MCPAuthService(authRuntime)); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		if _, err := ctx.Provide(contracts.KeyMCPReloader, contracts.MCPReloader(controller)); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		if _, err := ctx.Provide(contracts.KeyMCPToolCatalog, contracts.MCPToolCatalog(controller)); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		if _, err := ctx.Provide(contracts.KeyTaskBoard, contracts.TaskBoard(taskBoard)); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		// The session header's background-jobs chip reads the live root-scoped
		// job manager through this service. Builds are retained for the module
		// lifetime and job ids route to the build that created them, so the
		// service can list/kill jobs across a workspace or permission rebuild.
		if _, err := ctx.Provide(contracts.KeyJobs, contracts.JobService(newJobService(group))); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		// The execution-history read model folds the durable execution/* events
		// (bounded, session-scoped) and overlays the live managers, so the session
		// header can show what ran in a conversation after a restart. Its read
		// happens off the UI thread; a product without a store simply has no
		// history.
		if _, err := ctx.Provide(contracts.KeyExecutionHistory,
			contracts.ExecutionHistoryService(newExecutionHistoryService(executionStore, groupExecutionLiveness{group: group}))); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		// The session's Todo dock reads the agent's todo_write list through this
		// optional service. It is one process-level list (see contracts.TodoService
		// for the limitation), refreshed by each frame's light snapshot read.
		if _, err := ctx.Provide(contracts.KeyTodo,
			contracts.TodoService(todoService{store: todoStore})); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		// The success path must release everything activation started, exactly
		// like the error paths above: the LSP session, the MCP controller (its
		// connections and refresh loop) and every retained root-scoped build
		// (its background-job process groups). All three closes are idempotent,
		// so the composed effect is safe even if a partial teardown already ran
		// one of them (2026-11 audit).
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{
				cordis.CleanupOf(closeLSP),
				cordis.CleanupOf(controller.close),
				cordis.CleanupOf(group.close),
			}
		}); err != nil {
			closeLSP()
			controller.close()
			group.close()
			return err
		}
		return nil
	}).AddProvides(
		module.Provides(hosttools.KeyRegistry),
		module.Provides(contracts.KeyToolCatalog),
		module.Provides(contracts.KeyPlugins),
		module.Provides(contracts.KeyTaskBoard),
		module.Provides(contracts.KeyJobs),
		module.Provides(contracts.KeyExecutionHistory),
		module.Provides(contracts.KeyTodo),
		module.Provides(contracts.KeyMCPAuth),
		module.Provides(contracts.KeyMCPReloader),
		module.Provides(contracts.KeyMCPToolCatalog),
		module.Provides(contracts.KeyToolPolicy),
		module.Provides(contracts.KeyUndo),
	).WithName("ZeoKeel Rig toolset")
}

// todoService adapts the rig todo store (the model-facing todo_write tool's
// state) to contracts.TodoService. The toolkit keeps ONE store for the process,
// so the list is process-level today — one conversation's todo_write replaces
// what another conversation sees; the contract documents that limitation and a
// per-session store can replace this adapter without touching the UI.
type todoService struct {
	store *misc.TodoStore
}

var _ contracts.TodoService = todoService{}

// Todos implements contracts.TodoService: a copied, contract-typed snapshot.
func (s todoService) Todos() []contracts.TodoItem {
	if s.store == nil {
		return nil
	}
	todos := s.store.Todos()
	out := make([]contracts.TodoItem, 0, len(todos))
	for _, todo := range todos {
		out = append(out, contracts.TodoItem{
			ID:      todo.ID,
			Content: todo.Content,
			Status:  todo.Status,
		})
	}
	return out
}

// permissionSandboxMode maps a stored/logged agent.permission preset onto the
// OS sandbox mode the tools are rebuilt with:
//
//   - read-only        -> read-only        (file writes and shell are denied)
//   - workspace        -> workspace-write  (writes only inside the workspace)
//   - full             -> danger-full-access (file tools unrestricted, runner
//     passes commands through)
//
// Legacy/unknown spellings go through contracts.NormalizePermissionMode, so
// "ask"/"never" become read-only and empty/unknown values keep the workspace
// default.
func permissionSandboxMode(permission string) sandbox.Mode {
	switch contracts.NormalizePermissionMode(permission) {
	case contracts.PermissionReadOnly:
		return sandbox.ModeReadOnly
	case contracts.PermissionFull:
		return sandbox.ModeDangerFullAccess
	default:
		return sandbox.ModeWorkspaceWrite
	}
}

// configuredSandboxMode resolves the live mode for the next root-scoped build:
// the stored agent.permission preset, with the assembly-level cfg.ReadOnly
// opt-out forcing read-only. It is read on every rebuild, so a permission
// change applies without a restart. cfg.IgnorePermissionSetting skips the
// stored preset entirely (headless/ACP pin their OS confinement to the assembly
// default, mirroring the approvals module's flag of the same name).
func configuredSandboxMode(cfg Config, settings contracts.SettingsService) sandbox.Mode {
	if cfg.ReadOnly {
		return sandbox.ModeReadOnly
	}
	if cfg.IgnorePermissionSetting || settings == nil {
		return sandbox.ModeWorkspaceWrite
	}
	return permissionSandboxMode(settings.Get(contracts.SettingAgentPermission, ""))
}

// policyMode returns the effective policy mode of one build: cfg.ReadOnly
// forces read-only and an empty/unknown mode falls back to workspace-write.
func policyMode(cfg Config, mode sandbox.Mode) sandbox.Mode {
	if cfg.ReadOnly {
		return sandbox.ModeReadOnly
	}
	switch mode {
	case sandbox.ModeReadOnly, sandbox.ModeWorkspaceWrite, sandbox.ModeDangerFullAccess:
		return mode
	default:
		return sandbox.ModeWorkspaceWrite
	}
}

// webFetcher builds the product's web_fetch tool. It intentionally passes a
// nil policy: the product allows fetching any http(s) address, including
// loopback, private and reserved ranges (requested explicitly; no
// SSRF-style address blocking). The tool still accepts only http/https and
// keeps its timeout, redirect and response-size caps. Network=false disables
// the tool entirely.
func webFetcher(cfg Config) rig.Tool {
	if !cfg.Network {
		return nil
	}
	return web.NewFetcher(nil)
}

// shellTimeout resolves the shell-tool timeout: the capability setting
// (contracts.SettingToolkitShellTimeoutSeconds, 5–600 seconds) when the row is
// valid, else the module's Config.ShellTimeoutMs (0 = 30s). The toolkit resolves
// it once at activation, so a settings change applies after a restart (the
// settings page states that).
func shellTimeout(cfg Config, settings contracts.SettingsService, hasSettings bool) time.Duration {
	fallback := time.Duration(cfg.ShellTimeoutMs) * time.Millisecond
	if fallback <= 0 {
		fallback = 30 * time.Second
	}
	if !hasSettings || settings == nil {
		return fallback
	}
	seconds := contracts.NormalizeShellTimeoutSeconds(
		settings.Get(contracts.SettingToolkitShellTimeoutSeconds, ""),
		int(fallback/time.Second))
	return time.Duration(seconds) * time.Second
}

// shellMaxOutputBytes resolves the tool-output cap: an explicit 0 row selects
// the rig/tool default, a 1..CapabilityMaxShellOutputKiB row becomes bytes, and
// an absent, malformed or out-of-range row keeps Config.MaxOutputBytes
// unchanged (exactly, not rounded to KiB). Resolved once at activation (restart
// applies), like the shell timeout.
func shellMaxOutputBytes(cfg Config, settings contracts.SettingsService, hasSettings bool) int {
	if !hasSettings || settings == nil {
		return cfg.MaxOutputBytes
	}
	raw := strings.TrimSpace(settings.Get(contracts.SettingToolkitShellMaxOutputKiB, ""))
	if raw == "" {
		return cfg.MaxOutputBytes
	}
	kiB, err := strconv.Atoi(raw)
	if err != nil || kiB < contracts.CapabilityShellOutputDefaultKiB || kiB > contracts.CapabilityMaxShellOutputKiB {
		return cfg.MaxOutputBytes
	}
	return kiB * 1024
}

// newPolicy builds the sandbox policy from the module config. It is a helper so
// the shell-policy behavior (allowlist, optional shell grammar) has a direct
// unit-test seam. mode is the linked OS sandbox mode; cfg.ReadOnly keeps forcing
// read-only.
func newPolicy(cfg Config, root string, timeout time.Duration, mode sandbox.Mode) *sandbox.Policy {
	options := []sandbox.Option{
		sandbox.WithRoots(root),
		sandbox.WithMode(policyMode(cfg, mode)),
		sandbox.WithReadOnly(cfg.ReadOnly),
		sandbox.WithNetwork(cfg.Network),
		sandbox.WithTimeout(timeout),
		sandbox.WithMaxOutputBytes(cfg.MaxOutputBytes),
		// Hidden paths are enforced by rig itself (read, write, list, glob,
		// grep and search all consult them), so the toolkit only has to hand the
		// patterns over. hiddenPatterns widens every entry to cover the entry
		// itself and its subtree, which is what "hide this file" meant under the
		// pre-v0.25 denied-globs rule.
		sandbox.WithHidden(hiddenPatterns(cfg.DeniedGlobs)...),
		// Personal client: no command-name allowlist by default — an approved
		// command runs. An explicit list is an opt-in restriction.
		sandbox.WithAnyCommandAllowed(true),
		// Shell grammar (pipes, lists, redirections, env assignments) is on:
		// ordinary development commands must work without extra configuration.
		sandbox.WithAllowShellOperators(),
	}
	if commands := cfg.CommandAllowlist; len(commands) > 0 {
		options = append(options, sandbox.WithCommandAllowlist(commands...))
	}
	return sandbox.New(options...)
}
