package hosttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	v1 "github.com/zeokeel/zeokeel-rig/contracts/v1"

	hostcaps "github.com/zeokeel/zeokeel/internal/hosttools"
)

// Model-facing host tool names. They are stable identifiers shared with the
// rest of the agent tool catalog.
const (
	ToolAskUser      = "ask_user"
	ToolPresent      = "present"
	ToolPlanReview   = "plan_review"
	ToolExitPlanMode = "exit_plan_mode"
	ToolGoal         = "goal"
	ToolTeam         = "team"
)

// newTools builds the host tools bound to capabilities. Every tool is always
// constructed (a stable tool catalog); a missing capability becomes a readable
// tool error instead of changing the catalog between products.
func newTools(capabilities hostcaps.Host) []v1.Tool {
	return []v1.Tool{
		&askUserTool{asker: capabilities.Asker},
		&presentTool{presenter: capabilities.Presenter},
		&planReviewTool{plan: capabilities.Plan},
		&exitPlanModeTool{plan: capabilities.Plan},
		&goalTool{goals: capabilities.Goals},
		&teamTool{team: capabilities.Team},
	}
}

// --- ask_user ---------------------------------------------------------------

type askUserTool struct {
	asker hostcaps.Asker
}

func (t *askUserTool) Name() string { return ToolAskUser }

func (t *askUserTool) Description() string {
	return "Ask the user a clarifying question and wait for their answer. " +
		"Use it when a decision belongs to the user (requirements, destructive actions, ambiguous scope). " +
		"Interactive sessions return the answer directly; sessions without a UI report that no interactive UI is available, " +
		"in which case ask the question in your final message instead."
}

func (t *askUserTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"question": stringProperty("The question to ask the user."),
		"options":  stringArrayProperty("Optional suggested answers."),
	}, "question")
}

func (t *askUserTool) Invoke(ctx context.Context, call v1.ToolCall) (v1.ToolResult, error) {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return v1.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return v1.ToolResult{}, err
	}
	if strings.TrimSpace(args.Question) == "" {
		return errorResult("ask_user: question is required"), nil
	}
	if t.asker == nil {
		return noInteractiveResult(), nil
	}
	answer, err := t.asker.Ask(ctx, strings.TrimSpace(args.Question), args.Options)
	if err != nil {
		if contextErr := contextError(ctx); contextErr != nil {
			return v1.ToolResult{}, contextErr
		}
		if errors.Is(err, hostcaps.ErrNoInteractive) {
			return noInteractiveResult(), nil
		}
		return errorResult("ask_user: %v", err), nil
	}
	return v1.ToolResult{Content: "user answered: " + answer}, nil
}

func noInteractiveResult() v1.ToolResult {
	return errorResult("ask_user: no interactive UI is available in this session; " +
		"put the question in your final message instead and wait for the user's reply")
}

// --- present ----------------------------------------------------------------

type presentTool struct {
	presenter hostcaps.Presenter
}

func (t *presentTool) Name() string { return ToolPresent }

func (t *presentTool) Description() string {
	return "Present a finished artifact (a report, plan, diff summary or any Markdown text) to the user. " +
		"Use it when the result is something the user should look at as a distinct deliverable rather than chat prose. " +
		"The host records the artifact and returns its id."
}

func (t *presentTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"title":   stringProperty("Short title of the artifact."),
		"content": stringProperty("Artifact body as Markdown or plain text."),
	}, "title", "content")
}

func (t *presentTool) Invoke(ctx context.Context, call v1.ToolCall) (v1.ToolResult, error) {
	var args struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return v1.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return v1.ToolResult{}, err
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
			return v1.ToolResult{}, contextErr
		}
		return errorResult("present: %v", err), nil
	}
	return v1.ToolResult{Content: fmt.Sprintf("presented artifact %s: %s", artifactID, strings.TrimSpace(args.Title))}, nil
}

// --- plan_review ------------------------------------------------------------

type planReviewTool struct {
	plan hostcaps.PlanController
}

func (t *planReviewTool) Name() string { return ToolPlanReview }

func (t *planReviewTool) Description() string {
	return "Submit a plan for the user's review before executing it. Markdown, starting with a heading that names the plan. " +
		"Reviewing is not approval: the user (or the host on their behalf) approves or cancels it through exit_plan_mode. " +
		"Use it for multi-step or irreversible work; skip it for trivial tasks."
}

func (t *planReviewTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"plan": stringProperty("The complete plan as Markdown, starting with a \"#\" heading that names it."),
	}, "plan")
}

func (t *planReviewTool) Invoke(ctx context.Context, call v1.ToolCall) (v1.ToolResult, error) {
	var args struct {
		Plan string `json:"plan"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return v1.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return v1.ToolResult{}, err
	}
	if strings.TrimSpace(args.Plan) == "" {
		return errorResult("plan_review: plan is required"), nil
	}
	if t.plan == nil {
		return errorResult("plan_review: no plan controller is available in this product"), nil
	}
	verdict, err := t.plan.Review(args.Plan)
	if err != nil {
		if contextErr := contextError(ctx); contextErr != nil {
			return v1.ToolResult{}, contextErr
		}
		return errorResult("plan_review: %v", err), nil
	}
	return v1.ToolResult{Content: fmt.Sprintf(
		"plan submitted for review (state: %s); use exit_plan_mode with approve, cancel or status", verdict)}, nil
}

// --- exit_plan_mode ---------------------------------------------------------

type exitPlanModeTool struct {
	plan hostcaps.PlanController
}

func (t *exitPlanModeTool) Name() string { return ToolExitPlanMode }

func (t *exitPlanModeTool) Description() string {
	return "Leave plan mode after a plan review: apply the user's decision. " +
		"action=approve leaves plan mode and starts executing the reviewed plan, action=cancel cancels the plan and stays in planning, " +
		"action=status reports the current plan state without changing it."
}

func (t *exitPlanModeTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"action": enumProperty("The decision to apply.", "approve", "cancel", "status"),
	}, "action")
}

func (t *exitPlanModeTool) Invoke(ctx context.Context, call v1.ToolCall) (v1.ToolResult, error) {
	var args struct {
		Action string `json:"action"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return v1.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return v1.ToolResult{}, err
	}
	if strings.TrimSpace(args.Action) == "" {
		return errorResult("exit_plan_mode: action is required (approve, cancel or status)"), nil
	}
	if t.plan == nil {
		return errorResult("exit_plan_mode: no plan controller is available in this product"), nil
	}
	state, err := t.plan.Exit(args.Action)
	if err != nil {
		if contextErr := contextError(ctx); contextErr != nil {
			return v1.ToolResult{}, contextErr
		}
		return errorResult("exit_plan_mode: %v", err), nil
	}
	return v1.ToolResult{Content: "plan mode: " + state}, nil
}

// --- goal -------------------------------------------------------------------

type goalTool struct {
	goals hostcaps.GoalStore
}

func (t *goalTool) Name() string { return ToolGoal }

func (t *goalTool) Description() string {
	return "Set, inspect or clear the session goal (the long-running objective shown above the conversation). " +
		"action=set replaces the goal text and returns its id, action=get (or status) reports the goal, action=clear removes it. " +
		"Use it to keep one explicit objective across many turns."
}

func (t *goalTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"action": enumProperty("The goal operation.", "set", "get", "clear", "status"),
		"text":   stringProperty("New goal text (required for action=set)."),
	}, "action")
}

func (t *goalTool) Invoke(ctx context.Context, call v1.ToolCall) (v1.ToolResult, error) {
	var args struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return v1.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return v1.ToolResult{}, err
	}
	if t.goals == nil {
		return errorResult("goal: no goal store is available in this product"), nil
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	switch action {
	case "set":
		text := strings.TrimSpace(args.Text)
		if text == "" {
			return errorResult("goal: text is required for action=set"), nil
		}
		id, err := t.goals.Set(text)
		if err != nil {
			if contextErr := contextError(ctx); contextErr != nil {
				return v1.ToolResult{}, contextErr
			}
			return errorResult("goal: %v", err), nil
		}
		return v1.ToolResult{Content: fmt.Sprintf("goal set (%s): %s", id, text)}, nil
	case "get", "status":
		id, text, status, ok := t.goals.Get()
		if !ok {
			return v1.ToolResult{Content: "no goal is set"}, nil
		}
		return v1.ToolResult{Content: fmt.Sprintf("goal %s [%s]: %s", id, status, text)}, nil
	case "clear":
		if t.goals.Clear() {
			return v1.ToolResult{Content: "goal cleared"}, nil
		}
		return v1.ToolResult{Content: "no goal is set"}, nil
	default:
		return errorResult("goal: action must be one of set, get, clear, status"), nil
	}
}

// --- team -------------------------------------------------------------------

type teamTool struct {
	team hostcaps.TeamManager
}

func (t *teamTool) Name() string { return ToolTeam }

func (t *teamTool) Description() string {
	return "Manage the session's team members: action=spawn starts a named member, action=list reports them with their status, " +
		"action=interrupt stops a member's current work, action=close removes it. " +
		"Spawned members run no real subagent in this product build (in-memory manager); " +
		"a product that wires a real TeamManager starts them."
}

func (t *teamTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"action": enumProperty("The team operation.", "spawn", "list", "close", "interrupt"),
		"name":   stringProperty("Member name (required for action=spawn)."),
		"id":     stringProperty("Member id (required for action=close or interrupt)."),
	}, "action")
}

func (t *teamTool) Invoke(ctx context.Context, call v1.ToolCall) (v1.ToolResult, error) {
	var args struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		ID     string `json:"id"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return v1.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return v1.ToolResult{}, err
	}
	if t.team == nil {
		return errorResult("team: no team manager is available in this product"), nil
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	switch action {
	case "spawn":
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return errorResult("team: name is required for action=spawn"), nil
		}
		id, err := t.team.Spawn(name)
		if err != nil {
			if contextErr := contextError(ctx); contextErr != nil {
				return v1.ToolResult{}, contextErr
			}
			return errorResult("team: %v", err), nil
		}
		return v1.ToolResult{Content: fmt.Sprintf("spawned team member %s (%s)", name, id)}, nil
	case "list":
		members := t.team.List()
		if len(members) == 0 {
			return v1.ToolResult{Content: "no team members"}, nil
		}
		lines := make([]string, 0, len(members))
		for _, member := range members {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s", member.ID, member.Name, member.Status))
		}
		return v1.ToolResult{Content: strings.Join(lines, "\n")}, nil
	case "close":
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return errorResult("team: id is required for action=close"), nil
		}
		if err := t.team.Close(id); err != nil {
			return errorResult("team: %v", err), nil
		}
		return v1.ToolResult{Content: "closed team member " + id}, nil
	case "interrupt":
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return errorResult("team: id is required for action=interrupt"), nil
		}
		if err := t.team.Interrupt(id); err != nil {
			return errorResult("team: %v", err), nil
		}
		return v1.ToolResult{Content: "interrupted team member " + id}, nil
	default:
		return errorResult("team: action must be one of spawn, list, close, interrupt"), nil
	}
}

// --- shared helpers ---------------------------------------------------------

// decodeArguments decodes one call's raw arguments; an empty argument string is
// the empty object.
func decodeArguments(call v1.ToolCall, target any) error {
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
func errorResult(format string, args ...any) v1.ToolResult {
	return v1.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
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

func stringArrayProperty(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func enumProperty(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
