package agent

// Thinking/reasoning resolution for one run.
//
// The composer lets the user pick a thinking mode per conversation (for
// example "thinking off" for Qwen, "high" for reasoning_effort-based models, or
// a user-defined mode with arbitrary request fields). The model catalog stores
// each model's family, default mode and custom modes; this file resolves the
// effective mode for a session and injects its parameters into every provider
// request through rig.Request.Extra. The legacy per-session/config
// reasoning_effort flow remains supported as a fallback so existing settings and
// conversations keep working.

import (
	"context"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sanitizeReasoningEffort accepts only the documented legacy effort values.
func sanitizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case contracts.SessionEffortLow:
		return contracts.SessionEffortLow
	case contracts.SessionEffortMedium:
		return contracts.SessionEffortMedium
	case contracts.SessionEffortHigh:
		return contracts.SessionEffortHigh
	default:
		return ""
	}
}

// sessionEffort resolves the legacy reasoning effort for one session: an
// explicit per-session setting wins, otherwise the module config default. It
// stays in ChatConfig for compatibility; the thinking-mode resolution below is
// the current control surface.
func (s *service) sessionEffort(sessionID string) string {
	base := sanitizeReasoningEffort(s.cfg.ReasoningEffort)
	if s.settings == nil || sessionID == "" {
		return base
	}
	return sanitizeReasoningEffort(s.settings.Get(contracts.SessionEffortSettingKey(sessionID), base))
}

// sessionLegacyEffort is the legacy effort fallback used when the conversation
// has no thinking-mode selection and the model has no default mode.
func (s *service) sessionLegacyEffort(sessionID string) string {
	if s.settings != nil && sessionID != "" {
		if raw := strings.TrimSpace(s.settings.Get(contracts.SessionEffortSettingKey(sessionID), "")); raw != "" {
			return sanitizeReasoningEffort(raw)
		}
	}
	return sanitizeReasoningEffort(s.cfg.ReasoningEffort)
}

// modelCatalogInfo resolves the configured model entry for modelID. Unknown
// models get an inferred family so custom model IDs still offer the generic
// reasoning-effort modes.
func (s *service) modelCatalogInfo(modelID string) contracts.ModelInfo {
	models := contracts.DefaultModelCatalog()
	if s.settings != nil {
		if raw := s.settings.Get(contracts.SettingModelCatalogModels, ""); strings.TrimSpace(raw) != "" {
			if parsed, err := contracts.ParseModelInfoList(raw); err == nil && len(parsed) > 0 {
				models = parsed
			}
		}
	}
	return contracts.ResolveModelInfo(models, modelID)
}

// thinkingParamsForSession resolves the effective thinking parameters for the
// next request of one conversation. An empty map means "send no thinking
// fields" (the provider default).
func (s *service) thinkingParamsForSession(sessionID, model string) map[string]any {
	info := s.modelCatalogInfo(model)
	sessionMode := ""
	if s.settings != nil && sessionID != "" {
		sessionMode = strings.TrimSpace(s.settings.Get(contracts.SessionThinkingSettingKey(sessionID), ""))
	}
	_, params := contracts.ResolveThinkingMode(info, sessionMode, s.sessionLegacyEffort(sessionID))
	return params
}

// withThinkingParams wraps a provider so every request carries the resolved
// thinking fields in Request.Extra. An explicit field already present in the
// request wins, matching the old reasoning_effort behavior.
func withThinkingParams(provider rig.Provider, params map[string]any) rig.Provider {
	if provider == nil || len(params) == 0 {
		return provider
	}
	return &thinkingProvider{inner: provider, params: contracts.CloneRequestParams(params)}
}

// thinkingProvider injects top-level request fields without mutating a map owned
// by another caller.
type thinkingProvider struct {
	inner  rig.Provider
	params map[string]any
}

// Name implements rig.Provider.
func (p *thinkingProvider) Name() string { return p.inner.Name() }

// Stream implements rig.Provider.
func (p *thinkingProvider) Stream(ctx context.Context, request rig.Request) (rig.Stream, error) {
	extra := make(map[string]any, len(request.Extra)+len(p.params))
	for key, value := range request.Extra {
		extra[key] = value
	}
	for key, value := range p.params {
		if _, explicit := extra[key]; !explicit {
			extra[key] = value
		}
	}
	if len(extra) > 0 {
		request.Extra = extra
	}
	return p.inner.Stream(ctx, request)
}
