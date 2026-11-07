package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// SubagentRequest is one synchronous delegation to an in-process subagent (the
// old shell's subagent tool): the caller waits for the final answer.
type SubagentRequest struct {
	// Prompt is the self-contained task for the subagent (required).
	Prompt string
	// Name is the display name ("" generates one).
	Name string
	// Model overrides the parent model ("" uses the session model).
	Model string
	// Fork seeds the subagent with a copy of the parent conversation history.
	Fork bool
	// MaxIterations caps the subagent model/tool rounds (0 uses the default).
	MaxIterations int
}

// SubagentResult is the finished subagent answer.
type SubagentResult struct {
	AgentID string
	Name    string
	Text    string
}

// Subagents runs synchronous in-process subagents for the orchestration tools.
type Subagents interface {
	Run(ctx context.Context, req SubagentRequest) (SubagentResult, error)
}

// KeySubagents is provided by the agent module (rig agent.NewRunner based).
var KeySubagents = cordis.NewServiceKey[Subagents]("zeokeel/subagents")

// Agent permission modes (the old shell's permission setting). The approval
// gate reads the current mode on every tool call, so switching is immediate.
const (
	// SettingAgentPermission stores the current permission mode.
	SettingAgentPermission = "agent.permission"
	// PermissionAsk asks for every tool call (the default).
	PermissionAsk = "ask"
	// PermissionWorkspace auto-allows read-only workspace tools and asks for
	// everything else.
	PermissionWorkspace = "workspace"
	// PermissionFull allows every tool call without asking.
	PermissionFull = "full"
	// PermissionNever denies every tool call (fail-closed).
	PermissionNever = "never"
)
