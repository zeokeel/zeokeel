package agent

// Plan-mode gate: while settings agent.mode is "plan", only read-only
// exploration, memory/skill reads, the planning bridge and the task-board read
// tools are allowed; every other tool is denied fail-closed. The mode is read on
// every approval, so switching back to normal resumes the approval policy.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/gate"
	"github.com/zeokeel/zeokeel-rig/tools/surface"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// planModeAllowedTools is the plan-mode whitelist (exact, model-visible names).
var planModeAllowedTools = []string{
	"read_file",
	"list_dir",
	"glob",
	"grep",
	"ask_user",
	"present",
	"plan_review",
	"exit_plan_mode",
	"goal",
	"get_goal",
	"create_goal",
	"update_goal",
	"team_task_list",
	"team_task_get",
	"memory",
	"skill",
	"skills",
	"search",
	"sandbox_status",
	"spill_read",
	"tool_search",
	"web_fetch",
}

// minimalModeAllowedTools is the read-only exploration surface for chat.mode
// minimal; every other tool is denied fail-closed.
var minimalModeAllowedTools = []string{
	"read_file",
	"list_dir",
	"glob",
	"grep",
	"memory",
	"skill",
	"skills",
	"search",
	"sandbox_status",
	"spill_read",
	"tool_search",
	"web_fetch",
	"ask_user",
	"present",
	"goal",
	"get_goal",
	"create_goal",
	"update_goal",
	"plan_review",
	"exit_plan_mode",
	"team_task_list",
	"team_task_get",
}

// creatorModeAllowedTools is the writing/persona surface for chat.mode creator;
// coding tools are denied even when they only read files.
var creatorModeAllowedTools = []string{
	"ask_user",
	"present",
	"goal",
	"get_goal",
	"create_goal",
	"update_goal",
	"plan_review",
	"exit_plan_mode",
	"memory",
	"skill",
	"skills",
	"spill_read",
	"tool_search",
}

// toolAllowed checks one tool against a whitelist (exact, trimmed).
func toolAllowed(name string, allowedTools []string) bool {
	name = strings.TrimSpace(name)
	for _, allowed := range allowedTools {
		if name == allowed {
			return true
		}
	}
	return false
}

// planModeToolAllowed reports whether a plan-mode tool call is on the whitelist.
func planModeToolAllowed(name string) bool {
	return toolAllowed(name, planModeAllowedTools)
}

// runtimeMode reports the effective model-facing execution mode: the plan mode
// wins over the chat modes (exactly like the gate), then minimal/creator, then
// standard.
func (s *service) runtimeMode() string {
	if s.agentModePlan() {
		return contracts.AgentModePlan
	}
	return s.chatMode()
}

// modeToolWhitelist returns the model-visible tool whitelist of one runtime
// mode. An empty result means "no filtering" (standard) — the gate still applies
// its own policy.
func modeToolWhitelist(mode string) []string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case contracts.AgentModePlan:
		return planModeAllowedTools
	case contracts.ChatModeMinimal:
		return minimalModeAllowedTools
	case contracts.ChatModeCreator:
		return creatorModeAllowedTools
	default:
		return nil
	}
}

// filterToolsForMode narrows a tool surface to the mode whitelist, in place and
// preserving registration order. The gate keeps denying non-whitelisted calls
// (fail-closed) even if a caller accidentally hands it an unfiltered surface;
// this function only stops the model from being invited to call tools it can
// never use.
func filterToolsForMode(tools []rig.Tool, mode string) []rig.Tool {
	allowed := modeToolWhitelist(mode)
	if len(allowed) == 0 {
		return tools
	}
	return surface.OnlyNames(tools, allowed)
}

// modePolicyLine is the model-facing runtime-mode sentence: the current mode,
// the exact tool set the run receives, and the fail-closed consequence of
// calling anything else. Standard mode adds no line (the approval policy
// sentence already describes it).
func modePolicyLine(mode string, toolNames map[string]bool) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	var label string
	switch mode {
	case contracts.AgentModePlan:
		label = "plan"
	case contracts.ChatModeMinimal:
		label = "minimal"
	case contracts.ChatModeCreator:
		label = "creator"
	default:
		return ""
	}
	names := make([]string, 0, len(toolNames))
	for name := range toolNames {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	sort.Strings(names)
	allowed := "no tools"
	if len(names) > 0 {
		allowed = strings.Join(names, ", ")
	}
	return fmt.Sprintf("Runtime mode: %s — only these tools are available in this run: %s. Calls to any other tool are denied by the runtime (no approval dialog); plan your work with this restriction.", label, allowed)
}

// chatMode reads chat.mode, normalizing unknown/missing values to standard.
func (s *service) chatMode() string {
	if s.settings == nil {
		return contracts.ChatModeStandard
	}
	switch strings.ToLower(strings.TrimSpace(s.settings.Get(contracts.SettingChatMode, ""))) {
	case contracts.ChatModeMinimal:
		return contracts.ChatModeMinimal
	case contracts.ChatModeCreator:
		return contracts.ChatModeCreator
	case contracts.ChatModeStandard:
		return contracts.ChatModeStandard
	default:
		return contracts.ChatModeStandard
	}
}

// agentModePlan reports whether the effective mode setting selects plan mode.
func (s *service) agentModePlan() bool {
	if s.settings == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(s.settings.Get(contracts.SettingAgentMode, "")),
		contracts.AgentModePlan)
}

// The per-session "workspace" override auto-allows the shared
// contracts.WorkspaceAutoAllowedTools set: the workspace file tools plus
// read-only/informational tools that cannot leave or modify the workspace.
// Tools that can leave the workspace (network, settings writes and unknown
// tools) fall through to the approvals service, and the same frozen list is read
// by the approvals service so the two layers cannot drift. Two families are
// exempt outside that list as well, by product decision (2026-11-26, personal
// helper): the shell family (OS sandbox exemption, see sandbox.go) and the MCP
// family (mcp__ prefix, see contracts.IsMCPTool).

// approvalModeNever is the approvals service's headless deny-all policy value
// (approvals.ModeNever). It is not a selectable UI permission mode: stored
// "never" rows normalize to read-only, so this only guards an assembly config
// that explicitly installs the deny-all backend.
const approvalModeNever = "never"

// interactionTools are the host tools that interact with the user directly:
// ask_user, present and goal render their own question/artifact/goal overlay,
// and plan_review/exit_plan_mode drive the plan-review overlay. They are exempt
// from the generic tool approval prompt because that prompt would be a second,
// meaningless modal in front of the real interaction (asking the user to
// approve asking a question); denying it silently drops the interaction
// entirely. The names are model-visible literals because the host-tools
// constants live in internal/modules/hosttools, which this module does not
// import.
var interactionTools = map[string]bool{
	"ask_user":       true,
	"present":        true,
	"plan_review":    true,
	"exit_plan_mode": true,
	// "goal" is the retired Goal v1 name. It is no longer registered, but the
	// interaction exemption keeps naming it so old transcripts and the plan/
	// creator whitelists stay consistent (a call can never arrive).
	"goal": true,
	// The DSH goal trio renders no approval dialog of its own: the goal tools
	// prompt nothing, and approval would only add a second meaningless modal.
	"get_goal":    true,
	"create_goal": true,
	"update_goal": true,
}

// gate builds the tool approval gate for one session. Plan and chat modes are
// checked on every call (highest priority); then an optional per-session
// permission override applies (full allows everything, read-only allows the
// shared contracts.ReadOnlyAutoAllowedTools set, workspace allows the
// contracts.WorkspaceAutoAllowedTools set); the OS sandbox exempts the command
// family (confined: silent, unconfined: recorded); the MCP family is exempt by
// prefix; interaction/presentation host tools are then allowed without an
// approval prompt (their own overlay is the user interaction); otherwise the
// approvals service's global policy applies.
func (s *service) gate(sessionIDs ...string) rig.Gate {
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = strings.TrimSpace(sessionIDs[0])
	}
	return s.gateForRole(sessionID, "")
}

// gateForRole is gate() for a run executing as rolePresetID instead of the
// session's own preset ("" = the session's own).
//
// It exists because a delegated child deliberately runs as a DIFFERENT role from
// its parent: the child of a lead session executes (contracts.PresetExecutorID)
// while the parent orchestrates. Judging the child's calls against the parent's
// allowlist would deny the very write/shell calls the child was asked to make —
// the surface and the veto have to come from the same role.
func (s *service) gateForRole(sessionID, rolePresetID string) rig.Gate {
	sessionID = strings.TrimSpace(sessionID)
	rolePresetID = strings.TrimSpace(rolePresetID)
	// Snapshot the run's tool root once: the sandbox exemption must be judged
	// against the policy that will actually dispatch these calls, not against
	// whichever workspace is current when a call happens.
	runRoot := s.sessionWorkspaceRoot(sessionID)
	presetDenial := func(name string) string {
		if rolePresetID != "" {
			return s.presetToolsDenialForRole(rolePresetID, name)
		}
		return s.presetToolsDenial(sessionID, name)
	}
	return s.presetToolsGate(sessionID, rolePresetID, s.taskBindingGate(sessionID, gate.Func(func(ctx context.Context, tool rig.Tool, call rig.ToolCall) (rig.Decision, error) {
		name := call.Name
		if tool != nil && strings.TrimSpace(tool.Name()) != "" {
			name = tool.Name()
		}
		// Task binding (M5, taskbinding.go): a session bound to a board task may
		// only call the tools that task's binding names. This sits immediately
		// after the name is resolved and BEFORE every branch that can allow —
		// the plan/chat whitelists, the per-session permission override, the OS
		// sandbox exemption, the interaction-tool exemption, the remembered
		// "always allow" set and the approval dialog — because each of those can
		// only widen: after them, a remembered tool or a sandbox exemption could
		// bypass the task's policy. Unbound sessions are unaffected ("" reason).
		if s.taskBindingDenial(ctx, sessionID, name) != "" {
			return rig.DecisionDeny, nil
		}
		// Role boundary (preset.go, 2026-11-26): a session that selected an
		// agent preset with a tool allowlist may only call the tools it names.
		// Same position and same reason as the task binding above: it sits
		// BEFORE every branch that can allow (plan/chat whitelists, the
		// per-session permission override, the sandbox exemption, interaction
		// tools, the remembered "always allow" set and the approval dialog), so
		// a remembered tool cannot widen a role. It is what lets the built-in
		// lead role be unable — not merely unwilling — to write files or run
		// commands. Sessions with no preset are unaffected.
		if presetDenial(name) != "" {
			return rig.DecisionDeny, nil
		}
		if s.agentModePlan() {
			if planModeToolAllowed(name) {
				return rig.DecisionAllow, nil
			}
			return rig.DecisionDeny, nil
		}
		switch s.chatMode() {
		case contracts.ChatModeMinimal:
			if toolAllowed(name, minimalModeAllowedTools) {
				return rig.DecisionAllow, nil
			}
			return rig.DecisionDeny, nil
		case contracts.ChatModeCreator:
			if toolAllowed(name, creatorModeAllowedTools) {
				return rig.DecisionAllow, nil
			}
			return rig.DecisionDeny, nil
		}
		if override := strings.TrimSpace(s.sessionPermission(sessionID)); override != "" {
			// The judgement comes from overridePermission (permission_policy.go),
			// not from an inline switch: the model-facing policy sentence is
			// rendered from the same function, so "what the prompt promises" and
			// "what the gate decides" cannot drift for this layer (2026-11-26
			// audit, 高: a read-only override refused shell/MCP while the prompt
			// still called them exempt).
			switch overridePermission(override, name) {
			case permissionAllowed:
				return rig.DecisionAllow, nil
			case permissionRefused:
				// A session-level read-only override is a hard restriction: a
				// tool outside the read-only set must never fall through to the
				// global (often workspace-write) policy, which would silently
				// auto-allow write_file. Interaction host tools and tools the
				// user explicitly remembered for this session stay allowed.
				if interactionTools[name] || s.allowedToolForSession(sessionID, name) {
					if s.denyAllApprovalPolicy() {
						return rig.DecisionDeny, nil
					}
					return rig.DecisionAllow, nil
				}
				return rig.DecisionDeny, nil
			}
		}
		// OS sandbox: the SDK decides whether this shell call is already confined
		// (gate.AllowSandboxedCommands: shell surface + runner reports real
		// enforcement + policy is not danger-full-access). The branch sits after
		// the plan/chat whitelists and the per-session override so those keep
		// their veto, and before the interaction/remembered/approval branches so
		// no dialog is requested. An explicit headless deny-all policy still
		// denies: skipping the prompt must never bypass it.
		//
		// A missing approvals service does NOT get a veto here: this call is
		// confined by the OS, so it never needed an approval — the fail-closed
		// rule below is about calls that would have to ask.
		if s.sandboxedCommand(ctx, runRoot, tool, call) {
			if s.denyAllApprovalPolicy() {
				return rig.DecisionDeny, nil
			}
			return rig.DecisionAllow, nil
		}
		// Command family without SDK-certified confinement (personal-helper
		// permission model, 2026-11-26): shell commands never park on an approval
		// card while the sandbox module is assembled. This branch is the honest
		// other half of the sandbox exemption above — the call runs, but there is
		// no OS-level constraint on it any more, and recordUnconfinedShell says so
		// once per session+tool instead of letting the difference degrade into a
		// generic "approved". Explicit restrictions keep their veto: the plan/chat
		// whitelists and the per-session override ran earlier, and the headless
		// deny-all policy still denies.
		//
		// It must also deny when the assembly has no approvals service at all:
		// "there is a sandbox module" is not the same as "somebody is
		// accountable for this call", and skipping the fail-closed branch below
		// by position would make the exemption the one hole in the policy.
		if s.shellCommandExempt(runRoot, tool, call) {
			if decision, err, vetoed := s.exemptionVeto(); vetoed {
				return decision, err
			}
			s.recordUnconfinedShell(sessionID, name, runRoot)
			return rig.DecisionAllow, nil
		}
		// MCP family: the model calls the user's own MCP servers as ordinary
		// tools, and an approval card in front of every call only interrupts work
		// (personal-helper permission model, 2026-11-26). The family is matched by
		// the mcp__ prefix (contracts.IsMCPTool) because the concrete tool names
		// are runtime data. Explicit restrictions still veto: plan/chat whitelists
		// and the per-session read-only override ran earlier, and the headless
		// deny-all policy still denies.
		//
		// Like the command family it must not jump over the fail-closed branch:
		// without an approvals service this product would otherwise allow every
		// long-tail MCP tool silently.
		if contracts.IsMCPTool(name) {
			if decision, err, vetoed := s.exemptionVeto(); vetoed {
				return decision, err
			}
			return rig.DecisionAllow, nil
		}
		if interactionTools[name] {
			// A headless "never" assembly policy is an explicit deny-all switch:
			// it must still deny the interaction tools even though the default
			// approval prompt is skipped for them. It is not a UI permission
			// mode (no stored row maps to it any more).
			if s.denyAllApprovalPolicy() {
				return rig.DecisionDeny, nil
			}
			return rig.DecisionAllow, nil
		}
		// A tool the user already chose to "always allow in this session" no
		// longer asks. Plan/chat modes and a headless deny-all policy keep
		// their veto: the remembered set only skips the prompt.
		if s.allowedToolForSession(sessionID, name) {
			if s.denyAllApprovalPolicy() {
				return rig.DecisionDeny, nil
			}
			return rig.DecisionAllow, nil
		}
		if s.approvals == nil {
			// Fail-closed: this product has no approvals service, so a tool that
			// needs an approval prompt cannot be authorized. Silently allowing it
			// would bypass the user's approval policy entirely; the readable error
			// aborts the run instead of executing the call.
			return rig.DecisionDeny, fmt.Errorf(
				"tool approval requires an approvals service; this product has none")
		}
		summary := summarizeToolArguments(name, call.Arguments)
		decision, err := s.approvals.Request(ctx, contracts.ApprovalRequest{
			Tool:      name,
			Summary:   summary,
			Arguments: call.Arguments,
			CreatedAt: time.Now(),
			SessionID: sessionID,
			// Provenance is persisted by the automation module; an empty source
			// means the user's own conversation.
			Source: sessionOrigin(s.settings, sessionID),
		})
		if err != nil {
			return "", err
		}
		switch decision {
		case contracts.ApprovalAllow:
			return rig.DecisionAllow, nil
		case contracts.ApprovalAllowAlways:
			// The dialog promised to stop asking for this tool in this session:
			// remember it across runs (persisted per session) and still return
			// allow-always so the current run also skips repeated identical calls.
			s.rememberAllowedTool(sessionID, name)
			return rig.DecisionAllowAlways, nil
		default:
			return rig.DecisionDeny, nil
		}
	})))
}

// denyAllApprovalPolicy reports whether this assembly's approval policy is the
// headless deny-all sentinel (Mode() == "never"). It is a veto, not a default:
// every branch that skips the approval prompt checks it first.
func (s *service) denyAllApprovalPolicy() bool {
	return s != nil && s.approvals != nil &&
		strings.EqualFold(strings.TrimSpace(s.approvals.Mode()), approvalModeNever)
}

// exemptionVeto is the guard every exemption that runs PAST the approval dialog
// has to consult before allowing a call.
//
// Two rules, both of which used to live only at the end of the chain:
//
//  1. No approvals service → deny with a readable error (fail-closed). The
//     exemption branches sit before the approval branch, so without this check a
//     sandbox-module assembly with no approvals service would allow the command
//     and MCP families silently — the exact opposite of the promise made below.
//  2. Deny-all policy (headless) → deny without asking.
//
// vetoed reports whether the caller must return (decision, err) immediately.
func (s *service) exemptionVeto() (rig.Decision, error, bool) {
	if s == nil || s.approvals == nil {
		return rig.DecisionDeny, fmt.Errorf(
			"tool approval requires an approvals service; this product has none"), true
	}
	if s.denyAllApprovalPolicy() {
		return rig.DecisionDeny, nil, true
	}
	return "", nil, false
}
