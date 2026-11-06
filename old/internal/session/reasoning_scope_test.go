package session_test

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// effortProvider is a scripted Provider whose reasoning effort can be
// retargeted, like the OpenAI-compatible provider the app builds.
type effortProvider struct {
	*fakeProvider
	mu     sync.Mutex
	effort string
}

func (p *effortProvider) SetReasoningEffort(effort string) {
	p.mu.Lock()
	p.effort = effort
	p.mu.Unlock()
}

func (p *effortProvider) currentEffort() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.effort
}

func newEffortProvider() *effortProvider {
	return &effortProvider{fakeProvider: &fakeProvider{respond: func(context.Context, runtime.Request, int) []runtime.Chunk {
		return []runtime.Chunk{{Text: "ok", Done: true}}
	}}}
}

// TestSessionReasoningEffortAppliesAndFallsBack pins the composer's reasoning
// strength control end to end at the controller level: the selected model's
// configured effort applies by default, an explicit session choice wins and is
// persisted, switching models adopts the new model's effort, and clearing the
// choice falls back to the model again.
func TestSessionReasoningEffortAppliesAndFallsBack(t *testing.T) {
	provider := newEffortProvider()
	h := newHarness(t, provider)
	alpha, err := h.store.CreateModel(store.Model{
		Name: "Alpha", Model: "alpha-1", ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("create Alpha: %v", err)
	}
	beta, err := h.store.CreateModel(store.Model{
		Name: "Beta", Model: "beta-1", ReasoningEffort: "low",
	})
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

	// Opening a session applies its model's configured effort.
	if _, err := h.ctrl.OpenSession(context.Background(), first); err != nil {
		t.Fatalf("OpenSession first: %v", err)
	}
	if got := provider.currentEffort(); got != "high" {
		t.Fatalf("default model effort = %q, want high", got)
	}
	if got := h.ctrl.SessionReasoningEffort(first); got != "" {
		t.Fatalf("SessionReasoningEffort before a choice = %q, want empty", got)
	}

	// An explicit session choice wins, is persisted, and reaches the provider.
	if err := h.ctrl.SetSessionReasoningEffort(first, "max"); err != nil {
		t.Fatalf("SetSessionReasoningEffort: %v", err)
	}
	if got := provider.currentEffort(); got != "max" {
		t.Fatalf("explicit session effort = %q, want max", got)
	}
	if got, err := h.store.GetSetting(session.SessionReasoningEffortKey(first)); err != nil || got != "max" {
		t.Fatalf("persisted effort = %q, %v; want max", got, err)
	}

	// A session without a choice still gets the model's configured effort.
	if _, err := h.ctrl.OpenSession(context.Background(), second); err != nil {
		t.Fatalf("OpenSession second: %v", err)
	}
	if got := provider.currentEffort(); got != "high" {
		t.Fatalf("second session effort = %q, want high", got)
	}

	// Submit for the first session re-applies its own choice.
	if err := h.ctrl.Submit(context.Background(), first, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := provider.currentEffort(); got != "max" {
		t.Fatalf("first session Submit effort = %q, want max", got)
	}

	// Unknown levels are rejected before anything is written.
	if err := h.ctrl.SetSessionReasoningEffort(first, "High"); err == nil {
		t.Fatal("SetSessionReasoningEffort accepted mis-cased High")
	}
	if err := h.ctrl.SetSessionReasoningEffort(first, "extreme"); err == nil {
		t.Fatal("SetSessionReasoningEffort accepted an unknown level")
	}
	if got := provider.currentEffort(); got != "max" {
		t.Fatalf("rejected effort changed the provider to %q", got)
	}

	// Switching models adopts the new model's own configured effort.
	if err := h.ctrl.SetSessionModel(first, beta.ID); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if got := h.ctrl.SessionReasoningEffort(first); got != "" {
		t.Fatalf("explicit effort after a model switch = %q, want empty", got)
	}
	if got := provider.currentEffort(); got != "low" {
		t.Fatalf("effort after model switch = %q, want Beta's low", got)
	}

	// Clearing the choice falls back to the model's configured effort.
	if err := h.ctrl.SetSessionReasoningEffort(first, "max"); err != nil {
		t.Fatalf("SetSessionReasoningEffort (#2): %v", err)
	}
	if err := h.ctrl.SetSessionReasoningEffort(first, ""); err != nil {
		t.Fatalf("clear reasoning effort: %v", err)
	}
	if got := h.ctrl.SessionReasoningEffort(first); got != "" {
		t.Fatalf("explicit effort after clear = %q, want empty", got)
	}
	if got := provider.currentEffort(); got != "low" {
		t.Fatalf("effort after clear = %q, want Beta's low", got)
	}

	// The explicit choice survives a restart (same store, new controller) and
	// is applied when the session is reopened.
	if err := h.ctrl.SetSessionReasoningEffort(first, "max"); err != nil {
		t.Fatalf("SetSessionReasoningEffort before restart: %v", err)
	}
	h.restart()
	if got := h.ctrl.SessionReasoningEffort(first); got != "max" {
		t.Fatalf("effort after restart = %q, want max", got)
	}
	if _, err := h.ctrl.OpenSession(context.Background(), first); err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if got := provider.currentEffort(); got != "max" {
		t.Fatalf("effort applied after restart = %q, want max", got)
	}
}

// TestSessionModelContextWindowSizesCompactionBudget pins the model-derived
// context budget: a session whose selected model publishes a context window
// uses four fifths of it for compaction, and a model without one restores the
// build-time configuration.
func TestSessionModelContextWindowSizesCompactionBudget(t *testing.T) {
	provider := &fakeProvider{respond: func(context.Context, runtime.Request, int) []runtime.Chunk {
		return []runtime.Chunk{{Text: "ok", Done: true}}
	}}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		cfg.ContextBudget = 24000
	})
	small, err := h.store.CreateModel(store.Model{Name: "Small", Model: "small-1"})
	if err != nil {
		t.Fatalf("create Small: %v", err)
	}
	big, err := h.store.CreateModel(store.Model{
		Name: "Big", Model: "big-1", ContextWindow: 1000000,
	})
	if err != nil {
		t.Fatalf("create Big: %v", err)
	}
	if err := h.store.SetDefaultModel(small.ID); err != nil {
		t.Fatalf("default Small: %v", err)
	}
	id, err := h.ctrl.CreateSession(context.Background(), "budget")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := h.ctrl.OpenSession(context.Background(), id); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got := h.agent.ContextBudget(); got != 24000 {
		t.Fatalf("global budget = %d, want 24000", got)
	}

	if err := h.ctrl.SetSessionModel(id, big.ID); err != nil {
		t.Fatalf("SetSessionModel(big): %v", err)
	}
	if got, want := h.agent.ContextBudget(), session.ContextBudgetForWindow(1000000); got != want || got != 800000 {
		t.Fatalf("model budget = %d, want %d (800000)", got, want)
	}

	if err := h.ctrl.SetSessionModel(id, small.ID); err != nil {
		t.Fatalf("SetSessionModel(small): %v", err)
	}
	if got := h.agent.ContextBudget(); got != 24000 {
		t.Fatalf("budget after a windowless model = %d, want the build-time 24000", got)
	}
	if got := session.ContextBudgetForWindow(0); got != 0 {
		t.Fatalf("ContextBudgetForWindow(0) = %d, want 0", got)
	}
	if got := session.ContextBudgetForWindow(-5); got != 0 {
		t.Fatalf("ContextBudgetForWindow(-5) = %d, want 0", got)
	}
}
