package agent

// Tests for per-model thinking injection: catalog family modes, model default
// modes, custom mode parameters and the legacy effort fallback all reach the
// provider through Request.Extra.

import (
	"fmt"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// thinkingCatalogJSON renders a catalog with one qwen model and one custom-family
// model with a user-defined mode.
func thinkingCatalogJSON(t *testing.T) string {
	t.Helper()
	encoded, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{
		{ID: "qwen-test", Name: "Qwen Test", Family: contracts.ModelFamilyQwen},
		{ID: "custom-test", Family: contracts.ModelFamilyCustom,
			ThinkingModes: []contracts.ThinkingMode{
				{ID: "budgeted", NameZH: "预算思考", Params: map[string]any{
					"enable_thinking":      true,
					"thinking_budget":      4096,
					"chat_template_kwargs": map[string]any{"thinking": true},
				}},
			}},
	})
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	return encoded
}

// newThinkingTestService builds a service with a capturing provider factory so
// tests can inspect the request the agent would send.
func newThinkingTestService(t *testing.T) (*service, *captureProvider, *fakeSettings, string) {
	t.Helper()
	catalog := thinkingCatalogJSON(t)
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:         "qwen-test",
		contracts.SettingModelCatalogModels: catalog,
		contracts.SettingsChatBaseURL:       "http://127.0.0.1:1/v1",
		contracts.SettingsChatAPIKey:        "test-key",
	})
	provider := &captureProvider{}
	svc := newService(Config{}, rigtoolRegistry(t), settings, &fakeLogger{}, nil, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("thinking")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, provider, settings, id
}

// lastExtra returns the Extra map of the last captured request.
func lastExtra(t *testing.T, provider *captureProvider) map[string]any {
	t.Helper()
	request := provider.lastRequest()
	if request.Extra == nil {
		return nil
	}
	return request.Extra
}

func TestThinkingQwenModeInjection(t *testing.T) {
	svc, provider, settings, id := newThinkingTestService(t)
	if err := settings.Set(contracts.SessionThinkingSettingKey(id), contracts.ThinkingModeQwenOff); err != nil {
		t.Fatalf("set thinking: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	extra := lastExtra(t, provider)
	if got, ok := extra["enable_thinking"].(bool); !ok || got {
		t.Fatalf("enable_thinking = %v (Extra=%v), want false", extra["enable_thinking"], extra)
	}

	// Switching to deep thinking sends enable_thinking + the budget.
	if err := settings.Set(contracts.SessionThinkingSettingKey(id), contracts.ThinkingModeQwenDeep); err != nil {
		t.Fatalf("set deep thinking: %v", err)
	}
	if err := svc.Send(id, "again"); err != nil {
		t.Fatalf("Send again: %v", err)
	}
	waitIdle(t, svc, id)
	extra = lastExtra(t, provider)
	if got, ok := extra["enable_thinking"].(bool); !ok || !got {
		t.Fatalf("enable_thinking = %v, want true", extra["enable_thinking"])
	}
	if got := fmt.Sprint(extra["thinking_budget"]); got != "8192" {
		t.Fatalf("thinking_budget = %v, want 8192", extra["thinking_budget"])
	}

	// The family default ("default") mode sends no thinking fields.
	if err := settings.Set(contracts.SessionThinkingSettingKey(id), contracts.ThinkingModeDefault); err != nil {
		t.Fatalf("set default thinking: %v", err)
	}
	if err := svc.Send(id, "third"); err != nil {
		t.Fatalf("Send third: %v", err)
	}
	waitIdle(t, svc, id)
	if extra := lastExtra(t, provider); len(extra) != 0 {
		t.Fatalf("default mode Extra = %v, want empty", extra)
	}
}

func TestThinkingCustomModeNestedParams(t *testing.T) {
	svc, provider, settings, id := newThinkingTestService(t)
	// Switch the session's model to the custom-family model.
	if err := settings.Set(contracts.SessionModelSettingKey(id), "custom-test"); err != nil {
		t.Fatalf("set session model: %v", err)
	}
	if err := settings.Set(contracts.SessionThinkingSettingKey(id), "budgeted"); err != nil {
		t.Fatalf("set thinking: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	extra := lastExtra(t, provider)
	if got := fmt.Sprint(extra["thinking_budget"]); got != "4096" {
		t.Fatalf("thinking_budget = %v, want 4096", extra["thinking_budget"])
	}
	nested, ok := extra["chat_template_kwargs"].(map[string]any)
	if !ok || nested["thinking"] != true {
		t.Fatalf("chat_template_kwargs = %v, want nested thinking=true", extra["chat_template_kwargs"])
	}
}

func TestThinkingModelDefaultMode(t *testing.T) {
	svc := newService(Config{}, rigtoolRegistry(t), nil, &fakeLogger{}, nil, fakeApprovals{})
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("default-thinking")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The built-in deepseek-reasoner entry defaults to high reasoning.
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "deepseek-reasoner",
		contracts.SettingsChatBaseURL: "http://127.0.0.1:1/v1",
		contracts.SettingsChatAPIKey:  "test-key",
	})
	svc.settings = settings
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if got := lastExtra(t, provider)["reasoning_effort"]; got != contracts.SessionEffortHigh {
		t.Fatalf("reasoning_effort = %v, want high (built-in model default)", got)
	}
}

func TestThinkingStaleSessionModeFallsBack(t *testing.T) {
	svc, provider, settings, id := newThinkingTestService(t)
	if err := settings.Set(contracts.SessionThinkingSettingKey(id), "removed-mode"); err != nil {
		t.Fatalf("set stale thinking: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if extra := lastExtra(t, provider); len(extra) != 0 {
		t.Fatalf("stale mode Extra = %v, want the default (empty)", extra)
	}
}

func TestThinkingUnknownModelInfersFamily(t *testing.T) {
	svc, provider, settings, id := newThinkingTestService(t)
	if err := settings.Set(contracts.SettingsChatModel, "some-qwen-model"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if err := settings.Set(contracts.SessionThinkingSettingKey(id), contracts.ThinkingModeQwenOff); err != nil {
		t.Fatalf("set thinking: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if got, ok := lastExtra(t, provider)["enable_thinking"].(bool); !ok || got {
		t.Fatalf("enable_thinking = %v, want false (model ID inferred as qwen)", got)
	}
}

// rigtoolRegistry returns a fresh tool registry for the thinking tests.
func rigtoolRegistry(t *testing.T) *rigtools.Registry {
	t.Helper()
	return rigtools.NewRegistry()
}
