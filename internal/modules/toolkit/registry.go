package toolkit

// Shared tool-registry assembly. The product provides one registry to every
// module; registration problems (a duplicate model-visible name contributed by
// a dynamic source such as an MCP server) must degrade to a warning instead of
// panicking the whole activation.

import (
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newToolRegistry registers the tools one by one so a duplicate name is
// skipped with a warning instead of aborting activation (rig's NewRegistry
// panics on duplicates, which used to make one misbehaving MCP server kill the
// whole product startup).
func newToolRegistry(tools []rig.Tool, logf func(format string, args ...any)) *rigtool.Registry {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	registry := rigtool.NewRegistry()
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if err := registry.Register(tool); err != nil {
			logf("[toolkit] skip tool %q: %v", tool.Name(), err)
		}
	}
	return registry
}

// toolCatalog adapts the shared registry to contracts.ToolCatalog (2026-11
// audit AR-06): consumers that only need the tool names/count depend on the
// narrow rig-free interface instead of the concrete *rigtool.Registry behind
// the legacy KeyToolkit. It is a LIVE view — Names/Len read the registry on
// every call, so MCP hot reloads are reflected without re-providing the
// service. Names follows Registry.Specs order (rig sorts by name); Len uses
// the registry's cheap count (no spec allocation).
type toolCatalog struct {
	registry *rigtool.Registry
}

var _ contracts.ToolCatalog = toolCatalog{}

// Names implements contracts.ToolCatalog: a fresh copy the caller may retain.
func (c toolCatalog) Names() []string {
	specs := c.registry.Specs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

// Len implements contracts.ToolCatalog.
func (c toolCatalog) Len() int {
	return c.registry.Len()
}
