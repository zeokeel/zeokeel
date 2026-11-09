package agent

// Tests for the settings-store overrides of the agent runtime limits and for
// the per-model sampling options reaching the provider request.

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// captureRequestBody records the JSON body of every model request.
type captureRequestBody struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *captureRequestBody) record(body []byte) {
	c.mu.Lock()
	c.bodies = append(c.bodies, append([]byte(nil), body...))
	c.mu.Unlock()
}

func (c *captureRequestBody) last(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		t.Fatal("no model request captured")
	}
	var payload map[string]any
	if err := json.Unmarshal(c.bodies[len(c.bodies)-1], &payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return payload
}

func TestModelGenerationOptionsReachRequest(t *testing.T) {
	captured := &captureRequestBody{}
	server := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.record(body)
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	})
	catalog := `[{"id":"gen-model","family":"other","temperature":0.3,"topP":0.8,"maxTokens":512}]`
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:         "gen-model",
		contracts.SettingsChatBaseURL:       server.URL,
		contracts.SettingModelCatalogModels: catalog,
	})
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("gen")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	payload := captured.last(t)
	if got := payload["temperature"]; got != 0.3 {
		t.Fatalf("temperature = %v, want 0.3", got)
	}
	if got := payload["top_p"]; got != 0.8 {
		t.Fatalf("top_p = %v, want 0.8", got)
	}
	if got := payload["max_tokens"]; got != float64(512) {
		t.Fatalf("max_tokens = %v, want 512", got)
	}
}

func TestRuntimeLimitSettingsOverrides(t *testing.T) {
	svc := newService(Config{}, rigtoolRegistry(t), newFakeSettings(map[string]string{
		contracts.SettingsChatMaxIterations: "4",
		contracts.SettingsChatContextBudget: "12000",
	}), &fakeLogger{}, nil, fakeApprovals{})
	cfg := svc.Config()
	if cfg.MaxIterations != 4 || cfg.ContextBudget != 12000 {
		t.Fatalf("runtime overrides = iterations %d / budget %d, want 4 / 12000",
			cfg.MaxIterations, cfg.ContextBudget)
	}

	// Explicit zero budget disables compaction; a non-positive iteration value
	// and malformed numbers fall back to the module config.
	svc2 := newService(Config{MaxIterations: 20, ContextBudget: 9000},
		rigtoolRegistry(t), newFakeSettings(map[string]string{
			contracts.SettingsChatMaxIterations: "0",
			contracts.SettingsChatContextBudget: "0",
		}), &fakeLogger{}, nil, fakeApprovals{})
	cfg = svc2.Config()
	if cfg.MaxIterations != 20 || cfg.ContextBudget != 0 {
		t.Fatalf("fallback/zero overrides = iterations %d / budget %d, want 20 / 0",
			cfg.MaxIterations, cfg.ContextBudget)
	}

	svc3 := newService(Config{MaxIterations: 12, ContextBudget: 5000},
		rigtoolRegistry(t), newFakeSettings(map[string]string{
			contracts.SettingsChatMaxIterations: "bogus",
			contracts.SettingsChatContextBudget: "-5",
		}), &fakeLogger{}, nil, fakeApprovals{})
	cfg = svc3.Config()
	if cfg.MaxIterations != 12 || cfg.ContextBudget != 5000 {
		t.Fatalf("malformed overrides = iterations %d / budget %d, want 12 / 5000",
			cfg.MaxIterations, cfg.ContextBudget)
	}
}

func TestEffectiveMaxIterationsUnlimitedDefault(t *testing.T) {
	if got := effectiveMaxIterations(0); got != agentIterationLimitUnbounded {
		t.Fatalf("effectiveMaxIterations(0) = %d, want %d (unlimited default)", got, agentIterationLimitUnbounded)
	}
	if got := effectiveMaxIterations(-3); got != agentIterationLimitUnbounded {
		t.Fatalf("effectiveMaxIterations(-3) = %d, want unbounded", got)
	}
	if got := effectiveMaxIterations(12); got != 12 {
		t.Fatalf("effectiveMaxIterations(12) = %d, want 12", got)
	}
}
