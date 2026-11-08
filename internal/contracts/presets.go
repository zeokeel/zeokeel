package contracts

import (
	"encoding/json"
	"strings"
)

// PromptPreset is one reusable prompt/persona template offered in the composer
// (the old shell's prompt presets). Selecting it inserts its prompt into the
// draft; presets are user-editable.
type PromptPreset struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// SettingPromptPresets stores the user's prompt presets as a JSON array.
const SettingPromptPresets = "prompt.presets"

// ParsePromptPresets decodes the prompt.presets settings row. Empty/malformed
// JSON returns nil/no error for empty, an error for malformed.
func ParsePromptPresets(raw string) ([]PromptPreset, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var presets []PromptPreset
	if err := json.Unmarshal([]byte(raw), &presets); err != nil {
		return nil, err
	}
	return presets, nil
}

// EncodePromptPresets renders the prompt.presets settings row.
func EncodePromptPresets(presets []PromptPreset) (string, error) {
	if presets == nil {
		presets = []PromptPreset{}
	}
	encoded, err := json.Marshal(presets)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// LegacyPromptPresets returns the built-in default presets when the user has
// not configured any (the old shell shipped a small starter set).
func LegacyPromptPresets() []PromptPreset {
	return []PromptPreset{
		{Name: "简洁回答", Prompt: "请用简洁的中文回答，先给结论，再给必要的要点。"},
		{Name: "代码审查", Prompt: "请以严格的代码审查视角检查下面的改动，指出正确性、并发、错误处理和可测试性问题。"},
		{Name: "逐步推理", Prompt: "请先给出逐步推理过程，再给出最终结论。"},
	}
}

// SettingModelCatalogModels stores user-edited model presets as a JSON array of
// ModelInfo; the modelcatalog module merges them over its configured list.
const SettingModelCatalogModels = "modelcatalog.models"

// ParseModelInfoList decodes the modelcatalog.models settings row.
func ParseModelInfoList(raw string) ([]ModelInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var models []ModelInfo
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil, err
	}
	return models, nil
}

// EncodeModelInfoList renders the modelcatalog.models settings row.
func EncodeModelInfoList(models []ModelInfo) (string, error) {
	if models == nil {
		models = []ModelInfo{}
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// AgentPreset is one declarative agent preset: a persona/prompt fragment, an
// optional model default and an optional model-visible tool allowlist. A
// session stores only the preset ID; the agent backend applies the preset on
// every run.
type AgentPreset struct {
	// ID is the stable identifier a session stores. Required and unique.
	ID string `json:"id"`
	// Name is the display label.
	Name string `json:"name"`
	// Tools is the tool allowlist (empty = every registered tool).
	Tools []string `json:"tools,omitempty"`
	// Prompt is extra system-prompt guidance appended after the persona.
	Prompt string `json:"prompt,omitempty"`
	// Persona is the persona prefix text appended after the base prompt and
	// workspace instructions.
	Persona string `json:"persona,omitempty"`
	// Model is the preset's default model (a session model override wins).
	Model string `json:"model,omitempty"`
	// Order sorts presets in the picker; ties fall back to ID.
	Order int `json:"order,omitempty"`
}

// LegacyAgentPresets returns the built-in agent presets. There are none (the
// legacy product shipped an empty per-user preset set), so this is always nil.
func LegacyAgentPresets() []AgentPreset { return nil }

// SettingAgentPresets stores the user-edited agent presets as a JSON array.
const SettingAgentPresets = "presets.agents"

// ParseAgentPresets decodes the presets.agents settings row. Empty returns
// nil; malformed JSON is an error.
func ParseAgentPresets(raw string) ([]AgentPreset, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var presets []AgentPreset
	if err := json.Unmarshal([]byte(raw), &presets); err != nil {
		return nil, err
	}
	return presets, nil
}

// EncodeAgentPresets renders the presets.agents settings row.
func EncodeAgentPresets(presets []AgentPreset) (string, error) {
	if presets == nil {
		presets = []AgentPreset{}
	}
	encoded, err := json.Marshal(presets)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// SessionPresetSetter is the optional chat half that selects one agent preset
// per session ("" clears the selection). The UI's persona picker uses it.
type SessionPresetSetter interface {
	SetSessionPreset(sessionID, presetID string) error
	SessionPreset(sessionID string) string
}
