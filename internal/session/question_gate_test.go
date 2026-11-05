package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// FP-16 Controller seam: AskQuestion blocks the turn on a pending question and
// AnswerQuestion delivers the answer to that same call instead of starting a new
// user turn.

// askQuestion runs AskQuestion in a goroutine and returns its result channel.
func askQuestion(ctx context.Context, h *presetHarness, sessionID, callID string, q tools.Question) <-chan error {
	out := make(chan error, 1)
	go func() {
		_, err := h.ctrl.AskQuestion(ctx, sessionID, callID, q)
		out <- err
	}()
	return out
}

// waitQuestion polls until the session has a pending question or the deadline
// expires.
func waitQuestion(t *testing.T, h *presetHarness, callID string) tools.Question {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, question := range h.ctrl.Questions() {
			if question.CallID == callID {
				return question
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pending question %q", callID)
	return tools.Question{}
}

// TestAskQuestionBlocksUntilAnswered covers the happy path: the question is
// registered (visible to the UI), AskQuestion blocks, and the answer is returned
// from the same call.
func TestAskQuestionBlocksUntilAnswered(t *testing.T) {
	h := newPresetHarness(t, t.TempDir())
	ctx := context.Background()
	sessionID, err := h.ctrl.CreateSession(ctx, "question session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	type answer struct {
		text string
		err  error
	}
	answers := make(chan answer, 1)
	go func() {
		text, aerr := h.ctrl.AskQuestion(ctx, sessionID, "call-1", tools.Question{
			Question: "Which database should I target?",
			Options:  []string{"sqlite", "postgres"},
		})
		answers <- answer{text: text, err: aerr}
	}()
	pending := waitQuestion(t, h, "call-1")
	if pending.Question != "Which database should I target?" {
		t.Fatalf("pending question = %+v, want the tool question", pending)
	}
	if err := h.ctrl.AnswerQuestion(ctx, "call-1", "postgres"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	select {
	case got := <-answers:
		if got.err != nil || got.text != "postgres" {
			t.Fatalf("AskQuestion = (%q, %v), want (postgres, nil)", got.text, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskQuestion did not unblock after AnswerQuestion")
	}
	// The pending card is resolved.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.ctrl.Questions()) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pending questions after answer = %+v, want none", h.ctrl.Questions())
}

// TestAskQuestionCancelsOnContext covers cancellation: the turn context ending
// unblocks AskQuestion with the context error and clears the card.
func TestAskQuestionCancelsOnContext(t *testing.T) {
	h := newPresetHarness(t, t.TempDir())
	sessionID, err := h.ctrl.CreateSession(context.Background(), "cancel session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := askQuestion(ctx, h, sessionID, "call-cancel", tools.Question{Question: "Continue?"})
	waitQuestion(t, h, "call-cancel")
	cancel()
	select {
	case got := <-errc:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("AskQuestion after cancel = %v, want context.Canceled", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskQuestion did not unblock after context cancellation")
	}
	if len(h.ctrl.Questions()) != 0 {
		t.Fatalf("pending questions after cancel = %+v, want none", h.ctrl.Questions())
	}
}

// TestAskQuestionDeadlineTimeout covers the timeout path the app maps to the
// runtime's ErrQuestionTimeout (degrade to a pending question).
func TestAskQuestionDeadlineTimeout(t *testing.T) {
	h := newPresetHarness(t, t.TempDir())
	sessionID, err := h.ctrl.CreateSession(context.Background(), "timeout session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, aerr := h.ctrl.AskQuestion(ctx, sessionID, "call-timeout", tools.Question{Question: "Still there?"})
	if !errors.Is(aerr, context.DeadlineExceeded) {
		t.Fatalf("AskQuestion = %v, want context.DeadlineExceeded", aerr)
	}
	if len(h.ctrl.Questions()) != 0 {
		t.Fatalf("pending questions after timeout = %+v, want none", h.ctrl.Questions())
	}
}

// TestApproveAlwaysWithPerSessionGatesOnly is the RT-04 seam-consistency P3
// regression: a pure per-session configuration (no shared Agent/Gate) must still
// resolve ApproveAlways against the session's own approval queue.
func TestApproveAlwaysWithPerSessionGatesOnly(t *testing.T) {
	gate := runtime.NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "echo"
	})
	provider := &fakeProvider{respond: approvalRespond}
	h := newHarness(t, provider)
	// Drop the shared agent/gate: this controller is per-session only.
	h.ctrl.SetAgent(nil, nil)
	h.ctrl.SetAgentFactory(perSessionAgentFactory(provider, func(cfg *runtime.Config) {
		cfg.Gate = gate
	}), nil)
	h.ctrl.SetAgentGateFactory(func(string) *runtime.ApprovalQueue { return gate })

	ctx := context.Background()
	sessionID, err := h.ctrl.CreateSession(ctx, "approve always")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, sessionID, "use the gated tool"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApproval(t, h.ctrl, "call-1")
	if !h.ctrl.ApproveAlways("call-1") {
		t.Fatal("ApproveAlways returned false with only per-session gates configured")
	}
	waitIdleAndDrain(t, h.ctrl)
}
