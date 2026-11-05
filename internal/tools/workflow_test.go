package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

func decodeWorkflowResult(t *testing.T, raw string) WorkflowResult {
	t.Helper()
	var out WorkflowResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("workflow result is not JSON: %v\n%s", err, raw)
	}
	return out
}

func TestWorkflowToolRunsStepsInOrder(t *testing.T) {
	runner := &fakeSubagentRunner{byTask: map[string]runtime.SubagentResult{
		"read":    {Output: "read done"},
		"analyze": {Output: "analyze done"},
		"report":  {Output: "report done"},
	}}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})

	raw, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"steps":["read","analyze","report"],"agent":"worker"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out := decodeWorkflowResult(t, raw)
	if out.Kind != "workflow" || out.Status != WorkflowCompleted {
		t.Fatalf("result = %+v", out)
	}
	if len(out.Steps) != 3 {
		t.Fatalf("steps = %+v", out.Steps)
	}
	wantOutputs := []string{"read done", "analyze done", "report done"}
	for i, step := range out.Steps {
		if step.Index != i || step.Status != WorkflowStepCompleted || step.Output != wantOutputs[i] {
			t.Fatalf("step %d = %+v", i, step)
		}
	}
	reqs := runner.requests()
	if len(reqs) != 3 {
		t.Fatalf("runner calls = %d, want 3", len(reqs))
	}
	for i, want := range []string{"read", "analyze", "report"} {
		if reqs[i].Task != want || reqs[i].Agent != "worker" || reqs[i].Depth != 1 {
			t.Fatalf("request %d = %+v", i, reqs[i])
		}
	}
	if tool.Runs() != 1 {
		t.Fatalf("Runs() = %d, want 1", tool.Runs())
	}
}

func TestWorkflowToolStopsAndMarksSkipped(t *testing.T) {
	// The first step has a scripted success; every other task hits the error.
	runner := &fakeSubagentRunner{
		byTask: map[string]runtime.SubagentResult{"one": {Output: "one done"}},
		err:    errors.New("step failed"),
	}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})

	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"steps":["one","two","three"]}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out := decodeWorkflowResult(t, raw)
	if out.Status != WorkflowFailed {
		t.Fatalf("status = %q, want failed", out.Status)
	}
	if len(out.Steps) != 3 {
		t.Fatalf("steps = %+v", out.Steps)
	}
	if out.Steps[0].Status != WorkflowStepCompleted {
		t.Fatalf("step 0 = %+v", out.Steps[0])
	}
	if out.Steps[1].Status != WorkflowStepFailed || !strings.Contains(out.Steps[1].Error, "step failed") {
		t.Fatalf("step 1 = %+v", out.Steps[1])
	}
	if out.Steps[2].Status != WorkflowStepSkipped || out.Steps[2].Output != "" {
		t.Fatalf("step 2 = %+v", out.Steps[2])
	}
}

func TestWorkflowToolCapsAndValidation(t *testing.T) {
	runner := &fakeSubagentRunner{}
	tool := NewWorkflowTool(WorkflowConfig{Runner: runner})

	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("Invoke with no steps = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"steps":["ok","  "]}`)); err == nil {
		t.Fatal("Invoke with a blank step = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("Invoke with invalid JSON = nil error")
	}

	nine := `{"steps":["1","2","3","4","5","6","7","8","9"]}`
	if _, err := tool.Invoke(context.Background(), json.RawMessage(nine)); err == nil {
		t.Fatal("Invoke with 9 steps = nil error, want the default cap")
	}
	small := NewWorkflowTool(WorkflowConfig{Runner: runner, MaxSteps: 2})
	if _, err := small.Invoke(context.Background(), json.RawMessage(`{"steps":["a","b","c"]}`)); err == nil {
		t.Fatal("Invoke with 3 steps and MaxSteps 2 = nil error")
	}

	ctx := runtime.WithSubagentDepth(context.Background(), 2)
	if _, err := tool.Invoke(ctx, json.RawMessage(`{"steps":["a"]}`)); err == nil {
		t.Fatal("Invoke at depth 3 = nil error, want the depth cap")
	}
}

func TestWorkflowToolRequiresRunner(t *testing.T) {
	tool := NewWorkflowTool(WorkflowConfig{})
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"steps":["a"]}`)); err == nil {
		t.Fatal("Invoke without a runner = nil error")
	}
}
