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

// applyProviderPreset fills the model/base URL drafts from a preset (the custom
// entry only clears the base URL). Presets are form helpers, not a second
// writer: the page's single Save action persists the filled form, so the model
// page has exactly one save semantic.
func applyProviderPreset(preset providerQuickPreset, state *moduleState, locale contracts.LocaleService) {
	if preset.id != "custom" {
		state.model = preset.model
	}
	state.baseURL = preset.baseURL
	state.status = localized(locale, "已填入预设：", "Preset filled: ") +
		providerQuickPresetName(preset, locale)
	state.statusErr = false
}

// providerPresetRow renders the provider preset row inside the model page's
// quick-preset section. The chip matching the draft base URL is highlighted.
func buildProviderPresets(c *ui.Context, t *design.Tokens, state *moduleState,
	locale contracts.LocaleService) {
	current := matchingProviderPreset(state.baseURL)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, localized(locale, "服务商预设", "Provider presets")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Shrink(0)
		for _, preset := range providerQuickPresets {
			preset := preset
			label := providerQuickPresetName(preset, locale)
			button := design.Apply(ui.Button(c, label), t.SecondaryButton())
			if button.Clicked() {
				applyProviderPreset(preset, state, locale)
				c.Invalidate()
			}
			if current != nil && preset.id != "custom" && current.id == preset.id {
				design.Apply(ui.Badge(c, localized(locale, "已匹配", "Active")), t.Badge()).
					TextColor(t.SuccessText)
			}
		}
	})
}
