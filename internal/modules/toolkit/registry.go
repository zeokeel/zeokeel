package toolkit

// Shared tool-registry assembly. The product provides one registry to every
// module; registration problems (a duplicate model-visible name contributed by
// a dynamic source such as an MCP server) must degrade to a warning instead of
// panicking the whole activation.

import (
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
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
