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

// promptSummary truncates a preset prompt for the menu's muted note line.
func promptSummary(prompt string) string {
	runes := []rune(strings.TrimSpace(prompt))
	const limit = 60
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

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
