// Package toolkit assembles the ZeoKeel Rig toolset into the product's kernel
// tree. The product owns the policy and the wiring; Rig supplies the tool
// implementations:
//
//   - sandbox policy (roots, read-only, shell allowlist, network, caps)
//   - built-in file/shell/pwsh tools, editor and image tools
//   - stateful terminal sessions (PTY when available) and background jobs
//   - web fetch/search, and remote MCP servers over Streamable HTTP
//
// The resulting *tools.Registry is provided as contracts.KeyToolkit; the
// host-tools module adds product-only tools to the same registry.
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
	"github.com/zeokeel/zeokeel/internal/memorytools"
	"github.com/zeokeel/zeokeel/internal/websearch"
)

// defaultCommands is the shell allowlist used when the assembly does not set
// one. An explicit empty list in assembly.json disables shell execution.
//
// Besides the language/build/runtime commands it carries the routine
// inspection and navigation commands an agent needs for real development work
// (pwd, cd, echo, mkdir, ...). The previous list rejected even `pwd`/`echo`
// with "not in the allowlist", so an already-approved tool call could still
// not run and real tasks burned iterations on policy errors.
//
// Entries are bare command names. Rig compares them with platform semantics
// (since v0.13.2 Windows ignores case and the ".exe" suffix), so the Windows
// PowerShell executables are covered by "pwsh" / "powershell" without separate
// ".exe" spellings.
var defaultCommands = []string{
	"bash", "sh", "zsh", "git", "go", "make", "gofmt",
	"rg", "grep", "sed", "awk", "cat", "ls", "find", "head", "tail", "wc",
	"pwd", "cd", "echo", "printf", "env", "export", "mkdir", "cp", "mv", "rm",
	"touch", "tee", "date", "seq", "sort", "uniq", "cut", "tr", "xargs",
	"test", "true", "false", "which", "basename", "dirname", "realpath",
	"stat", "file", "diff", "patch", "tar", "unzip", "zip", "curl", "wget",
	"node", "npm", "python3", "pwsh", "powershell",
}

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
	// ReadOnly disables file writes, shell and terminal execution.
	ReadOnly bool `json:"readOnly"`
	// Network allows web_fetch and outbound connections from shell tools.
	Network bool `json:"network"`
	// CommandAllowlist is the shell allowlist; nil means defaultCommands.
	CommandAllowlist []string `json:"commandAllowlist"`
	// AllowShellOperators permits shell grammar (pipes, lists, &&, ||,
	// redirections and leading environment assignments) in the shell tool.
	// The default (false) keeps Rig's strict single-simple-command policy;
	// products that want the agent to run normal development commands enable
	// it. The command allowlist still applies to the first command of a line.
	AllowShellOperators bool `json:"allowShellOperators"`
	// DeniedGlobs hides files (e.g. "**/.env") from every file tool.
	DeniedGlobs []string `json:"deniedGlobs"`
	// MaxOutputBytes caps one tool result (0 = tool default).
	MaxOutputBytes int `json:"maxOutputBytes"`
	// TerminalTransport selects the terminal transport: "auto" (default),
	// "pty" or "pipe".
	TerminalTransport string `json:"terminalTransport"`
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
		// root-scoped tools (files, shell, editor, terminal). Both are resolved
		// here, at activation: the settings page says a restart applies them.
		// policyCfg carries only those two values; the MCP runtime below keeps
		// the assembly config's own output cap.
		timeout := shellTimeout(cfg, settingsService, hasSettings)
		policyCfg := cfg
		policyCfg.MaxOutputBytes = shellMaxOutputBytes(cfg, settingsService, hasSettings)
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
		// Root-dependent tools (files, shell, editor, image, terminal, jobs,
		// worktree, working_directory) follow the workspace that is current at
		// call time; tools without a filesystem root are registered once below.
		rootOf := workspaceRootSource(ctx, cfg.Root)
		group := newRebindGroup(policyCfg, timeout, rootOf, sandboxOf, logf)
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
			// marks their rendered output as untrusted.
			for _, tool := range skill.Tools(skillStore) {
				tools = append(tools, newTrustAwareSkillTool(tool, skillStore, rootOf))
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

		rawMCPServers := ""
		if hasSettings {
			rawMCPServers = settingsService.Get(contracts.SettingMCPServers, "")
		}
		servers := mergeMCPServers(cfg.MCPServers, rawMCPServers, logf)

		// Workspace mcp.json is the lowest-priority source and only when the
		// user explicitly opted in; the workspace module is optional here.
		workspaceOptIn := false
		if hasSettings {
			workspaceOptIn = mcpWorkspaceOptInEnabled(settingsService.Get(contracts.SettingMCPWorkspaceOptIn, ""))
		}
		if workspaceOptIn {
			if workspaceService, ok := ctx.Get(contracts.KeyWorkspace); ok && workspaceService != nil {
				workspaceRows := readWorkspaceMCPServers(context.Background(), workspaceService, logf)
				if len(workspaceRows) > 0 {
					servers = mergeMCPServerRows(servers, workspaceRows, "workspace mcp.json", false, logf)
				}
			}
		}

		// mcp.disabledServers drops whole servers after the assembly/settings/
		// workspace merge and before any connection is attempted: a disabled
		// server is not started and its tools are not registered. Bad JSON is a
		// warning and starts everything (never fails activation).
		if hasSettings {
			var skipped int
			servers, skipped = filterDisabledMCPServers(servers,
				settingsService.Get(contracts.SettingMCPDisabledServers, ""), logf)
			if skipped > 0 {
				logf("[toolkit] skipped %d disabled MCP server(s) (%s)", skipped, contracts.SettingMCPDisabledServers)
			}
		}

		// tools.disabled applies to every registry tool; mcp.disabledTools is
		// kept as a legacy MCP-only alias. The union is applied both while the
		// MCP clients' tools are collected and once more to the final registry
		// (built-ins, LSP tools).
		disabledTools := make(map[string]bool)
		if hasSettings {
			for _, key := range []string{contracts.SettingToolsDisabled, contracts.SettingMCPDisabledTools} {
				disabledNames, err := contracts.ParseStringListJSON(settingsService.Get(key, ""))
				if err != nil {
					logf("[toolkit] parse %s: %v", key, err)
					continue
				}
				for _, name := range disabledNames {
					if trimmed := strings.TrimSpace(name); trimmed != "" {
						disabledTools[trimmed] = true
					}
				}
			}
		}

		// OAuth connectors need a rig OAuth client before they can start (and
		// even when their startup fails: the client is what later performs the
		// interactive authorization). The adapter is also the UI-facing
		// contracts.MCPAuthService.
		authRuntime := newMCPAuthRuntime(settingsService, logf)
		for _, server := range servers {
			if !authOAuth2(server.config()) {
				continue
			}
			if _, err := authRuntime.clientForServer(server); err != nil {
				logf("[toolkit] mcp %q: OAuth setup: %v", server.Name, err)
			}
		}

		// MCP servers start concurrently under one bounded budget; every
		// connection is managed at runtime so a session expiry or a dead stdio
		// child process reconnects on the next call and the Plugins read model
		// follows the live connection. A failed startup is registered too, so
		// an OAuth authorization can reconnect it without restarting.
		runtime := newMCPRuntime(logf, disabledTools, cfg.MaxOutputBytes, authRuntime)
		plans := prepareMCPServerPlans(servers, logf)
		outcomes := runtime.startMCPServers(context.Background(), plans)
		pluginEntries := make([]pluginEntry, 0, len(plans))
		mcpSeen := make(map[string]bool)
		for index, plan := range plans {
			entry := plan.entry
			if plan.invalid {
				pluginEntries = append(pluginEntries, entry)
				continue
			}
			managed := runtime.registerServer(plan, index)
			outcome := outcomes[index]
			if outcome.err != nil {
				entry.plugin.Status = statusConnectFailed + outcome.err.Error()
				if authStatus := authRuntime.failureStatus(plan.server); authStatus != "" {
					entry.plugin.Status = authStatus
				}
				pluginEntries = append(pluginEntries, entry)
				logf("[toolkit] mcp %q: connect failed: %v", entry.plugin.Name, outcome.err)
				continue
			}
			entry.client = outcome.client
			entry.plugin.Name = pluginDisplayName(plan.server.Name, outcome.client)
			entry.plugin.ServerName = outcome.serverName
			entry.plugin.Version = outcome.serverVer
			entry.plugin.Protocol = outcome.protocolVer
			entry.plugin.Status = statusConnected
			wrapped, regularTools := runtime.attachOutcome(managed, outcome, mcpSeen)
			entry.plugin.Tools = regularTools
			tools = append(tools, wrapped...)
			pluginEntries = append(pluginEntries, entry)
			logf("[toolkit] mcp %q connected: %d tool(s), protocol %s",
				entry.plugin.Name, regularTools, entry.plugin.Protocol)
		}
		plugins := newPluginsService(pluginEntries)
		runtime.bindPlugins(plugins)

		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				runtime.close()
				group.close()
			})}
		}); err != nil {
			runtime.close()
			group.close()
			return err
		}

		// Optional single LSP server: its tools join the same registry and the
		// server is closed when the module unloads.
		lspPolicy := newPolicy(policyCfg, rootScoped.root, timeout, rootScoped.mode)
		lspTools, closeLSP := startLSPTools(context.Background(), ctx, logf, settingsService, hasSettings, tools, lspPolicy)
		tools = append(tools, lspTools...)
		tools = filterDisabledTools(tools, disabledTools, logf)

		registry := newToolRegistry(tools, logf)
		runtime.bindRegistry(registry)
		// After a completed OAuth flow the adapter asks the runtime to rebuild
		// that connector's connection (the registry is bound by now).
		authRuntime.onAuthorized = func(name string) { runtime.reconnectByName(name) }
		if _, err := ctx.Provide(contracts.KeyToolkit, registry); err != nil {
			closeLSP()
			runtime.close()
			group.close()
			return err
		}
		_, err := ctx.Provide(contracts.KeyPlugins, contracts.PluginsService(plugins))
		if err != nil {
			closeLSP()
			runtime.close()
			group.close()
			return err
		}
		if _, err := ctx.Provide(contracts.KeyMCPAuth, contracts.MCPAuthService(authRuntime)); err != nil {
			closeLSP()
			runtime.close()
			group.close()
			return err
		}
		if _, err := ctx.Provide(contracts.KeyTaskBoard, contracts.TaskBoard(taskBoard)); err != nil {
			closeLSP()
			runtime.close()
			group.close()
			return err
		}
		// The session header's background-jobs chip reads the live root-scoped
		// job manager through this service. Builds are retained for the module
		// lifetime and job ids route to the build that created them, so the
		// service can list/kill jobs across a workspace or permission rebuild.
		if _, err := ctx.Provide(contracts.KeyJobs, contracts.JobService(newJobService(group))); err != nil {
			closeLSP()
			runtime.close()
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
			runtime.close()
			group.close()
			return err
		}
		// The session's Todo dock reads the agent's todo_write list through this
		// optional service. It is one process-level list (see contracts.TodoService
		// for the limitation), refreshed by each frame's light snapshot read.
		if _, err := ctx.Provide(contracts.KeyTodo,
			contracts.TodoService(todoService{store: todoStore})); err != nil {
			closeLSP()
			runtime.close()
			group.close()
			return err
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(closeLSP)}
		}); err != nil {
			closeLSP()
			return err
		}
		return nil
	}).AddProvides(
		module.Provides(contracts.KeyToolkit),
		module.Provides(contracts.KeyPlugins),
		module.Provides(contracts.KeyTaskBoard),
		module.Provides(contracts.KeyJobs),
		module.Provides(contracts.KeyExecutionHistory),
		module.Provides(contracts.KeyTodo),
		module.Provides(contracts.KeyMCPAuth),
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
// change applies without a restart.
func configuredSandboxMode(cfg Config, settings contracts.SettingsService) sandbox.Mode {
	if cfg.ReadOnly {
		return sandbox.ModeReadOnly
	}
	if settings == nil {
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
	commands := cfg.CommandAllowlist
	if commands == nil {
		commands = defaultCommands
	}
	options := []sandbox.Option{
		sandbox.WithRoots(root),
		sandbox.WithMode(policyMode(cfg, mode)),
		sandbox.WithReadOnly(cfg.ReadOnly),
		sandbox.WithNetwork(cfg.Network),
		sandbox.WithTimeout(timeout),
		sandbox.WithMaxOutputBytes(cfg.MaxOutputBytes),
		sandbox.WithDeniedGlobs(cfg.DeniedGlobs...),
		sandbox.WithCommandAllowlist(commands...),
	}
	if cfg.AllowShellOperators {
		options = append(options, sandbox.WithAllowShellOperators())
	}
	return sandbox.New(options...)
}
