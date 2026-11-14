package hosttools

// The model-facing host tools. Most of the catalog is the official Rig host-tool
// set (github.com/zeokeel/zeokeel-rig/tools/hosttools): ask_user, plan_review,
// exit_plan_mode and goal. present stays product-owned because ZeoKeel's present
// is a content artifact (title + body -> contracts.ArtifactPresenter), while
// Rig's present declares existing files for a FilePresenter; the two must not
// share a name.
//
// The product exposes exactly five host tools. The catalog below is a
// name-filtered view of the Rig catalog: Rig's DSH goal trio (get_goal,
// create_goal, update_goal), present_artifact and Rig's file present are
// deliberately not advertised. Tool names are hard-coded elsewhere (the agent
// system prompt, the plan-mode whitelist, UI tool cards and snapshot seeds), so
// they are stable identifiers, not an implementation detail.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	righosttools "github.com/zeokeel/zeokeel-rig/tools/hosttools"

	hostcaps "github.com/zeokeel/zeokeel/internal/hosttools"
)

// Model-facing host tool names. They are stable identifiers shared with the
// rest of the agent tool catalog; the Rig-provided names come from the Rig
// constants so the two can never drift.
const (
	ToolAskUser      = righosttools.ToolAskUser
	ToolPresent      = "present"
	ToolPlanReview   = righosttools.ToolPlanReview
	ToolExitPlanMode = righosttools.ToolExitPlanMode
	ToolGoal         = righosttools.ToolGoal
)

// rigHostToolNames are the Rig host tools this product registers, in Rig
// catalog order. Anything else Rig returns is filtered out.
var rigHostToolNames = []string{ToolAskUser, ToolPlanReview, ToolExitPlanMode, ToolGoal}

// planReviewToolNames are the tools whose state machine needs a live user
// decision; non-interactive products omit them.
var planReviewToolNames = []string{ToolPlanReview, ToolExitPlanMode}

// newTools builds the host-tool catalog bound to capabilities. Non-interactive
// (headless/ACP) products omit the plan-review pair: a plan can only leave
// "reviewing" through a user decision (exit_plan_mode approve/cancel), which a
// headless run can never obtain, so exposing plan_review there would make the
// model submit a plan and stall with the task silently unfinished. ask_user and
// the remaining tools keep their behavior; a missing capability becomes a
// readable model-facing tool error instead of failing activation.
func newTools(capabilities hostcaps.Host, interactive bool) []rig.Tool {
	wanted := make(map[string]bool, len(rigHostToolNames))
	for _, name := range rigHostToolNames {
		wanted[name] = true
	}
	if !interactive {
		for _, name := range planReviewToolNames {
			delete(wanted, name)
		}
	}
	tools := make([]rig.Tool, 0, len(rigHostToolNames))
	for _, tool := range righosttools.Tools(rigHostOptions(capabilities, interactive)) {
		if !wanted[tool.Name()] {
			continue
		}
		tools = append(tools, tool)
	}
	return append(tools, &presentTool{presenter: capabilities.Presenter})
}

// rigHostOptions maps the product's host capability seam onto the Rig host-tool
// options. hostcaps.Asker and hostcaps.GoalStore already have the Rig method
// sets, so they are passed straight through; hostcaps.PlanController has no
// context in its methods (the old hand-written tool checked cancellation before
// calling it), so Rig's context-carrying PlanController goes through an
// adapter. Root is left empty: none of the registered tools resolves workspace
// files (present is our own content-artifact tool, not Rig's file presenter).
func rigHostOptions(capabilities hostcaps.Host, interactive bool) righosttools.Options {
	options := righosttools.Options{
		Asker:       capabilities.Asker,
		Goal:        capabilities.Goals,
		Interactive: interactive,
	}
	if capabilities.Plan != nil {
		options.Plan = rigPlanController{plan: capabilities.Plan}
	}
	return options
}

// rigPlanController adapts the product PlanController seam to Rig's
// context-carrying PlanController.
type rigPlanController struct {
	plan hostcaps.PlanController
}

// State implements rig.PlanController.
func (c rigPlanController) State() string { return c.plan.State() }

// Review implements rig.PlanController.
func (c rigPlanController) Review(ctx context.Context, content string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	return c.plan.Review(content)
}

// Exit implements rig.PlanController.
func (c rigPlanController) Exit(ctx context.Context, action string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	return c.plan.Exit(action)
}

// Compile-time checks: the product capability interfaces are assignable to the
// Rig interfaces without wrappers (the Plan and file-presenter halves do need
// adapters and are therefore not asserted here).
var (
	_ rig.Asker     = hostcaps.Asker(nil)
	_ rig.GoalStore = hostcaps.GoalStore(nil)
)

// --- present ----------------------------------------------------------------

// presentTool presents a finished content artifact to the user. It is
// product-owned: the host records the artifact (contracts.ArtifactPresenter)
// and returns its id.
type presentTool struct {
	presenter hostcaps.Presenter
}

// Name implements rig.Tool.
func (t *presentTool) Name() string { return ToolPresent }

// Description implements rig.Tool.
func (t *presentTool) Description() string {
	return "Present a finished artifact (a report, plan, diff summary or any Markdown text) to the user. " +
		"Use it when the result is something the user should look at as a distinct deliverable rather than chat prose. " +
		"The host records the artifact and returns its id."
}

// Schema implements rig.Tool.
func (t *presentTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"title":   stringProperty("Short title of the artifact."),
		"content": stringProperty("Artifact body as Markdown or plain text."),
	}, "title", "content")
}

// Invoke implements rig.Tool.
func (t *presentTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return rig.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return rig.ToolResult{}, err
	}
	if strings.TrimSpace(args.Title) == "" {
		return errorResult("present: title is required"), nil
	}
	if t.presenter == nil {
		return errorResult("present: no artifact presenter is available in this product"), nil
	}
	artifactID, err := t.presenter.Present(ctx, strings.TrimSpace(args.Title), args.Content)
	if err != nil {
		if contextErr := contextError(ctx); contextErr != nil {
			return rig.ToolResult{}, contextErr
		}
		return errorResult("present: %v", err), nil
	}
	return rig.ToolResult{Content: fmt.Sprintf("presented artifact %s: %s", artifactID, strings.TrimSpace(args.Title))}, nil
}

// --- shared helpers ---------------------------------------------------------

// decodeArguments decodes one call's raw arguments; an empty argument string is
// the empty object.
func decodeArguments(call rig.ToolCall, target any) error {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid arguments JSON: %w", err)
	}
	return nil
}

// contextError returns the context's error, tolerating a nil context.
func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// errorResult is a model-facing tool failure.
func errorResult(format string, args ...any) rig.ToolResult {
	return rig.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}

// objectSchema builds the JSON Schema object advertised for one tool.
func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
