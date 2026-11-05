package session_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// RT-04: per-session agents.
//
// The Controller now accepts an AgentFactory: each session gets its own
// runtime.Agent (and therefore its own runMu), so turns of different sessions
// run in parallel. The Controller falls back to the shared agent when the
// factory is nil (the app's headless CLI path and older tests). These tests pin
// the seam and the real parallelism; keep the timing assertion strict.

// TestSessionsRunInParallelWithPerSessionAgents submits one turn on each of two
// sessions concurrently. Each model call takes modelLatency; with per-session
// agents the wall time is about one call, with the shared agent it is two.
func TestSessionsRunInParallelWithPerSessionAgents(t *testing.T) {
	const modelLatency = 300 * time.Millisecond
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, _ int) []runtime.Chunk {
		time.Sleep(modelLatency)
		return []runtime.Chunk{{Text: "done", Done: true}}
	}}
	h := newHarness(t, provider)
	// Install the per-session factory exactly like the app does: one agent per
	// session, each with its own runMu.
	h.ctrl.SetAgentFactory(perSessionAgentFactory(provider, nil), nil)
	ctx := context.Background()
	first, err := h.ctrl.CreateSession(ctx, "parallel session a")
	if err != nil {
		t.Fatalf("CreateSession(a): %v", err)
	}
	second, err := h.ctrl.CreateSession(ctx, "parallel session b")
	if err != nil {
		t.Fatalf("CreateSession(b): %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	submitted := make(chan time.Time, 2)
	submit := func(sessionID, prompt string) {
		defer wg.Done()
		submitted <- time.Now()
		if err := h.ctrl.Submit(ctx, sessionID, prompt); err != nil {
			t.Errorf("Submit(%s): %v", sessionID, err)
		}
	}
	go submit(first, "run the first turn")
	go submit(second, "run the second turn")
	wg.Wait()
	// Wait for both turns to reach a terminal state.
	h.ctrl.Wait()
	elapsed := time.Since(<-submitted) // both submissions happen within microseconds
	// Serialized: >= 2*latency. Parallel: ~1*latency plus scheduling overhead.
	limit := modelLatency + 150*time.Millisecond
	if elapsed >= limit {
		t.Fatalf("two one-call sessions took %v (limit %v): per-session agents still serialize on a shared runMu", elapsed, limit)
	}
}

// TestAgentFactorySeamAvailableForPerSessionAgents pins the seam shape:
// session.Config must offer an AgentFactory the app can set, so the controller
// can build/own one runtime.Agent per session instead of one global agent.
func TestAgentFactorySeamAvailableForPerSessionAgents(t *testing.T) {
	configType := reflect.TypeOf(session.Config{})
	field, ok := configType.FieldByName("AgentFactory")
	if !ok {
		t.Fatalf("session.Config has no AgentFactory seam (want func(sessionID string) (*runtime.Agent, error))")
	}
	if field.Type.Kind() != reflect.Func {
		t.Fatalf("session.Config.AgentFactory is %v, want a func", field.Type)
	}
}

// perSessionAgentFactory returns a factory that builds one independent
// runtime.Agent per session for these tests, mirroring the app's factory. The
// optional configure callback customizes each agent (for example to install a
// Gate).
func perSessionAgentFactory(provider runtime.Provider, configure func(*runtime.Config)) func(string) (*runtime.Agent, error) {
	var mu sync.Mutex
	agents := make(map[string]*runtime.Agent)
	return func(sessionID string) (*runtime.Agent, error) {
		mu.Lock()
		defer mu.Unlock()
		if agent := agents[sessionID]; agent != nil {
			return agent, nil
		}
		registry := runtime.NewRegistry()
		if err := registry.Register(echoTool{}); err != nil {
			return nil, err
		}
		cfg := runtime.Config{Provider: provider, Tools: registry, Instruction: "test agent"}
		if configure != nil {
			configure(&cfg)
		}
		agent, err := runtime.New(cfg)
		if err != nil {
			return nil, err
		}
		agents[sessionID] = agent
		return agent, nil
	}
}
