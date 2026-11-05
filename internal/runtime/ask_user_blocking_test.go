package runtime_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// gateAsk is the QuestionGate method signature the app/controller implements.
type gateAsk = func(ctx context.Context, callID, question string, options []string) (string, error)

// questionGateFunc adapts a function to runtime.QuestionGate.
type questionGateFunc gateAsk

func (f questionGateFunc) Ask(ctx context.Context, callID, question string, options []string) (string, error) {
	return f(ctx, callID, question, options)
}

// TestQuestionGateSeamExists pins the agreed seam: runtime.Config carries a
// QuestionGate whose Ask has the negotiated signature, so the app/controller can
// publish the card and block for the answer.
func TestQuestionGateSeamExists(t *testing.T) {
	field, ok := reflect.TypeOf(runtime.Config{}).FieldByName("QuestionGate")
	if !ok {
		t.Fatal("runtime.Config has no QuestionGate seam")
	}
	iface := field.Type
	if iface.Kind() != reflect.Interface || iface.NumMethod() != 1 || iface.Method(0).Name != "Ask" {
		t.Fatalf("QuestionGate type = %v, want an interface with one Ask method", iface)
	}
	method := iface.Method(0).Type
	want := reflect.TypeOf(gateAsk(nil))
	if method != want {
		t.Fatalf("QuestionGate.Ask signature = %v, want %v", method, want)
	}
	// compile-time: the adapter really satisfies the interface.
	var _ runtime.QuestionGate = questionGateFunc(nil)
}

// fakeStreamProvider scripts model chunks and records requests.
type fakeStreamProvider struct {
	mu   sync.Mutex
	reqs []runtime.Request
	fn   func(call int, req runtime.Request) []runtime.Chunk
}

func (p *fakeStreamProvider) Stream(ctx context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := len(p.reqs)
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	chunks := p.fn(call, req)
	out := make(chan runtime.Chunk)
	go func() {
		defer close(out)
		for _, chunk := range chunks {
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *fakeStreamProvider) requestAt(i int) (runtime.Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.reqs) {
		return runtime.Request{}, false
	}
	return p.reqs[i], true
}

func toolCallChunks(name string, args string) []runtime.Chunk {
	return []runtime.Chunk{{
		ToolCalls: []runtime.ToolCall{{ID: "call-ask", Name: name, Arguments: json.RawMessage(args)}},
		Done:      true,
	}}
}

func drainEvents(t *testing.T, ch <-chan state.Event) []state.Event {
	t.Helper()
	var events []state.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatal("turn did not finish")
		}
	}
}

// TestAskUserBlocksWithinTurn pins the core FP-16 behavior: the runtime
// publishes the question through the gate, blocks the same tool call, and the
// answer is returned as that call's result so the model continues in-turn.
func TestAskUserBlocksWithinTurn(t *testing.T) {
	asked := make(chan struct{})
	answer := make(chan string, 1)
	sawQuestion := make(chan string, 1)
	gate := questionGateFunc(func(ctx context.Context, callID, question string, options []string) (string, error) {
		sawQuestion <- question
		close(asked)
		select {
		case value := <-answer:
			return value, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	provider := &fakeStreamProvider{fn: func(call int, _ runtime.Request) []runtime.Chunk {
		if call == 0 {
			return toolCallChunks("ask_user", `{"question":"Which database should I target?","options":["sqlite","postgres"]}`)
		}
		return []runtime.Chunk{{Text: "using sqlite", Done: true}}
	}}
	reg := runtime.NewRegistry()
	if err := reg.Register(tools.NewAskUserTool()); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}
	agent, err := runtime.New(runtime.Config{
		Name:         "test",
		Provider:     provider,
		Tools:        reg,
		QuestionGate: gate,
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-ask", "book a database")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case question := <-sawQuestion:
		if !strings.Contains(question, "Which database") {
			t.Fatalf("gate question = %q", question)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("QuestionGate.Ask was never called")
	}
	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("question card was not published before the answer")
	}
	answer <- "sqlite"
	_ = drainEvents(t, ch)

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("provider got no follow-up request after the answer")
	}
	var joined strings.Builder
	for _, msg := range second.Messages {
		joined.WriteString(msg.Content)
		joined.WriteString("\n")
	}
	if !strings.Contains(joined.String(), "sqlite") {
		t.Fatalf("follow-up request does not carry the answer: %s", joined.String())
	}
}

// TestQuestionGateTimeoutDegradesToPending pins the compatibility path: a gate
// timeout returns the original tool result (status pending) and lets the turn
// end normally instead of failing or hanging.
func TestQuestionGateTimeoutDegradesToPending(t *testing.T) {
	gate := questionGateFunc(func(context.Context, string, string, []string) (string, error) {
		return "", runtime.ErrQuestionTimeout
	})
	provider := &fakeStreamProvider{fn: func(call int, _ runtime.Request) []runtime.Chunk {
		if call == 0 {
			return toolCallChunks("ask_user", `{"question":"continue?"}`)
		}
		return []runtime.Chunk{{Text: "ok", Done: true}}
	}}
	reg := runtime.NewRegistry()
	if err := reg.Register(tools.NewAskUserTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent, err := runtime.New(runtime.Config{Provider: provider, Tools: reg, QuestionGate: gate})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-timeout", "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = drainEvents(t, ch)
	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("provider got no follow-up request after the timeout")
	}
	joined := ""
	for _, msg := range second.Messages {
		joined += msg.Content
	}
	if !strings.Contains(joined, "pending") {
		t.Fatalf("timeout did not degrade to the pending result: %s", joined)
	}
}

// TestQuestionGateCancelStopsTurn pins cancellation: a turn waiting for an
// answer stops when its context is cancelled instead of hanging.
func TestQuestionGateCancelStopsTurn(t *testing.T) {
	started := make(chan struct{})
	gate := questionGateFunc(func(ctx context.Context, callID, question string, options []string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	provider := &fakeStreamProvider{fn: func(int, runtime.Request) []runtime.Chunk {
		return toolCallChunks("ask_user", `{"question":"waiting"}`)
	}}
	reg := runtime.NewRegistry()
	if err := reg.Register(tools.NewAskUserTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent, err := runtime.New(runtime.Config{Provider: provider, Tools: reg, QuestionGate: gate})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := agent.Run(ctx, "session-cancel", "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("QuestionGate.Ask was never called")
	}
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn did not finish")
	}
}
