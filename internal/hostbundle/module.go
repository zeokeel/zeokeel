// Package hostbundle adapts ZeoKeel's product services into the kit composition
// layer.
//
// It is the seam for the thin-shell migration: the product keeps its UI and
// business modules, while this package exposes the already host-agnostic pieces
// (session history/log/checkpoints, product prompt sections, business tools and
// stages) as one kit.Module. The command entry can then compile an agent with
// kit.Build instead of hand-assembling an AgentSpec.
package hostbundle

import (
	"github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/kit"
)

// Options is the product surface handed to kit. Only non-nil pieces are
// contributed; nil leaves kit's own default in place.
type Options struct {
	// History/SessionLog/CheckpointStore come from the product session store.
	History         rig.History
	SessionLog      rig.SessionLog
	CheckpointStore rig.CheckpointStore
	// PromptSections are the product's per-run prompt contributions.
	PromptSections []kit.PromptSection
	// Tools are product/business tools (for example future DingTalk tools).
	Tools []rig.Tool
	// Stages/Hooks are product loop extensions.
	Stages kit.StageSet
	Hooks  []rig.TurnHook
}

// Module is the product's kit.Module: it contributes the host services and the
// business surface without owning the agent loop.
type Module struct {
	opts Options
}

var _ kit.Module = (*Module)(nil)

// New builds the module.
func New(opts Options) *Module {
	return &Module{opts: opts}
}

// Name implements kit.Module.
func (m *Module) Name() string { return "zeokeel.host" }

// Order implements kit.Module: the host bundle runs before business modules
// that may read its services.
func (m *Module) Order() int { return 0 }

// Tools implements kit.Module.
func (m *Module) Tools() []rig.Tool { return m.opts.Tools }

// PromptSections implements kit.Module.
func (m *Module) PromptSections() []kit.PromptSection { return m.opts.PromptSections }

// Stages implements kit.Module.
func (m *Module) Stages() kit.StageSet { return m.opts.Stages }

// Hooks implements kit.Module.
func (m *Module) Hooks() []rig.TurnHook { return m.opts.Hooks }

// Capabilities implements kit.ModuleCapabilities.
func (m *Module) Capabilities() map[string]any {
	capabilities := make(map[string]any)
	if m.opts.History != nil {
		capabilities["history"] = m.opts.History
	}
	if m.opts.SessionLog != nil {
		capabilities["sessionLog"] = m.opts.SessionLog
	}
	if m.opts.CheckpointStore != nil {
		capabilities["checkpointStore"] = m.opts.CheckpointStore
	}
	return capabilities
}
