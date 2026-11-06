// Package toolkit assembles the ZeoKeel Rig toolset into the product's kernel
// tree: it builds a sandbox policy, the built-in tools under that policy, the
// web fetch tool when networking is allowed, and the standalone working-dir /
// todo tools, then provides the registry as a kernel service.
//
// The product (not the SDK) owns this wiring: the sandbox policy, the approval
// gate and the assembly live here; Rig supplies the tool implementations.
package toolkit

import (
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel-rig/sandbox"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
	"github.com/zeokeel/zeokeel-rig/tools/builtin"
	"github.com/zeokeel/zeokeel-rig/tools/misc"
	"github.com/zeokeel/zeokeel-rig/tools/web"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// defaultCommands is the shell allowlist used when the assembly does not set
// one. An explicit empty list in assembly.json disables shell execution.
var defaultCommands = []string{
	"bash", "sh", "zsh", "git", "go", "make",
	"rg", "grep", "sed", "awk", "cat", "ls", "find", "head", "tail", "wc",
	"node", "npm", "python3",
}

// Config is the module's typed assembly config.
type Config struct {
	// Root is the workspace root the file tools are sandboxed to. Empty means
	// the process working directory.
	Root string `json:"root"`
	// ShellTimeoutMs bounds shell tool executions (0 = 30s).
	ShellTimeoutMs int `json:"shellTimeoutMs"`
	// ReadOnly disables write_file/edit_file and shell execution.
	ReadOnly bool `json:"readOnly"`
	// Network allows web_fetch and outbound connections from shell tools.
	Network bool `json:"network"`
	// CommandAllowlist is the shell allowlist; nil means defaultCommands.
	CommandAllowlist []string `json:"commandAllowlist"`
	// DeniedGlobs hides files (e.g. "**/.env") from every file tool.
	DeniedGlobs []string `json:"deniedGlobs"`
	// MaxOutputBytes caps one tool result (0 = 32KiB).
	MaxOutputBytes int `json:"maxOutputBytes"`
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
			misc.NewWorkingDirectory(root),
			misc.NewTodoTool(misc.NewTodoStore()),
		)
		if cfg.Network {
			tools = append(tools, web.NewFetcher(policy))
		}

		_, err := ctx.Provide(contracts.KeyToolkit, rigtool.NewRegistry(tools...))
		return err
	}).AddProvides(module.Provides(contracts.KeyToolkit)).WithName("ZeoKeel Rig toolset")
}
