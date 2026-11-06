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
	"github.com/zeokeel/zeokeel-rig/tools/misc"
	"github.com/zeokeel/zeokeel-rig/tools/terminal"
	"github.com/zeokeel/zeokeel-rig/tools/web"

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
		tools = append(tools, terminals.Tools()...)

		background := jobs.NewManager(jobs.WithPolicy(policy))
		tools = append(tools, background.Tools()...)

		if cfg.Network {
			tools = append(tools, web.NewFetcher(policy))
		}

		var clients []*mcp.Client
		for _, server := range cfg.MCPServers {
			if strings.TrimSpace(server.URL) == "" {
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
				closeAll(clients)
				terminals.CloseAll()
				background.CloseAll()
				return err
			}
			clients = append(clients, client)
			tools = append(tools, mcp.Tools(client, "")...)
			tools = append(tools, mcp.ResourceTools(client, "")...)
		}

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

		_, err := ctx.Provide(contracts.KeyToolkit, rigtool.NewRegistry(tools...))
		return err
	}).AddProvides(module.Provides(contracts.KeyToolkit)).WithName("ZeoKeel Rig toolset")
}

func closeAll(clients []*mcp.Client) {
	for _, client := range clients {
		_ = client.Close()
	}
}
