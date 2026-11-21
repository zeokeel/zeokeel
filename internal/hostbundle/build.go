package hostbundle

import (
	"strings"

	"github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"
	"github.com/zeokeel/zeokeel-rig/kit"
)

// Mode selects which composition path builds one run's agent.
type Mode int

const (
	// ModeDirect builds the rig.AgentSpec directly (the pre-composition path).
	ModeDirect Mode = iota
	// ModeKit maps the same spec through kit.FromAgentSpec and lets kit build
	// the agent (the thin-shell path).
	ModeKit
)

// BuildAgent builds one run's agent from an already-resolved product spec.
// ModeKit and ModeDirect must produce the same surface for the same spec; the
// parity test in this package is the gate.
func BuildAgent(spec rig.AgentSpec, mode Mode) (rig.Agent, error) {
	if mode == ModeKit {
		cfg := kit.FromAgentSpec(spec)
		if spec.InitialActiveTools != nil && hasTool(spec.Tools, "tool_search") {
			// The product resolves its own start set and owns tool_search: tell kit
			// discovery is on so the set is passed through instead of nil (all
			// active). Without the host's tool_search, discovery stays off and the
			// full surface is active, matching the direct agent.
			cfg.ToolDiscovery = kit.Bool(true)
		}
		return kit.New(cfg)
	}
	return rigagent.New(spec)
}

// hasTool reports whether the surface contains name.
func hasTool(tools []rig.Tool, name string) bool {
	for _, tool := range tools {
		if tool != nil && strings.TrimSpace(tool.Name()) == name {
			return true
		}
	}
	return false
}
