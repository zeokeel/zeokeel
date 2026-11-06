package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// flushProvider streams one chunk with usage and then keeps the stream open
// until cancellation, so a test can cancel after the usage sample was received
// but before the stream settles.
type flushProvider struct {
	sent chan struct{}
	once sync.Once
}

func (p *flushProvider) Stream(ctx context.Context, _ Request) (<-chan Chunk, error) {
	out := make(chan Chunk)
	go func() {
		defer close(out)
		select {
		case out <- Chunk{
			Text:  "partial answer",
			Usage: &Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7},
		}:
		case <-ctx.Done():
			return
		}
		p.once.Do(func() { close(p.sent) })
		<-ctx.Done()
	}()
	return out, nil
}

// TestTrailingUsageFlushedOnCancel pins P1-1: a provider that already reported
// usage before the stream was cancelled still counts that call (step + tokens)
// exactly once, through the terminal emit path.
func TestTrailingUsageFlushedOnCancel(t *testing.T) {
	p := &flushProvider{sent: make(chan struct{})}
	agent := newTestAgent(t, Config{Provider: p})
	ctx, cancel := context.WithCancel(context.Background())
	ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ch, err := agent.Run(ctx, "session-trailing", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Drain concurrently and cancel once the runtime has consumed the chunk
	// (a token event proves the usage sample reached consumeAssistantStream).
	var (
		mu     sync.Mutex
		events []state.Event
	)
	sawToken := make(chan struct{}, 1)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range ch {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
			if _, ok := ev.(state.Token); ok {
				select {
				case sawToken <- struct{}{}:
				default:
				}
			}
		}
	}()
	select {
	case <-sawToken:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime never consumed the chunk")
	}
	cancel()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("events channel did not close after cancellation")
	}
	mu.Lock()
	defer mu.Unlock()

	total, ok := sumUsage(events)
	if !ok || total.Turns != 1 || total.Steps != 1 || total.In != 5 || total.Out != 2 {
		t.Fatalf("trailing usage = %+v, ok=%v; want the cancelled call's usage flushed once", total, ok)
	}
	// Exactly one usage event: the normal path and the cancel flush must not
	// double count.
	usageEvents := 0
	for _, ev := range events {
		if _, ok := ev.(state.Usage); ok {
			usageEvents++
		}
	}
	if usageEvents != 1 {
		t.Fatalf("usage events = %d, want exactly 1", usageEvents)
	}
	last := events[len(events)-1]
	if st, ok := last.(state.Status); !ok || st.Phase != state.PhaseIdle || st.Reason != state.ReasonStopped {
		t.Fatalf("last event = %#v, want idle/stopped", last)
	}
}

// requestContainsAdvisory reports whether the request's tool messages carry the
// given advisory text.
func requestContainsAdvisory(req Request, needle string) bool {
	for _, msg := range req.Messages {
		if msg.Role == RoleTool && strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

// TestBudgetAdvisorySoftWarnsOnly pins P1-3: crossing the configured step
// threshold injects a cost advisory into the next tool result (for the model)
// and into the usage delta (for the footer), but the turn keeps running to a
// normal completion.
func TestBudgetAdvisorySoftWarnsOnly(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatalf("register clock: %v", err)
	}
	call := 0
	var mu sync.Mutex
	p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		mu.Lock()
		defer mu.Unlock()
		call++
		if call > 3 {
			return []Chunk{{Text: "finished", Done: true}}, nil
		}
		return []Chunk{{
			ToolCalls: []ToolCall{{
				ID: "call-" + string(rune('a'+call)), Name: "clock",
				Arguments: json.RawMessage(`{"i":` + string(rune('0'+call)) + `}`),
			}},
			Done: true,
		}}, nil
	}}
	agent := newTestAgent(t, Config{
		Provider:         p,
		Tools:            reg,
		BudgetWarnSteps:  []int{2},
		BudgetWarnTokens: []int{}, // tokens dimension disabled for this test
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-budget", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	// The request built after the 2nd completed model call carries the
	// advisory in its tool result; earlier requests must not.
	req, ok := p.requestAt(2)
	if !ok {
		t.Fatal("provider never saw the request after the threshold")
	}
	if !requestContainsAdvisory(req, "Cost advisory") {
		t.Fatalf("request after budget crossing has no advisory: %+v", req.Messages)
	}
	for i := 0; i < 2; i++ {
		if earlier, ok := p.requestAt(i); ok && requestContainsAdvisory(earlier, "Cost advisory") {
			t.Fatalf("request %d warned before the threshold: %+v", i, earlier.Messages)
		}
	}
	// The footer-facing field rides the usage delta.
	found := false
	for _, ev := range events {
		if u, ok := ev.(state.Usage); ok && strings.Contains(u.Advisory, "Cost advisory") {
			found = true
		}
	}
	if !found {
		t.Fatal("usage delta carried no budget advisory for the footer")
	}
	// Soft only: the turn completes idle with no failure.
	if got := phases(events); len(got) == 0 || got[len(got)-1] != state.PhaseIdle {
		t.Fatalf("final phase = %v, want idle", got)
	}
	if text := findErrorText(events); text != "" {
		t.Fatalf("budget advisory produced an error event: %q", text)
	}
}

// TestBudgetAdvisoryDefaultThresholdsNotCrossed pins that ordinary short turns
// never receive an advisory with the default thresholds.
func TestBudgetAdvisoryDefaultThresholdsNotCrossed(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatalf("register clock: %v", err)
	}
	p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		return []Chunk{{Text: "done", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-short", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)
	for _, ev := range events {
		if u, ok := ev.(state.Usage); ok && u.Advisory != "" {
			t.Fatalf("short turn received an advisory: %+v", u)
		}
	}
}

// TestRepeatedToolReminder pins P1-4: identical tool calls (name + exact
// arguments) raise a reminder at 3 and 5 (and 8), while different arguments
// never do.
func TestRepeatedToolReminder(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatalf("register clock: %v", err)
	}

	run := func(t *testing.T, repeated int, identicalArgs bool) ([]Request, []state.Event) {
		t.Helper()
		call := 0
		var mu sync.Mutex
		p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
			mu.Lock()
			defer mu.Unlock()
			call++
			if call > repeated {
				return []Chunk{{Text: "finished", Done: true}}, nil
			}
			args := `{"same":true}`
			if !identicalArgs {
				args = `{"n":` + string(rune('0'+call)) + `}`
			}
			return []Chunk{{
				ToolCalls: []ToolCall{{ID: "call-" + string(rune('a'+call)), Name: "clock", Arguments: json.RawMessage(args)}},
				Done:      true,
			}}, nil
		}}
		agent := newTestAgent(t, Config{
			Provider:         p,
			Tools:            reg,
			BudgetWarnSteps:  []int{},
			BudgetWarnTokens: []int{},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ch, err := agent.Run(ctx, "session-repeat", "hi")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		events := collectEvents(t, ch)
		reqs := make([]Request, 0, repeated+1)
		for i := 0; i <= repeated; i++ {
			if req, ok := p.requestAt(i); ok {
				reqs = append(reqs, req)
			}
		}
		return reqs, events
	}

	// Six identical calls: the request after the 3rd and the 5th call carries a
	// reminder (the request after the 8th is never reached here).
	reqs, events := run(t, 6, true)
	for _, idx := range []int{3, 5} {
		if idx >= len(reqs) {
			t.Fatalf("missing request %d", idx)
		}
		if !requestContainsAdvisory(reqs[idx], "Repeated tool call advisory") {
			t.Fatalf("request %d after identical repeats has no reminder: %+v", idx, reqs[idx].Messages)
		}
	}
	for _, idx := range []int{1, 2, 4} {
		if idx < len(reqs) && requestContainsAdvisory(reqs[idx], "Repeated tool call advisory") {
			t.Fatalf("request %d warned before the 3rd repeat: %+v", idx, reqs[idx].Messages)
		}
	}
	if got := phases(events); len(got) == 0 || got[len(got)-1] != state.PhaseIdle {
		t.Fatalf("repeat reminder changed the turn outcome: %v", got)
	}

	// Different arguments never count as a repeat.
	reqs, _ = run(t, 6, false)
	for i, req := range reqs {
		if requestContainsAdvisory(req, "Repeated tool call advisory") {
			t.Fatalf("request %d warned for different arguments: %+v", i, req.Messages)
		}
	}
}
