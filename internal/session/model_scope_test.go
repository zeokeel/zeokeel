package session_test

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestSessionModelAppliesToThatSessionsRequest pins the per-session model:
// opening a session points the shared agent at its own model, and a session
// without a choice falls back to the Settings default instead of inheriting
// the previous session's model.
func TestSessionModelAppliesToThatSessionsRequest(t *testing.T) {
	provider := &fakeProvider{respond: func(context.Context, runtime.Request, int) []runtime.Chunk {
		return []runtime.Chunk{{Text: "ok", Done: true}}
	}}
	h := newHarness(t, provider)
	alpha, err := h.store.CreateModel(store.Model{Name: "Alpha", Model: "alpha-1"})
	if err != nil {
		t.Fatalf("create Alpha: %v", err)
	}
	beta, err := h.store.CreateModel(store.Model{Name: "Beta", Model: "beta-1"})
	if err != nil {
		t.Fatalf("create Beta: %v", err)
	}
	if err := h.store.SetDefaultModel(alpha.ID); err != nil {
		t.Fatalf("default Alpha: %v", err)
	}

	first, err := h.ctrl.CreateSession(context.Background(), "first")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	second, err := h.ctrl.CreateSession(context.Background(), "second")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := h.ctrl.SetSessionModel(first, beta.ID); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if _, err := h.ctrl.OpenSession(context.Background(), first); err != nil {
		t.Fatalf("OpenSession first: %v", err)
	}
	if got := h.agent.ModelName(); got != "beta-1" {
		t.Fatalf("first session model = %q, want beta-1", got)
	}

	if _, err := h.ctrl.OpenSession(context.Background(), second); err != nil {
		t.Fatalf("OpenSession second: %v", err)
	}
	if got := h.agent.ModelName(); got != "alpha-1" {
		t.Fatalf("second session model = %q, want the global default alpha-1", got)
	}

	// The next Submit for the first session applies its choice again.
	if err := h.ctrl.Submit(context.Background(), first, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := h.agent.ModelName(); got != "beta-1" {
		t.Fatalf("first session Submit used model %q, want beta-1", got)
	}
}
