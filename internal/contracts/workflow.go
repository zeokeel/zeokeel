package contracts

// The stage-based workflow definition (M2): the shape a lead authors once and
// the runner executes many times.
//
// Why validation lives here and not in the module: an invalid workflow is a
// *request* problem (a person or a model wrote it), and both the HTTP surface
// and the runner must answer it with the same words. A cycle that only the
// runner notices would be discovered after a run had already started.
//
// What this file deliberately does NOT decide: whether a stage's role exists.
// Roles are agent presets (user-editable settings); the definition layer only
// checks that the id is well-formed, and the runner reports an unknown preset as
// a stage failure. Doing it the other way round would make a saved workflow
// invalid the moment someone renamed a role.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Workflow limits. They are generous for a human-authored pipeline and small
// enough that a runaway generator cannot create a document nothing can render.
const (
	MaxWorkflowStages     = 32
	MaxWorkflowStageDeps  = 16
	MaxWorkflowPromptLen  = 4000
	MaxWorkflowAcceptLen  = 2000
	MaxWorkflowNameLen    = 120
	MaxWorkflowDescLen    = 2000
	MaxWorkflowIDLen      = 64
	MaxWorkflowStageIDLen = 64
)

// Workflow errors are typed so the gateway can answer -32600 for a bad document
// and 404 for an unknown id, instead of guessing from text.
var (
	// ErrWorkflowInvalid means the document failed validation; the message names
	// the offending field.
	ErrWorkflowInvalid = errors.New("workflow: invalid")
	// ErrWorkflowNotFound means the requested id does not exist.
	ErrWorkflowNotFound = errors.New("workflow: not found")
	// ErrWorkflowCycle means the stage dependencies contain a cycle.
	ErrWorkflowCycle = errors.New("workflow: dependency cycle")
)

// WorkflowStage is one step of a pipeline.
//
// It is executed as a task: the runner creates a board task from Prompt +
// Acceptance, dispatches a session with Role (a preset id, "" = executor) bound
// to that task, and the ordinary lease/audit loop does the rest. That is why a
// stage has no "on success" field — success is the task being accepted.
type WorkflowStage struct {
	// ID is the stage identifier, unique inside its workflow and used as the
	// dependency key. Same alphabet as a module id segment.
	ID string `json:"id"`
	// Name is the human label. Defaults to ID when empty.
	Name string `json:"name,omitempty"`
	// Role is the agent preset that executes the stage (lead, executor, auditor
	// or a user-defined one). Empty means executor.
	Role string `json:"role,omitempty"`
	// Prompt is what the stage asks for, in the words the executor should see.
	Prompt string `json:"prompt,omitempty"`
	// Acceptance is the line the auditor checks. It becomes part of the task
	// description, so a review has something to compare against.
	Acceptance string `json:"acceptance,omitempty"`
	// DependsOn lists stages that must be accepted before this one is
	// dispatched.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Tools is the tool binding of the stage's task: the executor may only call
	// these. Empty means "no binding" (the role's allowlist decides), which is
	// the historical behaviour; naming tools is how a workflow narrows a stage
	// without inventing a role per stage.
	Tools []string `json:"tools,omitempty"`
}

// Workflow is one stored pipeline definition.
type Workflow struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Stages      []WorkflowStage `json:"stages"`
}

// NormalizeWorkflow fills the defaults a caller may omit: stage names default to
// the stage id and a missing role means executor. It never invents stages.
func NormalizeWorkflow(wf Workflow) Workflow {
	out := wf
	out.ID = strings.TrimSpace(out.ID)
	out.Name = strings.TrimSpace(out.Name)
	out.Description = strings.TrimSpace(out.Description)
	out.Stages = make([]WorkflowStage, len(wf.Stages))
	for i, stage := range wf.Stages {
		stage.ID = strings.TrimSpace(stage.ID)
		stage.Name = strings.TrimSpace(stage.Name)
		stage.Role = strings.TrimSpace(stage.Role)
		stage.Prompt = strings.TrimSpace(stage.Prompt)
		stage.Acceptance = strings.TrimSpace(stage.Acceptance)
		tools := make([]string, 0, len(stage.Tools))
		for _, name := range stage.Tools {
			if name = strings.TrimSpace(name); name != "" {
				tools = append(tools, name)
			}
		}
		stage.Tools = tools
		if stage.Name == "" {
			stage.Name = stage.ID
		}
		if stage.Role == "" {
			stage.Role = PresetExecutorID
		}
		deps := make([]string, 0, len(stage.DependsOn))
		for _, dep := range stage.DependsOn {
			if dep = strings.TrimSpace(dep); dep != "" {
				deps = append(deps, dep)
			}
		}
		stage.DependsOn = deps
		out.Stages[i] = stage
	}
	return out
}

// ValidateWorkflow checks one definition. It returns an error wrapping
// ErrWorkflowInvalid (or ErrWorkflowCycle) whose message names the field, so a
// caller can surface it verbatim.
func ValidateWorkflow(wf Workflow) error {
	if wf.ID == "" {
		return invalid("id is required")
	}
	if len(wf.ID) > MaxWorkflowIDLen {
		return invalid("id is longer than %d characters", MaxWorkflowIDLen)
	}
	if !validWorkflowID(wf.ID) {
		return invalid("id %q must match [a-z0-9][a-z0-9-]*", wf.ID)
	}
	if wf.Name == "" {
		return invalid("name is required")
	}
	if len([]rune(wf.Name)) > MaxWorkflowNameLen {
		return invalid("name is longer than %d characters", MaxWorkflowNameLen)
	}
	if len([]rune(wf.Description)) > MaxWorkflowDescLen {
		return invalid("description is longer than %d characters", MaxWorkflowDescLen)
	}
	if len(wf.Stages) == 0 {
		return invalid("at least one stage is required")
	}
	if len(wf.Stages) > MaxWorkflowStages {
		return invalid("more than %d stages", MaxWorkflowStages)
	}
	seen := make(map[string]bool, len(wf.Stages))
	for _, stage := range wf.Stages {
		if stage.ID == "" {
			return invalid("a stage has no id")
		}
		if len(stage.ID) > MaxWorkflowStageIDLen {
			return invalid("stage id %q is longer than %d characters", stage.ID, MaxWorkflowStageIDLen)
		}
		if !validWorkflowID(stage.ID) {
			return invalid("stage id %q must match [a-z0-9][a-z0-9-]*", stage.ID)
		}
		if seen[stage.ID] {
			return invalid("stage id %q is used twice", stage.ID)
		}
		seen[stage.ID] = true
		if len([]rune(stage.Prompt)) > MaxWorkflowPromptLen {
			return invalid("stage %q prompt is longer than %d characters", stage.ID, MaxWorkflowPromptLen)
		}
		if len([]rune(stage.Acceptance)) > MaxWorkflowAcceptLen {
			return invalid("stage %q acceptance is longer than %d characters", stage.ID, MaxWorkflowAcceptLen)
		}
		if len(stage.DependsOn) > MaxWorkflowStageDeps {
			return invalid("stage %q has more than %d dependencies", stage.ID, MaxWorkflowStageDeps)
		}
		if len(stage.Tools) > MaxTaskTools {
			return invalid("stage %q binds more than %d tools", stage.ID, MaxTaskTools)
		}
		for _, name := range stage.Tools {
			if strings.TrimSpace(name) == "" {
				return invalid("stage %q lists a blank tool name", stage.ID)
			}
		}
		if stage.Role != "" && !validWorkflowID(stage.Role) {
			return invalid("stage %q role %q must match [a-z0-9][a-z0-9-]*", stage.ID, stage.Role)
		}
		for _, dep := range stage.DependsOn {
			if dep == stage.ID {
				return invalid("stage %q depends on itself", stage.ID)
			}
		}
	}
	// Dependencies must exist, checked after the id set is complete so a forward
	// reference reports "unknown" rather than "missing stage".
	for _, stage := range wf.Stages {
		for _, dep := range stage.DependsOn {
			if !seen[dep] {
				return invalid("stage %q depends on unknown stage %q", stage.ID, dep)
			}
		}
	}
	if _, err := StageOrder(wf); err != nil {
		return err
	}
	return nil
}

// StageOrder returns the stage ids in a valid execution order (Kahn's algorithm).
//
// Within the same "layer" the order is the declaration order, so a workflow that
// declares its happy path top to bottom reads top to bottom in every view:
// a stable order is what makes a run's report diffable.
func StageOrder(wf Workflow) ([]string, error) {
	idx := make(map[string]int, len(wf.Stages))
	for position, stage := range wf.Stages {
		if _, ok := idx[stage.ID]; ok {
			return nil, invalid("stage id %q is used twice", stage.ID)
		}
		idx[stage.ID] = position
	}
	remaining := make(map[string]int, len(wf.Stages))
	dependents := make(map[string][]string, len(wf.Stages))
	for _, stage := range wf.Stages {
		remaining[stage.ID] = 0
	}
	for _, stage := range wf.Stages {
		for _, dep := range stage.DependsOn {
			if _, ok := idx[dep]; !ok {
				return nil, invalid("stage %q depends on unknown stage %q", stage.ID, dep)
			}
			remaining[stage.ID]++
			dependents[dep] = append(dependents[dep], stage.ID)
		}
	}
	ready := make([]string, 0, len(wf.Stages))
	for _, stage := range wf.Stages {
		if remaining[stage.ID] == 0 {
			ready = append(ready, stage.ID)
		}
	}
	order := make([]string, 0, len(wf.Stages))
	for len(ready) > 0 {
		sort.SliceStable(ready, func(i, j int) bool { return idx[ready[i]] < idx[ready[j]] })
		next := ready[0]
		ready = ready[1:]
		order = append(order, next)
		freed := dependents[next]
		sort.SliceStable(freed, func(i, j int) bool { return idx[freed[i]] < idx[freed[j]] })
		for _, stageID := range freed {
			remaining[stageID]--
			if remaining[stageID] == 0 {
				ready = append(ready, stageID)
			}
		}
	}
	if len(order) != len(wf.Stages) {
		stuck := make([]string, 0, len(wf.Stages)-len(order))
		done := make(map[string]bool, len(order))
		for _, id := range order {
			done[id] = true
		}
		for _, stage := range wf.Stages {
			if !done[stage.ID] {
				stuck = append(stuck, stage.ID)
			}
		}
		return nil, cycleError(stuck)
	}
	return order, nil
}

// ReadyStages returns the stages that may start now: every dependency is in
// done, the stage itself is not. Declaration order is preserved.
func ReadyStages(wf Workflow, done map[string]bool) []WorkflowStage {
	ready := make([]WorkflowStage, 0, len(wf.Stages))
	for _, stage := range wf.Stages {
		if done[stage.ID] {
			continue
		}
		blocked := false
		for _, dep := range stage.DependsOn {
			if !done[dep] {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, stage)
		}
	}
	return ready
}

// validWorkflowID is the shared alphabet for workflow and stage ids: the same
// one module ids use, so a workflow id can be a path segment in a URL.
func validWorkflowID(id string) bool {
	if id == "" {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '-':
		default:
			return false
		}
	}
	return true
}

func invalid(format string, args ...any) error {
	return &workflowError{kind: ErrWorkflowInvalid, message: format, args: args}
}

func cycleError(stuck []string) error {
	return &workflowError{kind: ErrWorkflowCycle, message: "stage dependencies contain a cycle among %s", args: []any{strings.Join(stuck, ", ")}}
}

// workflowError carries both the sentinel and the message, so errors.Is works and
// the failure text names the field.
type workflowError struct {
	kind    error
	message string
	args    []any
}

func (e *workflowError) Error() string {
	if len(e.args) == 0 {
		return e.kind.Error() + ": " + e.message
	}
	return e.kind.Error() + ": " + fmt.Sprintf(e.message, e.args...)
}

func (e *workflowError) Unwrap() error { return e.kind }

// Workflow run states (M2).
const (
	// WorkflowRunRunning means at least one stage is still pending or in flight.
	WorkflowRunRunning = "running"
	// WorkflowRunDone means every stage's task was accepted.
	WorkflowRunDone = "done"
	// WorkflowStagePending means the stage's dependencies are not met yet.
	WorkflowStagePending = "pending"
	// WorkflowStageDispatched means a session was started for the stage's task.
	WorkflowStageDispatched = "dispatched"
	// WorkflowStageDone means the stage's task was accepted.
	WorkflowStageDone = "done"
)

// WorkflowStageState is one stage's progress inside a run.
type WorkflowStageState struct {
	StageID string `json:"stageId"`
	// TaskID is the board task the runner created for this stage ("" until the
	// stage is dispatched).
	TaskID string `json:"taskId,omitempty"`
	// SessionID is the conversation running the stage ("" until dispatched).
	SessionID string `json:"sessionId,omitempty"`
	// Status is one of the WorkflowStage* values.
	Status string `json:"status"`
}

// WorkflowRun is one execution of a workflow. A run is a projection of task
// states: the runner never keeps its own idea of "done" — it reads the board —
// so a task accepted by the audit loop advances the run without any second
// bookkeeping that could disagree.
type WorkflowRun struct {
	ID         string               `json:"id"`
	WorkflowID string               `json:"workflowId"`
	Status     string               `json:"status"`
	Stages     []WorkflowStageState `json:"stages"`
	CreatedAt  time.Time            `json:"createdAt"`
	UpdatedAt  time.Time            `json:"updatedAt,omitempty"`
}

// WorkflowReport is one Advance pass: how many runs were scanned, stages
// dispatched and runs completed.
type WorkflowReport struct {
	Runs       int
	Dispatched int
	Completed  int
	Errors     []string
}

// KeyWorkflow is provided by the workflow module.
var KeyWorkflow = cordis.NewServiceKey[WorkflowService]("zeokeel/workflow")

// WorkflowService is the workflow backend: definitions plus their runs.
type WorkflowService interface {
	List(ctx context.Context) ([]Workflow, error)
	Get(ctx context.Context, id string) (Workflow, bool, error)
	// Put creates or replaces a definition after validating it. The returned
	// value is the stored (normalized) document.
	Put(ctx context.Context, wf Workflow) (Workflow, error)
	Delete(ctx context.Context, id string) error
	Runs(ctx context.Context) ([]WorkflowRun, error)
	// Start opens a run of one workflow and dispatches its roots.
	Start(ctx context.Context, workflowID string) (WorkflowRun, error)
	// Advance is one idempotent pass: dispatch stages whose dependencies are
	// met, mark stages whose task was accepted, finish runs that are complete.
	Advance(ctx context.Context) (WorkflowReport, error)
}

// WorkflowDispatcher starts one stage: a session for the stage's task, run as
// the stage's role. The chat service implements all four methods.
type WorkflowDispatcher interface {
	NewSession(title string) (string, error)
	Send(sessionID, prompt string) error
	BindSessionTask(ctx context.Context, sessionID, taskID string) error
	SetSessionPreset(sessionID, presetID string) error
}
