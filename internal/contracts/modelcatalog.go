package contracts

import (
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

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
	// BaseURL is the model's own OpenAI-compatible service endpoint, so a
	// model list can mix providers. Empty falls back to chat.baseURL (the
	// legacy global connection / the assembly config).
	BaseURL string `json:"baseURL,omitempty"`
	// APIKey is the credential for BaseURL; it is only written when the user
	// enters one and is masked in diagnostics. Empty falls back to
	// chat.apiKey and then to the provider environment variable.
	APIKey string `json:"apiKey,omitempty"`
	// Family selects the built-in thinking protocol (qwen, deepseek, other,
	// custom). Empty infers it from the model ID.
	Family string `json:"family,omitempty"`
	// DefaultThinking is the thinking-mode ID this model uses unless a
	// conversation overrides it (empty = family default mode). It is the
	// per-model thinking strength edited in the composer's model menu.
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
	// InputPricePerMillion is the optional price of one million uncached input
	// tokens (in the user's currency; 0 = not configured).
	InputPricePerMillion float64 `json:"inputPricePerMillion,omitempty"`
	// CachedInputPricePerMillion is the optional price of one million cached
	// input tokens (0 = fall back to InputPricePerMillion).
	CachedInputPricePerMillion float64 `json:"cachedInputPricePerMillion,omitempty"`
	// OutputPricePerMillion is the optional price of one million output tokens
	// (0 = not configured).
	OutputPricePerMillion float64 `json:"outputPricePerMillion,omitempty"`
}

// HasPricing reports whether the model has any price configured. A row without
// pricing never reports a cost (an unknown price must not be rendered as free).
func (m ModelInfo) HasPricing() bool {
	return m.InputPricePerMillion > 0 || m.CachedInputPricePerMillion > 0 || m.OutputPricePerMillion > 0
}

// Cost estimates the price of one provider call from the catalog row's per-
// million prices. promptTokens includes cachedTokens (the provider convention);
// the uncached remainder and the cached part are priced separately, with the
// cached price falling back to the input price when only the latter is set.
// ok=false means no pricing is configured for this model.
func (m ModelInfo) Cost(promptTokens, cachedTokens, completionTokens int) (cost float64, ok bool) {
	if !m.HasPricing() {
		return 0, false
	}
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	cached := cachedTokens
	if cached < 0 {
		cached = 0
	}
	if cached > promptTokens {
		cached = promptTokens
	}
	uncached := promptTokens - cached
	cachedPrice := m.CachedInputPricePerMillion
	if cachedPrice <= 0 {
		cachedPrice = m.InputPricePerMillion
	}
	cost = float64(uncached)/1_000_000*m.InputPricePerMillion +
		float64(cached)/1_000_000*cachedPrice +
		float64(completionTokens)/1_000_000*m.OutputPricePerMillion
	return cost, true
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

// SanitizePrices normalizes optional per-million model prices: a negative or
// non-finite value becomes "not configured" (0). Zero stays "not configured".
func SanitizePrices(input, cached, output float64) (inputPrice, cachedPrice, outputPrice float64) {
	sanitize := func(value float64) float64 {
		if value <= 0 || value != value { // NaN is not representable in JSON but guard anyway
			return 0
		}
		if value > 1e9 {
			return 1e9
		}
		return value
	}
	return sanitize(input), sanitize(cached), sanitize(output)
}

// ModelCatalogService lists the model presets offered by the product.
type ModelCatalogService interface {
	Models() []ModelInfo
}

// KeyModelCatalog is provided by the model catalog backend module.
var KeyModelCatalog = cordis.NewServiceKey[ModelCatalogService]("zeokeel/modelcatalog")

// LoadModelCatalog decodes the user-edited catalog row (modelcatalog.models).
// An unset row returns nil/no error; malformed JSON returns an error and an
// empty list, which callers log and treat as "no models configured".
func LoadModelCatalog(settings SettingsService) ([]ModelInfo, error) {
	if settings == nil {
		return nil, nil
	}
	return ParseModelInfoList(settings.Get(SettingModelCatalogModels, ""))
}

// SanitizeModelInfoList normalizes catalog rows read from settings or the
// assembly config: fields are trimmed, URLs lose their trailing slash, empty
// ids are dropped, a missing name falls back to the id and only the first row
// of a duplicate id is kept. Generation options go through SanitizeGeneration.
func SanitizeModelInfoList(models []ModelInfo) []ModelInfo {
	out := make([]ModelInfo, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		contextWindow := model.ContextWindow
		if contextWindow < 0 {
			contextWindow = 0
		}
		temperature, topP, maxTokens := SanitizeGeneration(model.Temperature, model.TopP, model.MaxTokens)
		inputPrice, cachedPrice, outputPrice := SanitizePrices(model.InputPricePerMillion,
			model.CachedInputPricePerMillion, model.OutputPricePerMillion)
		out = append(out, ModelInfo{
			InputPricePerMillion:       inputPrice,
			CachedInputPricePerMillion: cachedPrice,
			OutputPricePerMillion:      outputPrice,
			ID:                         id,
			Name:                       name,
			Note:                       strings.TrimSpace(model.Note),
			ContextWindow:              contextWindow,
			BaseURL:                    strings.TrimRight(strings.TrimSpace(model.BaseURL), "/"),
			APIKey:                     strings.TrimSpace(model.APIKey),
			Family:                     strings.TrimSpace(model.Family),
			DefaultThinking:            strings.TrimSpace(model.DefaultThinking),
			ThinkingModes:              SanitizeThinkingModes(model.ThinkingModes),
			Temperature:                temperature,
			TopP:                       topP,
			MaxTokens:                  maxTokens,
		})
	}
	return out
}

// ModelConnection resolves the provider connection for one model: the model
// row's own BaseURL/APIKey when set, otherwise the given global fallbacks
// (chat.baseURL / chat.apiKey / the assembly config). Both results are
// normalized (trimmed, URL without trailing slash) and may be empty.
func ModelConnection(models []ModelInfo, modelID, fallbackBaseURL, fallbackAPIKey string) (baseURL, apiKey string) {
	baseURL = strings.TrimRight(strings.TrimSpace(fallbackBaseURL), "/")
	apiKey = strings.TrimSpace(fallbackAPIKey)
	if model, ok := FindModelInfo(models, modelID); ok {
		if own := strings.TrimRight(strings.TrimSpace(model.BaseURL), "/"); own != "" {
			baseURL = own
		}
		if own := strings.TrimSpace(model.APIKey); own != "" {
			apiKey = own
		}
	}
	return baseURL, apiKey
}

// LegacyConnectionModelInfo builds the one model row that represents the
// pre-catalog global connection (chat.model/chat.baseURL/chat.apiKey). The
// modelcatalog module migrates it into modelcatalog.models once, when the user
// has never saved a model list, so an existing configuration survives the
// multi-provider model-list upgrade. ok=false means no model was configured.
func LegacyConnectionModelInfo(settings SettingsService) (ModelInfo, bool) {
	if settings == nil {
		return ModelInfo{}, false
	}
	id := strings.TrimSpace(settings.Get(SettingsChatModel, ""))
	if id == "" {
		return ModelInfo{}, false
	}
	return ModelInfo{
		ID:      id,
		Name:    id,
		BaseURL: strings.TrimRight(strings.TrimSpace(settings.Get(SettingsChatBaseURL, "")), "/"),
		APIKey:  strings.TrimSpace(settings.Get(SettingsChatAPIKey, "")),
	}, true
}

// DefaultModelID returns the configured default model id: the first catalog
// row, or chat.model for a configuration that predates the model list. The
// composer's model chip and the settings page mark this row as the default.
func DefaultModelID(models []ModelInfo, settings SettingsService) string {
	if settings != nil {
		if id := strings.TrimSpace(settings.Get(SettingsChatModel, "")); id != "" {
			if _, ok := FindModelInfo(models, id); ok {
				return id
			}
		}
	}
	for _, model := range models {
		if id := strings.TrimSpace(model.ID); id != "" {
			return id
		}
	}
	return ""
}

// DefaultModelEntry returns the catalog entry of the default model, or the
// first row when chat.model is unset/unknown.
func DefaultModelEntry(models []ModelInfo, settings SettingsService) (ModelInfo, bool) {
	if id := DefaultModelID(models, settings); id != "" {
		return FindModelInfo(models, id)
	}
	return ModelInfo{}, false
}

// FindModelInfo finds one model by ID (trimmed, case-insensitive).
func FindModelInfo(models []ModelInfo, id string) (ModelInfo, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return ModelInfo{}, false
	}
	for _, model := range models {
		if strings.ToLower(strings.TrimSpace(model.ID)) == id {
			return model, true
		}
	}
	return ModelInfo{}, false
}

// ResolveModelInfo finds the configured model entry for modelID; an unknown
// model gets a synthesized entry with an inferred family so old/custom models
// still offer the generic reasoning-effort modes.
func ResolveModelInfo(models []ModelInfo, modelID string) ModelInfo {
	if model, ok := FindModelInfo(models, modelID); ok {
		return model
	}
	return ModelInfo{ID: strings.TrimSpace(modelID), Family: InferThinkingFamily(modelID)}
}
