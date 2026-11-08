package settings

// Provider preset chips on the model page: one-click base URL/model fill for the
// common OpenAI-compatible providers. Unlike the legacy form-filling presets,
// these write the settings rows directly (the model page's saved config is the
// product's source of truth), and the current base URL highlights its provider.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// providerQuickPreset is one one-click provider configuration. API keys are never
// filled: the user enters their own key in the field below.
type providerQuickPreset struct {
	id      string
	zh      string
	en      string
	baseURL string
	model   string
}

// providerQuickPresets is the preset table (the empty base URL of the custom entry
// clears the service URL while keeping the model).
var providerQuickPresets = []providerQuickPreset{
	{id: "deepseek", zh: "DeepSeek", en: "DeepSeek",
		baseURL: "https://api.deepseek.com/v1", model: "deepseek-chat"},
	{id: "openai", zh: "OpenAI", en: "OpenAI",
		baseURL: "https://api.openai.com/v1", model: "gpt-4o-mini"},
	{id: "ollama", zh: "Ollama（本地）", en: "Ollama (local)",
		baseURL: "http://127.0.0.1:11434/v1", model: "qwen2.5:7b"},
	{id: "custom", zh: "自定义", en: "Custom", baseURL: "", model: ""},
}

// normalizeProviderURL compares preset URLs without their trailing slash.
func normalizeProviderURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

// providerQuickPresetName renders one preset's localized chip label.
func providerQuickPresetName(preset providerQuickPreset, locale contracts.LocaleService) string {
	return localized(locale, preset.zh, preset.en)
}

// matchingProviderPreset returns the preset whose base URL equals the current
// one, or nil when the base URL is unset/custom.
func matchingProviderPreset(baseURL string) *providerQuickPreset {
	current := normalizeProviderURL(baseURL)
	if current == "" {
		return nil
	}
	for index, preset := range providerQuickPresets {
		if preset.id == "custom" || preset.baseURL == "" {
			continue
		}
		if normalizeProviderURL(preset.baseURL) == current {
			return &providerQuickPresets[index]
		}
	}
	return nil
}

// applyProviderPreset writes the preset's settings rows (base URL + model;
// the custom entry only clears the base URL) and updates the page state so the
// inputs and the match badge follow immediately.
func applyProviderPreset(preset providerQuickPreset, state *moduleState,
	store contracts.SettingsService, locale contracts.LocaleService) {
	if store == nil {
		state.status = localized(locale, "应用预设失败：", "Failed to apply preset: ") + "settings unavailable"
		state.statusErr = true
		return
	}
	// Applying a preset updates two rows; snapshot the old values first so a
	// partially failed write can roll back instead of leaving a mixed
	// baseURL/model pair (AUDIT-P3-15).
	oldBaseURL := store.Get(contracts.SettingsChatBaseURL, "")
	oldModel := store.Get(contracts.SettingsChatModel, "")
	rollback := func() {
		_ = store.Set(contracts.SettingsChatBaseURL, oldBaseURL)
		if preset.id != "custom" {
			_ = store.Set(contracts.SettingsChatModel, oldModel)
		}
	}
	fail := func(err error) {
		rollback()
		state.status = localized(locale, "应用预设失败：", "Failed to apply preset: ") + err.Error()
		state.statusErr = true
	}
	if err := store.Set(contracts.SettingsChatBaseURL, preset.baseURL); err != nil {
		fail(err)
		return
	}
	// The custom entry keeps the current model; the named providers set their
	// documented default model.
	if preset.id != "custom" {
		if err := store.Set(contracts.SettingsChatModel, preset.model); err != nil {
			fail(err)
			return
		}
		state.model = preset.model
	}
	state.baseURL = preset.baseURL
	state.status = localized(locale, "已应用预设：", "Applied preset: ") + providerQuickPresetName(preset, locale)
	state.statusErr = false
}

// buildProviderPresets renders the provider preset row inside the model page's
// quick-preset panel.
func buildProviderPresets(c *ui.Context, t *design.Tokens, state *moduleState,
	store contracts.SettingsService, locale contracts.LocaleService) {
	current := matchingProviderPreset(state.baseURL)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, localized(locale, "服务商预设", "Provider presets")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Shrink(0)
		for _, preset := range providerQuickPresets {
			preset := preset
			label := providerQuickPresetName(preset, locale)
			button := ui.Button(c, label)
			match := current != nil && preset.id != "custom" && current.id == preset.id
			if match {
				button = design.Apply(button, t.PrimaryButton())
			} else {
				button = design.Apply(button, secondaryButtonStyle(t))
			}
			if button.Clicked() {
				applyProviderPreset(preset, state, store, locale)
				c.Invalidate()
			}
			if match {
				design.Apply(ui.Badge(c, localized(locale, "已匹配", "Active")), t.Badge()).
					TextColor(t.Success)
			}
		}
	})
}
