// Package modelcatalog is ZeoKeel's model-catalog backend module: it lists the
// model presets the composer's model switcher offers. The catalog is static
// product data (assembly config overrides the built-in presets); it deliberately
// does not talk to any provider or persist the user's selection — the agent
// module owns per-session model overrides in settings.
package modelcatalog

import (
	"fmt"
	"strings"
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/modelcatalog"
	// Version is the module's semantic version.
	Version = "1.0.0"
)

// Model is one configurable catalog entry (assembly JSON).
type Model struct {
	// ID is the model identifier sent to the provider (chat.model).
	ID string `json:"id"`
	// Name is the display label (empty falls back to ID).
	Name string `json:"name"`
	// Note is a short hint (capability or provider).
	Note string `json:"note"`
	// ContextWindow is the model's context-window size in tokens (0 = unknown).
	ContextWindow int `json:"contextWindow,omitempty"`
	// Family selects the built-in thinking protocol (qwen/deepseek/other/custom).
	Family string `json:"family,omitempty"`
	// DefaultThinking is the model's default thinking-mode ID.
	DefaultThinking string `json:"defaultThinking,omitempty"`
	// ThinkingModes are custom thinking modes on top of the family built-ins.
	ThinkingModes []contracts.ThinkingMode `json:"thinkingModes,omitempty"`
	// Temperature/TopP/MaxTokens are per-model sampling options.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
	MaxTokens   int      `json:"maxTokens,omitempty"`
}

// Config is the module's typed assembly config.
type Config struct {
	// Models replaces the built-in presets when non-empty.
	Models []Model `json:"models"`
}

// defaultModels are the presets shipped when the assembly config is empty:
// DeepSeek leads because it is the product's primary provider. The list lives
// in contracts so the settings editor and the backend cannot drift apart.
var defaultModels = contracts.DefaultModelCatalog()

// Module is the model-catalog backend descriptor. It provides
// contracts.KeyModelCatalog.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		// The settings store is optional: when present the user-edited catalog
		// overrides the configured/default presets per Models() call.
		settings, _ := ctx.Get(contracts.KeySettings)
		service := newServiceWithSettings(cfg, logger, settings)
		if _, err := ctx.Provide(contracts.KeyModelCatalog, contracts.ModelCatalogService(service)); err != nil {
			return fmt.Errorf("%s: provide model catalog: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyModelCatalog)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		WithName("ZeoKeel model catalog").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects entries the selector could not render unambiguously.
func validateConfig(cfg Config) error {
	seen := make(map[string]int, len(cfg.Models))
	for index, model := range cfg.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return fmt.Errorf("%s: models[%d].id is empty", ModuleID, index)
		}
		if first, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%s: models[%d].id %q duplicates models[%d]", ModuleID, index, id, first)
		}
		seen[id] = index
	}
	return nil
}

// service is the contracts.ModelCatalogService implementation.
type service struct {
	models   []contracts.ModelInfo
	logger   contracts.Logger
	settings contracts.SettingsService

	mu         sync.Mutex
	warnedJSON bool
}

// newService builds the catalog without a settings store.
func newService(cfg Config, logger contracts.Logger) *service {
	return newServiceWithSettings(cfg, logger, nil)
}

// newServiceWithSettings builds the catalog, copying the configured entries so
// neither the assembly config nor callers can mutate the module's state. The
// optional settings store is read on every Models() call.
func newServiceWithSettings(cfg Config, logger contracts.Logger, settings contracts.SettingsService) *service {
	models := make([]contracts.ModelInfo, 0, len(cfg.Models))
	if len(cfg.Models) == 0 {
		models = append(models, defaultModels...)
	} else {
		for _, model := range cfg.Models {
			id := strings.TrimSpace(model.ID)
			name := strings.TrimSpace(model.Name)
			if name == "" {
				name = id
			}
			contextWindow := model.ContextWindow
			if contextWindow < 0 {
				contextWindow = 0
			}
			temperature, topP, maxTokens := contracts.SanitizeGeneration(model.Temperature, model.TopP, model.MaxTokens)
			models = append(models, contracts.ModelInfo{
				ID:              id,
				Name:            name,
				Note:            strings.TrimSpace(model.Note),
				ContextWindow:   contextWindow,
				Family:          strings.TrimSpace(model.Family),
				DefaultThinking: strings.TrimSpace(model.DefaultThinking),
				ThinkingModes:   contracts.SanitizeThinkingModes(model.ThinkingModes),
				Temperature:     temperature,
				TopP:            topP,
				MaxTokens:       maxTokens,
			})
		}
	}
	return &service{models: models, logger: logger, settings: settings}
}

// Models returns the catalog presets: the user-edited settings list when one is
// configured, else the assembly-configured/default baseline. The caller gets a
// copy: changing the returned slice or its elements never affects later calls.
// A malformed settings row falls back to the baseline and logs once.
func (s *service) Models() []contracts.ModelInfo {
	if s.settings != nil {
		raw := s.settings.Get(contracts.SettingModelCatalogModels, "")
		models, err := contracts.ParseModelInfoList(raw)
		switch {
		case err != nil:
			s.warnMalformedSettings(err)
		case len(models) > 0:
			return sanitizeCatalog(models)
		}
	}
	out := make([]contracts.ModelInfo, len(s.models))
	copy(out, s.models)
	return out
}

// warnMalformedSettings logs the malformed catalog row once per service, so a
// bad settings value cannot spam the log on every frame.
func (s *service) warnMalformedSettings(err error) {
	s.mu.Lock()
	first := !s.warnedJSON
	s.warnedJSON = true
	s.mu.Unlock()
	if first && s.logger != nil {
		s.logger.Printf("%s: ignoring malformed %s: %v", ModuleID, contracts.SettingModelCatalogModels, err)
	}
}

// sanitizeCatalog normalizes the stored override rows: fields are trimmed, empty
// ids are dropped, a missing name falls back to the id and only the first row
// of a duplicate id is kept.
func sanitizeCatalog(models []contracts.ModelInfo) []contracts.ModelInfo {
	out := make([]contracts.ModelInfo, 0, len(models))
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
		if contextWindow <= 0 {
			contextWindow = 0
		}
		temperature, topP, maxTokens := contracts.SanitizeGeneration(model.Temperature, model.TopP, model.MaxTokens)
		out = append(out, contracts.ModelInfo{
			ID:              id,
			Name:            name,
			Note:            strings.TrimSpace(model.Note),
			ContextWindow:   contextWindow,
			Family:          strings.TrimSpace(model.Family),
			DefaultThinking: strings.TrimSpace(model.DefaultThinking),
			ThinkingModes:   contracts.SanitizeThinkingModes(model.ThinkingModes),
			Temperature:     temperature,
			TopP:            topP,
			MaxTokens:       maxTokens,
		})
	}
	return out
}
