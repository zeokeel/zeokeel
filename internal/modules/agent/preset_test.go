package agent

// Tests for agent presets: per-session selection and run-time application of
// model, persona/prompt and the tool allowlist.

import (
	"context"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// presetTool is a minimal named tool for allowlist filtering assertions.
type presetTool struct{ name string }

func (t presetTool) Name() string           { return t.name }
func (t presetTool) Description() string    { return t.name }
func (t presetTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t presetTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{}, nil
}

func presetSettings(t *testing.T) *fakeSettings {
	t.Helper()
	raw, err := contracts.EncodeAgentPresets([]contracts.AgentPreset{
		{ID: "p1", Name: "Reviewer", Tools: []string{"bash"}, Persona: "persona-one", Prompt: "prompt-one", Model: "preset-model"},
		{ID: "p2", Name: "Plain"},
	})
	if err != nil {
		t.Fatalf("encode presets: %v", err)
	}
	return newFakeSettings(map[string]string{contracts.SettingAgentPresets: raw})
}

func presetService(t *testing.T, provider rig.Provider) (*service, *fakeStore) {
	t.Helper()
	registry := rigtools.NewRegistry(presetTool{name: "bash"}, presetTool{name: "read_file"})
	store := newFakeStore()
	svc := newService(Config{SystemPrompt: "base"}, registry, presetSettings(t), &fakeLogger{}, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc, store
}

func TestSetSessionPresetAndCleanup(t *testing.T) {
	svc, _ := presetService(t, &captureProvider{})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, " p1 "); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	if got := svc.SessionPreset(sessionID); got != "p1" {
		t.Fatalf("SessionPreset = %q", got)
	}
	if err := svc.SetSessionPreset(sessionID, ""); err != nil || svc.SessionPreset(sessionID) != "" {
		t.Fatalf("clear preset = %q/%v", svc.SessionPreset(sessionID), err)
	}
	if err := svc.SetSessionPreset(sessionID, "missing"); err == nil {
		t.Fatal("unknown preset accepted")
	}
	if err := svc.SetSessionPreset("missing-session", "p1"); err == nil {
		t.Fatal("unknown session accepted")
	}
	if err := svc.SetSessionPreset(sessionID, "p1"); err != nil {
		t.Fatalf("set again: %v", err)
	}
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	svc.mu.Lock()
	_, present := svc.sessionPresets[sessionID]
	svc.mu.Unlock()
	if present {
		t.Fatal("preset selection survived session deletion")
	}
}

func TestPresetAppliedOnRun(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := presetService(t, provider)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, "p1"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	request := provider.lastRequest()
	if request.Model != "preset-model" {
		t.Fatalf("request model = %q, want preset-model", request.Model)
	}
	var system strings.Builder
	for _, message := range request.Messages {
		if message.Role == rig.RoleSystem {
			system.WriteString(message.Content[0].Text)
		}
	}
	text := system.String()
	if !strings.Contains(text, "base") || !strings.Contains(text, "persona-one") || !strings.Contains(text, "prompt-one") {
		t.Fatalf("system prompt = %q", text)
	}
	baseIndex := strings.Index(text, "base")
	personaIndex := strings.Index(text, "persona-one")
	promptIndex := strings.Index(text, "prompt-one")
	if !(baseIndex < personaIndex && personaIndex < promptIndex) {
		t.Fatalf("preset order = %d/%d/%d in %q", baseIndex, personaIndex, promptIndex, text)
	}
	var names []string
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "bash" {
		t.Fatalf("filtered tools = %v, want [bash]", names)
	}
}

func TestPresetSessionModelOverrideWins(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := presetService(t, provider)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, "p1"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	svc.settings.(*fakeSettings).values[contracts.SessionModelSettingKey(sessionID)] = "explicit-model"
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	if got := provider.lastRequest().Model; got != "explicit-model" {
		t.Fatalf("request model = %q, want explicit-model", got)
	}
}

func TestNoPresetKeepsDefaults(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := presetService(t, provider)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	request := provider.lastRequest()
	if request.Model != DefaultModel || len(request.Tools) != 2 {
		t.Fatalf("default run = model %q tools %d", request.Model, len(request.Tools))
	}
}
