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
	"team_task_list",
	"team_task_get",
	"memory",
	"skill",
	"skills",
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
	"web_fetch",
	"ask_user",
	"present",
	"goal",
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
	"plan_review",
	"exit_plan_mode",
	"memory",
	"skill",
	"skills",
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
	filtered := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if toolAllowed(tool.Name(), allowed) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
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
// Tools that can leave the workspace (shell, network, MCP, settings writes and
// unknown tools) fall through to the approvals service, and the same frozen list
// is read by the approvals service so the two layers cannot drift.

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
	"goal":           true,
}

// gate builds the tool approval gate for one session. Plan and chat modes are
// checked on every call (highest priority); then an optional per-session
// permission override applies (full allows everything, read-only allows the
// shared contracts.ReadOnlyAutoAllowedTools set, workspace allows the
// contracts.WorkspaceAutoAllowedTools set); interaction/presentation host tools
// are then allowed without an approval prompt (their own overlay is the user
// interaction); otherwise the approvals service's global policy applies.
func (s *service) gate(sessionIDs ...string) rig.Gate {
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = strings.TrimSpace(sessionIDs[0])
	}
	return gate.Func(func(ctx context.Context, tool rig.Tool, call rig.ToolCall) (rig.Decision, error) {
		name := call.Name
		if tool != nil && strings.TrimSpace(tool.Name()) != "" {
			name = tool.Name()
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
		if override := s.sessionPermission(sessionID); override != "" {
			switch override {
			case contracts.PermissionFull:
				return rig.DecisionAllow, nil
			case contracts.PermissionReadOnly:
				// A session-level read-only override is a hard restriction: a
				// tool outside the read-only set must never fall through to the
				// global (often workspace-write) policy, which would silently
				// auto-allow write_file. Interaction host tools and tools the
				// user explicitly remembered for this session stay allowed.
				if contracts.ReadOnlyAutoAllowed(name) {
					return rig.DecisionAllow, nil
				}
				if interactionTools[name] || s.allowedToolForSession(sessionID, name) {
					if s.approvals != nil && strings.EqualFold(strings.TrimSpace(s.approvals.Mode()), approvalModeNever) {
						return rig.DecisionDeny, nil
					}
					return rig.DecisionAllow, nil
				}
				return rig.DecisionDeny, nil
			case contracts.PermissionWorkspace:
				if contracts.WorkspaceAutoAllowed(name) {
					return rig.DecisionAllow, nil
				}
			}
		}
		if interactionTools[name] {
			// A headless "never" assembly policy is an explicit deny-all switch:
			// it must still deny the interaction tools even though the default
			// approval prompt is skipped for them. It is not a UI permission
			// mode (no stored row maps to it any more).
			if s.approvals != nil && strings.EqualFold(strings.TrimSpace(s.approvals.Mode()), approvalModeNever) {
				return rig.DecisionDeny, nil
			}
			return rig.DecisionAllow, nil
		}
		// A tool the user already chose to "always allow in this session" no
		// longer asks. Plan/chat modes and a headless deny-all policy keep
		// their veto: the remembered set only skips the prompt.
		if s.allowedToolForSession(sessionID, name) {
			if s.approvals != nil && strings.EqualFold(strings.TrimSpace(s.approvals.Mode()), approvalModeNever) {
				return rig.DecisionDeny, nil
			}
			return rig.DecisionAllow, nil
		}
		if s.approvals == nil {
			return rig.DecisionAllow, nil
		}
		summary := summarizeToolArguments(call.Arguments)
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
	})
}
