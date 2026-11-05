package runtime

import (
	"errors"
	"fmt"
	"strings"
)

// Plan mode is a session-level review gate layered on top of the agent preset.
// It does not change the topology or the model-visible tool list: every tool
// is still advertised, and the runtime rejects the mutating/external ones when
// they are invoked. The controller persists the flag per session; Config.
// PlanModeFunc lets the shared agent resolve the flag for the session that is
// running, so enabling plan mode needs no registry or topology rebuild.
//
// Presenting a plan stays possible: the "present" tool is read-only with
// respect to the workspace (it records metadata in the session), so the agent
// can submit a plan through it and then stop for review. Read-only tools
// (read_file, glob, grep, read_image, session queries, terminal_read, ...) are
// likewise allowed, which is what lets the agent investigate before planning.

// ErrPlanMode reports a tool call rejected because plan mode is active. It is
// wrapped by the error the model receives as the tool result, so callers can
// classify it with errors.Is.
var ErrPlanMode = errors.New("plan mode: submit a plan with the present tool and wait for user approval")

// planModeBlockedPrefixes are tool-name families that mutate files/processes
// or reach outside the workspace. Plans are read-only investigation, so these
// are blocked by name instead of by argument inspection.
var planModeBlockedPrefixes = []string{
	"terminal_",
	"mcp__",
	"web_",
}

// planModeBlockedNames are mutating or external tools whose names do not share
// a prefix. job_list/job_output stay allowed because they only read a running
// job; job_start and job_kill change process state.
var planModeBlockedNames = map[string]bool{
	"bash":               true,
	"pwsh":               true,
	"write_file":         true,
	"edit_file":          true,
	"str_replace_editor": true,
	"job_start":          true,
	"job_kill":           true,
	"enter_worktree":     true,
	"exit_worktree":      true,
	"schedule_create":    true,
	"schedule_update":    true,
	"schedule_delete":    true,
}

// PlanModeBlocksTool reports whether plan mode must reject name. It is
// exported so the app and tests can describe the policy without invoking a
// tool; the runtime uses the same predicate before every call.
func PlanModeBlocksTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	if planModeBlockedNames[name] {
		return true
	}
	for _, prefix := range planModeBlockedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// planModeBlockedError is the tool error delivered to the model when plan mode
// rejects a call. The leading message is the plan-mode instruction; the tool
// name is appended so the model knows which call was blocked.
func planModeBlockedError(name string) error {
	return fmt.Errorf("%w (tool %q is blocked while planning)", ErrPlanMode, name)
}
