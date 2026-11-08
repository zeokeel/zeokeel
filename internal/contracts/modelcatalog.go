package contracts

import cordis "github.com/zeokeel/zeokeel-kernel"

// ModelInfo is one selectable model preset in the composer model menu.
type ModelInfo struct {
	// ID is the model identifier sent to the provider (chat.model).
	ID string
	// Name is the display label.
	Name string
	// Note is a short hint (capability or provider).
	Note string
	// ContextWindow is the model's context-window size in tokens (0 = unknown,
	// not displayed).
	ContextWindow int `json:"contextWindow,omitempty"`
	// Family selects the built-in thinking protocol (qwen, deepseek, other,
	// custom). Empty infers it from the model ID.
	Family string `json:"family,omitempty"`
	// DefaultThinking is the thinking-mode ID this model uses unless a
	// conversation overrides it (empty = family default mode).
	DefaultThinking string `json:"defaultThinking,omitempty"`
	// ThinkingModes are user-defined thinking modes in addition to (or
	// overriding) the family built-ins.
	ThinkingModes []ThinkingMode `json:"thinkingModes,omitempty"`
	// Temperature is the sampling temperature sent on every request for this
	// model (nil = provider default).
	Temperature *float64 `json:"temperature,omitempty"`
	// TopP is the nucleus-sampling probability (nil = provider default).
	TopP *float64 `json:"topP,omitempty"`
	// MaxTokens bounds one completion in tokens (0 = provider default).
	MaxTokens int `json:"maxTokens,omitempty"`
}

// SanitizeGeneration normalizes per-model sampling options: a temperature
// outside [0,2] is clamped (NaN-like JSON values are impossible in Go), top_p
// outside (0,1] is clamped to the valid range, and a negative token cap becomes
// "provider default" (0).
func SanitizeGeneration(temperature, topP *float64, maxTokens int) (*float64, *float64, int) {
	if temperature != nil {
		value := *temperature
		if value < 0 {
			value = 0
		}
		if value > 2 {
			value = 2
		}
		temperature = &value
	}
	if topP != nil {
		value := *topP
		if value <= 0 {
			value = 0
		}
		if value > 1 {
			value = 1
		}
		topP = &value
	}
	if maxTokens < 0 {
		maxTokens = 0
	}
	return temperature, topP, maxTokens
}

// ModelCatalogService lists the model presets offered by the product.
type ModelCatalogService interface {
	Models() []ModelInfo
}

// KeyModelCatalog is provided by the model catalog backend module.
var KeyModelCatalog = cordis.NewServiceKey[ModelCatalogService]("zeokeel/modelcatalog")
