package session_test

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestHasConfiguredModelFallback pins the controller-side contract without the
// app predicate: a shared agent counts as configured, while a factory-only
// controller (per-session agents, no shared agent) reports false.
func TestHasConfiguredModelFallback(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	if !h.ctrl.HasConfiguredModel() {
		t.Fatal("shared-agent controller should report a configured model by fallback")
	}

	// Factory-only controller: HasAgent is true (turns can be attempted) but no
	// agent/model exists yet, so HasConfiguredModel must be false.
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	broker := state.NewBroker()
	ctrl, err := session.New(session.Config{
		Store:  st,
		Broker: broker,
		AgentFactory: func(string) (*runtime.Agent, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		broker.Close()
		_ = st.Close()
	})
	if !ctrl.HasAgent() {
		t.Fatal("HasAgent() = false with an AgentFactory installed")
	}
	if ctrl.HasConfiguredModel() {
		t.Fatal("factory-only controller with no agent/model reported a configured model")
	}
}

// TestHasConfiguredModelPredicateOverridesFallback covers the app-wired
// predicate: it overrides the fallback in both directions and stays live.
func TestHasConfiguredModelPredicateOverridesFallback(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	h.ctrl.SetModelConfiguredFunc(func() bool { return false })
	if h.ctrl.HasConfiguredModel() {
		t.Fatal("predicate returning false was overridden by the agent fallback")
	}
	h.ctrl.SetModelConfiguredFunc(func() bool { return true })
	if !h.ctrl.HasConfiguredModel() {
		t.Fatal("predicate returning true was not honored")
	}
}
