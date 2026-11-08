package agent

// Plan-mode gate: while settings agent.mode is "plan", only read-only
// exploration, memory/skill reads, the planning bridge and the task-board read
// tools are allowed; every other tool is denied fail-closed. The mode is read on
// every approval, so switching back to normal resumes the approval policy.

import (
	"context"
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

// workspaceReadOnlyTools are the tools a per-session "workspace" permission
// override auto-allows; other tools fall through to the approvals service.
var workspaceReadOnlyTools = map[string]bool{
	"read_file": true,
	"list_dir":  true,
	"glob":      true,
	"grep":      true,
}

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
// permission override applies (full allows, never denies, workspace allows the
// read-only tools); interaction/presentation host tools are then allowed
// without an approval prompt (their own overlay is the user interaction);
// otherwise the approvals service's global policy applies.
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
			case contracts.PermissionNever:
				return rig.DecisionDeny, nil
			case contracts.PermissionWorkspace:
				if workspaceReadOnlyTools[name] {
					return rig.DecisionAllow, nil
				}
			}
		}
		if interactionTools[name] {
			// A global "never" policy is an explicit deny-all switch: it must
			// still deny the interaction tools even though the default "ask"
			// prompt is skipped for them.
			if s.approvals != nil && strings.EqualFold(strings.TrimSpace(s.approvals.Mode()), contracts.PermissionNever) {
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
		})
		if err != nil {
			return "", err
		}
		switch decision {
		case contracts.ApprovalAllow:
			return rig.DecisionAllow, nil
		case contracts.ApprovalAllowAlways:
			return rig.DecisionAllowAlways, nil
		default:
			return rig.DecisionDeny, nil
		}
	})
}
