package agent

// The role-boundary gate wrapper.
//
// The veto itself lives in gate() (plan_mode.go), next to the task-binding veto
// and before every branch that can only widen a call. This wrapper adds the
// model-visible reason: the SDK's gate contract carries a decision only, so the
// readable sentence ("角色 lead 只允许这些工具：…；这次调用 write_file 被拒绝")
// travels through the optional DenialExplainer channel — a structurally matched
// method, neither side importing the other — and the agent renders it into the
// tool error the model sees. Without it a denial is indistinguishable from
// "broken", and the model retries instead of delegating.
//
// It wraps the task-binding gate, so a denial from either layer keeps its own
// explanation: this layer answers first when the role refused the call, and
// otherwise delegates to the inner gate's explainer.

import (
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
)

// presetToolsGate attaches the role's denial reason to the service gate.
type presetToolsGate struct {
	rig.Gate
	service *service
	// sessionID is the session the run belongs to; rolePresetID is the role the
	// run actually executes as ("" = the session's own preset).
	sessionID    string
	rolePresetID string
}

// ExplainDenial implements the SDK's optional denial-explanation channel.
func (g presetToolsGate) ExplainDenial(tool rig.Tool, call rig.ToolCall) string {
	name := call.Name
	if tool != nil && strings.TrimSpace(tool.Name()) != "" {
		name = tool.Name()
	}
	reason := g.service.presetToolsDenial(g.sessionID, name)
	if g.rolePresetID != "" {
		reason = g.service.presetToolsDenialForRole(g.rolePresetID, name)
	}
	if reason != "" {
		return reason
	}
	// The inner gate (task binding) may be the layer that refused; forward its
	// explanation instead of replacing it with an empty one.
	if inner, ok := g.Gate.(interface {
		ExplainDenial(rig.Tool, rig.ToolCall) string
	}); ok {
		return inner.ExplainDenial(tool, call)
	}
	return ""
}

// presetToolsGate wraps inner in the explainer above.
func (s *service) presetToolsGate(sessionID, rolePresetID string, inner rig.Gate) rig.Gate {
	return presetToolsGate{Gate: inner, service: s, sessionID: sessionID, rolePresetID: rolePresetID}
}
