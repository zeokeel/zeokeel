package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// wiringProvider is a scripted provider for the production-wiring tests. It
// records every request and replies with text, or with one tool call on the
// first model call of a turn when toolFirst is set.
type wiringProvider struct {
	toolName string
	mu       sync.Mutex
	reqs     []runtime.Request
	calls    int
}

var _ runtime.Provider = (*wiringProvider)(nil)

func (p *wiringProvider) Stream(_ context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := p.calls
	p.calls++
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()

	var chunks []runtime.Chunk
	if p.toolName != "" && call%2 == 0 {
		chunks = []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        fmt.Sprintf("call-%d", call/2+1),
				Name:      p.toolName,
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			Done: true,
		}}
	} else {
		chunks = []runtime.Chunk{{Text: fmt.Sprintf("reply-%d", call), Done: true}}
	}
	out := make(chan runtime.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

func (p *wiringProvider) requestAt(i int) (runtime.Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.reqs) {
		return runtime.Request{}, false
	}
	return p.reqs[i], true
}

type wiringEchoTool struct{ calls int }

func (t *wiringEchoTool) Name() string        { return "echo" }
func (t *wiringEchoTool) Description() string { return "Echoes its JSON arguments." }
func (t *wiringEchoTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	t.calls++
	return string(args), nil
}

// newWiredController mirrors production: the agent is built against the store
// with the storeHistory adapter and the session controller owns persistence.
func newWiredController(t *testing.T, provider runtime.Provider, tool runtime.Tool) (*session.Controller, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	registry := runtime.NewRegistry()
	if tool != nil {
		if err := registry.Register(tool); err != nil {
			t.Fatalf("register tool: %v", err)
		}
	}
	var wiredStore *store.Store
	ctrl, cleanup, err := BuildServicesWith(dir, func(st *store.Store) (*AgentBundle, error) {
		wiredStore = st
		agent, err := runtime.New(runtime.Config{
			Provider:    provider,
			Tools:       registry,
			History:     &storeHistory{st: st},
			Instruction: "wiring test agent",
		})
		if err != nil {
			return nil, err
		}
		return &AgentBundle{Agent: agent}, nil
	})
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })
	return ctrl, wiredStore
}

func waitWiringIdle(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ctrl.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("turn did not finish: %+v", ctrl.Snapshot())
	}
	if snap := ctrl.Snapshot(); snap == nil || snap.Phase != state.PhaseIdle {
		t.Fatalf("turn ended in %+v, want idle", ctrl.Snapshot())
	}
}

// TestProductionWiringPersistsOneMessagePerRolePerTurn is the regression for
// the duplicate write: the controller persisted the user prompt before the
// runtime loaded history and storeHistory.Append wrote user+assistant again.
func TestProductionWiringPersistsOneMessagePerRolePerTurn(t *testing.T) {
	provider := &wiringProvider{}
	ctrl, st := newWiredController(t, provider, nil)
	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "wiring")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, text := range []string{"hello", "continue"} {
		if err := ctrl.Submit(ctx, id, text); err != nil {
			t.Fatalf("Submit(%q): %v", text, err)
		}
		waitWiringIdle(t, ctrl)
		ctrl.Wait()
	}

	msgs, err := st.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages = %d (%+v), want one user and one assistant per turn", len(msgs), msgs)
	}
	want := []struct {
		role, content string
	}{
		{"user", "hello"},
		{"assistant", "reply-0"},
		{"user", "continue"},
		{"assistant", "reply-1"},
	}
	for i, w := range want {
		if msgs[i].Role != w.role || msgs[i].Content != w.content {
			t.Fatalf("message[%d] = %s:%q, want %s:%q", i, msgs[i].Role, msgs[i].Content, w.role, w.content)
		}
	}

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("provider received no second request")
	}
	counts := map[string]int{}
	for _, m := range second.Messages {
		counts[string(m.Role)+":"+m.Content]++
	}
	for _, key := range []string{"user:hello", "assistant:reply-0", "user:continue"} {
		if counts[key] != 1 {
			t.Fatalf("second request %q count = %d in %+v, want 1", key, counts[key], second.Messages)
		}
	}
}

// TestProductionWiringRoundTripsToolExchange checks that a tool call and its
// result reach the next provider request: the messages table alone cannot
// carry them, so storeHistory derives them from the durable tool_calls rows.
func TestProductionWiringRoundTripsToolExchange(t *testing.T) {
	provider := &wiringProvider{toolName: "echo"}
	tool := &wiringEchoTool{}
	ctrl, _ := newWiredController(t, provider, tool)
	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "tool wiring")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "what time is it?"); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", tool.calls)
	}

	if err := ctrl.Submit(ctx, id, "again"); err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()

	// The first model call of the second turn is request index 2 (the tool
	// turn used two calls).
	req, ok := provider.requestAt(2)
	if !ok {
		t.Fatalf("provider requests = %d, want 3", provider.calls)
	}
	var sawCall, sawResult bool
	for _, m := range req.Messages {
		switch m.Role {
		case runtime.RoleAssistant:
			for _, call := range m.ToolCalls {
				if call.ID == "call-1" && call.Name == "echo" {
					sawCall = true
				}
			}
		case runtime.RoleTool:
			if m.ToolCallID == "call-1" && m.Content != "" {
				sawResult = true
			}
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("second request lost the tool exchange: %+v", req.Messages)
	}
	users := 0
	for _, m := range req.Messages {
		if m.Role == runtime.RoleUser && m.Content == "what time is it?" {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("first prompt occurrences = %d in %+v, want 1", users, req.Messages)
	}
}
