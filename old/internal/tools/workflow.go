package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
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

// WorkflowStepResult is one step's outcome. Group/Item/Stage are set only for
// the expanded forms: a parallel entry carries Group "parallel[<n>]", and a
// pipeline entry carries Group "pipeline[<n>]", the item text and the 1-based
// stage number.
type WorkflowStepResult struct {
	Index  int    `json:"index"`
	Step   string `json:"step"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	Group  string `json:"group,omitempty"`
	Item   string `json:"item,omitempty"`
	Stage  int    `json:"stage,omitempty"`
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
const workflowDescription = `Run a restricted workflow over subagent tasks.

Call it with a JSON object:
  {"steps":["Read the migration files","Propose the schema changes","Write the summary"]}
  {"steps":[{"parallel":["Review the parser","Review the lexer"]},"Combine the reviews"]}
  {"steps":[{"pipeline":{"items":["src/a.go","src/b.go"],"stages":["Review {{item}}","Summarize the review of {{item}}"]}}]}
  {"steps":["Run the tests","Explain each failure"],"agent":"verifier"}

"steps" is required and accepts plain task strings and two grouped forms:
  - {"parallel":[task,...]} starts every listed task at the same time and
    collects the outputs. A failed task does not cancel its siblings, and the
    workflow continues with the next entry; the overall status is failed if any
    task failed.
  - {"pipeline":{"items":[item,...],"stages":[stage,...]}} runs each item
    through every stage in order, items concurrently. A stage prompt may use
    {{item}} and {{prev}} placeholders; without {{prev}} the previous output is
    appended as context. A failed stage stops the remaining stages of that item
    only; the other items carry on.

Duplicate steps and blank tasks are rejected. "agent" is an optional child agent
name applied to every step. The workflow runs at most 8 task executions by
default (a parallel group counts once per task, a pipeline once per item×stage).

The tool returns {"kind":"workflow","status":"completed|failed|cancelled","steps":[
{"index":0,"step":...,"status":"completed|failed|skipped|cancelled","output":...,
"group":"parallel[0]","item":...,"stage":1}]} so you can see exactly which task
failed and why. A cancelled run keeps the steps that already completed, marks
the in-flight one cancelled and the remaining ones skipped.

This is a deliberately restricted pure-Go subset of a general workflow engine:
unlike DSH's JavaScript orchestration scripts there is no arbitrary code, loops,
per-stage schema or nested workflow run; parallel/pipeline plus placeholders are
the supported composition surface.`

// WorkflowConfig configures a WorkflowTool.
type WorkflowConfig struct {
	// Runner executes each step. It is required at call time; runtime.New
	// auto-binds itself when the tool is registered.
	Runner runtime.SubagentRunner
	// MaxSteps caps the number of task executions in one workflow call. Zero
	// means DefaultWorkflowMaxSteps.
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
	Steps []workflowSpec `json:"steps"`
	Agent string         `json:"agent,omitempty"`
}

// workflowSpec is one top-level workflow entry: a plain task, a parallel group
// or a pipeline. Exactly one of Task/Parallel/Pipeline is set.
type workflowSpec struct {
	Task     string
	Parallel []string
	Pipeline *workflowPipeline
}

// workflowPipeline is the {items, stages} pipeline form.
type workflowPipeline struct {
	Items  []string `json:"items"`
	Stages []string `json:"stages"`
}

// UnmarshalJSON accepts either a plain task string or one grouped object, so
// the old {"steps":["a","b"]} form keeps working unchanged.
func (w *workflowSpec) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return errors.New("workflow: step is empty")
	}
	if strings.HasPrefix(trimmed, `"`) {
		var task string
		if err := json.Unmarshal(data, &task); err != nil {
			return err
		}
		w.Task = task
		return nil
	}
	var raw struct {
		Task     string            `json:"task"`
		Parallel []string          `json:"parallel"`
		Pipeline *workflowPipeline `json:"pipeline"`
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("workflow: step must be a task string, {\"parallel\":[...]} or {\"pipeline\":{...}}: %w", err)
	}
	set := 0
	if raw.Task != "" {
		set++
	}
	if raw.Parallel != nil {
		set++
	}
	if raw.Pipeline != nil {
		set++
	}
	if set != 1 {
		return errors.New(`workflow: each step must set exactly one of "task", "parallel" or "pipeline"`)
	}
	w.Task = raw.Task
	w.Parallel = raw.Parallel
	w.Pipeline = raw.Pipeline
	return nil
}

// label renders the spec's human text for envelope entries.
func (w workflowSpec) label() string {
	switch {
	case w.Pipeline != nil:
		return fmt.Sprintf("pipeline(%d items x %d stages)", len(w.Pipeline.Items), len(w.Pipeline.Stages))
	case w.Parallel != nil:
		return fmt.Sprintf("parallel(%d tasks)", len(w.Parallel))
	default:
		return w.Task
	}
}

// taskCount is how many subagent executions the spec expands to.
func (w workflowSpec) taskCount() int {
	switch {
	case w.Pipeline != nil:
		return len(w.Pipeline.Items) * len(w.Pipeline.Stages)
	case w.Parallel != nil:
		return len(w.Parallel)
	default:
		return 1
	}
}

// normalize runs basic validation on one spec and returns it normalized.
func (w workflowSpec) normalize(index int) (workflowSpec, error) {
	if w.Pipeline != nil {
		if len(w.Pipeline.Items) == 0 {
			return w, fmt.Errorf("workflow: step %d pipeline has no items", index+1)
		}
		if len(w.Pipeline.Stages) == 0 {
			return w, fmt.Errorf("workflow: step %d pipeline has no stages", index+1)
		}
		items := make([]string, 0, len(w.Pipeline.Items))
		for i, item := range w.Pipeline.Items {
			item = strings.TrimSpace(item)
			if item == "" {
				return w, fmt.Errorf("workflow: step %d pipeline item %d is empty", index+1, i+1)
			}
			items = append(items, item)
		}
		stages := make([]string, 0, len(w.Pipeline.Stages))
		for i, stage := range w.Pipeline.Stages {
			stage = strings.TrimSpace(stage)
			if stage == "" {
				return w, fmt.Errorf("workflow: step %d pipeline stage %d is empty", index+1, i+1)
			}
			stages = append(stages, stage)
		}
		w.Pipeline = &workflowPipeline{Items: items, Stages: stages}
		return w, nil
	}
	if w.Parallel != nil {
		if len(w.Parallel) == 0 {
			return w, fmt.Errorf("workflow: step %d parallel group has no tasks", index+1)
		}
		tasks := make([]string, 0, len(w.Parallel))
		for i, task := range w.Parallel {
			task = strings.TrimSpace(task)
			if task == "" {
				return w, fmt.Errorf("workflow: step %d parallel task %d is empty", index+1, i+1)
			}
			tasks = append(tasks, task)
		}
		w.Parallel = tasks
		return w, nil
	}
	task := strings.TrimSpace(w.Task)
	if task == "" {
		return w, fmt.Errorf("workflow: step %d is empty", index+1)
	}
	w.Task = task
	return w, nil
}

// pipelinePrompt renders one pipeline stage for one item. {{item}} and {{prev}}
// are replaced literally; without a {{prev}} placeholder the previous output is
// appended, so the simple form still passes context on.
func pipelinePrompt(stage, item, prev string) string {
	prompt := strings.ReplaceAll(stage, "{{item}}", item)
	usesPrev := strings.Contains(stage, "{{prev}}")
	prompt = strings.ReplaceAll(prompt, "{{prev}}", prev)
	if prev != "" && !usesPrev {
		prompt += "\n\nPrevious stage output:\n" + prev
	}
	return prompt
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
	if len(in.Steps) == 0 {
		return "", errors.New(`workflow: "steps" must contain at least one task`)
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
	if maxDepth <= 0 {
		maxDepth = DefaultSubagentMaxDepth
	}
	depth := runtime.SubagentDepth(ctx) + 1
	if depth > maxDepth {
		return "", fmt.Errorf("workflow: depth limit %d reached", maxDepth)
	}

	specs := make([]workflowSpec, 0, len(in.Steps))
	total := 0
	for i, spec := range in.Steps {
		normalized, err := spec.normalize(i)
		if err != nil {
			return "", err
		}
		total += normalized.taskCount()
		if total > maxSteps {
			return "", fmt.Errorf("workflow: %d task executions exceed the limit of %d", total, maxSteps)
		}
		specs = append(specs, normalized)
	}

	agent := strings.TrimSpace(in.Agent)
	results := make([]WorkflowStepResult, 0, total)
	status := WorkflowCompleted
	cancelMessage := ""
	next := 0

	runTask := func(index int, prompt, group, item string, stage int) (WorkflowStepResult, error) {
		res := WorkflowStepResult{Index: index, Step: prompt, Group: group, Item: item, Stage: stage}
		result, runErr := runner.RunSubagent(ctx, runtime.SubagentRequest{
			Task:  prompt,
			Agent: agent,
			Depth: depth,
		})
		if runErr != nil {
			if ctx.Err() != nil {
				// The child was stopped by the caller's cancellation, not by a
				// task failure: keep it visible as cancelled.
				res.Status = WorkflowStepCancelled
				res.Error = "cancelled"
				return res, runErr
			}
			res.Status = WorkflowStepFailed
			res.Error = runErr.Error()
			return res, runErr
		}
		res.Status = WorkflowStepCompleted
		res.Output = result.Output
		return res, nil
	}

specLoop:
	for specIndex, spec := range specs {
		if ctx.Err() != nil {
			status = WorkflowCancelled
			cancelMessage = fmt.Sprintf("Workflow was cancelled before step %d started.", specIndex+1)
			for j := specIndex; j < len(specs); j++ {
				results = append(results, WorkflowStepResult{
					Index: next, Step: specs[j].label(), Status: WorkflowStepSkipped,
				})
				next++
			}
			break specLoop
		}
		switch {
		case spec.Pipeline != nil:
			p := spec.Pipeline
			group := fmt.Sprintf("pipeline[%d]", specIndex)
			stageResults := make([]WorkflowStepResult, len(p.Items)*len(p.Stages))
			var wg sync.WaitGroup
			for itemIndex, item := range p.Items {
				itemIndex, item := itemIndex, item
				wg.Add(1)
				go func() {
					defer wg.Done()
					prev := ""
					for stageIndex, stage := range p.Stages {
						resIndex := itemIndex*len(p.Stages) + stageIndex
						if ctx.Err() != nil {
							stageResults[resIndex] = WorkflowStepResult{
								Index: next + resIndex, Step: pipelinePrompt(stage, item, prev),
								Status: WorkflowStepCancelled, Group: group, Item: item, Stage: stageIndex + 1,
							}
							return
						}
						res, err := runTask(next+resIndex, pipelinePrompt(stage, item, prev), group, item, stageIndex+1)
						stageResults[resIndex] = res
						if err != nil {
							// Fail this item: mark the remaining stages skipped.
							for k := stageIndex + 1; k < len(p.Stages); k++ {
								idx := itemIndex*len(p.Stages) + k
								stageResults[idx] = WorkflowStepResult{
									Index: next + idx, Step: p.Stages[k], Status: WorkflowStepSkipped,
									Group: group, Item: item, Stage: k + 1,
								}
							}
							return
						}
						prev = res.Output
					}
				}()
			}
			wg.Wait()
			// Merge in deterministic item-major order and detect failures.
			for i := range stageResults {
				results = append(results, stageResults[i])
				if stageResults[i].Status == WorkflowStepFailed || stageResults[i].Status == WorkflowStepCancelled {
					status = WorkflowFailed
				}
				if stageResults[i].Status == WorkflowStepCancelled && ctx.Err() != nil {
					status = WorkflowCancelled
					cancelMessage = fmt.Sprintf("Workflow was cancelled during step %d; the child agent was stopped.", specIndex+1)
				}
			}
			next += len(stageResults)
			if status == WorkflowCancelled {
				for j := specIndex + 1; j < len(specs); j++ {
					results = append(results, WorkflowStepResult{
						Index: next, Step: specs[j].label(), Status: WorkflowStepSkipped,
					})
					next++
				}
				break specLoop
			}
		case spec.Parallel != nil:
			group := fmt.Sprintf("parallel[%d]", specIndex)
			parResults := make([]WorkflowStepResult, len(spec.Parallel))
			var wg sync.WaitGroup
			for taskIndex, task := range spec.Parallel {
				taskIndex, task := taskIndex, task
				wg.Add(1)
				go func() {
					defer wg.Done()
					if ctx.Err() != nil {
						parResults[taskIndex] = WorkflowStepResult{
							Index: next + taskIndex, Step: task, Status: WorkflowStepCancelled, Group: group,
						}
						return
					}
					res, _ := runTask(next+taskIndex, task, group, "", 0)
					parResults[taskIndex] = res
				}()
			}
			wg.Wait()
			for i := range parResults {
				results = append(results, parResults[i])
				if parResults[i].Status == WorkflowStepFailed {
					status = WorkflowFailed
				}
				if parResults[i].Status == WorkflowStepCancelled && ctx.Err() != nil {
					status = WorkflowCancelled
					cancelMessage = fmt.Sprintf("Workflow was cancelled during step %d; the child agent was stopped.", specIndex+1)
				}
			}
			next += len(parResults)
			if status == WorkflowCancelled {
				for j := specIndex + 1; j < len(specs); j++ {
					results = append(results, WorkflowStepResult{
						Index: next, Step: specs[j].label(), Status: WorkflowStepSkipped,
					})
					next++
				}
				break specLoop
			}
		default:
			res, err := runTask(next, spec.Task, "", "", 0)
			results = append(results, res)
			next++
			if res.Status == WorkflowStepCancelled {
				status = WorkflowCancelled
				cancelMessage = fmt.Sprintf("Workflow was cancelled during step %d; the child agent was stopped.", specIndex+1)
				for j := specIndex + 1; j < len(specs); j++ {
					results = append(results, WorkflowStepResult{
						Index: next, Step: specs[j].label(), Status: WorkflowStepSkipped,
					})
					next++
				}
				break specLoop
			}
			if err != nil {
				status = WorkflowFailed
				for j := specIndex + 1; j < len(specs); j++ {
					results = append(results, WorkflowStepResult{
						Index: next, Step: specs[j].label(), Status: WorkflowStepSkipped,
					})
					next++
				}
				break specLoop
			}
		}
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
