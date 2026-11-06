package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// effortProvider is a scripted Provider whose reasoning effort can be
// retargeted, like the OpenAI-compatible provider.
type effortProvider struct {
	fakeProvider
	effortMu sync.Mutex
	effort   string
}

func (p *effortProvider) SetReasoningEffort(effort string) {
	p.effortMu.Lock()
	p.effort = effort
	p.effortMu.Unlock()
}

func (p *effortProvider) currentEffort() string {
	p.effortMu.Lock()
	defer p.effortMu.Unlock()
	return p.effort
}

func newProviderAgent(t *testing.T, provider Provider, cfg Config) *Agent {
	t.Helper()
	cfg.Provider = provider
	if cfg.Tools == nil {
		cfg.Tools = NewRegistry()
	}
	agent, err := New(cfg)
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	return agent
}

// TestSetReasoningEffortRetargetsProvider pins the composer's reasoning
// strength apply path: it reaches the provider the agent already holds, works
// after a model switch, and reports unsupported providers instead of silently
// doing nothing.
func TestSetReasoningEffortRetargetsProvider(t *testing.T) {
	provider := &effortProvider{fakeProvider: fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return []Chunk{{Done: true}}, nil
	}}}
	agent := newProviderAgent(t, provider, Config{ModelName: "one"})

	if err := agent.SetReasoningEffort(" low "); err != nil {
		t.Fatalf("SetReasoningEffort: %v", err)
	}
	if got := provider.currentEffort(); got != "low" {
		t.Fatalf("provider effort = %q, want low", got)
	}
	if err := agent.SetModel("two"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := agent.SetReasoningEffort("max"); err != nil {
		t.Fatalf("SetReasoningEffort after model switch: %v", err)
	}
	if got := provider.currentEffort(); got != "max" {
		t.Fatalf("provider effort after model switch = %q, want max", got)
	}
	if err := agent.SetReasoningEffort(""); err != nil {
		t.Fatalf("clearing the effort: %v", err)
	}
	if got := provider.currentEffort(); got != "" {
		t.Fatalf("provider effort after clear = %q, want empty", got)
	}

	// A provider without the optional interface reports it; the eino test seam
	// (no provider) does too instead of panicking.
	if err := newProviderAgent(t, &fakeProvider{}, Config{}).SetReasoningEffort("high"); err == nil {
		t.Fatal("SetReasoningEffort on a provider without support returned nil")
	}
}

// TestSetContextBudgetChangesCompactionThreshold proves the model's context
// window can retarget compaction at runtime: the compactor sees the new budget
// on the next turn boundary, and a non-positive value leaves the build-time
// budget alone.
func TestSetContextBudgetChangesCompactionThreshold(t *testing.T) {
	compactor := &recordingCompactor{}
	agent := newProviderAgent(t, &fakeProvider{}, Config{ContextBudget: 100, Compactor: compactor})
	if got := agent.ContextBudget(); got != 100 {
		t.Fatalf("ContextBudget = %d, want 100", got)
	}
	agent.SetContextBudget(0)
	if got := agent.ContextBudget(); got != 100 {
		t.Fatalf("ContextBudget after zero = %d, want the build-time 100 restored", got)
	}
	agent.SetContextBudget(5000)
	if got := agent.ContextBudget(); got != 5000 {
		t.Fatalf("ContextBudget after update = %d, want 5000", got)
	}

	// Once past the new budget, compaction runs with exactly that budget.
	prior := []Message{{Role: RoleUser, Content: strings.Repeat("x", 40000)}}
	agent.SetContextBudget(10)
	if _, err := agent.compactPrior(context.Background(), "s", prior); err != nil {
		t.Fatalf("compactPrior: %v", err)
	}
	if compactor.callCount() != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactor.callCount())
	}
	if got := compactor.budgets[0]; got != 10 {
		t.Fatalf("compactor budget = %d, want 10", got)
	}
}

// TestAgentSettingsNilSafety keeps the setters harmless on a nil receiver
// (defensive: the shell holds *Agent handles).
func TestAgentSettingsNilSafety(t *testing.T) {
	var agent *Agent
	agent.SetContextBudget(10)
	if got := agent.ContextBudget(); got != 0 {
		t.Fatalf("nil agent ContextBudget = %d", got)
	}
	if err := agent.SetReasoningEffort("high"); err == nil {
		t.Fatal("nil agent SetReasoningEffort returned nil")
	}
}
