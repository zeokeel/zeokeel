package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// fakeSubagentRunner scripts child-agent runs for the subagent and workflow
// tool tests.
type fakeSubagentRunner struct {
	mu     sync.Mutex
	reqs   []runtime.SubagentRequest
	result runtime.SubagentResult
	err    error
	byTask map[string]runtime.SubagentResult
}

var _ runtime.SubagentRunner = (*fakeSubagentRunner)(nil)

func (r *fakeSubagentRunner) RunSubagent(_ context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	result, scripted := r.byTask[req.Task]
	err := r.err
	r.mu.Unlock()
	if scripted {
		return result, nil
	}
	if err != nil {
		return runtime.SubagentResult{}, err
	}
	return r.result, nil
}

func (r *fakeSubagentRunner) requests() []runtime.SubagentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtime.SubagentRequest(nil), r.reqs...)
}

func TestSubagentToolRunsChildAndReportsResult(t *testing.T) {
	runner := &fakeSubagentRunner{result: runtime.SubagentResult{
		Agent:  "worker",
		Task:   "summarize",
		Output: "the child's answer",
		Depth:  1,
		Tools:  []string{"read_file"},
	}}
	tool := NewSubagentTool(SubagentConfig{Runner: runner})

	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"task":"summarize","agent":" worker "}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if raw != "the child's answer" {
		t.Fatalf("Invoke = %q, want the child's final text", raw)
	}
	reqs := runner.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Task != "summarize" || reqs[0].Agent != "worker" || reqs[0].Depth != 1 {
		t.Fatalf("request = %+v", reqs[0])
	}
	if tool.Calls() != 1 {
		t.Fatalf("Calls() = %d, want 1", tool.Calls())
	}
	if tool.Name() != "subagent" || strings.TrimSpace(tool.Description()) == "" {
		t.Fatalf("tool metadata = %q / %q", tool.Name(), tool.Description())
	}
}

func TestSubagentToolDepthLimit(t *testing.T) {
	runner := &fakeSubagentRunner{result: runtime.SubagentResult{Output: "ok"}}
	tool := NewSubagentTool(SubagentConfig{Runner: runner})

	// Depth 2 is the default limit: a call from depth 2 would be depth 3.
	ctx := runtime.WithSubagentDepth(context.Background(), 2)
	if _, err := tool.Invoke(ctx, json.RawMessage(`{"task":"nested"}`)); err == nil {
		t.Fatal("Invoke at depth 3 = nil error, want depth limit")
	}
	if len(runner.requests()) != 0 {
		t.Fatalf("runner was called despite the depth limit: %+v", runner.requests())
	}

	// A call from depth 1 is depth 2 and is allowed.
	ctx = runtime.WithSubagentDepth(context.Background(), 1)
	if _, err := tool.Invoke(ctx, json.RawMessage(`{"task":"nested"}`)); err != nil {
		t.Fatalf("Invoke at depth 2: %v", err)
	}
	if got := runner.requests()[0].Depth; got != 2 {
		t.Fatalf("request depth = %d, want 2", got)
	}
}

func TestSubagentToolCustomMaxDepth(t *testing.T) {
	runner := &fakeSubagentRunner{result: runtime.SubagentResult{Output: "ok"}}
	tool := NewSubagentTool(SubagentConfig{Runner: runner, MaxDepth: 1})
	ctx := runtime.WithSubagentDepth(context.Background(), 1)
	if _, err := tool.Invoke(ctx, json.RawMessage(`{"task":"nested"}`)); err == nil {
		t.Fatal("Invoke at depth 2 with MaxDepth 1 = nil error")
	}
}

func TestSubagentToolValidationAndRunnerBinding(t *testing.T) {
	tool := NewSubagentTool(SubagentConfig{})
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"task":"x"}`)); err == nil {
		t.Fatal("Invoke without a runner = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"task":"  "}`)); err == nil {
		t.Fatal("Invoke with a blank task = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("Invoke with invalid JSON = nil error")
	}

	runner := &fakeSubagentRunner{result: runtime.SubagentResult{Output: "bound"}}
	tool.SetSubagentRunner(runner)
	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"task":"x"}`))
	if err != nil {
		t.Fatalf("Invoke after binding: %v", err)
	}
	if raw != "bound" {
		t.Fatalf("Invoke after binding = %q, want bound", raw)
	}
}

func TestSubagentToolPropagatesRunnerError(t *testing.T) {
	runner := &fakeSubagentRunner{err: errors.New("child exploded")}
	tool := NewSubagentTool(SubagentConfig{Runner: runner})
	_, err := tool.Invoke(context.Background(), json.RawMessage(`{"task":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "child exploded") {
		t.Fatalf("Invoke error = %v", err)
	}
}
