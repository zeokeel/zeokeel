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
)

// defaultCommands is the shell allowlist used when the assembly does not set
// one. An explicit empty list in assembly.json disables shell execution.
var defaultCommands = []string{
	"bash", "sh", "zsh", "git", "go", "make",
	"rg", "grep", "sed", "awk", "cat", "ls", "find", "head", "tail", "wc",
	"node", "npm", "python3", "pwsh", "powershell",
}

// MCPServer configures one remote MCP server reached over Streamable HTTP.
type MCPServer struct {
	// Name is the server identity used in tool names (default: "remote").
	Name string `json:"name,omitempty"`
	// URL is the MCP endpoint (required when the server is listed).
	URL string `json:"url"`
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
		tools = append(tools, team.TaskTools(team.NewTaskManager())...)
		if settingsService, ok := ctx.Get(contracts.KeySettings); ok {
			tools = append(tools, settings.Tools(settingsService)...)
		}

		if cfg.Network {
			tools = append(tools, web.NewFetcher(policy))
		}

		var clients []*mcp.Client
		pluginEntries := make([]pluginEntry, 0, len(cfg.MCPServers))
		for _, server := range cfg.MCPServers {
			entry := pluginEntry{plugin: contracts.MCPPlugin{
				Name:   strings.TrimSpace(server.Name),
				URL:    strings.TrimSpace(server.URL),
				Status: statusConnectFailed + "未配置 URL",
			}}
			if entry.plugin.URL == "" {
				// Keep a failed entry so the Plugins screen can show the
				// misconfiguration instead of silently dropping the server.
				entry.plugin.Name = pluginDisplayName(server.Name, nil)
				pluginEntries = append(pluginEntries, entry)
				continue
			}
			serverTimeout := time.Duration(server.TimeoutMs) * time.Millisecond
			client, err := mcp.StartHTTP(context.Background(), mcp.HTTPConfig{
				URL:        server.URL,
				Headers:    server.Headers,
				ServerName: server.Name,
				Timeout:    serverTimeout,
			})
			if err != nil {
				// A failed server must not fail the whole toolkit activation:
				// keep the entry with a readable failure status and continue.
				entry.plugin.Name = pluginDisplayName(server.Name, nil)
				entry.plugin.Status = statusConnectFailed + err.Error()
				pluginEntries = append(pluginEntries, entry)
				continue
			}
			clients = append(clients, client)
			mcpTools := mcp.Tools(client, "")
			tools = append(tools, mcpTools...)
			tools = append(tools, mcp.ResourceTools(client, "")...)
			entry.client = client
			entry.plugin.Name = pluginDisplayName(server.Name, client)
			entry.plugin.ServerName = client.ServerName()
			entry.plugin.Version = client.ServerVersion()
			entry.plugin.Protocol = client.ProtocolVersion()
			entry.plugin.Tools = len(mcpTools)
			entry.plugin.Status = statusConnected
			pluginEntries = append(pluginEntries, entry)
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

		if _, err := ctx.Provide(contracts.KeyToolkit, rigtool.NewRegistry(tools...)); err != nil {
			return err
		}
		_, err := ctx.Provide(contracts.KeyPlugins, contracts.PluginsService(plugins))
		return err
	}).AddProvides(
		module.Provides(contracts.KeyToolkit),
		module.Provides(contracts.KeyPlugins),
	).WithName("ZeoKeel Rig toolset")
}

func closeAll(clients []*mcp.Client) {
	for _, client := range clients {
		_ = client.Close()
	}
}
