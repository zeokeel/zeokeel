package hosttools

// The task tools (M1): the only two places a model can move a board task through
// its lifecycle.
//
//	task_submit  executor → evidence → "waiting for the auditor"
//	task_review  auditor  → accept | reject → done | back to the pool
//
// Why these are product tools and not Rig tools: a submission needs the session
// ⇄ task binding (which session is working on what) and the durable lease layer
// (claim, review log, rework counter). Both are product state; a generic agent
// toolset cannot know them.
//
// Two design choices worth keeping:
//
//   - task_submit claims the task when nobody holds it. An automatically
//     dispatched run has already been given the task; making the model call a
//     separate "claim" tool first would add a step whose only failure mode is
//     "the model forgot", and the lease layer would then refuse the submission
//     for a reason that has nothing to do with the work.
//   - the reviewer is the auditor ROLE string, not a per-session identity. The
//     lease layer's rule is role-shaped (executor may not sign off); the tool
//     cannot widen it, and the review log records the role so an audit can be
//     checked without knowing which session typed it.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Tool names. Stable identifiers, like every other host tool name: the presets'
// allowlists, the system prompt and the tests spell them.
const (
	ToolTaskSubmit = "task_submit"
	ToolTaskReview = "task_review"
	// ToolTaskTools assigns the tool binding of one task (the lead's half of
	// "this task may use these tools"). The binding is enforced by the agent's
	// task gate for every session bound to the task, so the assignment is a
	// ceiling rather than a note.
	ToolTaskTools = "task_tools"
)

// taskLeaseService is the slice of contracts.TaskLeaseService the tools use.
// Declared here so a test can implement it in ten lines.
type taskLeaseService interface {
	Claim(ctx context.Context, taskID, owner string) (contracts.TaskLease, error)
	Lease(ctx context.Context, taskID string) (contracts.TaskLease, bool, error)
	Submit(ctx context.Context, taskID, owner string, evidence []contracts.Evidence) (contracts.TaskLease, error)
	Accept(ctx context.Context, taskID, reviewer, note string) (contracts.TaskLease, error)
	Reject(ctx context.Context, taskID, reviewer, reason string) (contracts.TaskLease, error)
	SetTools(ctx context.Context, taskID string, tools []string) (contracts.TaskTools, error)
}

// taskBinder is the read half of contracts.SessionTaskBinder.
type taskBinder interface {
	SessionTask(sessionID string) (string, error)
}

// taskDeps resolves the two services lazily. The host-tools module activates
// before the lease layer in some assemblies and after it in others; resolving at
// call time keeps the tool catalog identical either way, and a product without a
// board gets a readable tool error instead of a failed activation.
type taskDeps struct {
	lease  func() (taskLeaseService, bool)
	binder func() (taskBinder, bool)
}

// newTaskTools builds the task tools over deps.
func newTaskTools(deps taskDeps) []rig.Tool {
	return []rig.Tool{
		&taskSubmitTool{deps: deps},
		&taskReviewTool{deps: deps},
		&taskToolsTool{deps: deps},
	}
}

// resolve returns the lease service and the task this call's session is bound
// to, or a model-facing error result.
func (d taskDeps) resolve(ctx context.Context, toolName string) (taskLeaseService, string, *rig.ToolResult) {
	sessionID := contracts.AgentSessionFromContext(ctx)
	if strings.TrimSpace(sessionID) == "" {
		result := errorResult("%s: no session context (this tool only runs inside an agent run)", toolName)
		return nil, "", &result
	}
	if d.lease == nil {
		result := errorResult("%s: this product has no task board", toolName)
		return nil, "", &result
	}
	lease, ok := d.lease()
	if !ok || lease == nil {
		result := errorResult("%s: this product has no task board", toolName)
		return nil, "", &result
	}
	if d.binder == nil {
		result := errorResult("%s: this product cannot resolve session task bindings", toolName)
		return nil, "", &result
	}
	binder, ok := d.binder()
	if !ok || binder == nil {
		result := errorResult("%s: this product cannot resolve session task bindings", toolName)
		return nil, "", &result
	}
	taskID, err := binder.SessionTask(sessionID)
	if err != nil {
		result := errorResult("%s: read the session's task: %v", toolName, err)
		return nil, "", &result
	}
	if strings.TrimSpace(taskID) == "" {
		result := errorResult("%s: this session is not bound to a task", toolName)
		return nil, "", &result
	}
	return lease, taskID, nil
}

// --- task_submit ------------------------------------------------------------

type evidenceArg struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Note string `json:"note"`
}

type taskSubmitTool struct{ deps taskDeps }

func (t *taskSubmitTool) Name() string { return ToolTaskSubmit }

func (t *taskSubmitTool) Description() string {
	return "Submit the task this session is working on, with evidence. " +
		"Evidence is required: what proves the work is done (command output, test names, file paths, links). " +
		"Claiming is implicit — if nobody holds the task, this call claims it for the executor. " +
		"After a successful submission the task waits for the auditor; do not claim it again."
}

func (t *taskSubmitTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"evidence": map[string]any{
			"type":        "array",
			"description": "One or more proofs. Each item: kind (file|link|output|screenshot), ref, optional note.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"kind": stringProperty("Evidence kind: file, link, output or screenshot."),
					"ref":  stringProperty("Path, URL or command output reference."),
					"note": stringProperty("One line of context (optional)."),
				},
				"required": []any{"kind", "ref"},
			},
		},
	}, "evidence")
}

func (t *taskSubmitTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		Evidence []evidenceArg `json:"evidence"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return rig.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return rig.ToolResult{}, err
	}
	if len(args.Evidence) == 0 {
		return errorResult("task_submit: at least one evidence item is required (kind + ref)"), nil
	}
	lease, taskID, problem := t.deps.resolve(ctx, ToolTaskSubmit)
	if problem != nil {
		return *problem, nil
	}
	// Claim when nobody holds it; a task already held by the executor (a
	// previous submission that was rejected) moves straight to submitting.
	current, held, err := lease.Lease(ctx, taskID)
	if err != nil {
		return errorResult("task_submit: read task %s: %v", taskID, err), nil
	}
	if held && !strings.EqualFold(strings.TrimSpace(current.Owner), contracts.PresetExecutorID) {
		return errorResult("task_submit: task %s is held by %q; ask the lead to reassign it", taskID, current.Owner), nil
	}
	if !held {
		if _, err := lease.Claim(ctx, taskID, contracts.PresetExecutorID); err != nil && !errors.Is(err, contracts.ErrLeaseHeld) {
			return errorResult("task_submit: claim task %s: %v", taskID, err), nil
		}
	}
	evidence := make([]contracts.Evidence, 0, len(args.Evidence))
	for _, item := range args.Evidence {
		evidence = append(evidence, contracts.Evidence{
			Kind: strings.TrimSpace(item.Kind),
			Ref:  strings.TrimSpace(item.Ref),
			Note: strings.TrimSpace(item.Note),
		})
	}
	if _, err := lease.Submit(ctx, taskID, contracts.PresetExecutorID, evidence); err != nil {
		return errorResult("task_submit: %v", err), nil
	}
	return rig.ToolResult{Content: fmt.Sprintf(
		"submitted task %s with %d evidence item(s); it is now waiting for the auditor", taskID, len(evidence))}, nil
}

// --- task_review ------------------------------------------------------------

type taskReviewTool struct{ deps taskDeps }

func (t *taskReviewTool) Name() string { return ToolTaskReview }

func (t *taskReviewTool) Description() string {
	return "Decide the review of the task this session is auditing: accept it (the work is done) " +
		"or reject it (the executor must try again). Rejecting requires a note saying what is wrong and how to reproduce it. " +
		"Only the auditor role may call this; the executor cannot sign off its own work."
}

func (t *taskReviewTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"decision": map[string]any{
			"type":        "string",
			"description": "accept or reject.",
			"enum":        []any{"accept", "reject"},
		},
		"note": stringProperty("Why: what was verified, or what is wrong and how to reproduce it. Required for reject."),
	}, "decision", "note")
}

func (t *taskReviewTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return rig.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return rig.ToolResult{}, err
	}
	decision := strings.ToLower(strings.TrimSpace(args.Decision))
	if decision != "accept" && decision != "reject" {
		return errorResult("task_review: decision must be accept or reject"), nil
	}
	note := strings.TrimSpace(args.Note)
	if decision == "reject" && note == "" {
		return errorResult("task_review: a rejection needs a note (what is wrong and how to reproduce it)"), nil
	}
	lease, taskID, problem := t.deps.resolve(ctx, ToolTaskReview)
	if problem != nil {
		return *problem, nil
	}
	if decision == "accept" {
		if _, err := lease.Accept(ctx, taskID, contracts.PresetAuditorID, note); err != nil {
			return errorResult("task_review: accept task %s: %v", taskID, err), nil
		}
		return rig.ToolResult{Content: fmt.Sprintf("accepted task %s; the task is done", taskID)}, nil
	}
	if _, err := lease.Reject(ctx, taskID, contracts.PresetAuditorID, note); err != nil {
		return errorResult("task_review: reject task %s: %v", taskID, err), nil
	}
	return rig.ToolResult{Content: fmt.Sprintf(
		"rejected task %s; it goes back to the executor with the note (or to the human if the rework ceiling is reached)", taskID)}, nil
}

// --- task_tools -------------------------------------------------------------

type taskToolsTool struct{ deps taskDeps }

func (t *taskToolsTool) Name() string { return ToolTaskTools }

func (t *taskToolsTool) Description() string {
	return "Assign the tools one task may use. The executor working on that task can only call the tools named here — " +
		"the binding is enforced by the agent gate, not by advice. Use it when you dispatch work: name exactly the tools the " +
		"job needs, and nothing else. An empty list is refused: un-binding is a separate, deliberate action."
}

func (t *taskToolsTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"taskId": stringProperty("Task to bind. Omit to bind the task this session is working on."),
		"tools": map[string]any{
			"type":        "array",
			"description": "Tool names the task may use (e.g. read_file, apply_patch, bash).",
			"items":       map[string]any{"type": "string"},
		},
	}, "tools")
}

func (t *taskToolsTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		TaskID string   `json:"taskId"`
		Tools  []string `json:"tools"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return rig.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return rig.ToolResult{}, err
	}
	if len(args.Tools) == 0 {
		// Un-binding is "no policy", which is wider than what the lead said.
		// Clearing a binding is an explicit, separate decision (tasks.tools.set
		// with an empty list from the UI), never a side effect of an empty call.
		return errorResult("task_tools: at least one tool name is required; clearing a binding is a separate action"), nil
	}
	if t.deps.lease == nil {
		return errorResult("task_tools: this product has no task board"), nil
	}
	lease, ok := t.deps.lease()
	if !ok || lease == nil {
		return errorResult("task_tools: this product has no task board"), nil
	}
	taskID := strings.TrimSpace(args.TaskID)
	if taskID == "" {
		// No explicit target: use the session's binding when there is one.
		sessionID := strings.TrimSpace(contracts.AgentSessionFromContext(ctx))
		if sessionID == "" || t.deps.binder == nil {
			return errorResult("task_tools: taskId is required (this session has no bound task)"), nil
		}
		binder, ok := t.deps.binder()
		if !ok || binder == nil {
			return errorResult("task_tools: taskId is required (this session has no bound task)"), nil
		}
		bound, err := binder.SessionTask(sessionID)
		if err != nil || strings.TrimSpace(bound) == "" {
			return errorResult("task_tools: taskId is required (this session has no bound task)"), nil
		}
		taskID = strings.TrimSpace(bound)
	}
	binding, err := lease.SetTools(ctx, taskID, args.Tools)
	if err != nil {
		return errorResult("task_tools: bind task %s: %v", taskID, err), nil
	}
	return rig.ToolResult{Content: fmt.Sprintf(
		"bound task %s to %d tool(s): %s", taskID, len(binding.Tools), strings.Join(binding.Tools, ", "))}, nil
}
