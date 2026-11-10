package agent

// Tests for a subagent/team model override: the override runs with its own
// provider connection (the catalog row's BaseURL/APIKey) and its own thinking
// mode, while an override that names no configured model keeps the parent
// session's connection.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// subagentOverrideCatalog is the model list for the delegation override tests:
// model-a is the parent session default; model-b is the override with its own
// connection and a deepseek-family default thinking mode.
func subagentOverrideCatalog(t *testing.T) string {
	t.Helper()
	encoded, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{
		{ID: "model-a", Name: "Model A", BaseURL: "https://a.example/v1", APIKey: "key-a"},
		{ID: "model-b", Name: "Model B", BaseURL: "https://b.example/v1", APIKey: "key-b",
			Family: contracts.ModelFamilyDeepSeek, DefaultThinking: contracts.ThinkingModeReasoningHigh},
	})
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	return encoded
}

// newOverrideService builds a service whose parent default is model-a, with a
// provider factory recording each built config and serving recorded requests.
func newOverrideService(t *testing.T) (*service, *configCapture, *captureProvider) {
	t.Helper()
	svc := newService(Config{}, rigtoolRegistry(t), newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: subagentOverrideCatalog(t),
		contracts.SettingsChatModel:         "model-a",
		contracts.SettingsChatBaseURL:       "https://global.example/v1",
		contracts.SettingsChatAPIKey:        "global-key",
	}), &fakeLogger{}, nil, fakeApprovals{})
	capture := &configCapture{}
	provider := &captureProvider{}
	svc.providerFactory = capture.factoryWith(provider)
	return svc, capture, provider
}

// TestSubagentModelOverrideResolvesConnection covers service.Run with a model
// override: the child provider, the request model, the thinking parameters and
// the activity record all follow the overridden model and its catalog row
// connection.
func TestSubagentModelOverrideResolvesConnection(t *testing.T) {
	svc, capture, provider := newOverrideService(t)
	result, err := svc.Run(context.Background(), contracts.SubagentRequest{
		Prompt: "review the diff",
		Model:  "model-b",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Text != "ok" {
		t.Fatalf("subagent result = %q, want ok", result.Text)
	}
	cfg := capture.last(t)
	if cfg.Model != "model-b" || cfg.BaseURL != "https://b.example/v1" || cfg.APIKey != "key-b" {
		t.Fatalf("subagent config = model %q / url %q / key %q, want the model-b row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
	request := provider.lastRequest()
	if request.Model != "model-b" {
		t.Fatalf("request model = %q, want model-b", request.Model)
	}
	// Thinking resolves against the override model, not the parent session model.
	if got := request.Extra["reasoning_effort"]; got != contracts.SessionEffortHigh {
		t.Fatalf("reasoning_effort = %v, want high (from the override model row)", got)
	}
	activities := svc.SubagentActivities()
	if len(activities) != 1 {
		t.Fatalf("activities = %+v, want one delegation", activities)
	}
	if activities[0].Model != "model-b" || activities[0].Status != contracts.SubagentStatusDone {
		t.Fatalf("activity = %+v, want model-b done (config and record consistent)", activities[0])
	}
}

// TestSubagentModelOverrideUnknownKeepsParentConnection covers an override that is
// not a catalog row: the parent session's connection is reused.
func TestSubagentModelOverrideUnknownKeepsParentConnection(t *testing.T) {
	svc, capture, provider := newOverrideService(t)
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{
		Prompt: "review the diff",
		Model:  "external-model",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg := capture.last(t)
	if cfg.Model != "external-model" || cfg.BaseURL != "https://a.example/v1" ||
		cfg.APIKey != "key-a" {
		t.Fatalf("unknown-override config = model %q / url %q / key %q, want the parent connection",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
	if request := provider.lastRequest(); request.Model != "external-model" {
		t.Fatalf("request model = %q, want external-model", request.Model)
	}
}

// TestTeamModelOverrideResolvesConnection covers the team runner: a per-member
// spawn model override resolves that model's own connection and thinking mode.
func TestTeamModelOverrideResolvesConnection(t *testing.T) {
	svc, capture, provider := newOverrideService(t)
	runner := &dynamicTeamRunner{service: svc}
	sub, err := runner.Start(context.Background(), rig.SubagentSpec{
		Name:   "worker",
		Prompt: "do the work",
		Model:  "model-b",
	})
	if err != nil {
		t.Fatalf("team runner Start: %v", err)
	}
	defer func() {
		if stopErr := sub.Stop(); stopErr != nil {
			t.Fatalf("stop teammate: %v", stopErr)
		}
	}()
	result, err := sub.Wait(context.Background())
	if err != nil {
		t.Fatalf("teammate Wait: %v", err)
	}
	if result.Output != "ok" {
		t.Fatalf("teammate output = %q, want ok", result.Output)
	}
	cfg := capture.last(t)
	if cfg.Model != "model-b" || cfg.BaseURL != "https://b.example/v1" || cfg.APIKey != "key-b" {
		t.Fatalf("team member config = model %q / url %q / key %q, want the model-b row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
	request := provider.lastRequest()
	if request.Model != "model-b" {
		t.Fatalf("team request model = %q, want model-b", request.Model)
	}
	if got := request.Extra["reasoning_effort"]; got != contracts.SessionEffortHigh {
		t.Fatalf("team reasoning_effort = %v, want high (from the override model row)", got)
	}
}
