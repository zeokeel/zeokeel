package agent

// Reasoning-effort resolution: a per-session setting (written by the composer)
// overrides the agent module's default; malformed values are ignored so the
// provider field is omitted.

import (
	"context"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sanitizeReasoningEffort accepts only the documented effort values.
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

// sessionEffort resolves the effective reasoning effort for one session: an
// explicit per-session setting wins, otherwise the agent module config's default
// applies. An invalid setting is treated as empty (the field is omitted), per
// the settings contract.
func (s *service) sessionEffort(sessionID string) string {
	base := sanitizeReasoningEffort(s.cfg.ReasoningEffort)
	if s.settings == nil || sessionID == "" {
		return base
	}
	return sanitizeReasoningEffort(s.settings.Get(contracts.SessionEffortSettingKey(sessionID), base))
}

// withReasoningEffort wraps provider so every request carries the effective
// reasoning_effort in Request.Extra. This keeps the effort visible to scripted
// providers in tests and complements openai.Config.DefaultReasoningEffort (the
// openai provider honours an explicit Extra value).
func withReasoningEffort(provider rig.Provider, effort string) rig.Provider {
	effort = sanitizeReasoningEffort(effort)
	if provider == nil || effort == "" {
		return provider
	}
	return &reasoningEffortProvider{inner: provider, effort: effort}
}

// reasoningEffortProvider injects reasoning_effort into each request's Extra
// map without mutating a map owned by another caller.
type reasoningEffortProvider struct {
	inner  rig.Provider
	effort string
}

// Name implements rig.Provider.
func (p *reasoningEffortProvider) Name() string { return p.inner.Name() }

// Stream implements rig.Provider.
func (p *reasoningEffortProvider) Stream(ctx context.Context, request rig.Request) (rig.Stream, error) {
	if _, explicit := request.Extra["reasoning_effort"]; !explicit {
		extra := make(map[string]any, len(request.Extra)+1)
		for key, value := range request.Extra {
			extra[key] = value
		}
		extra["reasoning_effort"] = p.effort
		request.Extra = extra
	}
	return p.inner.Stream(ctx, request)
}
