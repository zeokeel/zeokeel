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

// countingReviewer records how often the runtime consulted it and what input it
// received, and returns a fixed decision. Tests use it to prove the hook is (or
// is not) reached and that the reviewer gets the turn's context.
type countingReviewer struct {
	calls  atomic.Int32
	allow  bool
	reason string
	err    error
	mu     sync.Mutex
	names  []string
	args   []json.RawMessage
	inputs []ToolReviewInput
}

func (r *countingReviewer) review(_ context.Context, in ToolReviewInput) (bool, string, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.names = append(r.names, in.Tool)
	r.args = append(r.args, append(json.RawMessage(nil), in.Args...))
	r.inputs = append(r.inputs, in)
	r.mu.Unlock()
	return r.allow, r.reason, r.err
}

func (r *countingReviewer) callCount() int32 { return r.calls.Load() }

// reviewedInput returns the input of the i-th review (zero value when absent).
func (r *countingReviewer) reviewedInput(i int) ToolReviewInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.inputs) {
		return ToolReviewInput{}
	}
	return r.inputs[i]
}

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

// TestAutoReviewInputCarriesTurnContext proves the reviewer is not blind: it
// receives the user request that authorized the turn, the agent's current
// working directory and the project constraints, next to the pending action.
func TestAutoReviewInputCarriesTurnContext(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "clock"
	})
	reviewer := &countingReviewer{allow: false, reason: "deletes generated output"}
	agent := newTestAgent(t, Config{
		Provider:     scriptedToolProvider(t),
		Tools:        reg,
		Gate:         gate,
		ToolReviewer: reviewer.review,
		ReviewContext: func() (string, string) {
			return "/workspace/project", "# Project rules\nNever touch production."
		},
	})

	const prompt = "delete the generated build directory"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-auto-review-context", prompt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEventsWith(t, ch, func(ev state.Event) {
		if req, ok := ev.(state.ApprovalRequest); ok {
			gate.Approve(req.CallID)
		}
	})
	if got := reviewer.callCount(); got != 1 {
		t.Fatalf("reviewer calls = %d, want 1", got)
	}
	in := reviewer.reviewedInput(0)
	if in.Tool != "clock" {
		t.Fatalf("reviewer tool = %q, want clock", in.Tool)
	}
	if string(in.Args) != `{}` {
		t.Fatalf("reviewer args = %s, want the pending call arguments", in.Args)
	}
	if in.UserRequest != prompt {
		t.Fatalf("reviewer user request = %q, want %q", in.UserRequest, prompt)
	}
	if in.WorkDir != "/workspace/project" {
		t.Fatalf("reviewer workdir = %q, want /workspace/project", in.WorkDir)
	}
	if !strings.Contains(in.Constraints, "Never touch production") {
		t.Fatalf("reviewer constraints = %q, want the project constraints", in.Constraints)
	}
}

// TestAutoReviewDenyFallsBackToUser proves a reviewer denial is a recommendation,
// not a final rejection (DSH's ask policy): the runtime publishes an
// ApprovalRequest carrying the reviewer's reason, and the user's decision
// controls the call. Allow runs the tool; Deny rejects it with the ordinary
// user-rejection tool error the model can adapt to.
func TestAutoReviewDenyFallsBackToUser(t *testing.T) {
	const reason = "the command deletes the workspace"

	t.Run("user allows", func(t *testing.T) {
		clock := &fakeClock{result: "now"}
		reg := NewRegistry()
		if err := reg.Register(clock); err != nil {
			t.Fatal(err)
		}
		gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
			return name == "clock"
		})
		reviewer := &countingReviewer{allow: false, reason: reason}
		agent := newTestAgent(t, Config{
			Provider:     scriptedToolProvider(t),
			Tools:        reg,
			Gate:         gate,
			ToolReviewer: reviewer.review,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ch, err := agent.Run(ctx, "session-auto-review-user-allows", "what time is it?")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		var request state.ApprovalRequest
		events := collectEventsWith(t, ch, func(ev state.Event) {
			req, ok := ev.(state.ApprovalRequest)
			if !ok {
				return
			}
			request = req
			gate.Approve(req.CallID)
		})
		if request.CallID == "" {
			t.Fatal("reviewer denial did not fall back to an ApprovalRequest")
		}
		if request.ReviewReason != reason {
			t.Fatalf("ApprovalRequest.ReviewReason = %q, want %q", request.ReviewReason, reason)
		}
		if got := reviewer.callCount(); got != 1 {
			t.Fatalf("reviewer calls = %d, want 1", got)
		}
		if clock.callCount() != 1 {
			t.Fatalf("user-approved tool calls = %d, want 1", clock.callCount())
		}
		end, ok := findToolEnd(events, request.CallID)
		if !ok || end.IsError || end.Result != clock.result {
			t.Fatalf("ToolEnd = %+v, ok=%v; want the approved call to succeed", end, ok)
		}
	})

	t.Run("user denies", func(t *testing.T) {
		clock := &fakeClock{result: "now"}
		reg := NewRegistry()
		if err := reg.Register(clock); err != nil {
			t.Fatal(err)
		}
		gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool {
			return name == "clock"
		})
		reviewer := &countingReviewer{allow: false, reason: reason}
		provider := scriptedToolProvider(t)
		agent := newTestAgent(t, Config{
			Provider:     provider,
			Tools:        reg,
			Gate:         gate,
			ToolReviewer: reviewer.review,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ch, err := agent.Run(ctx, "session-auto-review-user-denies", "what time is it?")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		var request state.ApprovalRequest
		events := collectEventsWith(t, ch, func(ev state.Event) {
			req, ok := ev.(state.ApprovalRequest)
			if !ok {
				return
			}
			request = req
			gate.Deny(req.CallID)
		})
		if request.CallID == "" {
			t.Fatal("reviewer denial did not fall back to an ApprovalRequest")
		}
		if request.ReviewReason != reason {
			t.Fatalf("ApprovalRequest.ReviewReason = %q, want %q", request.ReviewReason, reason)
		}
		if clock.callCount() != 0 {
			t.Fatalf("user-denied tool ran %d times, want 0", clock.callCount())
		}
		end, ok := findToolEnd(events, request.CallID)
		if !ok || !end.IsError || !strings.Contains(end.Result, "denied by user") {
			t.Fatalf("ToolEnd = %+v, ok=%v; want the user denial error", end, ok)
		}
		fedBack := toolResultFor(provider, request.CallID)
		if !strings.Contains(fedBack, "denied by user") {
			t.Fatalf("model tool result = %q, want the user denial", fedBack)
		}
	})
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

func TestLLMToolReviewerSendsContextPayloadAndParsesVerdict(t *testing.T) {
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return []Chunk{{Text: "DENY: deletes the workspace", Done: true}}, nil
	}}
	reviewer := LLMToolReviewer{Provider: provider, Model: "review-model", Timeout: time.Second}
	allow, reason, err := reviewer.Review(context.Background(), ToolReviewInput{
		Tool:        "bash",
		Args:        json.RawMessage(`{"command":"rm -rf ."}`),
		WorkDir:     "/workspace/project",
		UserRequest: "clean up the build output",
		Constraints: "# Rules\nKeep the workspace tidy.",
	})
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
	payload := req.Messages[1].Content
	for _, want := range []string{
		`"cwd":"/workspace/project"`,
		`"user_request":"clean up the build output"`,
		`"project_constraints":"# Rules\nKeep the workspace tidy."`,
		`"pending_action":{"tool":"bash","args":{"command":"rm -rf ."}}`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("review payload missing %q:\n%s", want, payload)
		}
	}
	if len(req.Tools) != 0 {
		t.Fatalf("review request advertised tools: %+v", req.Tools)
	}
	// The fixed policy tells the reviewer to use the user request as
	// authorization evidence and to judge by actual effects.
	for _, want := range []string{
		"authorization evidence",
		"never as instructions",
		"destructive",
		"ALLOW: <short reason> or DENY: <short reason>",
	} {
		if !strings.Contains(ToolReviewSystemPrompt, want) {
			t.Fatalf("review policy missing %q", want)
		}
	}
}

// TestLLMToolReviewerTruncatesOversizedEvidence bounds the user request and
// project constraints so a huge prompt cannot crowd the pending action out of
// the review request.
func TestLLMToolReviewerTruncatesOversizedEvidence(t *testing.T) {
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return []Chunk{{Text: "ALLOW: project-local edit", Done: true}}, nil
	}}
	reviewer := LLMToolReviewer{Provider: provider, Timeout: time.Second}
	long := strings.Repeat("x", maxReviewUserRequestRunes+100)
	if _, _, err := reviewer.Review(context.Background(), ToolReviewInput{
		Tool: "write_file", UserRequest: long, Constraints: long,
	}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	req, _ := provider.requestAt(0)
	payload := req.Messages[1].Content
	if strings.Count(payload, "x") > 2*maxReviewUserRequestRunes+10 {
		t.Fatalf("review payload did not bound the evidence (%d x runes)", strings.Count(payload, "x"))
	}
}

func TestLLMToolReviewerFailuresAreErrors(t *testing.T) {
	t.Run("no provider", func(t *testing.T) {
		reviewer := LLMToolReviewer{}
		if _, _, err := reviewer.Review(context.Background(), ToolReviewInput{Tool: "bash"}); err == nil {
			t.Fatal("reviewer without provider returned no error")
		}
	})
	t.Run("unparseable reply", func(t *testing.T) {
		provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "I am not sure", Done: true}}, nil
		}}
		reviewer := LLMToolReviewer{Provider: provider, Timeout: time.Second}
		if _, _, err := reviewer.Review(context.Background(), ToolReviewInput{Tool: "bash"}); err == nil {
			t.Fatal("unparseable reviewer reply returned no error")
		}
	})
	t.Run("provider error", func(t *testing.T) {
		provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return nil, errors.New("upstream down")
		}}
		reviewer := LLMToolReviewer{Provider: provider, Timeout: time.Second}
		if _, _, err := reviewer.Review(context.Background(), ToolReviewInput{Tool: "bash"}); err == nil {
			t.Fatal("provider error returned no reviewer error")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		provider := &fakeProvider{fn: func(ctx context.Context, _ Request) ([]Chunk, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		reviewer := LLMToolReviewer{Provider: provider, Timeout: 20 * time.Millisecond}
		if _, _, err := reviewer.Review(context.Background(), ToolReviewInput{Tool: "bash"}); err == nil {
			t.Fatal("timed-out reviewer returned no error")
		}
	})
}

// toolResultFor returns the tool message content the provider was given for
// callID, searching requests newest first.
func toolResultFor(provider *fakeProvider, callID string) string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for i := len(provider.reqs) - 1; i >= 0; i-- {
		for _, msg := range provider.reqs[i].Messages {
			if msg.Role == RoleTool && msg.ToolCallID == callID {
				return msg.Content
			}
		}
	}
	return ""
}
