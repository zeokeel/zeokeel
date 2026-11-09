package contracts

import (
	"context"
	"strings"

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
	// SettingAgentPermission stores the current permission mode. Unset or
	// unknown values resolve to workspace.
	SettingAgentPermission = "agent.permission"
	// PermissionAsk asks for every tool call.
	PermissionAsk = "ask"
	// PermissionWorkspace (the default) auto-allows the workspace file tools
	// (reading, listing, searching, writing and editing inside the workspace)
	// and asks for everything that can leave it: shell, network, MCP and
	// unknown tools.
	PermissionWorkspace = "workspace"
	// PermissionFull allows every tool call without asking.
	PermissionFull = "full"
	// PermissionNever denies every tool call (fail-closed).
	PermissionNever = "never"
)

// WorkspaceAutoAllowedTools is the set of tools the default "workspace"
// permission mode runs without an approval prompt: the workspace file tools
// plus read-only / informational tools that cannot leave or modify the
// workspace. Everything that can leave the workspace, mutate app settings,
// reach the network, spawn MCP work or is unknown still asks. The agent gate
// and the approvals service both read this one list so the two layers cannot
// drift apart.
var WorkspaceAutoAllowedTools = map[string]bool{
	// Workspace file tools.
	"read_file":          true,
	"write_file":         true,
	"edit_file":          true,
	"str_replace_editor": true,
	"list_dir":           true,
	"glob":               true,
	"grep":               true,
	// Read-only / informational tools.
	"read_image":           true,
	"working_directory":    true,
	"memory":               true,
	"skill":                true,
	"skills":               true,
	"settings_get":         true,
	"todo":                 true,
	"todos":                true,
	"todo_write":           true,
	"team_task_list":       true,
	"team_task_get":        true,
	"terminal_list":        true,
	"job_list":             true,
	"job_output":           true,
	"job_wait":             true,
	"session_search":       true,
	"session_event_search": true,
	"session_event_read":   true,
	"session_event_trace":  true,
	"session_trace":        true,
	"lsp_definition":       true,
	"lsp_diagnostics":      true,
	"lsp_hover":            true,
	"lsp_implementation":   true,
	"lsp_references":       true,
	"lsp_symbols":          true,
}

// WorkspaceAutoAllowed reports whether the default workspace permission mode
// runs one tool without asking for approval.
func WorkspaceAutoAllowed(tool string) bool {
	return WorkspaceAutoAllowedTools[strings.TrimSpace(tool)]
}
