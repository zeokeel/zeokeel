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

	"github.com/zeokeel/zeokeel-rig/sandbox"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
	"github.com/zeokeel/zeokeel-rig/tools/builtin"
	"github.com/zeokeel/zeokeel-rig/tools/editor"
	"github.com/zeokeel/zeokeel-rig/tools/image"
	"github.com/zeokeel/zeokeel-rig/tools/jobs"
	"github.com/zeokeel/zeokeel-rig/tools/mcp"
	"github.com/zeokeel/zeokeel-rig/tools/memory"
	"github.com/zeokeel/zeokeel-rig/tools/misc"
	"github.com/zeokeel/zeokeel-rig/tools/schedule"
	"github.com/zeokeel/zeokeel-rig/tools/settings"
	"github.com/zeokeel/zeokeel-rig/tools/skill"
	"github.com/zeokeel/zeokeel-rig/tools/team"
	"github.com/zeokeel/zeokeel-rig/tools/terminal"
	"github.com/zeokeel/zeokeel-rig/tools/web"
	"github.com/zeokeel/zeokeel-rig/tools/worktree"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/websearch"
)

// defaultCommands is the shell allowlist used when the assembly does not set
// one. An explicit empty list in assembly.json disables shell execution.
var defaultCommands = []string{
	"bash", "sh", "zsh", "git", "go", "make",
	"rg", "grep", "sed", "awk", "cat", "ls", "find", "head", "tail", "wc",
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
	// Root is the workspace root the file tools are sandboxed to. Empty means
	// the process working directory.
	Root string `json:"root"`
	// ShellTimeoutMs bounds shell tool executions (0 = 30s).
	ShellTimeoutMs int `json:"shellTimeoutMs"`
	// ReadOnly disables file writes, shell and terminal execution.
	ReadOnly bool `json:"readOnly"`
	// Network allows web_fetch and outbound connections from shell tools.
	Network bool `json:"network"`
	// CommandAllowlist is the shell allowlist; nil means defaultCommands.
	CommandAllowlist []string `json:"commandAllowlist"`
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
		root := strings.TrimSpace(cfg.Root)
		if root == "" {
			root = "."
		}
		timeout := time.Duration(cfg.ShellTimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		commands := cfg.CommandAllowlist
		if commands == nil {
			commands = defaultCommands
		}
		policy := sandbox.New(
			sandbox.WithRoots(root),
			sandbox.WithReadOnly(cfg.ReadOnly),
			sandbox.WithNetwork(cfg.Network),
			sandbox.WithTimeout(timeout),
			sandbox.WithMaxOutputBytes(cfg.MaxOutputBytes),
			sandbox.WithDeniedGlobs(cfg.DeniedGlobs...),
			sandbox.WithCommandAllowlist(commands...),
		)

		tools := builtin.NewWithPolicy(policy)
		tools = append(tools,
			editor.New(policy),
			image.New(policy),
			misc.NewWorkingDirectory(root),
			misc.NewTodoTool(misc.NewTodoStore()),
		)

		terminalOpts := []terminal.Option{terminal.WithPolicy(policy)}
		switch strings.ToLower(strings.TrimSpace(cfg.TerminalTransport)) {
		case "pty":
			terminalOpts = append(terminalOpts, terminal.WithTransport(terminal.TransportPTY))
		case "pipe":
			terminalOpts = append(terminalOpts, terminal.WithTransport(terminal.TransportPipe))
		}
		terminals := terminal.NewManager(terminalOpts...)
		tools = append(tools, terminals.ToolsWithList()...)

		background := jobs.NewManager(jobs.WithPolicy(policy))
		tools = append(tools,
			background.Tools()...,
		)
		if memoryStore, ok := ctx.Get(contracts.KeyMemory); ok {
			// The memory backend provides the durable rig.MemoryStore; without
			// it the memory tools fall back to their in-memory default.
			tools = append(tools, memory.Tools(memoryStore)...)
		} else {
			tools = append(tools, memory.Tools(nil)...)
		}
		tools = append(tools, schedule.Tools(nil)...)
		tools = append(tools, worktree.Tools(policy)...)
		if skillStore, ok := ctx.Get(contracts.KeySkills); ok {
			// The skills backend discovers SKILL.md files from the data dir and
			// the current workspace; the skill tool lists/searches/loads them.
			tools = append(tools, skill.Tools(skillStore)...)
		} else {
			tools = append(tools, skill.Tools(nil)...)
		}
		taskManager := team.NewTaskManager()
		tools = append(tools, team.TaskTools(taskManager)...)
		taskBoard := newTaskBoard(taskManager)
		settingsService, hasSettings := ctx.Get(contracts.KeySettings)
		if hasSettings {
			tools = append(tools, settings.Tools(settingsService)...)
		}

		if cfg.Network {
			tools = append(tools, web.NewFetcher(policy))
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

		disabledMCPTools := make(map[string]bool)
		if hasSettings {
			rawDisabled := settingsService.Get(contracts.SettingMCPDisabledTools, "")
			disabledNames, err := contracts.ParseStringListJSON(rawDisabled)
			if err != nil {
				logf("[toolkit] parse %s: %v", contracts.SettingMCPDisabledTools, err)
			} else {
				for _, name := range disabledNames {
					if trimmed := strings.TrimSpace(name); trimmed != "" {
						disabledMCPTools[trimmed] = true
					}
				}
			}
		}

		var clients []*mcp.Client
		pluginEntries := make([]pluginEntry, 0, len(servers))
		for _, server := range servers {
			transport := mcpServerTransport(server)
			entry := pluginEntry{plugin: contracts.MCPPlugin{
				Name:   pluginDisplayName(server.Name, nil),
				URL:    strings.TrimSpace(server.URL),
				Status: statusConnectFailed + "未配置 URL",
			}}
			attach := func(client *mcp.Client) {
				clients = append(clients, client)
				mcpTools := mcp.Tools(client, "")
				tools = append(tools, filterMCPTools(mcpTools, disabledMCPTools, logf)...)
				tools = append(tools, filterMCPTools(mcp.ResourceTools(client, ""), disabledMCPTools, logf)...)
				entry.client = client
				entry.plugin.Name = pluginDisplayName(server.Name, client)
				entry.plugin.ServerName = client.ServerName()
				entry.plugin.Version = client.ServerVersion()
				entry.plugin.Protocol = client.ProtocolVersion()
				entry.plugin.Tools = len(mcpTools)
				entry.plugin.Status = statusConnected
				pluginEntries = append(pluginEntries, entry)
			}
			switch transport {
			case mcp.TransportStdio:
				entry.plugin.URL = ""
				if strings.TrimSpace(server.Command) == "" {
					entry.plugin.Status = statusConnectFailed + "stdio command is required"
					pluginEntries = append(pluginEntries, entry)
					continue
				}
				client, err := mcp.Start(context.Background(), mcpStartConfig(server))
				if err != nil {
					entry.plugin.Status = statusConnectFailed + err.Error()
					pluginEntries = append(pluginEntries, entry)
					continue
				}
				attach(client)
			case mcp.TransportHTTP:
				if entry.plugin.URL == "" {
					// Keep a failed entry so the Plugins screen can show the
					// misconfiguration instead of silently dropping the server.
					pluginEntries = append(pluginEntries, entry)
					continue
				}
				client, err := startHTTPMCP(server)
				if err != nil {
					entry.plugin.Status = statusConnectFailed + err.Error()
					pluginEntries = append(pluginEntries, entry)
					continue
				}
				attach(client)
			default:
				// Legacy assembly rows (and unclassified rows) with no URL keep
				// the original missing-URL diagnostic.
				if entry.plugin.URL == "" && strings.TrimSpace(server.Command) == "" {
					entry.plugin.Status = statusConnectFailed + "未配置 URL"
				} else {
					entry.plugin.Status = statusConnectFailed + "unsupported transport"
				}
				pluginEntries = append(pluginEntries, entry)
			}
		}
		plugins := newPluginsService(pluginEntries)

		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				closeAll(clients)
				terminals.CloseAll()
				background.CloseAll()
			})}
		}); err != nil {
			closeAll(clients)
			terminals.CloseAll()
			background.CloseAll()
			return err
		}

		// Optional single LSP server: its tools join the same registry and the
		// server is closed when the module unloads.
		lspTools, closeLSP := startLSPTools(context.Background(), logf, settingsService, hasSettings, tools)
		tools = append(tools, lspTools...)

		if _, err := ctx.Provide(contracts.KeyToolkit, rigtool.NewRegistry(tools...)); err != nil {
			closeLSP()
			return err
		}
		_, err := ctx.Provide(contracts.KeyPlugins, contracts.PluginsService(plugins))
		if err != nil {
			closeLSP()
			return err
		}
		if _, err := ctx.Provide(contracts.KeyTaskBoard, contracts.TaskBoard(taskBoard)); err != nil {
			closeLSP()
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
	).WithName("ZeoKeel Rig toolset")
}

func closeAll(clients []*mcp.Client) {
	for _, client := range clients {
		_ = client.Close()
	}
}
