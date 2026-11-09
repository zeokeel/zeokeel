package session

// Composer prompt-preset menu: reads prompt.presets from the optional settings
// store (custom list first, built-in LegacyPromptPresets() when unset or
// malformed) and inserts one preset's prompt into the current draft. The menu
// item title is the preset name; native MenuItem has no secondary text, so the
// prompt is only inserted on choice.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
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

// chat backend implements contracts.SessionPresetSetter and presets exist).
func buildAgentPresetPicker(c *ui.Context, t *design.Tokens, chat contracts.ChatService, st *state, runtime contracts.UIRuntime, activeID string) {
	setter, ok := chat.(contracts.SessionPresetSetter)
	if !ok || activeID == "" {
		return
	}
	presets := effectiveAgentPresets(st)
	if len(presets) == 0 {
		return
	}
	currentID := strings.TrimSpace(setter.SessionPreset(activeID))
	currentName := ""
	for _, preset := range presets {
		if preset.ID == currentID {
			currentName = preset.Name
			break
		}
	}
	// The chip stays short (the toolbar has to fit the window): it names the
	// feature, and the selected preset name when one is applied.
	label := st.tr("人设", "Persona")
	if currentName != "" {
		label = st.tr("人设：", "Persona: ") + currentName
	}
	ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		m.Item(st.tr("选择代理预设", "Choose an agent preset")).Disabled(true)
		m.Separator()
		none := m.Item(st.tr("无", "None"))
		none.Radio(currentID == "")
		if none.Chosen() && currentID != "" {
			applySessionPreset(setter, st, runtime, activeID, "")
		}
		for _, preset := range presets {
			preset := preset
			item := m.Item(preset.Name)
			item.Radio(preset.ID == currentID)
			if item.Chosen() && preset.ID != currentID {
				applySessionPreset(setter, st, runtime, activeID, preset.ID)
			}
		}
	}).Tooltip(st.tr("为本会话选择代理预设（人设）", "Choose an agent preset (persona) for this session"))
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
