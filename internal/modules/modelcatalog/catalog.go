// Package modelcatalog is ZeoKeel's model-catalog backend module: it lists the
// models the composer's model switcher offers. The product ships no built-in
// model list: every row is user data (the modelcatalog.models settings row), and
// each row carries its own OpenAI-compatible base URL and credential so one list
// can mix providers. The assembly config is only a fallback baseline (a product
// may pin models for headless deployments). The module deliberately does not
// talk to any provider or persist the user's selection — the agent module owns
// per-session model overrides in settings.
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
	// BaseURL is the model's own OpenAI-compatible endpoint (empty falls back to
	// chat.baseURL and then the assembly config), so one model list can mix
	// providers.
	BaseURL string `json:"baseURL,omitempty"`
	// APIKey is the credential for BaseURL (empty falls back to chat.apiKey and
	// then the provider environment variable).
	APIKey string `json:"apiKey,omitempty"`
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
	// Models is the assembly-configured baseline list. It replaces nothing: the
	// product ships no built-in models, so the effective catalog is this
	// baseline only while the user has never saved the modelcatalog.models row.
	Models []Model `json:"models"`
}

// Module is the model-catalog backend descriptor. It provides
// contracts.KeyModelCatalog.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		// The settings store is optional: when present the user-edited catalog
		// wins over the configured baseline per Models() call, and the legacy
		// single-provider connection is migrated into it once (see
		// migrateLegacyConnection).
		settings, _ := ctx.Get(contracts.KeySettings)
		migrateLegacyConnection(settings, logger)
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
	// baseline is the sanitized assembly-configured list (possibly empty).
	baseline []contracts.ModelInfo
	logger   contracts.Logger
	settings contracts.SettingsService

	mu         sync.Mutex
	warnedJSON bool
}

// newService builds the catalog without a settings store.
func newService(cfg Config, logger contracts.Logger) *service {
	return newServiceWithSettings(cfg, logger, nil)
}

// newServiceWithSettings builds the catalog, sanitizing the configured baseline
// so neither the assembly config nor callers can mutate the module's state. The
// optional settings store is read on every Models() call.
func newServiceWithSettings(cfg Config, logger contracts.Logger, settings contracts.SettingsService) *service {
	rows := make([]contracts.ModelInfo, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		rows = append(rows, contracts.ModelInfo{
			ID:              model.ID,
			Name:            model.Name,
			Note:            model.Note,
			ContextWindow:   model.ContextWindow,
			BaseURL:         model.BaseURL,
			APIKey:          model.APIKey,
			Family:          model.Family,
			DefaultThinking: model.DefaultThinking,
			ThinkingModes:   model.ThinkingModes,
			Temperature:     model.Temperature,
			TopP:            model.TopP,
			MaxTokens:       model.MaxTokens,
		})
	}
	return &service{baseline: contracts.SanitizeModelInfoList(rows), logger: logger, settings: settings}
}

// Models returns the effective catalog. A present modelcatalog.models row is
// authoritative — including an explicit empty list ("[]"), which means the user
// has removed every model and the product must not re-add built-in presets. An
// absent/blank row falls back to the assembly baseline (empty by default), and
// malformed JSON falls back to it too, logging once.
//
// The caller gets a copy: changing the returned slice or its elements never
// affects later calls.
func (s *service) Models() []contracts.ModelInfo {
	if s.settings != nil {
		if raw := strings.TrimSpace(s.settings.Get(contracts.SettingModelCatalogModels, "")); raw != "" {
			models, err := contracts.ParseModelInfoList(raw)
			if err != nil {
				s.warnMalformedSettings(err)
			} else {
				return contracts.SanitizeModelInfoList(models)
			}
		}
	}
	return cloneModelInfoList(s.baseline)
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

// cloneModelInfoList copies the catalog rows so callers cannot mutate the
// service's baseline through the returned slice.
func cloneModelInfoList(models []contracts.ModelInfo) []contracts.ModelInfo {
	out := make([]contracts.ModelInfo, len(models))
	copy(out, models)
	return out
}

// migrateLegacyConnection writes the pre-catalog global connection into the
// model list exactly once: when the user has never saved a modelcatalog.models
// row, the legacy chat.model/chat.baseURL/chat.apiKey become one model row so an
// existing single-provider configuration keeps working after the multi-provider
// model-list upgrade. An existing row — including an explicit empty list — is
// never overwritten, the legacy chat.* keys stay in place (chat.model remains
// the default-model mirror the agent falls back to), and a failed write is
// logged without failing module activation.
func migrateLegacyConnection(settings contracts.SettingsService, logger contracts.Logger) {
	if settings == nil {
		return
	}
	if strings.TrimSpace(settings.Get(contracts.SettingModelCatalogModels, "")) != "" {
		return
	}
	model, ok := contracts.LegacyConnectionModelInfo(settings)
	if !ok {
		return
	}
	encoded, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{model})
	if err == nil {
		err = settings.Set(contracts.SettingModelCatalogModels, encoded)
	}
	if err != nil && logger != nil {
		logger.Printf("%s: migrate legacy chat connection into %s: %v",
			ModuleID, contracts.SettingModelCatalogModels, err)
	}
}
