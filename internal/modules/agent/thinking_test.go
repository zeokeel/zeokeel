package agent

// Tests for per-model thinking injection: catalog family modes, per-(conversation,
// model) overrides, model default modes, custom mode parameters and the legacy
// effort/thinking fallbacks all reach the provider through Request.Extra.

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

// TestThinkingNoBuiltinDefaultMode pins the removal of the built-in model
// catalog: without a user-configured model row there is no per-model default
// thinking mode, so a deepseek-named model starts with no reasoning fields (the
// legacy per-session/config effort still applies when set explicitly).
func TestThinkingNoBuiltinDefaultMode(t *testing.T) {
	svc := newService(Config{}, rigtoolRegistry(t), nil, &fakeLogger{}, nil, fakeApprovals{})
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("default-thinking")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.settings = newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "deepseek-reasoner",
		contracts.SettingsChatBaseURL: "http://127.0.0.1:1/v1",
		contracts.SettingsChatAPIKey:  "test-key",
	})
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if extra := lastExtra(t, provider); len(extra) != 0 {
		t.Fatalf("thinking Extra = %v, want empty (no built-in model default)", extra)
	}
}

// TestThinkingCatalogModelDefaultMode covers the replacement for the removed
// defaults: a user catalog row may declare its own default thinking mode.
func TestThinkingCatalogModelDefaultMode(t *testing.T) {
	catalog, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{
		{ID: "deepseek-reasoner", Family: contracts.ModelFamilyDeepSeek,
			DefaultThinking: contracts.ThinkingModeReasoningHigh},
	})
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	provider := &captureProvider{}
	svc := newService(Config{}, rigtoolRegistry(t), newFakeSettings(map[string]string{
		contracts.SettingsChatModel:         "deepseek-reasoner",
		contracts.SettingsChatBaseURL:       "http://127.0.0.1:1/v1",
		contracts.SettingsChatAPIKey:        "test-key",
		contracts.SettingModelCatalogModels: catalog,
	}), &fakeLogger{}, nil, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("catalog-default-thinking")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if got := lastExtra(t, provider)["reasoning_effort"]; got != contracts.SessionEffortHigh {
		t.Fatalf("reasoning_effort = %v, want high (catalog model default)", got)
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

// TestThinkingMalformedCatalogInfersFamily covers a malformed model list: it
// resolves as "no configured rows", so the model ID still infers the family and
// the run keeps working.
func TestThinkingMalformedCatalogInfersFamily(t *testing.T) {
	svc, provider, settings, id := newThinkingTestService(t)
	if err := settings.Set(contracts.SettingModelCatalogModels, "{not-json"); err != nil {
		t.Fatalf("set catalog: %v", err)
	}
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

// perModelThinkingCatalog renders two reasoning_effort-family models: model-a has
// no default thinking mode, model-b defaults to high. Both share the family
// built-in modes, so an override written for one model would be detectable if it
// leaked to the other.
func perModelThinkingCatalog(t *testing.T) string {
	t.Helper()
	encoded, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{
		{ID: "model-a", Name: "Model A", Family: contracts.ModelFamilyDeepSeek},
		{ID: "model-b", Name: "Model B", Family: contracts.ModelFamilyDeepSeek,
			DefaultThinking: contracts.ThinkingModeReasoningHigh},
	})
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	return encoded
}

// newPerModelThinkingService builds a service over two models and a capturing
// provider; the session's model can be switched between sends so the tests can
// assert the thinking override does not follow the conversation across models.
func newPerModelThinkingService(t *testing.T) (*service, *captureProvider, *fakeSettings, string) {
	t.Helper()
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:         "model-a",
		contracts.SettingModelCatalogModels: perModelThinkingCatalog(t),
		contracts.SettingsChatBaseURL:       "http://127.0.0.1:1/v1",
		contracts.SettingsChatAPIKey:        "test-key",
	})
	provider := &captureProvider{}
	svc := newService(Config{}, rigtoolRegistry(t), settings, &fakeLogger{}, nil, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("per-model-thinking")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, provider, settings, id
}

// sendExtra sends one prompt and returns the thinking Extra of its request.
func sendExtra(t *testing.T, svc *service, provider *captureProvider, id, prompt string) map[string]any {
	t.Helper()
	if err := svc.Send(id, prompt); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	return lastExtra(t, provider)
}

// TestThinkingOverrideIsScopedToSessionAndModel covers the product behavior: the
// thinking strength is stored per (conversation, model), so switching the
// conversation's model uses that model's own default instead of carrying the
// previous model's override over, and switching back restores it.
func TestThinkingOverrideIsScopedToSessionAndModel(t *testing.T) {
	svc, provider, settings, id := newPerModelThinkingService(t)
	if err := settings.Set(contracts.SessionModelThinkingSettingKey(id, "model-a"),
		contracts.ThinkingModeReasoningLow); err != nil {
		t.Fatalf("set per-model thinking: %v", err)
	}
	if got := sendExtra(t, svc, provider, id, "first")["reasoning_effort"]; got != contracts.SessionEffortLow {
		t.Fatalf("model-a reasoning_effort = %v, want low (its own override)", got)
	}

	// Switch the same conversation to model-b: its DefaultThinking applies and no
	// model-a strength leaks in (a leaked reasoning-low would win over the
	// default, so this assertion catches it).
	if err := settings.Set(contracts.SessionModelSettingKey(id), "model-b"); err != nil {
		t.Fatalf("set session model: %v", err)
	}
	if got := sendExtra(t, svc, provider, id, "second")["reasoning_effort"]; got != contracts.SessionEffortHigh {
		t.Fatalf("model-b reasoning_effort = %v, want high (model-b default, no carry-over)", got)
	}

	// Switching back restores model-a's stored per-model override.
	if err := settings.Set(contracts.SessionModelSettingKey(id), "model-a"); err != nil {
		t.Fatalf("set session model back: %v", err)
	}
	if got := sendExtra(t, svc, provider, id, "third")["reasoning_effort"]; got != contracts.SessionEffortLow {
		t.Fatalf("model-a reasoning_effort = %v, want low after switching back", got)
	}
}

// TestThinkingPerModelOverrideBeatsLegacySessionKey covers the precedence: a
// per-(conversation, model) row wins over the legacy conversation-wide row.
func TestThinkingPerModelOverrideBeatsLegacySessionKey(t *testing.T) {
	svc, provider, settings, id := newPerModelThinkingService(t)
	if err := settings.Set(contracts.SessionThinkingSettingKey(id),
		contracts.ThinkingModeReasoningMedium); err != nil {
		t.Fatalf("set legacy thinking: %v", err)
	}
	if err := settings.Set(contracts.SessionModelThinkingSettingKey(id, "model-a"),
		contracts.ThinkingModeReasoningLow); err != nil {
		t.Fatalf("set per-model thinking: %v", err)
	}
	if got := sendExtra(t, svc, provider, id, "hello")["reasoning_effort"]; got != contracts.SessionEffortLow {
		t.Fatalf("reasoning_effort = %v, want low (per-model row beats the legacy row)", got)
	}
}

// TestThinkingLegacySessionKeyAppliesWithoutPerModelOverride covers old
// conversations: the legacy conversation-wide row still applies when no
// per-(conversation, model) row exists.
func TestThinkingLegacySessionKeyAppliesWithoutPerModelOverride(t *testing.T) {
	svc, provider, settings, id := newPerModelThinkingService(t)
	if err := settings.Set(contracts.SessionThinkingSettingKey(id),
		contracts.ThinkingModeReasoningMedium); err != nil {
		t.Fatalf("set legacy thinking: %v", err)
	}
	// model-b's DefaultThinking is high, but the explicit legacy override (which
	// predates per-model rows) still wins over the default.
	if err := settings.Set(contracts.SessionModelSettingKey(id), "model-b"); err != nil {
		t.Fatalf("set session model: %v", err)
	}
	if got := sendExtra(t, svc, provider, id, "hello")["reasoning_effort"]; got != contracts.SessionEffortMedium {
		t.Fatalf("reasoning_effort = %v, want medium (legacy session row fallback)", got)
	}
}

// rigtoolRegistry returns a fresh tool registry for the thinking tests.
func rigtoolRegistry(t *testing.T) *rigtools.Registry {
	t.Helper()
	return rigtools.NewRegistry()
}
