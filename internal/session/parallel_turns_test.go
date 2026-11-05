//go:build tdd_red

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

// RT-04 TDD (RED): per-session agents.
//
// Today the Controller owns ONE runtime.Agent and every turn takes that agent's
// global runMu, so two sessions' model calls run strictly serialized. The next
// round gives each session its own agent (an AgentFactory seam injected through
// session.Config, wired by the app to buildAgentWithOptions per session) and the
// controller falls back to the single shared agent only when the factory is nil.
//
// This file is a failing test today: it must stay red until the seam and the
// per-session agent map exist. Do not weaken the timing assertion to make the
// current serialized implementation pass.

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
		t.Fatalf("RED (RT-04): two one-call sessions took %v (limit %v): turns still share one agent and serialize on its runMu; implement per-session agents via an AgentFactory seam", elapsed, limit)
	}
}

// TestAgentFactorySeamAvailableForPerSessionAgents pins the planned seam shape:
// session.Config must offer an AgentFactory the app can set, so the controller
// can build/own one runtime.Agent per session instead of one global agent.
func TestAgentFactorySeamAvailableForPerSessionAgents(t *testing.T) {
	configType := reflect.TypeOf(session.Config{})
	field, ok := configType.FieldByName("AgentFactory")
	if !ok {
		t.Fatalf("RED (RT-04): session.Config has no AgentFactory seam; the next round must add it (func(sessionID string) (*runtime.Agent, error) or equivalent) so per-session agents can be injected and faked in tests")
	}
	if field.Type.Kind() != reflect.Func {
		t.Fatalf("session.Config.AgentFactory is %v, want a func", field.Type)
	}
}
