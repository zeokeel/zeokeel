package agent

// Context safety defaults (DSH-aligned).
//
// A run's prompt-token budget has two sources:
//
//  1. an explicit chat.contextBudget setting (or the assembly ContextBudget):
//     when > 0 it is used verbatim — the operator's choice always wins;
//  2. otherwise the budget is derived from the model's context window from the
//     model catalog: budget = min(0.8*W, W - MaxTokens - 65536). The 65536
//     reserve keeps a safety margin for provider overhead and one more turn,
//     MaxTokens covers the completion itself, and 0.8*W is the DSH-style cap.
//     A window too small for the reserve falls back to 0.8*W instead of a
//     non-positive budget; an unknown window (0) keeps the budget disabled.
//
// The effective budget decides Rig's context protection, whether the LLM
// compactor is installed and the manual /compact budget, so a user who never
// touched the setting still gets protection.

import (
	"strconv"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/compact"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// contextBudgetWindowRatioNum/Den encode the 0.8 cap as integer math.
	contextBudgetWindowRatioNum = 8
	contextBudgetWindowRatioDen = 10
	// contextBudgetReservedTokens is the safety margin kept free below the
	// model window (provider overhead + one more turn), matching DSH.
	contextBudgetReservedTokens = 65536
)

// deriveContextBudget computes the prompt-token budget of a known context
// window. It returns 0 when the window is unknown (no automatic budget).
func deriveContextBudget(contextWindow, maxOutputTokens int) int {
	if contextWindow <= 0 {
		return 0
	}
	capped := contextWindow * contextBudgetWindowRatioNum / contextBudgetWindowRatioDen
	reserve := maxOutputTokens + contextBudgetReservedTokens
	derived := contextWindow - reserve
	budget := capped
	if derived > 0 && derived < budget {
		budget = derived
	}
	if budget <= 0 {
		// Floor protection: a window smaller than the reserve still gets the
		// 0.8*W cap instead of a disabled (or negative) budget.
		budget = capped
	}
	if budget <= 0 {
		return 0
	}
	return budget
}

// effectiveContextBudget resolves the budget of one run/session config: an
// explicit positive setting wins, an explicitly saved 0 disables the automatic
// budget (the settings page's "0 = off"), and an absent/negative-invalid row
// derives the budget from the model window.
func (s *service) effectiveContextBudget(cfg contracts.ChatConfig) int {
	if cfg.ContextBudget > 0 {
		return cfg.ContextBudget
	}
	if s.settings != nil {
		if raw := strings.TrimSpace(s.settings.Get(contracts.SettingsChatContextBudget, "")); raw != "" {
			if value, err := strconv.Atoi(raw); err == nil && value == 0 {
				return 0
			}
		}
	}
	info := s.modelCatalogInfo(cfg.Model)
	return deriveContextBudget(info.ContextWindow, info.MaxTokens)
}

// withContextBudget installs the effective prompt budget and, when one is known,
// the LLM compactor on one runner spec. It gives full runs and delegated
// children the same automatic protection whether or not the user ever set
// chat.contextBudget by hand.
func (s *service) withContextBudget(spec *rig.AgentSpec, cfg contracts.ChatConfig, provider rig.Provider) {
	if spec == nil {
		return
	}
	budget := s.effectiveContextBudget(cfg)
	spec.ContextBudget = budget
	// The compactor is installed even without a known budget: the SDK's
	// context-overflow recovery compacts mid-turn and needs it (the pre-turn
	// path still gates on ContextBudget > 0).
	spec.Compactor = compact.LLMCompactor{
		Provider: provider,
		Model:    cfg.Model,
		Fallback: compact.WindowCompactor{},
	}
}
