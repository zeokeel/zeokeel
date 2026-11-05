package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const (
	// DefaultWorkflowMaxSteps caps how many steps one workflow call may run.
	DefaultWorkflowMaxSteps = 8

	workflowKind = "workflow"

	// WorkflowCompleted is the overall status of a workflow whose every step
	// completed.
	WorkflowCompleted = "completed"
	// WorkflowFailed is the overall status of a workflow with a failed step.
	WorkflowFailed = "failed"
	// WorkflowCancelled is the overall status of a workflow that stopped
	// because its caller was cancelled (for example the user's Stop). The
	// steps that already ran are kept; the in-flight and remaining steps are
	// reported as cancelled/skipped instead of surfacing a bare context error.
	WorkflowCancelled = "cancelled"
)

// Workflow step statuses.
const (
	// WorkflowStepCompleted is a step whose subagent finished.
	WorkflowStepCompleted = "completed"
	// WorkflowStepFailed is a step whose subagent failed.
	WorkflowStepFailed = "failed"
	// WorkflowStepSkipped is a step that never ran because an earlier one
	// failed or the workflow was cancelled.
	WorkflowStepSkipped = "skipped"
	// WorkflowStepCancelled is the step whose child agent was stopped by the
	// caller's cancellation.
	WorkflowStepCancelled = "cancelled"
)

// WorkflowStepResult is one step's outcome.
type WorkflowStepResult struct {
	Index  int    `json:"index"`
	Step   string `json:"step"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// WorkflowResult is the JSON document WorkflowTool returns. Steps are in the
// order they were requested; a failure stops the run and marks the remaining
// steps skipped.
type WorkflowResult struct {
	Kind    string               `json:"kind"`
	Status  string               `json:"status"`
	Steps   []WorkflowStepResult `json:"steps"`
	Message string               `json:"message,omitempty"`
}

// workflowDescription documents the tool and its JSON argument schema.
const workflowDescription = `Run a fixed list of subagent tasks in order.

Call it with a JSON object:
  {"steps":["Read the migration files","Propose the schema changes","Write the summary"]}
  {"steps":["Run the tests","Explain each failure"],"agent":"verifier"}

"steps" is required: an ordered list of self-contained tasks, at most 8 by
default. Each step runs as its own subagent task, sequentially; a failing step
stops the run and the remaining steps are reported as skipped. "agent" is an
optional child agent name applied to every step.

The tool returns {"kind":"workflow","status":"completed|failed|cancelled","steps":[
{"index":0,"step":...,"status":"completed|failed|skipped|cancelled","output":...}]}
so you can see exactly which step failed and why. A cancelled run keeps the
steps that already completed, marks the in-flight one cancelled and the
remaining ones skipped, and reports status "cancelled". The depth cap is the
same as the subagent tool.`

// WorkflowConfig configures a WorkflowTool.
type WorkflowConfig struct {
	// Runner executes each step. It is required at call time; runtime.New
	// auto-binds itself when the tool is registered.
	Runner runtime.SubagentRunner
	// MaxSteps caps len(steps). Zero means DefaultWorkflowMaxSteps.
	MaxSteps int
	// MaxDepth caps nesting. Zero means DefaultSubagentMaxDepth.
	MaxDepth int
}

// WorkflowTool is the "workflow" tool. All state lives in this instance,
// guarded by mu.
type WorkflowTool struct {
	mu       sync.Mutex
	runner   runtime.SubagentRunner
	maxSteps int
	maxDepth int
	runs     int
}

// NewWorkflowTool returns a workflow tool. When cfg.Runner is nil the tool
// reports a clear error until a runner is bound.
func NewWorkflowTool(cfg WorkflowConfig) *WorkflowTool {
	return &WorkflowTool{
		runner:   cfg.Runner,
		maxSteps: cfg.MaxSteps,
		maxDepth: cfg.MaxDepth,
	}
}

// Name implements runtime.Tool.
func (t *WorkflowTool) Name() string { return "workflow" }

// Description implements runtime.Tool.
func (t *WorkflowTool) Description() string { return workflowDescription }

// SetSubagentRunner implements runtime.SubagentRunnerBinder.
func (t *WorkflowTool) SetSubagentRunner(runner runtime.SubagentRunner) {
	t.mu.Lock()
	t.runner = runner
	t.mu.Unlock()
}

// Runs reports how many workflows this tool executed. It is used by tests.
func (t *WorkflowTool) Runs() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs
}

// workflowArgs is the decoded workflow argument object.
type workflowArgs struct {
	Steps []string `json:"steps"`
	Agent string   `json:"agent,omitempty"`
}

// Invoke implements runtime.Tool.
func (t *WorkflowTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var in workflowArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("workflow: invalid arguments: %w", err)
		}
	}
	steps, err := normalizeWorkflowSteps(in.Steps)
	if err != nil {
		return "", err
	}

	t.mu.Lock()
	runner := t.runner
	maxSteps := t.maxSteps
	maxDepth := t.maxDepth
	t.mu.Unlock()
	if runner == nil {
		return "", errors.New("workflow: no agent runner configured")
	}
	if maxSteps <= 0 {
		maxSteps = DefaultWorkflowMaxSteps
	}
	if len(steps) > maxSteps {
		return "", fmt.Errorf("workflow: %d steps exceed the limit of %d", len(steps), maxSteps)
	}
	if maxDepth <= 0 {
		maxDepth = DefaultSubagentMaxDepth
	}
	depth := runtime.SubagentDepth(ctx) + 1
	if depth > maxDepth {
		return "", fmt.Errorf("workflow: depth limit %d reached", maxDepth)
	}

	agent := strings.TrimSpace(in.Agent)
	results := make([]WorkflowStepResult, 0, len(steps))
	status := WorkflowCompleted
	cancelMessage := ""
	for i, step := range steps {
		if ctx.Err() != nil {
			status = WorkflowCancelled
			cancelMessage = fmt.Sprintf("Workflow was cancelled before step %d started.", i+1)
			for j := i; j < len(steps); j++ {
				results = append(results, WorkflowStepResult{
					Index: j, Step: steps[j], Status: WorkflowStepSkipped,
				})
			}
			break
		}
		res := WorkflowStepResult{Index: i, Step: step, Status: WorkflowStepCompleted}
		result, runErr := runner.RunSubagent(ctx, runtime.SubagentRequest{
			Task:  step,
			Agent: agent,
			Depth: depth,
		})
		if ctx.Err() != nil {
			// The child agent was stopped by the caller's cancellation: keep
			// the step visible as cancelled and skip the rest instead of
			// reporting a failure the user did not cause.
			status = WorkflowCancelled
			cancelMessage = fmt.Sprintf("Workflow was cancelled during step %d; the child agent was stopped.", i+1)
			res.Status = WorkflowStepCancelled
			res.Error = "cancelled"
			results = append(results, res)
			for j := i + 1; j < len(steps); j++ {
				results = append(results, WorkflowStepResult{
					Index: j, Step: steps[j], Status: WorkflowStepSkipped,
				})
			}
			break
		}
		if runErr != nil {
			res.Status = WorkflowStepFailed
			res.Error = runErr.Error()
			status = WorkflowFailed
			results = append(results, res)
			for j := i + 1; j < len(steps); j++ {
				results = append(results, WorkflowStepResult{
					Index:  j,
					Step:   steps[j],
					Status: WorkflowStepSkipped,
				})
			}
			break
		}
		res.Output = result.Output
		results = append(results, res)
	}

	t.mu.Lock()
	t.runs++
	t.mu.Unlock()

	out, err := json.Marshal(WorkflowResult{
		Kind:    workflowKind,
		Status:  status,
		Steps:   results,
		Message: cancelMessage,
	})
	if err != nil {
		return "", fmt.Errorf("workflow: encode result: %w", err)
	}
	return string(out), nil
}

// normalizeWorkflowSteps trims the steps and rejects empty ones. An all-blank
// list is an error, not a no-op: a workflow that runs nothing is a mistake.
func normalizeWorkflowSteps(steps []string) ([]string, error) {
	if len(steps) == 0 {
		return nil, errors.New(`workflow: "steps" must contain at least one task`)
	}
	out := make([]string, 0, len(steps))
	for i, step := range steps {
		step = strings.TrimSpace(step)
		if step == "" {
			return nil, fmt.Errorf("workflow: step %d is empty", i+1)
		}
		out = append(out, step)
	}
	return out, nil
}
