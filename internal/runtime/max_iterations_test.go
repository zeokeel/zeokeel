package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/zeokeel/zeokeel/internal/state"
)

// loopScript builds a fakeChatModel script that always emits one tool call, so
// a turn keeps going until eino's max-iterations guard stops it.
func loopScript(toolName string) func(context.Context, int, []*schema.Message) ([]*schema.Message, error) {
	return func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		return []*schema.Message{{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call-" + strings.Repeat("x", call+1),
				Type:     "function",
				Function: schema.FunctionCall{Name: toolName, Arguments: `{}`},
			}},
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{CompletionTokens: 10}},
		}}, nil
	}
}

// TestMaxIterationsCompletesGracefully pins the step-ceiling contract: eino
// allows exactly MaxIterations ChatModel generation cycles, and the (N+1)th
// would fail with ErrExceedMaxIterations. ZeoKeel must turn that boundary into a
// completed turn: every completed tool call stays, an assistant notice (localized
// by Config.StepLimitNotice) explains the ceiling, history is saved so the next
// message continues from here, and the session goes idle without a red failure.
func TestMaxIterationsCompletesGracefully(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	m := &fakeChatModel{script: loopScript("clock")}
	hist := newFakeHistory()
	noticeLimit := 0
	agent := newTestAgent(t, Config{
		einoModel: m, Tools: reg, MaxIterations: 3, History: hist,
		StepLimitNotice: func(limit int) string {
			noticeLimit = limit
			return fmt.Sprintf("step ceiling %d reached", limit)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-maxiter", "loop")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	t.Logf("model calls=%d tool calls=%d phases=%v error=%q tokens=%q",
		m.callCount(), clock.callCount(), phases(events), findErrorText(events), tokenTexts(events))

	// Counting semantics unchanged: MaxIterations model calls run, each tool call
	// completes, and the next generation is the blocked one.
	if m.callCount() != 3 || clock.callCount() != 3 {
		t.Fatalf("model calls=%d tool calls=%d, want 3/3", m.callCount(), clock.callCount())
	}
	if noticeLimit != 3 {
		t.Fatalf("notice limit = %d, want 3", noticeLimit)
	}

	// A completed turn, not a failure: no state.Error, no failed phase, idle end.
	if got := findErrorText(events); got != "" {
		t.Fatalf("step-limit turn emitted an error event: %q", got)
	}
	for _, phase := range phases(events) {
		if phase == state.PhaseFailed {
			t.Fatalf("step-limit turn failed: phases=%v", phases(events))
		}
	}
	if got := phases(events); len(got) == 0 || got[len(got)-1] != state.PhaseIdle {
		t.Fatalf("final phase = %v, want idle", got)
	}

	// The notice is assistant text on the turn's streamed message.
	if got := strings.Join(tokenTexts(events), ""); got != "step ceiling 3 reached" {
		t.Fatalf("assistant notice = %q, want the StepLimitNotice copy", got)
	}
	if usage, ok := lastUsage(events); !ok || usage.Steps != 3 {
		t.Fatalf("usage = %+v, ok=%v; want 3 completed steps", usage, ok)
	}
	if _, isLast := events[len(events)-1].(state.Usage); !isLast {
		t.Fatalf("last event = %T, want state.Usage", events[len(events)-1])
	}

	// History keeps the capped turn so a follow-up continues from here.
	appended := hist.messages("session-maxiter")
	if len(appended) == 0 {
		t.Fatal("history is empty; the capped turn must be saved")
	}
	last := appended[len(appended)-1]
	if last.Role != RoleAssistant || last.Content != "step ceiling 3 reached" {
		t.Fatalf("history last message = %+v, want the assistant notice", last)
	}
	var sawToolResult bool
	for _, msg := range appended {
		if msg.Role == RoleTool && msg.Content == "now" {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Fatalf("history lost the completed tool results: %+v", appended)
	}
}

// TestMaxIterationsBelowLimitCompletesNormally proves the graceful path does not
// trigger when the model answers before the ceiling.
func TestMaxIterationsBelowLimitCompletesNormally(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	m := &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		if call == 0 {
			return []*schema.Message{{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID: "call-1", Type: "function",
					Function: schema.FunctionCall{Name: "clock", Arguments: `{}`},
				}},
			}}, nil
		}
		return []*schema.Message{{Role: schema.Assistant, Content: "final answer"}}, nil
	}}
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, MaxIterations: 5})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-under", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	if m.callCount() != 2 || clock.callCount() != 1 {
		t.Fatalf("model calls=%d tool calls=%d, want 2/1", m.callCount(), clock.callCount())
	}
	if got := strings.Join(tokenTexts(events), ""); got != "final answer" {
		t.Fatalf("tokens = %q, want the model's final answer only", got)
	}
	if findErrorText(events) != "" {
		t.Fatalf("normal completion emitted an error: %q", findErrorText(events))
	}
	if usage, ok := lastUsage(events); !ok || usage.Steps != 2 {
		t.Fatalf("usage = %+v, ok=%v; want 2 steps", usage, ok)
	}
}

// TestMaxIterationsRealErrorStillFails proves the graceful step-limit branch
// does not swallow genuine model failures.
func TestMaxIterationsRealErrorStillFails(t *testing.T) {
	m := &fakeChatModel{script: func(context.Context, int, []*schema.Message) ([]*schema.Message, error) {
		return nil, errors.New("model exploded")
	}}
	agent := newTestAgent(t, Config{einoModel: m, MaxIterations: 5})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-err", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	errText := findErrorText(events)
	if !strings.Contains(errText, "model exploded") {
		t.Fatalf("error text = %q, want the model failure", errText)
	}
	if got := phases(events); len(got) == 0 || got[len(got)-1] != state.PhaseFailed {
		t.Fatalf("final phase = %v, want failed", got)
	}
	if _, ok := lastUsage(events); ok {
		t.Fatal("failed turn must not report usage")
	}
}

// TestMaxIterationsProviderPathCompletesGracefully covers the production model
// path (Config.Provider, not the eino test seam): the provider keeps asking for
// a tool until the ceiling. The turn must complete with the notice instead of
// surfacing eino's NodeRunError.
func TestMaxIterationsProviderPathCompletesGracefully(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		n := calls.Add(1)
		return []Chunk{{
			ToolCalls: []ToolCall{{
				ID:        fmt.Sprintf("call-%d", n),
				Name:      "clock",
				Arguments: json.RawMessage(`{}`),
			}},
			Done: true,
		}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg, MaxIterations: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-provider", "loop")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	if got := calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (MaxIterations)", got)
	}
	if _, ok := p.requestAt(2); ok {
		t.Fatal("provider saw a call past MaxIterations")
	}
	if clock.callCount() != 2 {
		t.Fatalf("tool calls = %d, want 2", clock.callCount())
	}
	if got := findErrorText(events); got != "" {
		t.Fatalf("provider-path step-limit turn emitted an error: %q", got)
	}
	if got := phases(events); len(got) == 0 || got[len(got)-1] != state.PhaseIdle {
		t.Fatalf("final phase = %v, want idle", got)
	}
	tokens := strings.Join(tokenTexts(events), "")
	if !strings.Contains(tokens, "step limit") || !strings.Contains(tokens, "2 steps") {
		t.Fatalf("assistant notice = %q, want the default English notice naming 2 steps", tokens)
	}
	if usage, ok := lastUsage(events); !ok || usage.Steps != 2 {
		t.Fatalf("usage = %+v, ok=%v; want 2 steps", usage, ok)
	}
}

// toolLoopModel scripts a model that calls the clock tool n times and then ends
// the turn with finalText. It is the "AI decides when to stop" shape: the model
// returns no tool calls on the last step, so no ceiling is involved.
func toolLoopModel(n int, finalText string) *fakeChatModel {
	return &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		if call < n {
			return []*schema.Message{{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID:       fmt.Sprintf("call-%d", call+1),
					Type:     "function",
					Function: schema.FunctionCall{Name: "clock", Arguments: `{}`},
				}},
			}}, nil
		}
		return []*schema.Message{{Role: schema.Assistant, Content: finalText}}, nil
	}}
}

// TestUnlimitedIterationsModelEndsTurn pins the DSH-aligned default: with no
// configured ceiling (MaxIterations=0), the model may take as many tool round
// trips as it needs and ends the turn itself by answering without tool calls.
// 25 round trips exceed eino's own 20 fallback, so this also proves the runtime
// passes the large sentinel instead of letting eino default to 20.
func TestUnlimitedIterationsModelEndsTurn(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	const toolCalls = 25
	m := toolLoopModel(toolCalls, "final answer")
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})
	if got := agent.MaxIterations(); got != 0 {
		t.Fatalf("MaxIterations() = %d, want 0 (no fixed ceiling)", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-unlimited", "do the long job")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	t.Logf("model calls=%d tool calls=%d phases=%v error=%q",
		m.callCount(), clock.callCount(), phases(events), findErrorText(events))

	if m.callCount() != toolCalls+1 || clock.callCount() != toolCalls {
		t.Fatalf("model calls=%d tool calls=%d, want %d/%d", m.callCount(), clock.callCount(), toolCalls+1, toolCalls)
	}
	if got := findErrorText(events); got != "" {
		t.Fatalf("unlimited turn emitted an error: %q", got)
	}
	if got := phases(events); len(got) == 0 || got[len(got)-1] != state.PhaseIdle {
		t.Fatalf("final phase = %v, want idle", got)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "final answer" {
		t.Fatalf("tokens = %q, want only the model's final answer (no step-limit notice)", got)
	}
	if usage, ok := lastUsage(events); !ok || usage.Steps != toolCalls+1 {
		t.Fatalf("usage = %+v, ok=%v; want %d steps", usage, ok, toolCalls+1)
	}
}

// TestNegativeMaxIterationsMeansUnlimited pins that a negative configuration
// means the same as 0: no fixed ceiling. It also pins the getter contract.
func TestNegativeMaxIterationsMeansUnlimited(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	m := toolLoopModel(3, "done")
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, MaxIterations: -7})
	if got := agent.MaxIterations(); got != 0 {
		t.Fatalf("MaxIterations() = %d, want 0 for a negative configuration", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-negative", "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)
	if m.callCount() != 4 || clock.callCount() != 3 {
		t.Fatalf("model calls=%d tool calls=%d, want 4/3", m.callCount(), clock.callCount())
	}
	if got := findErrorText(events); got != "" {
		t.Fatalf("negative-cap turn emitted an error: %q", got)
	}
	if got := phases(events); len(got) == 0 || got[len(got)-1] != state.PhaseIdle {
		t.Fatalf("final phase = %v, want idle", got)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "done" {
		t.Fatalf("tokens = %q, want the model's final answer", got)
	}
}

// TestExplicitMaxIterationsGetter pins that an explicit positive cap is kept
// as configured.
func TestExplicitMaxIterationsGetter(t *testing.T) {
	agent := newTestAgent(t, Config{
		einoModel:     toolLoopModel(0, "x"),
		MaxIterations: 42,
	})
	if got := agent.MaxIterations(); got != 42 {
		t.Fatalf("MaxIterations() = %d, want 42", got)
	}
	if got := agent.einoMaxIterations(); got != 42 {
		t.Fatalf("einoMaxIterations() = %d, want 42", got)
	}
	unlimited := newTestAgent(t, Config{einoModel: toolLoopModel(0, "x")})
	if got := unlimited.einoMaxIterations(); got != unlimitedEinoIterations {
		t.Fatalf("unlimited einoMaxIterations() = %d, want sentinel %d", got, unlimitedEinoIterations)
	}
}
