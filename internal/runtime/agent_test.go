package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/zeokeel/zeokeel/internal/state"
)

// fakeChatModel is a deterministic eino ChatModel used by the runtime tests. It
// records every input it receives and delegates chunk scripting to script so a
// test can emit text, reasoning, tool calls and usage without a network.
type fakeChatModel struct {
	mu     sync.Mutex
	calls  int
	inputs [][]*schema.Message
	script func(ctx context.Context, call int, input []*schema.Message) ([]*schema.Message, error)
}

var _ model.ChatModel = (*fakeChatModel)(nil)

func (m *fakeChatModel) record(input []*schema.Message) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	call := m.calls
	m.calls++
	m.inputs = append(m.inputs, append([]*schema.Message(nil), input...))
	return call
}

func (m *fakeChatModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *fakeChatModel) inputAt(i int) []*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < 0 || i >= len(m.inputs) {
		return nil
	}
	return m.inputs[i]
}

func (m *fakeChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	stream, err := m.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var chunks []*schema.Message
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if msg != nil {
			chunks = append(chunks, msg)
		}
	}
	if len(chunks) == 0 {
		return schema.AssistantMessage("", nil), nil
	}
	return schema.ConcatMessages(chunks)
}

func (m *fakeChatModel) Stream(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	call := m.record(input)
	chunks, err := m.script(ctx, call, input)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		for _, chunk := range chunks {
			if sw.Send(chunk, nil) {
				return
			}
		}
	}()
	return sr, nil
}

func (m *fakeChatModel) BindTools(_ []*schema.ToolInfo) error { return nil }

// fakeClock is the deterministic test tool. It records calls and can fail on
// demand.
type fakeClock struct {
	mu     sync.Mutex
	calls  int
	args   []json.RawMessage
	result string
	err    error
}

var _ Tool = (*fakeClock)(nil)

func (c *fakeClock) Name() string        { return "clock" }
func (c *fakeClock) Description() string { return "Returns the current time." }

func (c *fakeClock) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.args = append(c.args, append(json.RawMessage(nil), args...))
	if c.err != nil {
		return "", c.err
	}
	return c.result, nil
}

func (c *fakeClock) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// fakeProvider is a scripted Provider.
type fakeProvider struct {
	mu   sync.Mutex
	reqs []Request
	fn   func(ctx context.Context, req Request) ([]Chunk, error)
}

var _ Provider = (*fakeProvider)(nil)

func (p *fakeProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	fn := p.fn
	p.mu.Unlock()

	chunks, err := fn(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan Chunk)
	go func() {
		defer close(out)
		for _, c := range chunks {
			select {
			case out <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *fakeProvider) requestAt(i int) (Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.reqs) {
		return Request{}, false
	}
	return p.reqs[i], true
}

func usageChunk(in, out, cache int) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
			Usage: &schema.TokenUsage{
				PromptTokens:       in,
				CompletionTokens:   out,
				TotalTokens:        in + out,
				PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: cache},
			},
		},
	}
}

func newTestAgent(t *testing.T, cfg Config) *Agent {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func collectEvents(t *testing.T, ch <-chan state.Event) []state.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	var events []state.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("timed out draining events after %d events", len(events))
		}
	}
}

func tokenTexts(events []state.Event) []string {
	var out []string
	for _, ev := range events {
		if tok, ok := ev.(state.Token); ok {
			out = append(out, tok.Text)
		}
	}
	return out
}

func reasoningTexts(events []state.Event) []string {
	var out []string
	for _, ev := range events {
		if r, ok := ev.(state.Reasoning); ok {
			out = append(out, r.Text)
		}
	}
	return out
}

// lastUsage returns the most recent usage delta (a single model call).
func lastUsage(events []state.Event) (state.Usage, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if u, ok := events[i].(state.Usage); ok {
			return u, true
		}
	}
	return state.Usage{}, false
}

// sumUsage folds the usage deltas into the cumulative totals a snapshot keeps:
// turns count once per turn, steps/tokens are per model call.
func sumUsage(events []state.Event) (state.Usage, bool) {
	var total state.Usage
	found := false
	for _, ev := range events {
		u, ok := ev.(state.Usage)
		if !ok {
			continue
		}
		found = true
		total.Turns += u.Turns
		total.Steps += u.Steps
		total.In += u.In
		total.Out += u.Out
		total.CacheHit += u.CacheHit
	}
	return total, found
}

func findToolStart(events []state.Event, name string) (state.ToolStart, bool) {
	for _, ev := range events {
		if ts, ok := ev.(state.ToolStart); ok && ts.Name == name {
			return ts, true
		}
	}
	return state.ToolStart{}, false
}

func findToolEnd(events []state.Event, callID string) (state.ToolEnd, bool) {
	for _, ev := range events {
		if te, ok := ev.(state.ToolEnd); ok && te.CallID == callID {
			return te, true
		}
	}
	return state.ToolEnd{}, false
}

func findErrorText(events []state.Event) string {
	for _, ev := range events {
		if e, ok := ev.(state.Error); ok {
			return e.Err.Error()
		}
	}
	return ""
}

func phases(events []state.Event) []state.Phase {
	var out []state.Phase
	for _, ev := range events {
		if s, ok := ev.(state.Status); ok {
			out = append(out, s.Phase)
		}
	}
	return out
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New with no model/provider must fail")
	}
	m := &fakeChatModel{script: func(context.Context, int, []*schema.Message) ([]*schema.Message, error) {
		return []*schema.Message{schema.AssistantMessage("x", nil)}, nil
	}}
	p := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) { return nil, nil }}
	if _, err := New(Config{Provider: p, einoModel: m}); err == nil {
		t.Fatal("New with both Provider and eino model must fail")
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	clock := &fakeClock{result: "now"}
	if err := reg.Register(clock); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.Register(clock); err == nil {
		t.Fatal("duplicate Register must fail")
	}
	if err := reg.Register(nil); err == nil {
		t.Fatal("nil Register must fail")
	}
	if got, ok := reg.Get("clock"); !ok || got != clock {
		t.Fatalf("Get(clock) = %v, %v", got, ok)
	}
	if _, ok := reg.Get("missing"); ok {
		t.Fatal("Get(missing) must not be ok")
	}
	if list := reg.List(); len(list) != 1 || list[0].Name() != "clock" {
		t.Fatalf("List = %v", list)
	}
	reg.seal()
	if err := reg.Register(&fakeClock{result: "x"}); err == nil {
		t.Fatal("Register after seal must fail")
	}
}

func TestRunStreamsTokensInOrderAndEndsWithUsage(t *testing.T) {
	m := &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		if call > 0 {
			t.Errorf("unexpected extra model call %d", call)
		}
		return []*schema.Message{
			{Role: schema.Assistant, Content: "Hel"},
			{Role: schema.Assistant, Content: "lo, "},
			{Role: schema.Assistant, Content: "world", ResponseMeta: &schema.ResponseMeta{
				FinishReason: "stop",
				Usage:        &schema.TokenUsage{PromptTokens: 11, CompletionTokens: 3, TotalTokens: 14, PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 4}},
			}},
		}, nil
	}}
	agent := newTestAgent(t, Config{einoModel: m, Instruction: "be terse"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-1", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	gotTokens := tokenTexts(events)
	wantTokens := []string{"Hel", "lo, ", "world"}
	if strings.Join(gotTokens, "|") != strings.Join(wantTokens, "|") {
		t.Fatalf("tokens = %v, want %v", gotTokens, wantTokens)
	}

	// Streaming message identity is stable and routed to the session.
	msgIDs := map[string]bool{}
	for _, ev := range events {
		if tok, ok := ev.(state.Token); ok {
			msgIDs[tok.MsgID] = true
			if tok.SessionID != "session-1" {
				t.Fatalf("token SessionID = %q, want session-1", tok.SessionID)
			}
		}
	}
	if len(msgIDs) != 1 {
		t.Fatalf("expected one assistant MsgID, got %v", msgIDs)
	}

	// A turn starts running and ends idle.
	gotPhases := phases(events)
	if len(gotPhases) < 2 || gotPhases[0] != state.PhaseRunning {
		t.Fatalf("phases = %v, want first running", gotPhases)
	}

	// Usage is published as a delta per model call, before the terminal status.
	usage, ok := sumUsage(events)
	if !ok {
		t.Fatal("no usage event")
	}
	if usage.Turns != 1 || usage.Steps != 1 || usage.In != 11 || usage.Out != 3 || usage.CacheHit != 4 {
		t.Fatalf("cumulative usage = %+v", usage)
	}
	if _, isLast := events[len(events)-1].(state.Status); !isLast {
		t.Fatalf("last event = %T, want the terminal state.Status", events[len(events)-1])
	}
	if gotPhases[len(gotPhases)-1] != state.PhaseIdle {
		t.Fatalf("final phase = %v, want idle", gotPhases[len(gotPhases)-1])
	}
}

func TestRunToolCallExecutesAndResultIsFedBack(t *testing.T) {
	clock := &fakeClock{result: "2026-11-04T12:00:00Z"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}

	m := &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		switch call {
		case 0:
			return []*schema.Message{{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID:       "call-1",
					Type:     "function",
					Function: schema.FunctionCall{Name: "clock", Arguments: `{}`},
				}},
			}}, nil
		default:
			return []*schema.Message{
				{Role: schema.Assistant, Content: "It is "},
				{Role: schema.Assistant, Content: "12:00.", ResponseMeta: &schema.ResponseMeta{
					Usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
				}},
			}, nil
		}
	}}
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-tool", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.callCount())
	}
	if m.callCount() != 2 {
		t.Fatalf("model calls = %d, want 2 (tool loop)", m.callCount())
	}

	start, ok := findToolStart(events, "clock")
	if !ok {
		t.Fatalf("no ToolStart event; events: %#v", events)
	}
	if start.CallID != "call-1" || string(start.Args) != "{}" {
		t.Fatalf("ToolStart = %+v", start)
	}
	end, ok := findToolEnd(events, "call-1")
	if !ok {
		t.Fatal("no ToolEnd event")
	}
	if end.Result != clock.result || end.IsError || end.Duration < 0 {
		t.Fatalf("ToolEnd = %+v", end)
	}

	// The tool result must be fed back to the model as a tool message.
	secondCall := m.inputAt(1)
	var fedBack *schema.Message
	for _, msg := range secondCall {
		if msg != nil && msg.Role == schema.Tool {
			fedBack = msg
		}
	}
	if fedBack == nil {
		t.Fatalf("second model call has no tool message: %#v", secondCall)
	}
	if fedBack.ToolCallID != "call-1" || fedBack.Content != clock.result {
		t.Fatalf("fed-back tool message = %+v", fedBack)
	}

	gotTokens := tokenTexts(events)
	if strings.Join(gotTokens, "") != "It is 12:00." {
		t.Fatalf("final text = %q", strings.Join(gotTokens, ""))
	}
	usage, ok := sumUsage(events)
	if !ok || usage.Steps != 2 || usage.In != 20 || usage.Out != 5 {
		t.Fatalf("usage = %+v, ok=%v", usage, ok)
	}
}

func TestRunUnknownToolEmitsErrorEvent(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatal(err)
	}
	m := &fakeChatModel{script: func(context.Context, int, []*schema.Message) ([]*schema.Message, error) {
		return []*schema.Message{{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call-missing",
				Type:     "function",
				Function: schema.FunctionCall{Name: "missing_tool", Arguments: `{}`},
			}},
		}}, nil
	}}
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-unknown", "call a missing tool")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	errText := findErrorText(events)
	if !strings.Contains(errText, "unknown tool") || !strings.Contains(errText, "missing_tool") {
		t.Fatalf("error event = %q, events: %#v", errText, events)
	}
	if _, isLast := events[len(events)-1].(state.Error); !isLast {
		t.Fatalf("last event = %T, want state.Error", events[len(events)-1])
	}
	gotPhases := phases(events)
	if len(gotPhases) == 0 || gotPhases[len(gotPhases)-1] != state.PhaseFailed {
		t.Fatalf("phases = %v, want failed", gotPhases)
	}
}

// TestRunToolFailureIsFedBackToModel covers the tool-failure contract: a tool
// that returns an error is reported to the model as that call's result (with
// the bounded [tool error] marker), the model gets a second call, and the turn
// finishes normally. The ToolEnd still carries IsError for the UI's red card.
func TestRunToolFailureIsFedBackToModel(t *testing.T) {
	clock := &fakeClock{err: errors.New("clock is broken")}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	m := &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		if call == 0 {
			return []*schema.Message{{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID:       "call-err",
					Type:     "function",
					Function: schema.FunctionCall{Name: "clock", Arguments: `{}`},
				}},
			}}, nil
		}
		return []*schema.Message{{Role: schema.Assistant, Content: "The tool failed; continuing."}}, nil
	}}
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-tool-err", "call clock")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.callCount())
	}
	if m.callCount() != 2 {
		t.Fatalf("model calls = %d, want 2: a failed tool must not end the turn", m.callCount())
	}
	end, ok := findToolEnd(events, "call-err")
	if !ok {
		t.Fatalf("no ToolEnd for failed tool; events: %#v", events)
	}
	if !end.IsError || !strings.Contains(end.Result, "clock is broken") || !strings.Contains(end.Result, "[tool error]") {
		t.Fatalf("ToolEnd = %+v, want the error marker and IsError", end)
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("failed tool produced a turn error %q", errText)
	}
	if got := strings.Join(tokenTexts(events), ""); !strings.Contains(got, "The tool failed") {
		t.Fatalf("final text = %q", got)
	}
	// The second model call sees the failure as a tool message.
	second := m.inputAt(1)
	var fedBack *schema.Message
	for _, msg := range second {
		if msg != nil && msg.Role == schema.Tool && msg.ToolCallID == "call-err" {
			fedBack = msg
		}
	}
	if fedBack == nil || !strings.Contains(fedBack.Content, "clock is broken") {
		t.Fatalf("second call tool message = %+v, want the failure text", fedBack)
	}
	gotPhases := phases(events)
	if len(gotPhases) == 0 || gotPhases[len(gotPhases)-1] != state.PhaseIdle {
		t.Fatalf("phases = %v, want idle", gotPhases)
	}
}

func TestRunContextCancellationStopsTurn(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	m := &fakeChatModel{script: func(ctx context.Context, _ int, _ []*schema.Message) ([]*schema.Message, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	agent := newTestAgent(t, Config{einoModel: m})

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := agent.Run(ctx, "session-cancel", "block forever")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model never started")
	}
	cancel()

	var (
		mu     sync.Mutex
		events []state.Event
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("events channel did not close after cancellation")
	}
	mu.Lock()
	defer mu.Unlock()
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("cancellation produced error event %q", errText)
	}
}

func TestRunProviderStreamsChunks(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		if len(req.Messages) == 0 {
			t.Error("provider got no messages")
		}
		return []Chunk{
			{Reasoning: "thinking..."},
			{Text: "Hello"},
			{Text: ", provider"},
			{Text: "!", Usage: &Usage{InputTokens: 7, OutputTokens: 2, TotalTokens: 9}, Done: true},
		}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg, ModelName: "fake-model"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-provider", "hello there")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	req, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider got no request")
	}
	if req.SessionID != "session-provider" || req.Model != "fake-model" {
		t.Fatalf("request = %+v", req)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "clock" {
		t.Fatalf("request tools = %+v", req.Tools)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != RoleUser || req.Messages[0].Content != "hello there" {
		t.Fatalf("request messages = %+v", req.Messages)
	}

	if got := strings.Join(reasoningTexts(events), ""); got != "thinking..." {
		t.Fatalf("reasoning = %q", got)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "Hello, provider!" {
		t.Fatalf("text = %q", got)
	}
	usage, ok := sumUsage(events)
	if !ok || usage.In != 7 || usage.Out != 2 || usage.Steps != 1 {
		t.Fatalf("usage = %+v, ok=%v", usage, ok)
	}
}

func TestRunProviderErrorBecomesErrorEvent(t *testing.T) {
	p := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return nil, errors.New("provider exploded")
	}}
	agent := newTestAgent(t, Config{Provider: p})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-provider-err", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)
	if errText := findErrorText(events); !strings.Contains(errText, "provider exploded") {
		t.Fatalf("error = %q, events: %#v", errText, events)
	}
	if _, isLast := events[len(events)-1].(state.Error); !isLast {
		t.Fatalf("last event = %T, want state.Error", events[len(events)-1])
	}
}

func TestRunConcurrentSessions(t *testing.T) {
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		var last string
		for _, msg := range req.Messages {
			if msg.Role == RoleUser {
				last = msg.Content
			}
		}
		return []Chunk{{Text: "echo:" + last, Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p})

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			sessionID := "session-" + string(rune('a'+i))
			text := "prompt-" + string(rune('a'+i))
			ch, err := agent.Run(ctx, sessionID, text)
			if err != nil {
				errs <- err
				return
			}
			var got []string
			for ev := range ch {
				if tok, ok := ev.(state.Token); ok {
					if tok.SessionID != sessionID {
						errs <- errors.New("session id mismatch in token event")
						return
					}
					got = append(got, tok.Text)
				}
			}
			if strings.Join(got, "") != "echo:"+text {
				errs <- errors.New("wrong echo in " + sessionID + ": got " + strings.Join(got, ""))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestRunConcurrentSessionsAreSerialized is the race regression for two
// sessions sharing one Agent. eino's TypedChatModelAgent stores per-run state
// on the agent, so overlapping Runner.Run calls race (and can corrupt the
// second turn). The turn goroutine now holds runMu for the whole eino run: this
// test proves only one model call is ever active, even when session B is
// submitted while session A is blocked inside the provider.
func TestRunConcurrentSessionsAreSerialized(t *testing.T) {
	var (
		mu     sync.Mutex
		active int
		max    int
	)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	p := &fakeProvider{fn: func(ctx context.Context, _ Request) ([]Chunk, error) {
		mu.Lock()
		active++
		if active > max {
			max = active
		}
		mu.Unlock()
		once.Do(func() { close(started) })

		select {
		case <-release:
		case <-ctx.Done():
			mu.Lock()
			active--
			mu.Unlock()
			return nil, ctx.Err()
		}
		mu.Lock()
		active--
		mu.Unlock()
		return []Chunk{{Text: "ok", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := agent.Run(ctx, "session-a", "first")
	if err != nil {
		t.Fatalf("Run(session-a): %v", err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first turn never reached the provider")
	}

	// Session B is submitted while A is inside the provider. Without the turn
	// lock both provider calls overlap, so max would be 2.
	second, err := agent.Run(ctx, "session-b", "second")
	if err != nil {
		t.Fatalf("Run(session-b): %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	peak := max
	mu.Unlock()
	if peak != 1 {
		t.Fatalf("concurrent model calls = %d, want 1 (Agent.Run must serialize eino turns)", peak)
	}

	close(release)
	collectEvents(t, first)
	collectEvents(t, second)

	mu.Lock()
	peak = max
	mu.Unlock()
	if peak != 1 {
		t.Fatalf("peak concurrent model calls = %d, want 1", peak)
	}
}

// TestInstructionFuncRecomposesPerTurn proves the dynamic runtime context is
// not frozen at build time: each turn calls InstructionFunc and the model
// receives the recomposed system prompt.
func TestInstructionFuncRecomposesPerTurn(t *testing.T) {
	m := &fakeChatModel{script: func(_ context.Context, _ int, _ []*schema.Message) ([]*schema.Message, error) {
		return []*schema.Message{{Role: schema.Assistant, Content: "ok"}}, nil
	}}
	instruction := "first instruction"
	agent := newTestAgent(t, Config{
		einoModel:       m,
		Instruction:     instruction,
		InstructionFunc: func() string { return instruction },
	})

	systemText := func(input []*schema.Message) string {
		for _, msg := range input {
			if msg.Role == schema.System {
				return msg.Content
			}
		}
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "s1", "one")
	if err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	collectEvents(t, ch)
	if got := systemText(m.inputAt(0)); !strings.Contains(got, "first instruction") {
		t.Fatalf("first turn system prompt = %q", got)
	}

	instruction = "second instruction"
	ch, err = agent.Run(ctx, "s1", "two")
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	collectEvents(t, ch)
	if got := agent.Instruction(); !strings.Contains(got, "second instruction") {
		t.Fatalf("agent instruction after second turn = %q", got)
	}
	if got := systemText(m.inputAt(1)); !strings.Contains(got, "second instruction") {
		t.Fatalf("second turn system prompt = %q", got)
	}
}

// failingOutputTool is a bash-like tool: it ran and produced output, then
// reported a non-zero exit as an error.
type failingOutputTool struct{}

func (failingOutputTool) Name() string        { return "bash" }
func (failingOutputTool) Description() string { return "Runs a command." }
func (failingOutputTool) Invoke(context.Context, json.RawMessage) (string, error) {
	return "visible command output\n[exit code: 3]\n", errors.New("bash: command exited with status 3")
}

// TestRunToolErrorKeepsOutputForModel covers the bash case: the error result
// must keep the command's own output and exit marker, and the model's next
// call must see it rather than a fatal [NodeRunError].
func TestRunToolErrorKeepsOutputForModel(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(failingOutputTool{}); err != nil {
		t.Fatal(err)
	}
	m := &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		if call == 0 {
			return []*schema.Message{{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID: "call-bash", Type: "function",
					Function: schema.FunctionCall{Name: "bash", Arguments: `{"command":"exit 3"}`},
				}},
			}}, nil
		}
		return []*schema.Message{{Role: schema.Assistant, Content: "The command failed; handling it."}}, nil
	}}
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-bash-fail", "run it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)
	if m.callCount() != 2 {
		t.Fatalf("model calls = %d, want 2", m.callCount())
	}
	end, ok := findToolEnd(events, "call-bash")
	if !ok {
		t.Fatalf("no ToolEnd; events: %#v", events)
	}
	for _, want := range []string{"visible command output", "[exit code: 3]", "[tool error]", "exited with status 3"} {
		if !strings.Contains(end.Result, want) {
			t.Fatalf("ToolEnd result %q missing %q", end.Result, want)
		}
	}
	if !end.IsError {
		t.Fatalf("ToolEnd IsError = false, want the error card")
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("tool failure produced a turn error %q", errText)
	}
	var fedBack string
	for _, msg := range m.inputAt(1) {
		if msg != nil && msg.Role == schema.Tool && msg.ToolCallID == "call-bash" {
			fedBack = msg.Content
		}
	}
	if !strings.Contains(fedBack, "visible command output") || !strings.Contains(fedBack, "exited with status 3") {
		t.Fatalf("fed-back result = %q", fedBack)
	}
}
