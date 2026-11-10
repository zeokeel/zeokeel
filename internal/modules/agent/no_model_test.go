package agent

// Tests for the "no model configured" state: an explicitly saved empty model
// list (with no legacy chat.model) must not resurrect an assembly/DefaultModel
// fallback. Config().Model reports the truth so the UI shows the not-configured
// state, and Send fails with a readable error before touching the session or the
// provider.

import (
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestConfigExplicitEmptyListHasNoDefaultModel pins the tightened empty-list
// semantics: a saved empty list with no chat.model means "no model", not "use the
// assembly default", so the UI can show the not-configured state.
func TestConfigExplicitEmptyListHasNoDefaultModel(t *testing.T) {
	svc := newService(Config{Model: "assembly-model", BaseURL: "https://assembly.example/v1"},
		rigtoolRegistry(t), newFakeSettings(map[string]string{
			contracts.SettingModelCatalogModels: "[]",
		}), &fakeLogger{}, nil, fakeApprovals{})
	if got := svc.Config().Model; got != "" {
		t.Fatalf("Config().Model = %q, want empty (explicit empty list, no chat.model)", got)
	}
	// The session read model is equally truthful (the composer shows "未设置").
	id, err := svc.NewSession("empty-list")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session, _ := svc.Session(id); session.Model != "" {
		t.Fatalf("session model = %q, want empty", session.Model)
	}
}

// TestConfigExplicitEmptyListKeepsChatModel covers the legacy fallback that still
// applies when chat.model names a model: the list is empty but the old
// single-model configuration remains usable.
func TestConfigExplicitEmptyListKeepsChatModel(t *testing.T) {
	svc := newService(Config{Model: "assembly-model"}, rigtoolRegistry(t),
		newFakeSettings(map[string]string{
			contracts.SettingModelCatalogModels: "[]",
			contracts.SettingsChatModel:         "legacy-model",
			contracts.SettingsChatBaseURL:       "https://legacy.example/v1",
			contracts.SettingsChatAPIKey:        "legacy-key",
		}), &fakeLogger{}, nil, fakeApprovals{})
	cfg := svc.Config()
	if cfg.Model != "legacy-model" || cfg.BaseURL != "https://legacy.example/v1" ||
		cfg.APIKey != "legacy-key" {
		t.Fatalf("config = %+v, want the legacy chat.model fallback", cfg)
	}
}

// TestConfigMissingListRowFallsBackToDefaultModel covers a settings store that
// never saved the model list row (old installations/headless): the previous
// fallback chain stays unchanged.
func TestConfigMissingListRowFallsBackToDefaultModel(t *testing.T) {
	svc := newService(Config{}, rigtoolRegistry(t), newFakeSettings(nil),
		&fakeLogger{}, nil, fakeApprovals{})
	if got := svc.Config().Model; got != DefaultModel {
		t.Fatalf("Config().Model = %q, want %q", got, DefaultModel)
	}
	assembly := newService(Config{Model: "assembly-model"}, rigtoolRegistry(t),
		newFakeSettings(nil), &fakeLogger{}, nil, fakeApprovals{})
	if got := assembly.Config().Model; got != "assembly-model" {
		t.Fatalf("assembly Config().Model = %q, want assembly-model", got)
	}
}

// newNoModelService builds a service whose explicit model list is empty (so the
// effective model is empty) with a recording provider factory.
func newNoModelService(t *testing.T) (*service, *configCapture) {
	t.Helper()
	svc := newService(Config{Model: "assembly-model"}, rigtoolRegistry(t),
		newFakeSettings(map[string]string{
			contracts.SettingModelCatalogModels: "[]",
		}), &fakeLogger{}, nil, fakeApprovals{})
	capture := &configCapture{}
	svc.providerFactory = capture.factory
	return svc, capture
}

// TestSendWithoutConfiguredModelFails covers Send: a readable error, no provider
// build/request and no session mutation.
func TestSendWithoutConfiguredModelFails(t *testing.T) {
	svc, capture := newNoModelService(t)
	id, err := svc.NewSession("no-model")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	err = svc.Send(id, "hello")
	if !errors.Is(err, ErrNoModelConfigured) {
		t.Fatalf("Send without a model = %v, want ErrNoModelConfigured", err)
	}
	if !strings.Contains(err.Error(), "no model configured") ||
		!strings.Contains(err.Error(), "Settings → Model") {
		t.Fatalf("error text = %q, want the readable settings hint", err.Error())
	}
	if got := capture.count(); got != 0 {
		t.Fatalf("provider builds = %d, want none (no request may be sent)", got)
	}
	session, ok := svc.Session(id)
	if !ok {
		t.Fatalf("session %q disappeared", id)
	}
	if len(session.Messages) != 0 || session.Running {
		t.Fatalf("session after failed send = %d messages, running=%v; want untouched",
			len(session.Messages), session.Running)
	}
}

// TestSendWithAttachmentsWithoutConfiguredModelFails covers the attachment send
// path: same readable error before any provider request or session change.
func TestSendWithAttachmentsWithoutConfiguredModelFails(t *testing.T) {
	svc, capture := newNoModelService(t)
	id, err := svc.NewSession("no-model-attachments")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	err = svc.SendWithAttachments(id, "describe", []contracts.Attachment{imageAttachment()})
	if !errors.Is(err, ErrNoModelConfigured) {
		t.Fatalf("SendWithAttachments without a model = %v, want ErrNoModelConfigured", err)
	}
	if got := capture.count(); got != 0 {
		t.Fatalf("provider builds = %d, want none (no request may be sent)", got)
	}
	session, _ := svc.Session(id)
	if len(session.Messages) != 0 || session.Running {
		t.Fatalf("session after failed send = %d messages, running=%v; want untouched",
			len(session.Messages), session.Running)
	}
}

// TestRegenerateWithoutConfiguredModelFails covers the retry path: after the
// user removes every model, Regenerate returns the same readable error without
// rewinding the turn or building a provider.
func TestRegenerateWithoutConfiguredModelFails(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"model-a","baseURL":"https://a.example/v1","apiKey":"key-a"}]`,
		contracts.SettingsChatModel:         "model-a",
	})
	svc := newService(Config{}, rigtoolRegistry(t), settings, &fakeLogger{}, nil, fakeApprovals{})
	capture := &configCapture{}
	svc.providerFactory = capture.factory
	id, err := svc.NewSession("regen-no-model")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	before, _ := svc.Session(id)
	if len(before.Messages) != 2 {
		t.Fatalf("messages after send = %d, want 2", len(before.Messages))
	}

	// The user deletes the model list and the legacy mirror: Regenerate has no
	// model to call anymore.
	if err := settings.Set(contracts.SettingModelCatalogModels, "[]"); err != nil {
		t.Fatalf("clear catalog: %v", err)
	}
	if err := settings.Set(contracts.SettingsChatModel, ""); err != nil {
		t.Fatalf("clear chat.model: %v", err)
	}
	err = svc.Regenerate(id)
	if !errors.Is(err, ErrNoModelConfigured) {
		t.Fatalf("Regenerate without a model = %v, want ErrNoModelConfigured", err)
	}
	if !strings.Contains(err.Error(), "Settings → Model") {
		t.Fatalf("error text = %q, want the readable settings hint", err.Error())
	}
	if got := capture.count(); got != 1 {
		t.Fatalf("provider builds = %d, want only the original send (no request on retry)", got)
	}
	after, ok := svc.Session(id)
	if !ok {
		t.Fatalf("session %q disappeared", id)
	}
	if len(after.Messages) != 2 || after.Running {
		t.Fatalf("session after failed regenerate = %d messages, running=%v; want the turn untouched",
			len(after.Messages), after.Running)
	}
	if after.Messages[1].Streaming {
		t.Fatal("the failed regenerate left a streaming assistant placeholder")
	}
}

// TestConfigFallbackOrderWithModelRow covers the fallback review: a per-model row
// only owns BaseURL/APIKey. The system prompt keeps its chat.systemPrompt →
// assembly fallback, and an API key keeps the environment fallback when neither
// the row nor chat.apiKey provides one.
func TestConfigFallbackOrderWithModelRow(t *testing.T) {
	t.Setenv("AGENT_FALLBACK_TEST_KEY", "env-key")
	svc := newService(Config{
		BaseURL:      "https://assembly.example/v1",
		SystemPrompt: "assembly prompt",
		ApiKeyEnv:    "AGENT_FALLBACK_TEST_KEY",
	}, rigtoolRegistry(t), newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"plain","baseURL":"https://plain.example/v1"}]`,
	}), &fakeLogger{}, nil, fakeApprovals{})
	cfg := svc.Config()
	if cfg.SystemPrompt != "assembly prompt" {
		t.Fatalf("system prompt = %q, want the assembly fallback", cfg.SystemPrompt)
	}
	if cfg.BaseURL != "https://plain.example/v1" {
		t.Fatalf("base URL = %q, want the model row URL", cfg.BaseURL)
	}
	if cfg.APIKey != "env-key" {
		t.Fatalf("api key = %q, want the environment fallback", cfg.APIKey)
	}

	// chat.systemPrompt still wins over the assembly prompt.
	settings := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"plain"}]`,
		contracts.SettingsChatSystemPrompt:  "settings prompt",
	})
	svc2 := newService(Config{SystemPrompt: "assembly prompt"}, rigtoolRegistry(t),
		settings, &fakeLogger{}, nil, fakeApprovals{})
	if got := svc2.Config().SystemPrompt; got != "settings prompt" {
		t.Fatalf("system prompt = %q, want the settings override", got)
	}
}
