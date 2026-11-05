package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// reviewCallProvider scripts one gated "echo" tool call and then the final
// answer, so a turn reaches the automatic reviewer.
type reviewCallProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *reviewCallProvider) Stream(_ context.Context, _ runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := p.calls
	p.calls++
	p.mu.Unlock()

	var chunks []runtime.Chunk
	if call == 0 {
		chunks = []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-review-1",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			Done: true,
		}}
	} else {
		chunks = []runtime.Chunk{{Text: "done", Done: true}}
	}
	out := make(chan runtime.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

// captureReviewer denies every call and records what the runtime showed it.
type captureReviewer struct {
	mu     sync.Mutex
	inputs []runtime.ToolReviewInput
}

func (r *captureReviewer) review(_ context.Context, in runtime.ToolReviewInput) (bool, string, error) {
	r.mu.Lock()
	r.inputs = append(r.inputs, in)
	r.mu.Unlock()
	return false, "deletes a pre-existing file", nil
}

func (r *captureReviewer) last() runtime.ToolReviewInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.inputs) == 0 {
		return runtime.ToolReviewInput{}
	}
	return r.inputs[len(r.inputs)-1]
}

// waitReviewApproval waits for the reviewer fallback ApprovalRequest.
func waitReviewApproval(t *testing.T, ctrl *session.Controller) state.ApprovalRequest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pending := ctrl.PendingApprovals(); len(pending) > 0 {
			return pending[0]
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a reviewer approval request; snapshot: %+v", ctrl.Snapshot())
	return state.ApprovalRequest{}
}

// TestAutoReviewFallbackPersistsAndReplays proves the reviewer's recommended
// denial reaches the controller/state layers: it is visible on the pending
// request, persisted in the approval event payload, restored on the snapshot's
// tool state after a reopen, and the reviewer got the turn's user request and
// environment as its authorization evidence.
func TestAutoReviewFallbackPersistsAndReplays(t *testing.T) {
	const (
		reason      = "deletes a pre-existing file"
		userRequest = "delete the stale build files"
		workDir     = "/workspace/review"
		constraints = "# Project rules\nNever delete tracked files."
	)
	reviewer := &captureReviewer{}
	gate := runtime.NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "echo"
	})
	h := newHarnessConfigured(t, &reviewCallProvider{}, func(cfg *runtime.Config) {
		cfg.Gate = gate
		cfg.ToolReviewer = reviewer.review
		cfg.ReviewContext = func() (string, string) { return workDir, constraints }
	})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "auto review replay")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, userRequest); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	request := waitReviewApproval(t, h.ctrl)
	if request.ReviewReason != reason {
		t.Fatalf("pending ReviewReason = %q, want %q", request.ReviewReason, reason)
	}
	in := reviewer.last()
	if in.Tool != "echo" || in.UserRequest != userRequest {
		t.Fatalf("reviewer input = %+v, want the pending tool and user request", in)
	}
	if in.WorkDir != workDir || !strings.Contains(in.Constraints, "Never delete tracked files") {
		t.Fatalf("reviewer environment = (%q, %q), want the turn's cwd and constraints", in.WorkDir, in.Constraints)
	}

	// The user overrides the recommendation: the tool runs and the turn ends.
	gate.Approve(request.CallID)
	waitIdleAndDrain(t, h.ctrl)

	// The persisted event keeps the reason for replay/UX, in a snake_case field
	// that older rows (which lack it) still decode.
	events, err := h.ctrl.Events(ctx, id)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type != session.EventApprovalRequest {
			continue
		}
		found = true
		if !strings.Contains(string(ev.Payload), `"review_reason":"`+reason+`"`) {
			t.Fatalf("persisted approval payload missing review_reason: %s", ev.Payload)
		}
	}
	if !found {
		t.Fatalf("no approval_request event persisted: %+v", events)
	}

	// Restart over the same store: the replayed snapshot keeps the reason on
	// the call's tool state.
	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	toolState, ok := snap.Tools[request.CallID]
	if !ok {
		t.Fatalf("replayed snapshot lost call %q; tools: %+v", request.CallID, snap.Tools)
	}
	if toolState.ReviewReason != reason {
		t.Fatalf("replayed ReviewReason = %q, want %q", toolState.ReviewReason, reason)
	}
}
