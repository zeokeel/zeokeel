package agent

// Tests for per-model provider connections: each catalog row may carry its own
// OpenAI-compatible BaseURL/APIKey, so one model list can mix providers. The
// configured model list also decides the default model; only an empty list falls
// back to the legacy chat.baseURL/chat.apiKey → assembly config → environment
// chain.

import (
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// connectionCatalog encodes three model rows: two with their own connections and
// one that relies on the global fallback.
func connectionCatalog(t *testing.T) string {
	t.Helper()
	encoded, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{
		{ID: "model-a", Name: "Model A", BaseURL: "https://a.example/v1", APIKey: "key-a"},
		{ID: "model-b", Name: "Model B", BaseURL: "https://b.example/v1/", APIKey: "key-b"},
		{ID: "shared", Name: "Shared"},
	})
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	return encoded
}

// configCapture records the ChatConfig every provider build received (the
// providerFactory seam), so tests can assert the resolved connection.
type configCapture struct {
	mu   sync.Mutex
	cfgs []contracts.ChatConfig
}

func (c *configCapture) record(cfg contracts.ChatConfig) {
	c.mu.Lock()
	c.cfgs = append(c.cfgs, cfg)
	c.mu.Unlock()
}

func (c *configCapture) factory(cfg contracts.ChatConfig) (rig.Provider, error) {
	c.record(cfg)
	return &captureProvider{}, nil
}

// factoryWith records the config and serves requests through provider, so a test
// can assert both the provider config and the request the provider receives.
func (c *configCapture) factoryWith(provider rig.Provider) func(contracts.ChatConfig) (rig.Provider, error) {
	return func(cfg contracts.ChatConfig) (rig.Provider, error) {
		c.record(cfg)
		return provider, nil
	}
}

// count returns the number of provider builds recorded so far.
func (c *configCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cfgs)
}

func (c *configCapture) last(t *testing.T) contracts.ChatConfig {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cfgs) == 0 {
		t.Fatal("provider factory was never called")
	}
	return c.cfgs[len(c.cfgs)-1]
}

// newConnectionService builds a service over the given settings, with the
// provider factory recording each build's config.
func newConnectionService(t *testing.T, settings map[string]string) (*service, *configCapture) {
	t.Helper()
	svc := newService(Config{}, rigtoolRegistry(t), newFakeSettings(settings),
		&fakeLogger{}, nil, fakeApprovals{})
	capture := &configCapture{}
	svc.providerFactory = capture.factory
	return svc, capture
}

// TestConfigUsesDefaultModelConnection covers Config() for the default model: the
// catalog row's own base URL/key win over the global chat.baseURL/chat.apiKey.
func TestConfigUsesDefaultModelConnection(t *testing.T) {
	svc, _ := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatModel:         "model-b",
		contracts.SettingsChatBaseURL:       "https://global.example/v1",
		contracts.SettingsChatAPIKey:        "global-key",
	})
	cfg := svc.Config()
	if cfg.Model != "model-b" || cfg.BaseURL != "https://b.example/v1" || cfg.APIKey != "key-b" {
		t.Fatalf("default config = model %q / url %q / key %q, want the model-b row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}

	// A stale chat.model that is no longer a row falls back to the first row:
	// with a configured list the list decides, not the legacy mirror.
	settings := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatModel:         "removed-model",
	})
	svc2 := newService(Config{}, rigtoolRegistry(t), settings, &fakeLogger{}, nil, fakeApprovals{})
	cfg = svc2.Config()
	if cfg.Model != "model-a" || cfg.BaseURL != "https://a.example/v1" || cfg.APIKey != "key-a" {
		t.Fatalf("stale-default config = model %q / url %q / key %q, want the first row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
}

// TestConfigDefaultModelWithoutChatModel covers an unset chat.model: the first
// catalog row is the default and its connection applies.
func TestConfigDefaultModelWithoutChatModel(t *testing.T) {
	svc, _ := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatBaseURL:       "https://global.example/v1",
		contracts.SettingsChatAPIKey:        "global-key",
	})
	cfg := svc.Config()
	if cfg.Model != "model-a" || cfg.BaseURL != "https://a.example/v1" || cfg.APIKey != "key-a" {
		t.Fatalf("default config = model %q / url %q / key %q, want the first row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
}

// TestConfigRowWithoutConnectionFallsBackToGlobal covers a row that names no
// endpoint/credential: the global chat.* connection still applies.
func TestConfigRowWithoutConnectionFallsBackToGlobal(t *testing.T) {
	svc, _ := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatModel:         "shared",
		contracts.SettingsChatBaseURL:       "https://global.example/v1/",
		contracts.SettingsChatAPIKey:        "global-key",
	})
	cfg := svc.Config()
	if cfg.Model != "shared" || cfg.BaseURL != "https://global.example/v1" || cfg.APIKey != "global-key" {
		t.Fatalf("fallback config = model %q / url %q / key %q, want the global connection",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
}

// TestConfigConnectionFallbacks covers the assembly and environment fallbacks
// when the model row and chat.* leave a field empty.
func TestConfigConnectionFallbacks(t *testing.T) {
	t.Setenv("AGENT_MODEL_TEST_KEY", "env-key")
	svc := newService(Config{
		BaseURL:   "https://assembly.example/v1/",
		ApiKeyEnv: "AGENT_MODEL_TEST_KEY",
	}, rigtoolRegistry(t), newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"own-url","baseURL":"https://own.example/v1"}]`,
		contracts.SettingsChatModel:         "own-url",
	}), &fakeLogger{}, nil, fakeApprovals{})
	cfg := svc.Config()
	if cfg.BaseURL != "https://own.example/v1" || cfg.APIKey != "env-key" {
		t.Fatalf("env fallback = url %q / key %q, want the row URL and env key",
			cfg.BaseURL, cfg.APIKey)
	}

	// With no row of its own, the assembly base URL applies.
	svc2 := newService(Config{
		BaseURL:   "https://assembly.example/v1/",
		ApiKeyEnv: "AGENT_MODEL_TEST_KEY",
	}, rigtoolRegistry(t), newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"plain"}]`,
		contracts.SettingsChatModel:         "plain",
	}), &fakeLogger{}, nil, fakeApprovals{})
	cfg = svc2.Config()
	if cfg.BaseURL != "https://assembly.example/v1" || cfg.APIKey != "env-key" {
		t.Fatalf("assembly fallback = url %q / key %q", cfg.BaseURL, cfg.APIKey)
	}
}

// TestConfigExplicitEmptyListFallsBackToLegacy covers the explicit "[]" list: the
// user removed every model, so the default model and connection come from the
// legacy chain again.
func TestConfigExplicitEmptyListFallsBackToLegacy(t *testing.T) {
	svc, _ := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: "[]",
		contracts.SettingsChatModel:         "legacy-model",
		contracts.SettingsChatBaseURL:       "https://legacy.example/v1",
		contracts.SettingsChatAPIKey:        "legacy-key",
	})
	cfg := svc.Config()
	if cfg.Model != "legacy-model" || cfg.BaseURL != "https://legacy.example/v1" ||
		cfg.APIKey != "legacy-key" {
		t.Fatalf("legacy config = model %q / url %q / key %q",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
}

// TestSessionModelConnectionReachesProvider covers the run seam: a per-session
// model switch resolves that row's own connection for the provider, while the
// global Config() keeps the default row's connection.
func TestSessionModelConnectionReachesProvider(t *testing.T) {
	svc, capture := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatModel:         "model-a",
		contracts.SettingsChatBaseURL:       "https://global.example/v1",
		contracts.SettingsChatAPIKey:        "global-key",
	})
	id, err := svc.NewSession("connections")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session, _ := svc.Session(id); session.Model != "model-a" {
		t.Fatalf("default session model = %q, want model-a", session.Model)
	}
	if err := svc.SetSessionModel(id, "model-b"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)

	cfg := capture.last(t)
	if cfg.Model != "model-b" || cfg.BaseURL != "https://b.example/v1" || cfg.APIKey != "key-b" {
		t.Fatalf("run config = model %q / url %q / key %q, want the model-b row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
	// Config() is the default-model config other UI reads: the session override
	// must not leak into it.
	if def := svc.Config(); def.Model != "model-a" || def.APIKey != "key-a" {
		t.Fatalf("Config() = model %q / key %q, want the default row", def.Model, def.APIKey)
	}
	if session, _ := svc.Session(id); session.Model != "model-b" {
		t.Fatalf("session model = %q, want model-b", session.Model)
	}
}

// TestSessionModelUnknownConnectionFallsBackToGlobal covers a session model that
// is not a catalog row: its connection falls back to the global chain instead of
// borrowing the default row's endpoint.
func TestSessionModelUnknownConnectionFallsBackToGlobal(t *testing.T) {
	svc, capture := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatModel:         "model-a",
		contracts.SettingsChatBaseURL:       "https://global.example/v1",
		contracts.SettingsChatAPIKey:        "global-key",
	})
	id, err := svc.NewSession("unknown-connection")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionModel(id, "external-model"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	cfg := capture.last(t)
	if cfg.Model != "external-model" || cfg.BaseURL != "https://global.example/v1" ||
		cfg.APIKey != "global-key" {
		t.Fatalf("unknown-model run config = model %q / url %q / key %q",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
}

// TestPresetModelConnectionReachesProvider covers the preset-model ordering (a
// session override does not exist): the preset's model resolves its own row
// connection.
func TestPresetModelConnectionReachesProvider(t *testing.T) {
	svc, capture := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatModel:         "model-a",
		contracts.SettingsChatBaseURL:       "https://global.example/v1",
		contracts.SettingsChatAPIKey:        "global-key",
		contracts.SettingAgentPresets:       `[{"id":"p1","name":"P1","model":"model-b"}]`,
	})
	id, err := svc.NewSession("preset-connection")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(id, "p1"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	cfg := capture.last(t)
	if cfg.Model != "model-b" || cfg.BaseURL != "https://b.example/v1" || cfg.APIKey != "key-b" {
		t.Fatalf("preset run config = model %q / url %q / key %q, want the model-b row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
}

// TestSubagentParentUsesSessionConnection covers a delegation: the child inherits
// the parent session's model and its per-model connection.
func TestSubagentParentUsesSessionConnection(t *testing.T) {
	svc, _ := newConnectionService(t, map[string]string{
		contracts.SettingModelCatalogModels: connectionCatalog(t),
		contracts.SettingsChatModel:         "model-a",
		contracts.SettingsChatBaseURL:       "https://global.example/v1",
		contracts.SettingsChatAPIKey:        "global-key",
	})
	id, err := svc.NewSession("subagent-parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetActiveSession(id); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := svc.SetSessionModel(id, "model-b"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	cfg, active := svc.subagentParent("")
	if active != id {
		t.Fatalf("active session = %q, want %q", active, id)
	}
	if cfg.Model != "model-b" || cfg.BaseURL != "https://b.example/v1" || cfg.APIKey != "key-b" {
		t.Fatalf("subagent config = model %q / url %q / key %q, want the model-b row",
			cfg.Model, cfg.BaseURL, cfg.APIKey)
	}
}
