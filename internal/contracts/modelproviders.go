package contracts

// Common OpenAI-compatible model service providers, offered by the add-model
// dialog's provider selector. A preset only fills convenience defaults (the
// base URL and an example model id); the saved model row stores its own base
// URL and API key, so several providers can be configured side by side.

import "strings"

// ProviderPreset is one selectable service provider.
type ProviderPreset struct {
	// ID is the stable preset identifier ("deepseek", "openai", ..., "custom").
	ID string
	// NameZH/NameEN are the localized display labels.
	NameZH string
	NameEN string
	// BaseURL is the provider's OpenAI-compatible endpoint; the custom entry
	// has none and clears the field instead.
	BaseURL string
	// ExampleModel is a model id suggestion used to prefill an empty model id
	// when the preset is chosen; the custom entry has none.
	ExampleModel string
}

// DisplayName returns the localized label (falling back to the other language
// and then to the ID).
func (p ProviderPreset) DisplayName(english bool) string {
	if english {
		if name := strings.TrimSpace(p.NameEN); name != "" {
			return name
		}
		if name := strings.TrimSpace(p.NameZH); name != "" {
			return name
		}
		return p.ID
	}
	if name := strings.TrimSpace(p.NameZH); name != "" {
		return name
	}
	if name := strings.TrimSpace(p.NameEN); name != "" {
		return name
	}
	return p.ID
}

// IsCustom reports whether the preset is the free-form entry (no base URL).
func (p ProviderPreset) IsCustom() bool {
	return strings.TrimSpace(p.BaseURL) == ""
}

// providerPresets is the built-in provider table in display order. The last
// entry is the custom/any-OpenAI-compatible provider.
var providerPresets = []ProviderPreset{
	{ID: "deepseek", NameZH: "DeepSeek", NameEN: "DeepSeek",
		BaseURL: "https://api.deepseek.com/v1", ExampleModel: "deepseek-chat"},
	{ID: "openai", NameZH: "OpenAI", NameEN: "OpenAI",
		BaseURL: "https://api.openai.com/v1", ExampleModel: "gpt-4o-mini"},
	{ID: "ollama", NameZH: "Ollama（本地）", NameEN: "Ollama (local)",
		BaseURL: "http://127.0.0.1:11434/v1", ExampleModel: "qwen2.5:7b"},
	{ID: "dashscope", NameZH: "通义千问（百炼）", NameEN: "Qwen (DashScope)",
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", ExampleModel: "qwen-plus"},
	{ID: "moonshot", NameZH: "Kimi（月之暗面）", NameEN: "Kimi (Moonshot)",
		BaseURL: "https://api.moonshot.cn/v1", ExampleModel: "moonshot-v1-8k"},
	{ID: "zhipu", NameZH: "智谱 GLM", NameEN: "Zhipu GLM",
		BaseURL: "https://open.bigmodel.cn/api/paas/v4", ExampleModel: "glm-4-flash"},
	{ID: "siliconflow", NameZH: "硅基流动", NameEN: "SiliconFlow",
		BaseURL: "https://api.siliconflow.cn/v1", ExampleModel: "Qwen/Qwen2.5-7B-Instruct"},
	{ID: "openrouter", NameZH: "OpenRouter", NameEN: "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1", ExampleModel: "openai/gpt-4o-mini"},
	{ID: "gemini", NameZH: "Gemini", NameEN: "Gemini",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", ExampleModel: "gemini-2.0-flash"},
	{ID: "custom", NameZH: "自定义 / 其他兼容接口", NameEN: "Custom / other compatible endpoint"},
}

// ModelProviderPresets returns the built-in provider presets in display order.
func ModelProviderPresets() []ProviderPreset {
	out := make([]ProviderPreset, len(providerPresets))
	copy(out, providerPresets)
	return out
}

// ProviderPresetByID finds one provider preset by its stable ID.
func ProviderPresetByID(id string) (ProviderPreset, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, preset := range providerPresets {
		if preset.ID == id {
			return preset, true
		}
	}
	return ProviderPreset{}, false
}

// MatchProviderPreset finds the provider preset whose base URL matches the
// given endpoint (trailing slash and letter case are ignored). The custom entry
// never matches.
func MatchProviderPreset(baseURL string) (ProviderPreset, bool) {
	normalized := normalizeProviderURL(baseURL)
	if normalized == "" {
		return ProviderPreset{}, false
	}
	for _, preset := range providerPresets {
		if preset.IsCustom() {
			continue
		}
		if normalizeProviderURL(preset.BaseURL) == normalized {
			return preset, true
		}
	}
	return ProviderPreset{}, false
}

// normalizeProviderURL lower-cases and trims the trailing slash of one endpoint
// for preset matching.
func normalizeProviderURL(value string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
}
