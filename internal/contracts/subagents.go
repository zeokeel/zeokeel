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

// Agent permission modes. The three user-facing presets mirror DSH's
// read-only / workspace-write / danger-full-access permission presets. The
// approval gate reads the current mode on every tool call, so switching is
// immediate.
const (
	// SettingAgentPermission stores the current permission mode. Unset or
	// unknown values resolve to workspace.
	SettingAgentPermission = "agent.permission"
	// PermissionReadOnly is the read-only preset: read-only and informational
	// tools are auto-allowed, everything else goes through the approval gate
	// (the dialog's "always allow in this session" still stops repeats for a
	// remembered tool).
	PermissionReadOnly = "read-only"
	// PermissionWorkspace is the default (workspace-write) preset: it
	// auto-allows the workspace file tools (reading, listing, searching,
	// writing and editing inside the workspace) and asks for everything that
	// can leave it: shell, network, MCP and unknown tools.
	PermissionWorkspace = "workspace"
	// PermissionFull is the all-permissions preset: every tool call is allowed
	// without asking.
	PermissionFull = "full"

	// Deprecated: PermissionAsk and PermissionNever are the retired "ask" and
	// "never" modes. They are kept only as legacy stored-value spellings for
	// NormalizePermissionMode; both normalize to PermissionReadOnly. No UI may
	// offer them and no new code should branch on them.
	PermissionAsk   = "ask"
	PermissionNever = "never"
)

// NormalizePermissionMode maps every accepted stored/configured spelling onto
// the three permission presets. Legacy rows normalize deterministically:
// "ask" and "never" (the retired modes) become read-only, empty and unknown
// values become the workspace default, and the approvals service's legacy
// "auto" alias stays full.
func NormalizePermissionMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case PermissionReadOnly, PermissionAsk, PermissionNever:
		return PermissionReadOnly
	case PermissionWorkspace:
		return PermissionWorkspace
	case PermissionFull, "auto":
		return PermissionFull
	default:
		return PermissionWorkspace
	}
}

// ReadOnlyAutoAllowedTools is the set of tools the read-only preset runs
// without an approval prompt: read-only file tools plus informational tools
// that cannot modify the workspace, settings or the outside world. Everything
// else (workspace writes, shell, network, MCP, settings writes and unknown
// tools) asks. It is the read-only subset of WorkspaceAutoAllowedTools.
var ReadOnlyAutoAllowedTools = map[string]bool{
	// Read-only file tools.
	"read_file": true,
	"list_dir":  true,
	"glob":      true,
	"grep":      true,
	// Read-only / informational tools.
	"read_image":        true,
	"working_directory": true,
	// The memory tool is NOT read-only: it can add/update/remove/clear the
	// persistent memory store, so it lives in the workspace-only state set
	// below instead (read-only must not mutate durable state).
	"skill":                true,
	"skills":               true,
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

// workspaceWriteFileTools are the file tools the workspace preset adds on top
// of the read-only set: they modify files inside the workspace.
var workspaceWriteFileTools = map[string]bool{
	"write_file":         true,
	"edit_file":          true,
	"str_replace_editor": true,
}

// workspaceStateTools are the state-mutating product tools the default
// workspace preset keeps auto-allowed (they write app-owned state, not files):
// the memory tool can add/update/remove/clear persistent memory entries, which
// the read-only preset must ask for.
var workspaceStateTools = map[string]bool{
	"memory": true,
}

// WorkspaceAutoAllowedTools is the set of tools the default workspace
// (workspace-write) preset runs without an approval prompt: the read-only set
// plus the workspace write tools. Everything that can leave the workspace,
// mutate app settings, reach the network, spawn MCP work or is unknown still
// asks. The agent gate and the approvals service both read this one list so the
// two layers cannot drift apart; it is composed from ReadOnlyAutoAllowedTools
// so the read-only subset cannot be duplicated out of sync.
var WorkspaceAutoAllowedTools = composeAutoAllowedTools(
	ReadOnlyAutoAllowedTools, workspaceWriteFileTools, workspaceStateTools)

// composeAutoAllowedTools merges the tool-name sets into one read-only map.
func composeAutoAllowedTools(sets ...map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for _, set := range sets {
		for name, allowed := range set {
			if allowed {
				out[name] = true
			}
		}
	}
	return out
}

// ReadOnlyAutoAllowed reports whether the read-only preset runs one tool
// without asking for approval.
func ReadOnlyAutoAllowed(tool string) bool {
	return ReadOnlyAutoAllowedTools[strings.TrimSpace(tool)]
}

// WorkspaceAutoAllowed reports whether the default workspace preset runs one
// tool without asking for approval.
func WorkspaceAutoAllowed(tool string) bool {
	return WorkspaceAutoAllowedTools[strings.TrimSpace(tool)]
}
