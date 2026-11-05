package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/state"
)

// countingReviewer records how often the runtime consulted it and returns a
// fixed decision. Tests use it to prove the hook is (or is not) reached.
type countingReviewer struct {
	calls  atomic.Int32
	allow  bool
	reason string
	err    error
	mu     sync.Mutex
	names  []string
	args   []json.RawMessage
}

func (r *countingReviewer) review(_ context.Context, name string, args json.RawMessage) (bool, string, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.names = append(r.names, name)
	r.args = append(r.args, append(json.RawMessage(nil), args...))
	r.mu.Unlock()
	return r.allow, r.reason, r.err
}

func (r *countingReviewer) callCount() int32 { return r.calls.Load() }

// TestAutoReviewAllowsToolWithoutPrompt proves the allow path: the reviewer is
// consulted for a call the Gate would otherwise prompt for, no
// state.ApprovalRequest is published, and the tool executes immediately.
func TestAutoReviewAllowsToolWithoutPrompt(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "clock"
	})
	reviewer := &countingReviewer{allow: true, reason: "safe read-only call"}
	agent := newTestAgent(t, Config{
		Provider:     scriptedToolProvider(t),
		Tools:        reg,
		Gate:         gate,
		ToolReviewer: reviewer.review,
	})

	events := runTurn(t, agent, "session-auto-review-allow", "what time is it?")
	for _, ev := range events {
		if _, ok := ev.(state.ApprovalRequest); ok {
			t.Fatalf("auto-review allow published an ApprovalRequest: %#v", ev)
		}
	}
	if got := reviewer.callCount(); got != 1 {
		t.Fatalf("reviewer calls = %d, want 1", got)
	}
	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.callCount())
	}
	end, ok := findToolEnd(events, "call-1")
	if !ok || end.IsError || end.Result != clock.result {
		t.Fatalf("ToolEnd = %+v, ok=%v", end, ok)
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "It is 12:00." {
		t.Fatalf("assistant text = %q", got)
	}
}

// TestAutoReviewDeniesToolWithReason proves the deny path: the tool never
// executes, no ApprovalRequest is published, the model receives an error
// carrying the reviewer's reason, and the turn continues.
func TestAutoReviewDeniesToolWithReason(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "clock"
	})
	const reason = "the command deletes the workspace"
	reviewer := &countingReviewer{allow: false, reason: reason}
	provider := scriptedToolProvider(t)
	agent := newTestAgent(t, Config{
		Provider:     provider,
		Tools:        reg,
		Gate:         gate,
		ToolReviewer: reviewer.review,
	})

	events := runTurn(t, agent, "session-auto-review-deny", "what time is it?")
	for _, ev := range events {
		if _, ok := ev.(state.ApprovalRequest); ok {
			t.Fatalf("auto-review deny published an ApprovalRequest: %#v", ev)
		}
	}
	if clock.callCount() != 0 {
		t.Fatalf("denied tool ran %d times, want 0", clock.callCount())
	}
	if got := reviewer.callCount(); got != 1 {
		t.Fatalf("reviewer calls = %d, want 1", got)
	}
	end, ok := findToolEnd(events, "call-1")
	if !ok {
		t.Fatalf("no ToolEnd for denied call; events: %#v", events)
	}
	if !end.IsError {
		t.Fatalf("denied call ToolEnd is not an error: %+v", end)
	}
	if !strings.Contains(end.Result, "auto review denied: "+reason) {
		t.Fatalf("ToolEnd result = %q, want the auto-review denial and reason", end.Result)
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("denied tool produced a turn error: %q", errText)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "It is 12:00." {
		t.Fatalf("assistant text after denial = %q", got)
	}
	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("provider got no second request")
	}
	var fedBack string
	for _, msg := range second.Messages {
		if msg.Role == RoleTool && msg.ToolCallID == "call-1" {
			fedBack = msg.Content
		}
	}
	if !strings.Contains(fedBack, "auto review denied: "+reason) {
		t.Fatalf("model tool result = %q, want the denial", fedBack)
	}
}

// TestAutoReviewReviewerErrorFallsBackToApproval proves a reviewer failure is
// not treated as an allow or a deny: the runtime publishes the ordinary
// ApprovalRequest and the human decision still controls the call.
func TestAutoReviewReviewerErrorFallsBackToApproval(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "clock"
	})
	reviewer := &countingReviewer{err: errors.New("review provider unavailable")}
	agent := newTestAgent(t, Config{
		Provider:     scriptedToolProvider(t),
		Tools:        reg,
		Gate:         gate,
		ToolReviewer: reviewer.review,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-auto-review-error", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawRequest bool
	events := collectEventsWith(t, ch, func(ev state.Event) {
		req, ok := ev.(state.ApprovalRequest)
		if !ok {
			return
		}
		sawRequest = true
		gate.Approve(req.CallID)
	})

	if !sawRequest {
		t.Fatal("reviewer error did not fall back to an ApprovalRequest")
	}
	if got := reviewer.callCount(); got != 1 {
		t.Fatalf("reviewer calls = %d, want 1", got)
	}
	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want the approved call to run once", clock.callCount())
	}
	end, ok := findToolEnd(events, "call-1")
	if !ok || end.IsError || end.Result != clock.result {
		t.Fatalf("ToolEnd = %+v, ok=%v", end, ok)
	}
}

// TestAutoReviewBypassesReadOnlyAndFullAccess proves the reviewer only sees
// calls the Gate actually requires: a read-only tool call bypasses it, and a
// full-access session (whose Gate requires nothing) bypasses it too.
func TestAutoReviewBypassesReadOnlyAndFullAccess(t *testing.T) {
	t.Run("read-only", func(t *testing.T) {
		clock := &fakeClock{result: "now"}
		reg := NewRegistry()
		if err := reg.Register(clock); err != nil {
			t.Fatal(err)
		}
		// Only a mutating tool needs approval; clock is the read-only call the
		// model makes and must run without a review or a prompt.
		gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
			return name == "write_file"
		})
		reviewer := &countingReviewer{allow: false, reason: "must not run"}
		agent := newTestAgent(t, Config{
			Provider: scriptedToolProvider(t), Tools: reg, Gate: gate,
			ToolReviewer: reviewer.review,
		})
		events := runTurn(t, agent, "session-auto-review-read-only", "what time is it?")
		if got := reviewer.callCount(); got != 0 {
			t.Fatalf("read-only call consulted the reviewer %d times, want 0", got)
		}
		if clock.callCount() != 1 {
			t.Fatalf("read-only clock calls = %d, want 1", clock.callCount())
		}
		for _, ev := range events {
			if _, ok := ev.(state.ApprovalRequest); ok {
				t.Fatalf("read-only call produced an ApprovalRequest: %#v", ev)
			}
		}
	})

	t.Run("full-access", func(t *testing.T) {
		clock := &fakeClock{result: "now"}
		reg := NewRegistry()
		if err := reg.Register(clock); err != nil {
			t.Fatal(err)
		}
		// Full access is the app's Gate with a predicate that requires nothing.
		gate := NewApprovalQueue(func(string, json.RawMessage) bool { return false })
		reviewer := &countingReviewer{allow: false, reason: "must not run"}
		agent := newTestAgent(t, Config{
			Provider: scriptedToolProvider(t), Tools: reg, Gate: gate,
			ToolReviewer: reviewer.review,
		})
		events := runTurn(t, agent, "session-auto-review-full", "what time is it?")
		if got := reviewer.callCount(); got != 0 {
			t.Fatalf("full-access call consulted the reviewer %d times, want 0", got)
		}
		if clock.callCount() != 1 {
			t.Fatalf("full-access clock calls = %d, want 1", clock.callCount())
		}
		for _, ev := range events {
			if _, ok := ev.(state.ApprovalRequest); ok {
				t.Fatalf("full-access call produced an ApprovalRequest: %#v", ev)
			}
		}
	})
}

// TestNoToolReviewerPreservesApproval proves the nil-reviewer path is exactly
// the old behavior: the call prompts instead of being silently decided.
func TestNoToolReviewerPreservesApproval(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "clock"
	})
	agent := newTestAgent(t, Config{Provider: scriptedToolProvider(t), Tools: reg, Gate: gate})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-no-reviewer", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawRequest bool
	events := collectEventsWith(t, ch, func(ev state.Event) {
		req, ok := ev.(state.ApprovalRequest)
		if !ok {
			return
		}
		sawRequest = true
		gate.Deny(req.CallID)
	})
	if !sawRequest {
		t.Fatal("nil reviewer removed the approval prompt")
	}
	if clock.callCount() != 0 {
		t.Fatalf("denied clock calls = %d, want 0", clock.callCount())
	}
	end, ok := findToolEnd(events, "call-1")
	if !ok || !end.IsError || !strings.Contains(end.Result, "denied by user") {
		t.Fatalf("ToolEnd = %+v, ok=%v", end, ok)
	}
}

func TestParseToolReviewDecision(t *testing.T) {
	cases := []struct {
		text     string
		allow    bool
		reason   string
		wantErr  bool
		contains string
	}{
		{text: "ALLOW: reads a workspace file", allow: true, reason: "reads a workspace file"},
		{text: "DENY: writes outside the workspace", allow: false, reason: "writes outside the workspace"},
		{text: "  allow - safe", allow: true, reason: "safe"},
		{text: "**DENY**: credential exfiltration", allow: false, reason: "credential exfiltration"},
		{text: "ALLOWED: not a decision", wantErr: true},
		{text: "I cannot decide", wantErr: true},
		{text: "", wantErr: true},
	}
	for _, tc := range cases {
		allow, reason, err := ParseToolReviewDecision(tc.text)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseToolReviewDecision(%q) err = nil, want error", tc.text)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseToolReviewDecision(%q): %v", tc.text, err)
		}
		if allow != tc.allow || reason != tc.reason {
			t.Fatalf("ParseToolReviewDecision(%q) = (%v, %q), want (%v, %q)",
				tc.text, allow, reason, tc.allow, tc.reason)
		}
	}
}

func TestLLMToolReviewerSendsToolPayloadAndParsesVerdict(t *testing.T) {
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return []Chunk{{Text: "DENY: deletes the workspace", Done: true}}, nil
	}}
	reviewer := LLMToolReviewer{Provider: provider, Model: "review-model", Timeout: time.Second}
	allow, reason, err := reviewer.Review(context.Background(), "bash", json.RawMessage(`{"command":"rm -rf ."}`))
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if allow || reason != "deletes the workspace" {
		t.Fatalf("Review = (%v, %q), want denied with reason", allow, reason)
	}
	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("reviewer made no provider request")
	}
	if req.Model != "review-model" {
		t.Fatalf("review request model = %q", req.Model)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != RoleSystem || req.Messages[0].Content != ToolReviewSystemPrompt {
		t.Fatalf("review system message = %+v", req.Messages)
	}
	if !strings.Contains(req.Messages[1].Content, `"tool":"bash"`) ||
		!strings.Contains(req.Messages[1].Content, `"command":"rm -rf ."`) {
		t.Fatalf("review payload = %q", req.Messages[1].Content)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("review request advertised tools: %+v", req.Tools)
	}
}

func TestLLMToolReviewerFailuresAreErrors(t *testing.T) {
	t.Run("no provider", func(t *testing.T) {
		reviewer := LLMToolReviewer{}
		if _, _, err := reviewer.Review(context.Background(), "bash", nil); err == nil {
			t.Fatal("reviewer without provider returned no error")
		}
	})
	t.Run("unparseable reply", func(t *testing.T) {
		provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "I am not sure", Done: true}}, nil
		}}
		reviewer := LLMToolReviewer{Provider: provider, Timeout: time.Second}
		if _, _, err := reviewer.Review(context.Background(), "bash", nil); err == nil {
			t.Fatal("unparseable reviewer reply returned no error")
		}
	})
	t.Run("provider error", func(t *testing.T) {
		provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return nil, errors.New("upstream down")
		}}
		reviewer := LLMToolReviewer{Provider: provider, Timeout: time.Second}
		if _, _, err := reviewer.Review(context.Background(), "bash", nil); err == nil {
			t.Fatal("provider error returned no reviewer error")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		provider := &fakeProvider{fn: func(ctx context.Context, _ Request) ([]Chunk, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		reviewer := LLMToolReviewer{Provider: provider, Timeout: 20 * time.Millisecond}
		if _, _, err := reviewer.Review(context.Background(), "bash", nil); err == nil {
			t.Fatal("timed-out reviewer returned no error")
		}
	})
}

// TestAutoReviewBindsNestedPTCTool proves the PTC batch tool receives the same
// reviewer as direct calls, so a nested mutating call cannot bypass automatic
// review just because the outer batch was approved.
func TestAutoReviewBindsNestedPTCTool(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	ptc := NewPTCTool(reg)
	if err := reg.Register(ptc); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "clock"
	})
	reviewer := &countingReviewer{allow: false, reason: "nested tool deletes data"}
	_ = newTestAgent(t, Config{
		Provider: scriptedToolProvider(t), Tools: reg, Gate: gate,
		ToolReviewer: reviewer.review,
	})

	batch, ok := ptc.(*ptcTool)
	if !ok {
		t.Fatalf("NewPTCTool returned %T", ptc)
	}
	if batch.reviewer == nil || batch.gate == nil {
		t.Fatal("PTC tool did not receive the gate and reviewer")
	}
	err := batch.awaitNestedApproval(context.Background(), "clock", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "auto review denied: nested tool deletes data") {
		t.Fatalf("nested denied error = %v, want the auto-review denial", err)
	}
	if clock.callCount() != 0 {
		t.Fatalf("nested denied tool ran %d times, want 0", clock.callCount())
	}
	if got := reviewer.callCount(); got != 1 {
		t.Fatalf("nested reviewer calls = %d, want 1", got)
	}
}
