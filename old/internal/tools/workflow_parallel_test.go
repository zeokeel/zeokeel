package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// concRunner records runner concurrency and outputs for the parallel/pipeline
// workflow tests.
type concRunner struct {
	mu        sync.Mutex
	active    int
	maxActive int
	calls     []string
	delay     time.Duration
	fail      map[string]bool
}

func (r *concRunner) RunSubagent(ctx context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.calls = append(r.calls, req.Task)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return runtime.SubagentResult{}, ctx.Err()
		}
	}
	if r.fail[req.Task] {
		return runtime.SubagentResult{}, errors.New("task failed: " + req.Task)
	}
	return runtime.SubagentResult{Agent: req.Agent, Task: req.Task, Output: "out:" + req.Task, Depth: req.Depth}, nil
}

func (r *concRunner) snapshot() (calls []string, maxActive int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...), r.maxActive
}

// TestWorkflowParallelRunsConcurrently pins FP-01's parallel form: every task
// in a {"parallel":[...]} step starts at the same time and the outputs are
// returned in declared order.
func TestWorkflowParallelRunsConcurrently(t *testing.T) {
	runner := &concRunner{delay: 30 * time.Millisecond}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})
	raw, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"steps":[{"parallel":["review a","review b","review c"]}]}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out := decodeWorkflowResult(t, raw)
	if out.Status != WorkflowCompleted {
		t.Fatalf("status = %q, want completed (steps=%+v)", out.Status, out.Steps)
	}
	if len(out.Steps) != 3 {
		t.Fatalf("steps = %+v, want 3", out.Steps)
	}
	for i, step := range out.Steps {
		if step.Index != i || step.Status != WorkflowStepCompleted || step.Group != "parallel[0]" {
			t.Fatalf("step %d = %+v", i, step)
		}
	}
	want := []string{"review a", "review b", "review c"}
	for i := range want {
		if out.Steps[i].Output != "out:"+want[i] {
			t.Fatalf("output %d = %q, want out:%q", i, out.Steps[i].Output, want[i])
		}
	}
	_, maxActive := runner.snapshot()
	if maxActive < 2 {
		t.Fatalf("max concurrent runner calls = %d, want >= 2 (tasks should start together)", maxActive)
	}
}

// TestWorkflowPipelineThreadsPreviousOutput pins the pipeline form: each item
// walks every stage in order and the previous stage output is available via
// {{prev}} (or appended when the placeholder is absent).
func TestWorkflowPipelineThreadsPreviousOutput(t *testing.T) {
	runner := &concRunner{}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})
	raw, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"steps":[{"pipeline":{"items":["A","B"],"stages":["stage1 {{item}}","stage2 prev={{prev}}"]}}]}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out := decodeWorkflowResult(t, raw)
	if out.Status != WorkflowCompleted {
		t.Fatalf("status = %q, want completed (steps=%+v)", out.Status, out.Steps)
	}
	if len(out.Steps) != 4 {
		t.Fatalf("steps = %d, want 4 (2 items x 2 stages)", len(out.Steps))
	}
	// Item-major order: A/stage1, A/stage2, B/stage1, B/stage2.
	wantTasks := []string{"stage1 A", "stage2 prev=out:stage1 A", "stage1 B", "stage2 prev=out:stage1 B"}
	calls, _ := runner.snapshot()
	if len(calls) != len(wantTasks) {
		t.Fatalf("runner calls = %d, want %d (all: %v)", len(calls), len(wantTasks), calls)
	}
	// Items run concurrently, so the call arrival order is not deterministic;
	// the result envelope is (item-major), and every stage must have run once.
	seen := make(map[string]int, len(calls))
	for _, call := range calls {
		seen[call]++
	}
	for _, want := range wantTasks {
		if seen[want] != 1 {
			t.Fatalf("stage prompt %q ran %d times, want once (all: %v)", want, seen[want], calls)
		}
	}
	for i := range out.Steps {
		if out.Steps[i].Item == "" || out.Steps[i].Stage == 0 || !strings.HasPrefix(out.Steps[i].Group, "pipeline[") {
			t.Fatalf("step %d metadata = %+v", i, out.Steps[i])
		}
	}
}

// TestWorkflowParallelFailureDoesNotCancelSiblings pins the resilient parallel
// contract: one failed task is reported, the siblings still finish, and the
// workflow continues with the next step (overall status failed).
func TestWorkflowParallelFailureDoesNotCancelSiblings(t *testing.T) {
	runner := &concRunner{fail: map[string]bool{"bad": true}}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})
	raw, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"steps":[{"parallel":["ok1","bad","ok2"]},"after"]}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out := decodeWorkflowResult(t, raw)
	if out.Status != WorkflowFailed {
		t.Fatalf("status = %q, want failed", out.Status)
	}
	if len(out.Steps) != 4 {
		t.Fatalf("steps = %+v, want 4", out.Steps)
	}
	if out.Steps[0].Status != WorkflowStepCompleted || out.Steps[2].Status != WorkflowStepCompleted {
		t.Fatalf("siblings were not completed: %+v", out.Steps)
	}
	if out.Steps[1].Status != WorkflowStepFailed || !strings.Contains(out.Steps[1].Error, "task failed") {
		t.Fatalf("failed task = %+v", out.Steps[1])
	}
	if out.Steps[3].Step != "after" || out.Steps[3].Status != WorkflowStepCompleted {
		t.Fatalf("follow-up step = %+v, want completed", out.Steps[3])
	}
}

// TestWorkflowPipelineFailureStopsOnlyThatItem pins the per-item failure
// contract: a failed stage skips the rest of that item's stages while other
// items finish.
func TestWorkflowPipelineFailureStopsOnlyThatItem(t *testing.T) {
	runner := &concRunner{fail: map[string]bool{"stage1 A": true}}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})
	raw, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"steps":[{"pipeline":{"items":["A","B"],"stages":["stage1 {{item}}","stage2 {{item}}"]}}]}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out := decodeWorkflowResult(t, raw)
	if out.Status != WorkflowFailed {
		t.Fatalf("status = %q, want failed", out.Status)
	}
	if len(out.Steps) != 4 {
		t.Fatalf("steps = %+v, want 4", out.Steps)
	}
	// A/stage1 failed, A/stage2 skipped; B completed both stages.
	if out.Steps[0].Status != WorkflowStepFailed || out.Steps[0].Item != "A" {
		t.Fatalf("A/stage1 = %+v", out.Steps[0])
	}
	if out.Steps[1].Status != WorkflowStepSkipped || out.Steps[1].Item != "A" {
		t.Fatalf("A/stage2 = %+v", out.Steps[1])
	}
	if out.Steps[2].Status != WorkflowStepCompleted || out.Steps[3].Status != WorkflowStepCompleted {
		t.Fatalf("item B = %+v", out.Steps[2:])
	}
}

// TestWorkflowExpandedCapAndValidation pins that the step cap counts expanded
// task executions (not just top-level entries) and that malformed grouped
// steps are rejected.
func TestWorkflowExpandedCapAndValidation(t *testing.T) {
	runner := &concRunner{}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})
	nine := `{"steps":[{"parallel":["1","2","3","4","5","6","7","8","9"]}]}`
	if _, err := tool.Invoke(context.Background(), json.RawMessage(nine)); err == nil {
		t.Fatal("parallel group with 9 tasks = nil error, want the expanded cap")
	}
	pipeline := `{"steps":[{"pipeline":{"items":["1","2","3","4","5"],"stages":["a","b"]}}]}`
	if _, err := tool.Invoke(context.Background(), json.RawMessage(pipeline)); err == nil {
		t.Fatal("pipeline with 10 stage executions = nil error, want the expanded cap")
	}
	invalid := []string{
		`{"steps":[{"task":"x","parallel":["y"]}]}`,
		`{"steps":[{"pipeline":{"items":[],"stages":["a"]}}]}`,
		`{"steps":[{"pipeline":{"items":["a"],"stages":[]}}]}`,
		`{"steps":[{"parallel":["a"," "]}]}`,
		`{"steps":[{"unknown":"x"}]}`,
	}
	for _, args := range invalid {
		if _, err := tool.Invoke(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("Invoke(%s) = nil error, want validation error", args)
		}
	}
}
