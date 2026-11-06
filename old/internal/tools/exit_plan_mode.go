package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// ExitPlanModeToolName is the model-facing plan review tool (DSH parity). It
// stays registered while plan mode is inactive, so entering or leaving plan
// mode changes only the prompt section and the call check, not the tool
// catalog.
const ExitPlanModeToolName = "exit_plan_mode"

// ExitPlanModeResultKind marks a successful exit_plan_mode result. The shell
// parses the result to render the plan review card, so the tool itself does not
// need to reach the session layer.
const ExitPlanModeResultKind = "plan_review"

// ExitPlanModeConfig wires the per-session plan-mode state the app owns.
type ExitPlanModeConfig struct {
	// PlanMode reports whether sessionID is in plan mode. The tool is always
	// registered (a stable tool catalog, like DSH) but only runs while plan
	// mode is active; nil rejects every call, which is the right behavior for
	// a bare runtime whose caller did not wire plan mode.
	PlanMode func(sessionID string) bool
}

// ExitPlanModeTool records a finished plan for the user's review. It is
// deliberately non-blocking: the shell presents the review card from the
// tool's result, and Approve / Keep planning / Exit are user actions in that
// card, exactly like the previous present-based flow. The tool never switches
// plan mode itself.
type ExitPlanModeTool struct {
	cfg ExitPlanModeConfig
}

// NewExitPlanModeTool returns the plan review tool with cfg bound.
func NewExitPlanModeTool(cfg ExitPlanModeConfig) *ExitPlanModeTool {
	return &ExitPlanModeTool{cfg: cfg}
}

// Name implements runtime.Tool.
func (t *ExitPlanModeTool) Name() string { return ExitPlanModeToolName }

// Description implements runtime.Tool.
func (t *ExitPlanModeTool) Description() string { return exitPlanModeDescription }

const exitPlanModeDescription = `Present the completed plan for the user's review and, once approved, leave plan mode.

Use it only while plan mode is active. Call it with a JSON object:
  {"plan":"# Plan\n1. Inspect the workspace read-only\n2. ..."}

"plan" is required: the complete plan as Markdown, starting with a "#" heading that names it.
The tool records the plan for the user's review; submitting it does not change the mode by itself.
Stop after the call and wait for the decision:
- Approve leaves plan mode and the user's continuation starts the next step.
- Keep planning keeps plan mode on; the user's feedback arrives as the next user message, so revise the plan and call this tool again.
If plan mode is not active the call fails; enter plan mode with the /plan command first.`

// exitPlanModeArgs is the decoded exit_plan_mode argument object.
type exitPlanModeArgs struct {
	Plan string `json:"plan"`
}

// ExitPlanModeResult is the tool's successful result document. The shell finds
// the newest successful call in the session snapshot and renders its Markdown,
// so the plan survives a reopened session through the persisted tool result.
type ExitPlanModeResult struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Markdown  string `json:"markdown"`
	Submitted bool   `json:"submitted"`
}

// Invoke implements runtime.Tool. Outside plan mode (or from a subagent, which
// cannot open the session's review surface) the call fails with a message the
// model can act on.
func (t *ExitPlanModeTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if runtime.SubagentDepth(ctx) > 0 {
		return "", errors.New("exit_plan_mode: call it from the session's root agent; a subagent must include the plan in its final result")
	}
	sessionID := runtime.SessionIDFromContext(ctx)
	if t == nil || t.cfg.PlanMode == nil || !t.cfg.PlanMode(sessionID) {
		return "", errors.New("exit_plan_mode: plan mode is not active; enter plan mode with /plan first")
	}

	var in exitPlanModeArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("exit_plan_mode: invalid arguments: %w", err)
		}
	}
	plan := strings.TrimSpace(in.Plan)
	if plan == "" {
		return "", errors.New(`exit_plan_mode: "plan" is required`)
	}
	title, ok := planHeadingTitle(plan)
	if !ok {
		return "", errors.New(`exit_plan_mode: "plan" must be Markdown starting with a "#" heading that names the plan`)
	}

	out, err := json.Marshal(ExitPlanModeResult{
		Kind:      ExitPlanModeResultKind,
		Title:     title,
		Markdown:  plan,
		Submitted: true,
	})
	if err != nil {
		return "", fmt.Errorf("exit_plan_mode: encode result: %w", err)
	}
	return string(out), nil
}

// planHeadingTitle returns the text of the first heading in plan and whether
// the plan starts with a heading. DSH's exit_plan_mode requires the same shape:
// a plan that begins with "# <title>".
func planHeadingTitle(plan string) (string, bool) {
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			return "", false
		}
		rest := strings.TrimLeft(line, "#")
		if len(rest) == 0 || (rest[0] != ' ' && rest[0] != '\t') {
			return "", false
		}
		title := strings.TrimSpace(rest)
		if title == "" {
			return "", false
		}
		return title, true
	}
	return "", false
}
