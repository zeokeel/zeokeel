//go:build tdd_red

package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/state"
)

// RT-04 TDD (RED): an approval wait in one session must not freeze other
// sessions.
//
// Today every turn runs under one runtime.Agent's global runMu, and the approval
// wait happens inside the tool invocation, i.e. inside that lock. While session A
// waits for a human decision, session B's Submit cannot make progress, so the
// desktop UI's other conversations appear frozen until the approval is handled.
// The next round moves approval waits outside the global turn lock (or gives each
// session its own agent), which makes this test pass.
//
// This test must stay red until that fix lands; do not relax the B deadline.

// lastUserText returns the newest user message content of a request.
func lastUserText(req runtime.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == runtime.RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

// hasToolResult reports whether the request already carries a tool result.
func hasToolResult(req runtime.Request) bool {
	for _, message := range req.Messages {
		if message.Role == runtime.RoleTool {
			return true
		}
	}
	return false
}

// TestApprovalDoesNotBlockOtherSessions submits a turn on session A that blocks
// on an approval, then submits session B and expects B to finish while A is
// still waiting.
func TestApprovalDoesNotBlockOtherSessions(t *testing.T) {
	gate := runtime.NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "echo"
	})
	provider := &fakeProvider{respond: func(_ context.Context, req runtime.Request, _ int) []runtime.Chunk {
		if strings.Contains(lastUserText(req), "needs-approval") && !hasToolResult(req) {
			return []runtime.Chunk{{
				ToolCalls: []runtime.ToolCall{{
					ID:        "call-approval",
					Name:      "echo",
					Arguments: json.RawMessage(`{"text":"please approve"}`),
				}},
				Done: true,
			}}
		}
		return []runtime.Chunk{{Text: "done", Done: true}}
	}}
	h := newHarnessWithGate(t, provider, gate)
	ctx := context.Background()
	blocked, err := h.ctrl.CreateSession(ctx, "approval session a")
	if err != nil {
		t.Fatalf("CreateSession(a): %v", err)
	}
	other, err := h.ctrl.CreateSession(ctx, "free session b")
	if err != nil {
		t.Fatalf("CreateSession(b): %v", err)
	}

	if err := h.ctrl.Submit(ctx, blocked, "needs-approval turn"); err != nil {
		t.Fatalf("Submit(a): %v", err)
	}
	// Wait until A's turn is parked on the approval decision.
	waitForCondition(t, 5*time.Second, "session A to wait for an approval", func() bool {
		return len(h.ctrl.PendingApprovals()) > 0
	})
	// Make sure the approval is never decided while B runs: deny only in cleanup.
	t.Cleanup(func() {
		for _, request := range h.ctrl.PendingApprovals() {
			h.ctrl.Deny(request.CallID)
		}
		done := make(chan struct{})
		go func() { h.ctrl.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	start := time.Now()
	if err := h.ctrl.Submit(ctx, other, "plain turn"); err != nil {
		t.Fatalf("Submit(b): %v", err)
	}
	const bDeadline = 2 * time.Second
	deadline := time.Now().Add(bDeadline)
	for time.Now().Before(deadline) {
		// B became current when it was submitted; its snapshot is idle once done.
		if h.ctrl.Snapshot().Phase == state.PhaseIdle && time.Since(start) > 20*time.Millisecond {
			return // GREEN: B completed while A still waits for approval.
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("RED (RT-04): session B did not finish within %v while session A waited for approval (%.0fms elapsed): the global agent runMu is held across the approval wait; move approval waits outside the global turn lock or give each session its own agent", bDeadline, time.Since(start).Seconds()*1000)
}

// waitForCondition polls cond until it is true or the timeout elapses.
func waitForCondition(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
