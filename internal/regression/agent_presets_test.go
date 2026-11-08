package regression

// Adversarial audit: agent presets. The per-session preset selection
// (SessionPresetSetter) validates session/preset IDs, "" clears it, and every
// run resolves the preset fresh: persona+prompt appended after the base prompt,
// tool allowlist filters the model-visible tools, the preset model is the
// default and an explicit per-session model override wins. The composer picker
// exposes the same selection.

import (
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// presetRequest decodes the model/tools/system fields of one provider request.
type presetRequest struct {
	Model    string   `json:"model"`
	System   string   `json:"system"`
	ToolName []string `json:"toolNames"`
}

// decodePresetRequest extracts the relevant request fields.
func decodePresetRequest(t *testing.T, body []byte) presetRequest {
	t.Helper()
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	request := presetRequest{Model: payload.Model}
	for _, message := range payload.Messages {
		if message.Role == "system" {
			request.System = message.Content
		}
	}
	for _, tool := range payload.Tools {
		request.ToolName = append(request.ToolName, tool.Function.Name)
	}
	return request
}

// presetContainstool reports whether a request exposes the named tool.
func presetContainsTool(request presetRequest, name string) bool {
	for _, tool := range request.ToolName {
		if tool == name {
			return true
		}
	}
	return false
}

// presetSettings seeds two agent presets.
func presetSettings(t *testing.T, harness *agentHarness) {
	t.Helper()
	encoded, err := contracts.EncodeAgentPresets([]contracts.AgentPreset{
		{ID: "alpha", Name: "Alpha", Persona: "PERSONA-ALPHA", Prompt: "PROMPT-ALPHA",
			Tools: []string{"tool-a"}, Model: "preset-model"},
		{ID: "beta", Name: "Beta", Persona: "PERSONA-BETA", Prompt: "PROMPT-BETA",
			Tools: []string{"tool-b"}},
	})
	if err != nil {
		t.Fatalf("encode presets: %v", err)
	}
	if err := harness.h.settings.Set(contracts.SettingAgentPresets, encoded); err != nil {
		t.Fatalf("set presets: %v", err)
	}
}

// TestAgentPresetSelectionAndRunApplication covers the setter validation and
// the run-time application (persona/prompt order, tools allowlist, model default
// and explicit per-session override, clearing).
func TestAgentPresetSelectionAndRunApplication(t *testing.T) {
	harness := startAgentWith(t, agent.Config{SystemPrompt: "BASE-PROMPT"},
		newFakeModel(t, func([]byte) (int, string) { return 0, sseAnswer("ok") }),
		fakeRigTool{name: "tool-a"}, fakeRigTool{name: "tool-b"})
	presetSettings(t, harness)
	sessionID := harness.newChatSession(t, "Preset session")
	setter, ok := harness.chat.(contracts.SessionPresetSetter)
	if !ok {
		t.Fatal("the agent chat service does not implement SessionPresetSetter")
	}

	// Validation: unknown session, unknown preset.
	if err := setter.SetSessionPreset("missing-session", "alpha"); err == nil {
		t.Fatal("SetSessionPreset accepted an unknown session")
	}
	if err := setter.SetSessionPreset(sessionID, "missing-preset"); err == nil {
		t.Fatal("SetSessionPreset accepted an unknown preset")
	}
	if err := setter.SetSessionPreset(sessionID, "alpha"); err != nil {
		t.Fatalf("select alpha: %v", err)
	}
	if got := setter.SessionPreset(sessionID); got != "alpha" {
		t.Fatalf("SessionPreset = %q, want alpha", got)
	}

	// First run applies persona/prompt after the base prompt, filters tools and
	// uses the preset model.
	harness.sendAndWait(t, sessionID, "first")
	first := decodePresetRequest(t, harness.model.lastBody())
	if !presetContainsTool(first, "tool-a") || presetContainsTool(first, "tool-b") {
		t.Fatalf("preset tools = %v, want only tool-a", first.ToolName)
	}
	if first.Model != "preset-model" {
		t.Fatalf("preset model = %q, want preset-model", first.Model)
	}
	baseAt := indexOf(first.System, "BASE-PROMPT")
	personaAt := indexOf(first.System, "PERSONA-ALPHA")
	promptAt := indexOf(first.System, "PROMPT-ALPHA")
	if baseAt < 0 || personaAt < 0 || promptAt < 0 || !(baseAt < personaAt && personaAt < promptAt) {
		t.Fatalf("system prompt order = base:%d persona:%d prompt:%d (%q)", baseAt, personaAt, promptAt, first.System)
	}

	// An explicit per-session model override wins over the preset model.
	switcher, ok := harness.chat.(contracts.SessionModelSwitcher)
	if !ok {
		t.Fatal("the agent chat service does not implement SessionModelSwitcher")
	}
	if err := switcher.SetSessionModel(sessionID, "explicit-model"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	harness.sendAndWait(t, sessionID, "second")
	if got := decodePresetRequest(t, harness.model.lastBody()).Model; got != "explicit-model" {
		t.Fatalf("explicit model override = %q, want explicit-model", got)
	}

	// Clearing the preset removes persona/prompt/tools filtering; the explicit
	// session model override still wins over the configured default.
	if err := setter.SetSessionPreset(sessionID, ""); err != nil {
		t.Fatalf("clear preset: %v", err)
	}
	if got := setter.SessionPreset(sessionID); got != "" {
		t.Fatalf("SessionPreset after clear = %q", got)
	}
	harness.sendAndWait(t, sessionID, "third")
	third := decodePresetRequest(t, harness.model.lastBody())
	if third.Model != "explicit-model" {
		t.Fatalf("model after clearing = %q, want the explicit session override", third.Model)
	}
	if !presetContainsTool(third, "tool-a") || !presetContainsTool(third, "tool-b") {
		t.Fatalf("tools after clearing = %v, want every registered tool", third.ToolName)
	}
	if indexOf(third.System, "PERSONA-ALPHA") >= 0 || indexOf(third.System, "PROMPT-ALPHA") >= 0 {
		t.Fatalf("preset instructions leaked after clearing: %q", third.System)
	}
}

// TestAgentPresetPickerUI covers the composer picker: the label shows the
// current selection, choosing a preset updates the session, and "None" clears.
func TestAgentPresetPickerUI(t *testing.T) {
	harness := startAgentWith(t, agent.Config{},
		newFakeModel(t, func([]byte) (int, string) { return 0, sseAnswer("ok") }))
	presetSettings(t, harness)
	sessionID := harness.newChatSession(t, "Picker session")
	activate(t, harness.h.ctx, sessionui.Module(), sessionui.Config{})
	harness.h.push(sessionui.ScreenID)
	tester := harness.h.tester(1280, 800)
	tester.Frame()
	requireHasText(t, tester, "人设：无")

	if err := tester.Click("人设：无"); err != nil {
		t.Fatalf("open preset picker: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "选择代理预设", "无", "Alpha", "Beta")
	if err := tester.Click("Alpha"); err != nil {
		t.Fatalf("choose Alpha: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "人设：Alpha")
	setter := harness.chat.(contracts.SessionPresetSetter)
	if got := setter.SessionPreset(sessionID); got != "alpha" {
		t.Fatalf("selected preset = %q, want alpha", got)
	}

	if err := tester.Click("人设：Alpha"); err != nil {
		t.Fatalf("reopen preset picker: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("无"); err != nil {
		t.Fatalf("choose None: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "人设：无")
	if got := setter.SessionPreset(sessionID); got != "" {
		t.Fatalf("preset after None = %q, want empty", got)
	}
}

// indexOf returns the byte index of sub in text (-1 when absent).
func indexOf(text, sub string) int {
	for index := 0; index+len(sub) <= len(text); index++ {
		if text[index:index+len(sub)] == sub {
			return index
		}
	}
	return -1
}
