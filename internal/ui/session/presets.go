package session

// Composer prompt-preset menu: reads prompt.presets from the optional settings
// store (custom list first, built-in LegacyPromptPresets() when unset or
// malformed) and inserts one preset's prompt into the current draft. The menu
// item title is the preset name; native MenuItem has no secondary text, so the
// prompt is only inserted on choice.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// effectivePromptPresets returns the presets the composer menu offers. An empty
// or malformed settings row (and a missing settings service) falls back to the
// built-in defaults; rows without a usable name/prompt are skipped.
func effectivePromptPresets(st *state) []contracts.PromptPreset {
	if st.settings == nil {
		return contracts.LegacyPromptPresets()
	}
	presets, err := contracts.ParsePromptPresets(st.settings.Get(contracts.SettingPromptPresets, ""))
	if err != nil || len(presets) == 0 {
		return contracts.LegacyPromptPresets()
	}
	usable := make([]contracts.PromptPreset, 0, len(presets))
	for _, preset := range presets {
		name := strings.TrimSpace(preset.Name)
		prompt := strings.TrimSpace(preset.Prompt)
		if name == "" || prompt == "" {
			continue
		}
		usable = append(usable, contracts.PromptPreset{Name: name, Prompt: prompt})
	}
	if len(usable) == 0 {
		return contracts.LegacyPromptPresets()
	}
	return usable
}

// insertPromptPreset writes one preset prompt into the composer draft: an empty
// draft becomes the prompt, otherwise the prompt is appended after a blank
// line. This deliberately replaces the old selection/cursor insertion model
// with a deterministic draft operation.
func insertPromptPreset(st *state, prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return
	}
	if strings.TrimSpace(st.draft) == "" {
		st.draft = prompt
		return
	}
	st.draft = strings.TrimRight(st.draft, "\n") + "\n\n" + prompt
}

// effectiveAgentPresets parses presets.agents for the persona picker. Missing
// or malformed settings yield no presets (the picker is hidden).
func effectiveAgentPresets(st *state) []contracts.AgentPreset {
	if st == nil || st.settings == nil {
		return nil
	}
	presets, err := contracts.ParseAgentPresets(st.settings.Get(contracts.SettingAgentPresets, ""))
	if err != nil {
		return nil
	}
	out := make([]contracts.AgentPreset, 0, len(presets))
	for _, preset := range presets {
		preset.ID = strings.TrimSpace(preset.ID)
		preset.Name = strings.TrimSpace(preset.Name)
		if preset.ID == "" || preset.Name == "" {
			continue
		}
		out = append(out, preset)
	}
	return out
}

// applySessionPreset selects/clears the active session's preset, surfacing a
// failure in the composer's command error line.
func applySessionPreset(setter contracts.SessionPresetSetter, st *state, runtime contracts.UIRuntime, sessionID, presetID string) {
	if err := setter.SetSessionPreset(sessionID, presetID); err != nil {
		st.commandErr = st.tr("应用人设失败：", "Failed to apply persona: ") + err.Error()
	} else {
		st.commandErr = ""
	}
	runtime.Invalidate()
}
