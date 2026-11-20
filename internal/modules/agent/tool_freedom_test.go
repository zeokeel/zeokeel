package agent

// Tool freedom (user ruling, v0.2.50): loop protection and every tool-loop
// configuration surface are removed — no hard breaker, no advisory nudge, no
// user budgets. Under the DEFAULT assembly a run executes every tool call the
// model asks for: 8 consecutive identical calls and 45 consecutive read-only
// calls complete normally, with no cancellation and no loop-guard text appended
// to any tool result the model sees (or the transcript shows).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// loopGuardTextMarkers are the removed guards' result texts: the advisory
// repeat reminder (gentle + detailed) and the hard breaker's error. None may
// appear in a tool result again.
var loopGuardTextMarkers = []string{
	"You are repeating the exact same tool call",
	"Repeated tool call detected:",
	"The repeated calls are not making progress",
	"loop guard:",
}

// identicalCallProvider requests the same echo call for `repeats` rounds and
// then returns a final answer — a legitimate (if stubborn) personal-agent loop
// far beyond any legacy repeat limit. It records every model request so tests
// can inspect the tool results the model actually saw.
type identicalCallProvider struct {
	mu       sync.Mutex
	calls    int
	repeats  int
	requests []rig.Request
}

func (p *identicalCallProvider) Name() string { return "identical-calls" }

func (p *identicalCallProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if call <= p.repeats {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: fmt.Sprintf("call-%d", call), Name: "echo", Arguments: `{"text":"same"}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

func (p *identicalCallProvider) recordedRequests() []rig.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]rig.Request(nil), p.requests...)
}

// readOnlyChainProvider requests `count` distinct read-only calls (no file ever
// changes) and then returns a final answer — e.g. a long research turn beyond
// any legacy no-diff limit. It records every model request.
type readOnlyChainProvider struct {
	mu       sync.Mutex
	calls    int
	count    int
	requests []rig.Request
}

func (p *readOnlyChainProvider) Name() string { return "read-only-chain" }

func (p *readOnlyChainProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if call <= p.count {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: fmt.Sprintf("call-%d", call), Name: "read_file", Arguments: fmt.Sprintf(`{"path":"f%d"}`, call)}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

func (p *readOnlyChainProvider) recordedRequests() []rig.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]rig.Request(nil), p.requests...)
}

// freeReadOnlyTool is a non-mutating read-only tool stub.
type freeReadOnlyTool struct{ calls atomic.Int32 }

func (t *freeReadOnlyTool) Name() string           { return "read_file" }
func (t *freeReadOnlyTool) Description() string    { return "Read a file (stub)." }
func (t *freeReadOnlyTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *freeReadOnlyTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	t.calls.Add(1)
	return rig.ToolResult{Content: "ok"}, nil
}

// assertNoLoopGuardText fails when any tool message of any recorded model
// request carries a removed guard's nudge or breaker text.
func assertNoLoopGuardText(t *testing.T, requests []rig.Request) {
	t.Helper()
	for index, request := range requests {
		for _, message := range request.Messages {
			if message.Role != rig.RoleTool {
				continue
			}
			for _, part := range message.Content {
				for _, marker := range loopGuardTextMarkers {
					if strings.Contains(part.Text, marker) {
						t.Fatalf("request %d tool result carries loop-guard text %q: %q",
							index+1, marker, part.Text)
					}
				}
			}
		}
	}
}

// assertFreeRunFinished checks the run completed with its final answer and no
// error or cancellation of any kind, and that no transcript tool summary carries
// a removed guard's text either.
func assertFreeRunFinished(t *testing.T, session contracts.ChatSession) {
	t.Helper()
	if len(session.Messages) == 0 {
		t.Fatal("no messages after the run")
	}
	last := session.Messages[len(session.Messages)-1]
	if last.Error != "" || last.ErrorKind != "" {
		t.Fatalf("default run did not complete freely: kind=%q error=%q", last.ErrorKind, last.Error)
	}
	if !strings.Contains(last.Text, "done") {
		t.Fatalf("final answer = %q, want the provider's done text", last.Text)
	}
	for _, message := range session.Messages {
		for _, tool := range message.Tools {
			for _, marker := range loopGuardTextMarkers {
				if strings.Contains(tool.Summary, marker) {
					t.Fatalf("tool %s summary carries loop-guard text %q: %q", tool.Name, marker, tool.Summary)
				}
			}
		}
	}
}

// TestDefaultRunIdenticalCallsFollowRigRepeatGuard documents the one behavior
// change the rig v0.24.0 upgrade brings to tool freedom: rig's agent loop now
// hardcodes its own repeat-call guard — a notice after 3 identical rounds and
// "rig: repeated call blocked" errors from the 5th — and exposes no AgentSpec
// knob to switch it off. The product's 2026-11 "no nudge, no breaker" stance
// therefore still holds for distinct calls (see the 45-call read-only chain
// below), but identical repeats are bounded by rig. Revisit when rig ships a
// knob; until then this test pins the upstream behavior instead of pretending
// the product controls it.
func TestDefaultRunIdenticalCallsFollowRigRepeatGuard(t *testing.T) {
	provider := &identicalCallProvider{repeats: 8}
	tool := &echoTool{}
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil, tool)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("bounded repeat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "go"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	assertFreeRunFinished(t, session)
	// Rounds 1-4 execute; the 5th and later identical calls are blocked by rig.
	if got := tool.calls.Load(); got != 4 {
		t.Fatalf("echo calls = %d, want 4 (rig blocks the 5th identical round)", got)
	}
	var transcriptBuilder strings.Builder
	for _, request := range provider.recordedRequests() {
		transcriptBuilder.WriteString(rigMessagesText(request.Messages))
	}
	transcript := transcriptBuilder.String()
	if !strings.Contains(transcript, "rig: notice: you have called") {
		t.Fatalf("no repeat notice in the conversation:\n%s", transcript)
	}
	if !strings.Contains(transcript, "rig: repeated call blocked") {
		t.Fatalf("no blocked-call error in the conversation:\n%s", transcript)
	}
}

// TestDefaultRunReadOnlyChainCompletesWithoutNudge pins the second chain: under
// the DEFAULT configuration, 45 consecutive non-mutating calls run to completion
// with no cancellation and no loop-guard text in any tool result.
func TestDefaultRunReadOnlyChainCompletesWithoutNudge(t *testing.T) {
	provider := &readOnlyChainProvider{count: 45}
	tool := &freeReadOnlyTool{}
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil, tool)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("free research")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "go"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	assertFreeRunFinished(t, session)
	if got := tool.calls.Load(); got != 45 {
		t.Fatalf("read_file calls = %d, want 45 (every call executed)", got)
	}
	assertNoLoopGuardText(t, provider.recordedRequests())
}
