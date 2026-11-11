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
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/tools/misc"
	"github.com/zeokeel/zeokeel-rig/tools/schedule"
	"github.com/zeokeel/zeokeel-rig/tools/skill"
	"github.com/zeokeel/zeokeel-rig/tools/team"
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
		timeout := time.Duration(cfg.ShellTimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		// Root-dependent tools (files, shell, editor, image, terminal, jobs,
		// worktree, working_directory) follow the workspace that is current at
		// call time; tools without a filesystem root are registered once below.
		rootOf := workspaceRootSource(ctx, cfg.Root)
		group := newRebindGroup(cfg, timeout, rootOf, logf)
		rootScoped := group.initial()
		tools := append([]rig.Tool(nil), group.proxies(rootScoped)...)
		tools = append(tools, misc.NewTodoTool(misc.NewTodoStore()))
		// web_fetch is not workspace-rooted; give it a policy with the network
		// and output limits but no filesystem root.
		webPolicy := newPolicy(cfg, "", timeout)
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
		taskManager := team.NewTaskManager()
		tools = append(tools, team.TaskTools(taskManager)...)
		taskBoard := newTaskBoard(taskManager)
		settingsService, hasSettings := ctx.Get(contracts.KeySettings)
		if hasSettings {
			tools = append(tools, maskedSettingsTools(settingsService)...)
		}

		if cfg.Network {
			tools = append(tools, web.NewFetcher(webPolicy))
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

		// MCP servers start concurrently under one bounded budget; every
		// connection is managed at runtime so a session expiry or a dead stdio
		// child process reconnects on the next call and the Plugins read model
		// follows the live connection.
		runtime := newMCPRuntime(logf, disabledTools, cfg.MaxOutputBytes)
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
			outcome := outcomes[index]
			if outcome.err != nil {
				entry.plugin.Status = statusConnectFailed + outcome.err.Error()
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
			pluginIndex := len(pluginEntries)
			wrapped, regularTools := runtime.attachServer(pluginIndex, plan, outcome, mcpSeen)
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
		lspPolicy := newPolicy(cfg, rootScoped.root, timeout)
		lspTools, closeLSP := startLSPTools(context.Background(), ctx, logf, settingsService, hasSettings, tools, lspPolicy)
		tools = append(tools, lspTools...)
		tools = filterDisabledTools(tools, disabledTools, logf)

		registry := newToolRegistry(tools, logf)
		runtime.bindRegistry(registry)
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
		if _, err := ctx.Provide(contracts.KeyTaskBoard, contracts.TaskBoard(taskBoard)); err != nil {
			closeLSP()
			runtime.close()
			group.close()
			return err
		}
		// The session header's background-jobs chip reads the live root-scoped
		// job manager through this service. The manager is rebuilt on every
		// workspace switch, so the service resolves the current build per call
		// instead of being re-provided.
		if _, err := ctx.Provide(contracts.KeyJobs, contracts.JobService(newJobService(group))); err != nil {
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
	).WithName("ZeoKeel Rig toolset")
}

// newPolicy builds the sandbox policy from the module config. It is a helper so
// the shell-policy behavior (allowlist, optional shell grammar) has a direct
// unit-test seam.
func newPolicy(cfg Config, root string, timeout time.Duration) *sandbox.Policy {
	commands := cfg.CommandAllowlist
	if commands == nil {
		commands = defaultCommands
	}
	options := []sandbox.Option{
		sandbox.WithRoots(root),
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
