package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// binderTool is a minimal Tool that also implements SubagentRunnerBinder, so a
// runtime test can observe the auto-binding New performs.
type binderTool struct {
	mu     sync.Mutex
	runner SubagentRunner
}

func (t *binderTool) Name() string        { return "binder" }
func (t *binderTool) Description() string { return "Observes the subagent runner binding." }
func (t *binderTool) Invoke(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (t *binderTool) SetSubagentRunner(runner SubagentRunner) {
	t.mu.Lock()
	t.runner = runner
	t.mu.Unlock()
}
func (t *binderTool) bound() SubagentRunner {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runner
}

// jsonTool is an inert registered tool used to shape a restricted tool subset.
type jsonTool struct{ name string }

func (t *jsonTool) Name() string                                            { return t.name }
func (t *jsonTool) Description() string                                     { return "Test tool." }
func (t *jsonTool) Invoke(context.Context, json.RawMessage) (string, error) { return "", nil }

func TestNewBindsSubagentRunner(t *testing.T) {
	binder := &binderTool{}
	reg := NewRegistry()
	if err := reg.Register(binder); err != nil {
		t.Fatal(err)
	}
	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
	})
	if binder.bound() != agent {
		t.Fatalf("binder runner = %v, want the Agent", binder.bound())
	}
}

func TestRunSubagentRestrictsToolsAndReturnsText(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"subagent", "workflow", "clock", "writer"} {
		if err := reg.Register(&jsonTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("child result"), nil })
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})

	result, err := agent.RunSubagent(context.Background(), SubagentRequest{
		Task:  "do the thing",
		Agent: "worker",
		Depth: 1,
	})
	if err != nil {
		t.Fatalf("RunSubagent: %v", err)
	}
	if result.Output != "child result" {
		t.Fatalf("output = %q, want child result", result.Output)
	}
	if result.Agent != "worker" || result.Depth != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Tools) != 2 || result.Tools[0] != "clock" || result.Tools[1] != "writer" {
		t.Fatalf("child tools = %v, want [clock writer]", result.Tools)
	}
	if tools := m.toolNamesAt(0); hasTool(tools, "subagent") || hasTool(tools, "workflow") {
		t.Fatalf("child model tools = %v, want the orchestration tools excluded", tools)
	}
}
func TestRunSubagentHonorsAllowList(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"clock", "writer"} {
		if err := reg.Register(&jsonTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil })
	agent := newTestAgent(t, Config{
		einoModel:     m,
		Tools:         reg,
		SubagentTools: []string{"clock"},
	})
	result, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t", Depth: 1})
	if err != nil {
		t.Fatalf("RunSubagent: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0] != "clock" {
		t.Fatalf("child tools = %v, want [clock]", result.Tools)
	}
}

func TestRunSubagentDepthLimit(t *testing.T) {
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil })
	agent := newTestAgent(t, Config{einoModel: m})
	if _, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t", Depth: 3}); err == nil {
		t.Fatal("RunSubagent(depth 3) = nil error, want depth limit failure")
	}
	// Depth 2 is the default limit and must still run.
	if _, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t", Depth: 2}); err != nil {
		t.Fatalf("RunSubagent(depth 2): %v", err)
	}
	// A non-positive depth means "direct child".
	if _, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "t"}); err != nil {
		t.Fatalf("RunSubagent(depth 0): %v", err)
	}
}

func TestRunSubagentValidatesTask(t *testing.T) {
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil })
	agent := newTestAgent(t, Config{einoModel: m})
	if _, err := agent.RunSubagent(context.Background(), SubagentRequest{Task: "   ", Depth: 1}); err == nil {
		t.Fatal("RunSubagent(blank task) = nil error")
	}
}

// TestRunSubagentAndSetModelDoNotDeadlock is the regression for the
// subMu/agentMu lock-order inversion: childAgent held subMu while its builder
// called a.Mode() (agentMu), and SetModel held agentMu while clearing the child
// cache (subMu). The child builder now consumes a parent snapshot captured
// before subMu, and SetModel clears the cache only after releasing agentMu.
func TestRunSubagentAndSetModelDoNotDeadlock(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"clock", "writer"} {
		if err := reg.Register(&jsonTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	agent := newTestAgent(t, Config{
		Provider: &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "child result", Done: true}}, nil
		}},
		Tools: reg,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := agent.RunSubagent(ctx, SubagentRequest{
				Task: "t", Agent: fmt.Sprintf("worker-%d", i), Depth: 1,
			}); err != nil {
				errs <- err
			}
		}(i)
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := agent.SetModel(fmt.Sprintf("model-%d", i%3)); err != nil {
				errs <- err
			}
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("concurrent RunSubagent/SetModel did not finish (lock-order deadlock)")
	}
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent operation: %v", err)
	}
}
