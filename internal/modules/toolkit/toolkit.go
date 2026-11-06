// Package toolkit assembles the ZeoKeel Rig toolset into the product's kernel
// tree: it builds a tool registry from the SDK's built-in tools and provides it
// as a service. Session/agent modules will require this registry; until the
// agent runtime is ported the module proves that the shipped SDK composes into
// a kernel assembly like any other product module.
package toolkit

import (
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rigtool "github.com/zeokeel/zeokeel-rig/tool"
	"github.com/zeokeel/zeokeel-rig/tool/builtin"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Config is the module's typed assembly config.
type Config struct {
	// Root is the workspace root the file tools are sandboxed to. Empty means
	// the process working directory.
	Root string `json:"root"`
	// ShellTimeoutMs bounds shell tool executions (0 = 30s).
	ShellTimeoutMs int `json:"shellTimeoutMs"`
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
		registry := rigtool.NewRegistry(builtin.NewBuiltins(root, timeout)...)
		_, err := ctx.Provide(contracts.KeyToolkit, registry)
		return err
	}).AddProvides(module.Provides(contracts.KeyToolkit)).WithName("ZeoKeel Rig toolset")
}
