// Package modelcatalog is ZeoKeel's built-in provider and model catalog: the
// facts the Add/Edit model dialog and the composer's reasoning-effort menu need
// so a common provider works after the user pastes an API key.
//
// The catalog mirrors the built-in routes and model metadata the DSH reference
// client ships (its pi-ai catalog): one OpenAI-compatible endpoint per
// provider, the models it publishes, each model's combined context window, and
// the reasoning-effort levels its adapter accepts. ZeoKeel speaks OpenAI Chat
// Completions only, so providers that the framework routes over another
// protocol (Anthropic Messages, OpenAI Responses, Bedrock, ...) are not part of
// this catalog — a custom endpoint row stays available for them.
//
// The catalog is advisory: a configured model that does not match an entry
// still works; the entry only fills the form and decides which effort levels
// the composer offers.
package modelcatalog

import (
	"strings"
)

// Reasoning effort ids, from least to greatest. They are the wire values
// ZeoKeel sends as reasoning_effort; the framework's model metadata uses the
// same names. "off" disables thinking on models that accept it.
const (
	EffortOff     = "off"
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	EffortXHigh   = "xhigh"
	EffortMax     = "max"
)

// allEfforts is the ordered set of reasoning effort ids. A catalog entry lists
// the subset its model accepts, in the same order.
var allEfforts = []string{
	EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax,
}

// Model is one built-in model entry.
type Model struct {
	// ID is the wire model id the provider accepts.
	ID string
	// Name is the selector label; falls back to ID in the UI.
	Name string
	// ContextWindow is the model's combined request/response capacity in
	// tokens, 0 when the catalog does not know it.
	ContextWindow int
	// MaxTokens is the model's output cap in tokens, 0 when unknown.
	MaxTokens int
	// Efforts are the reasoning-effort ids this model accepts, ascending.
	// Empty means the model publishes no reasoning control (or does not
	// reason at all): the composer shows no effort section for it.
	Efforts []string
	// DefaultEffort is the effort a newly added model stores when the user
	// picks this entry; empty when Efforts is empty.
	DefaultEffort string
}

// Provider is one built-in OpenAI-compatible provider preset: the endpoint and
// key hint the Add model dialog fills, plus the models it publishes.
type Provider struct {
	// ID is the stable catalog id, also used as a settings value.
	ID string
	// Name is the product name (the same in every language).
	Name string
	// BaseURL is an OpenAI-compatible API root, without a trailing slash.
	BaseURL string
	// KeyHint is the placeholder shown in the API-key field.
	KeyHint string
	// KeyRequired is false for local endpoints that accept unauthenticated
	// requests (Ollama, llama.cpp, ...).
	KeyRequired bool
	// Models are the built-in entries for this provider, in presentation
	// order. The first one is prefilled when the provider is chosen.
	Models []Model
}

// providers is the built-in catalog. DeepSeek leads because it is ZeoKeel's
// primary provider; the rest follow the framework's common OpenAI-compatible
// routes. Ollama stays keyless.
var providers = []Provider{
	{
		ID:          "deepseek",
		Name:        "DeepSeek",
		BaseURL:     "https://api.deepseek.com",
		KeyHint:     "sk-...",
		KeyRequired: true,
		Models: []Model{
			{
				ID: "deepseek-flash", Name: "DeepSeek V4.1 Flash",
				ContextWindow: 1000000, MaxTokens: 384000,
				Efforts:       []string{EffortOff, EffortLow, EffortHigh, EffortMax},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro",
				ContextWindow: 1000000, MaxTokens: 384000,
				Efforts:       []string{EffortOff, EffortHigh, EffortMax},
				DefaultEffort: EffortHigh,
			},
		},
	},
	{
		ID:          "openai",
		Name:        "OpenAI",
		BaseURL:     "https://api.openai.com/v1",
		KeyHint:     "sk-...",
		KeyRequired: true,
		Models: []Model{
			{ID: "gpt-4.1", Name: "GPT-4.1", ContextWindow: 1047576, MaxTokens: 32768},
			{ID: "gpt-4.1-mini", Name: "GPT-4.1 mini", ContextWindow: 1047576, MaxTokens: 32768},
			{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000, MaxTokens: 16384},
			{ID: "gpt-4o-mini", Name: "GPT-4o mini", ContextWindow: 128000, MaxTokens: 16384},
		},
	},
	{
		ID:          "moonshotai",
		Name:        "Moonshot AI",
		BaseURL:     "https://api.moonshot.ai/v1",
		KeyHint:     "sk-...",
		KeyRequired: true,
		Models: []Model{
			{
				ID: "kimi-k3", Name: "Kimi K3",
				ContextWindow: 1048576, MaxTokens: 1048576,
				Efforts:       []string{EffortLow, EffortHigh, EffortMax},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "kimi-k2.6", Name: "Kimi K2.6",
				ContextWindow: 262144, MaxTokens: 262144,
				Efforts:       []string{EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code",
				ContextWindow: 262144, MaxTokens: 262144,
				Efforts:       []string{EffortMinimal, EffortLow, EffortMedium, EffortHigh},
				DefaultEffort: EffortHigh,
			},
		},
	},
	{
		ID:          "moonshotai-cn",
		Name:        "Moonshot AI (CN)",
		BaseURL:     "https://api.moonshot.cn/v1",
		KeyHint:     "sk-...",
		KeyRequired: true,
		Models: []Model{
			{
				ID: "kimi-k3", Name: "Kimi K3",
				ContextWindow: 1048576, MaxTokens: 1048576,
				Efforts:       []string{EffortLow, EffortHigh, EffortMax},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "kimi-k2.6", Name: "Kimi K2.6",
				ContextWindow: 262144, MaxTokens: 262144,
				Efforts:       []string{EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh},
				DefaultEffort: EffortHigh,
			},
		},
	},
	{
		ID:          "zai",
		Name:        "Z.AI",
		BaseURL:     "https://api.z.ai/api/coding/paas/v4",
		KeyHint:     "API key",
		KeyRequired: true,
		Models: []Model{
			{
				ID: "glm-5.3", Name: "GLM-5.3",
				ContextWindow: 1000000, MaxTokens: 131072,
				Efforts:       []string{EffortLow, EffortHigh, EffortMax},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "glm-5.2", Name: "GLM-5.2",
				ContextWindow: 1000000, MaxTokens: 131072,
				Efforts:       []string{EffortOff, EffortHigh, EffortMax},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "glm-4.7", Name: "GLM-4.7",
				ContextWindow: 204800, MaxTokens: 131072,
				Efforts:       []string{EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh},
				DefaultEffort: EffortHigh,
			},
		},
	},
	{
		ID:          "groq",
		Name:        "Groq",
		BaseURL:     "https://api.groq.com/openai/v1",
		KeyHint:     "gsk_...",
		KeyRequired: true,
		Models: []Model{
			{
				ID: "openai/gpt-oss-120b", Name: "GPT OSS 120B",
				ContextWindow: 131072, MaxTokens: 65536,
				Efforts:       []string{EffortLow, EffortMedium, EffortHigh},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "qwen/qwen3.8-27b", Name: "Qwen3.8 27B",
				ContextWindow: 131072, MaxTokens: 16384,
				Efforts:       []string{EffortOff, EffortLow, EffortMedium, EffortHigh},
				DefaultEffort: EffortHigh,
			},
			{ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B", ContextWindow: 131072, MaxTokens: 32768},
		},
	},
	{
		ID:          "together",
		Name:        "Together",
		BaseURL:     "https://api.together.ai/v1",
		KeyHint:     "tgp_v1_...",
		KeyRequired: true,
		Models: []Model{
			{
				ID: "deepseek-ai/DeepSeek-V4.1-Flash", Name: "DeepSeek V4.1 Flash",
				ContextWindow: 1048576, MaxTokens: 384000,
				Efforts:       []string{EffortOff, EffortHigh},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "moonshotai/Kimi-K3", Name: "Kimi K3",
				ContextWindow: 1048576, MaxTokens: 131072,
				Efforts:       []string{EffortOff, EffortHigh},
				DefaultEffort: EffortHigh,
			},
			{
				ID: "openai/gpt-oss-120b", Name: "GPT OSS 120B",
				ContextWindow: 131072, MaxTokens: 131072,
				Efforts:       []string{EffortLow, EffortMedium, EffortHigh},
				DefaultEffort: EffortHigh,
			},
		},
	},
	{
		ID:          "openrouter",
		Name:        "OpenRouter",
		BaseURL:     "https://openrouter.ai/api/v1",
		KeyHint:     "sk-or-v1-...",
		KeyRequired: true,
		Models: []Model{
			{ID: "openai/gpt-4.1", Name: "OpenAI: GPT-4.1", ContextWindow: 1047576, MaxTokens: 32768},
			{ID: "deepseek/deepseek-chat", Name: "DeepSeek: DeepSeek V3", ContextWindow: 128000, MaxTokens: 16000},
			{ID: "moonshotai/kimi-k2", Name: "MoonshotAI: Kimi K2", ContextWindow: 131072, MaxTokens: 98304},
		},
	},
	{
		ID:          "ollama",
		Name:        "Ollama",
		BaseURL:     "http://localhost:11434/v1",
		KeyHint:     "",
		KeyRequired: false,
		Models: []Model{
			{ID: "llama3.2", Name: "Llama 3.2", ContextWindow: 131072},
		},
	},
}

// Providers returns the built-in catalog in presentation order. The returned
// slice and its entries must be treated as read-only.
func Providers() []Provider {
	return providers
}

// LookupProvider returns the provider with id.
func LookupProvider(id string) (Provider, bool) {
	id = strings.TrimSpace(id)
	for _, provider := range providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}

// LookupModel returns the built-in model modelID of providerID. A provider id
// that is not in the catalog yields false.
func LookupModel(providerID, modelID string) (Model, bool) {
	provider, ok := LookupProvider(providerID)
	if !ok {
		return Model{}, false
	}
	modelID = strings.TrimSpace(modelID)
	for _, model := range provider.Models {
		if model.ID == modelID {
			return model, true
		}
	}
	return Model{}, false
}

// ProviderForBaseURL returns the provider whose endpoint is baseURL. It
// compares the normalized URLs (trailing slashes ignored) so a saved model
// re-selects the provider it came from when edited.
func ProviderForBaseURL(baseURL string) (Provider, bool) {
	base := normalizeBaseURL(baseURL)
	if base == "" {
		return Provider{}, false
	}
	for _, provider := range providers {
		if normalizeBaseURL(provider.BaseURL) == base {
			return provider, true
		}
	}
	return Provider{}, false
}

// normalizeBaseURL trims whitespace and trailing slashes.
func normalizeBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// EffortsFor returns the reasoning efforts modelID of providerID accepts.
// known is false when the provider/model pair is not in the catalog; callers
// then fall back to AllEfforts so a custom endpoint can still pick a level.
func EffortsFor(providerID, modelID string) (efforts []string, known bool) {
	model, ok := LookupModel(providerID, modelID)
	if !ok {
		return nil, false
	}
	return model.Efforts, true
}

// AllEfforts returns the ordered set of every known effort id. The returned
// slice must be treated as read-only.
func AllEfforts() []string {
	return allEfforts
}

// ValidEffort reports whether id is a known reasoning-effort id.
func ValidEffort(id string) bool {
	id = strings.TrimSpace(id)
	for _, effort := range allEfforts {
		if effort == id {
			return true
		}
	}
	return false
}

// EffortLabel is the visible name of an effort id: the id capitalized
// ("low" -> "Low"). Empty stays empty.
func EffortLabel(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	upper := strings.ToUpper(id[:1])
	return upper + id[1:]
}
