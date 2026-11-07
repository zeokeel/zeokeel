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

// planModeToolAllowed reports whether a plan-mode tool call is on the whitelist.
func planModeToolAllowed(name string) bool {
	name = strings.TrimSpace(name)
	for _, allowed := range planModeAllowedTools {
		if name == allowed {
			return true
		}
	}
	return false
}

// agentModePlan reports whether the effective mode setting selects plan mode.
func (s *service) agentModePlan() bool {
	if s.settings == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(s.settings.Get(contracts.SettingAgentMode, "")),
		contracts.AgentModePlan)
}

// gate builds the tool approval gate. Plan mode is checked on every call and
// bypasses approvals for the read-only whitelist; otherwise the approvals
// service's policy applies (or a permissive gate when none is wired).
func (s *service) gate() rig.Gate {
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
