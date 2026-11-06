package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// blockTool waits until its context ends so a test can observe a turn that is
// mid-tool when it cancels.
type blockTool struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockTool) Name() string        { return "block" }
func (b *blockTool) Description() string { return "Blocks until cancelled." }
func (b *blockTool) Invoke(ctx context.Context, _ json.RawMessage) (string, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return "", ctx.Err()
}

// toolCallChunk scripts one model call that asks for a tool.
func toolCallChunk(callID, name string) []Chunk {
	return []Chunk{{
		ToolCalls: []ToolCall{{ID: callID, Name: name, Arguments: []byte(`{}`)}},
		Done:      true,
	}}
}

// TestRunPublishesUsageDeltaPerCall pins the live contract: every completed
// model call publishes a usage delta on the cumulative axis, the first one
// counts the turn, and the terminal status still comes last.
func TestRunPublishesUsageDeltaPerCall(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatalf("register clock: %v", err)
	}
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		// The second request is the follow-up after the tool result; the first
		// one is the tool call itself.
		if len(req.Messages) <= 2 {
			return toolCallChunk("call-1", "clock"), nil
		}
		return []Chunk{{
			Text: "done",
			Usage: &Usage{
				InputTokens: 10, OutputTokens: 4, CachedInputTokens: 6,
			},
			Done: true,
		}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-usage-live", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	var usage []state.Usage
	var usageAt []int
	for i, ev := range events {
		if u, ok := ev.(state.Usage); ok {
			usage = append(usage, u)
			usageAt = append(usageAt, i)
		}
	}
	if len(usage) != 2 {
		t.Fatalf("usage events = %d (%+v), want one per completed model call", len(usage), usage)
	}
	if usage[0].Turns != 1 || usage[0].Steps != 1 {
		t.Fatalf("first delta = %+v, want the turn counted once and one step", usage[0])
	}
	if usage[1].Turns != 0 || usage[1].Steps != 1 || usage[1].In != 10 || usage[1].Out != 4 || usage[1].CacheHit != 6 {
		t.Fatalf("second delta = %+v, want only the new step and tokens", usage[1])
	}
	total, ok := sumUsage(events)
	if !ok || total.Turns != 1 || total.Steps != 2 || total.In != 10 || total.Out != 4 || total.CacheHit != 6 {
		t.Fatalf("cumulative usage = %+v, ok=%v", total, ok)
	}
	last := events[len(events)-1]
	if st, ok := last.(state.Status); !ok || st.Phase != state.PhaseIdle || st.Reason != state.ReasonNone {
		t.Fatalf("last event = %#v, want a completed idle Status", last)
	}
	// Every delta arrives before the terminal status: the footer updates while
	// the turn is still running.
	for _, at := range usageAt {
		if at >= len(events)-1 {
			t.Fatalf("usage event at %d is not before the terminal status", at)
		}
	}
}

// TestRunCancelClosesPendingToolAndReportsStopped covers the Stop path: the
// completed model calls already published their usage, the open tool call is
// closed as cancelled (no spinning card), and the turn ends stopped.
func TestRunCancelClosesPendingToolAndReportsStopped(t *testing.T) {
	block := &blockTool{started: make(chan struct{})}
	reg := NewRegistry()
	if err := reg.Register(block); err != nil {
		t.Fatalf("register block: %v", err)
	}
	p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		return toolCallChunk("call-block", "block"), nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := agent.Run(ctx, "session-cancel-tool", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Drain the stream concurrently and wait for the first model call's usage
	// delta before cancelling: the tool can start before the trailing usage
	// chunk is consumed, and a cancel at that instant would (correctly) drop it.
	var (
		mu     sync.Mutex
		events []state.Event
	)
	usageSeen := make(chan struct{}, 1)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range ch {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
			if _, ok := ev.(state.Usage); ok {
				select {
				case usageSeen <- struct{}{}:
				default:
				}
			}
		}
	}()
	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("block tool never started")
	}
	select {
	case <-usageSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("no usage delta before cancellation")
	}
	cancel()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("events channel did not close after cancellation")
	}
	mu.Lock()
	defer mu.Unlock()

	end, ok := findToolEnd(events, "call-block")
	if !ok || !end.Cancelled || end.IsError {
		t.Fatalf("ToolEnd = %+v, ok=%v; want a cancelled (not error) close", end, ok)
	}
	if !strings.Contains(end.Result, "context canceled") {
		t.Fatalf("cancelled tool result = %q, want the cancellation cause", end.Result)
	}
	total, ok := sumUsage(events)
	if !ok || total.Turns != 1 || total.Steps != 1 {
		t.Fatalf("cancelled turn usage = %+v, ok=%v; want the completed call counted", total, ok)
	}
	last := events[len(events)-1]
	if st, ok := last.(state.Status); !ok || st.Phase != state.PhaseIdle || st.Reason != state.ReasonStopped {
		t.Fatalf("last event = %#v, want idle/stopped", last)
	}
	if text := findErrorText(events); text != "" {
		t.Fatalf("cancelled turn produced error event %q", text)
	}
}

// TestRunFailureKeepsPartialUsage covers a real failure after work: the usage
// of the completed model call is already on the stream, and the turn terminates
// as failed with the reason recorded.
func TestRunFailureKeepsPartialUsage(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: "now"}); err != nil {
		t.Fatalf("register clock: %v", err)
	}
	calls := 0
	var mu sync.Mutex
	p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		mu.Lock()
		call := calls
		calls++
		mu.Unlock()
		if call == 0 {
			return toolCallChunk("call-1", "clock"), nil
		}
		return []Chunk{{Err: errors.New("provider exploded"), Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-fail-usage", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	total, ok := sumUsage(events)
	if !ok || total.Turns != 1 || total.Steps != 1 {
		t.Fatalf("failed turn usage = %+v, ok=%v; want the completed call kept", total, ok)
	}
	last := events[len(events)-1]
	if st, ok := last.(state.Status); ok {
		t.Fatalf("last event is Status %#v; the Error event must follow the failed status", st)
	}
	var failed *state.Status
	for i := len(events) - 1; i >= 0; i-- {
		if st, ok := events[i].(state.Status); ok {
			failed = &st
			break
		}
	}
	if failed == nil || failed.Phase != state.PhaseFailed || failed.Reason != state.ReasonFailed {
		t.Fatalf("failed status = %#v, want failed/failed", failed)
	}
	if text := findErrorText(events); !strings.Contains(text, "provider exploded") {
		t.Fatalf("error event = %q", text)
	}
}
