package contracts

import (
	"context"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel-rig/gate"
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

// The read-only approval set is derived from the SDK's judgement instead of a
// private copy: gate.SandboxReadOnlyTools is the SDK's list of tools that are
// safe without asking (its criterion is "does not touch the user's files,
// processes or network"), and it grows with every SDK release. Only two small
// deltas are product decisions.

// productReadOnlyRemovals are SDK read-only tools this product deliberately
// keeps OUT of the read-only preset.
var productReadOnlyRemovals = map[string]bool{
	// The memory tool writes the durable memory store (add/update/remove/clear).
	// The read-only preset must not mutate durable state, so it asks for those
	// edits; the default workspace preset keeps it auto-allowed (see
	// workspaceStateTools).
	"memory": true,
}

// productReadOnlyAdditions are product-only tools with no SDK counterpart that
// are informational/read-only, so the read-only preset runs them without asking.
var productReadOnlyAdditions = map[string]bool{
	"skills":             true,
	"todo":               true,
	"todos":              true,
	"team_task_list":     true,
	"team_task_get":      true,
	"lsp_definition":     true,
	"lsp_diagnostics":    true,
	"lsp_hover":          true,
	"lsp_implementation": true,
	"lsp_references":     true,
	"lsp_symbols":        true,
}

// ReadOnlyAutoAllowedTools is the set of tools the read-only preset runs without
// an approval prompt: the SDK's read-only set minus the documented product
// removals, plus the product-only informational tools. Everything else
// (workspace writes, shell, network, MCP, settings writes and unknown tools)
// asks. It is the read-only subset of WorkspaceAutoAllowedTools.
var ReadOnlyAutoAllowedTools = composeReadOnlyTools()

// composeReadOnlyTools derives the read-only set from the SDK list plus the
// documented product deltas above.
func composeReadOnlyTools() map[string]bool {
	out := make(map[string]bool, len(gate.SandboxReadOnlyTools)+len(productReadOnlyAdditions))
	for _, name := range gate.SandboxReadOnlyTools {
		name = strings.TrimSpace(name)
		if name == "" || productReadOnlyRemovals[name] {
			continue
		}
		out[name] = true
	}
	for name := range productReadOnlyAdditions {
		out[name] = true
	}
	return out
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
