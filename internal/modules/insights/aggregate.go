package insights

// The aggregation adapter: the durable-event fold itself lives in the
// host-agnostic github.com/zeokeel/zeokeel-rig/session/insights package. This
// file keeps the product half of the aggregation:
//
//   - the narrow Store -> SDK EventLog adapter (session metadata mapping plus
//     the bounded per-session Last read);
//   - the host CostFunc backed by the model catalog's current per-million
//     prices, so pricing stays a product concern;
//   - the SDK Result -> contracts.Insights mapping, including workspace display
//     names (the SDK only knows workspace IDs).
//
// The accounting rules (model_call tokens with a per-session usage fallback,
// turns, steps, tool calls, day/model/workspace/session buckets, bounded reads
// with truncation) are owned by the SDK; the module's public read model, cache
// and TTL stay in insights.go.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"
	sessioninsights "github.com/zeokeel/zeokeel-rig/session/insights"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Durable event types recorded by the agent backend (observability.go/usage.go).
// The fold and the module's event fixtures share the SDK's vocabulary.
const (
	eventRunStart        = sessioninsights.EventRunStart
	eventContextSnapshot = sessioninsights.EventContextSnapshot
	eventModelCall       = sessioninsights.EventModelCall
	eventUsage           = sessioninsights.EventUsage
	eventToolCall        = sessioninsights.EventToolCall
	eventCompact         = sessioninsights.EventCompact
	eventSpill           = sessioninsights.EventSpill
)

// storeLog adapts the product's durable store to the SDK's narrow EventLog:
// Sessions maps the product metadata to session/insights.SessionInfo and Last
// is delegated unchanged (the store already returns the newest bounded slice in
// ascending sequence order).
type storeLog struct {
	store contracts.Store
}

func (l storeLog) Sessions(ctx context.Context) ([]sessioninsights.SessionInfo, error) {
	metas, err := l.store.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]sessioninsights.SessionInfo, len(metas))
	for index, meta := range metas {
		sessions[index] = sessioninsights.SessionInfo{
			ID:          meta.ID,
			Title:       meta.Title,
			WorkspaceID: meta.WorkspaceID,
			UpdatedAt:   meta.UpdatedAt,
		}
	}
	return sessions, nil
}

func (l storeLog) Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	return l.store.Last(ctx, sessionID, limit)
}

// aggregate runs one aggregation over the store through the SDK fold.
func (s *service) aggregate(ctx context.Context, days, limit int) (contracts.Insights, error) {
	result, err := sessioninsights.Fold(ctx, storeLog{store: s.store}, sessioninsights.Options{
		Days:             days,
		SessionLimit:     limit,
		EventsPerSession: s.eventsPerSession,
		Location:         s.location,
		Now:              s.now,
		Cost:             catalogCost(s.catalog()),
	})
	if err != nil {
		return contracts.Insights{}, err
	}
	return s.contract(ctx, result), nil
}

// catalogCost returns the host pricing callback: the model catalog's current
// per-million prices. A model without pricing (or a model the catalog does not
// know) is unpriced, never free.
func catalogCost(catalog []contracts.ModelInfo) sessioninsights.CostFunc {
	return func(model string, tokens sessioninsights.TokenCount) (float64, bool) {
		info, ok := contracts.FindModelInfo(catalog, model)
		if !ok || !info.HasPricing() {
			return 0, false
		}
		return info.Cost(tokens.Prompt, tokens.Cached, tokens.Completion)
	}
}

// contract maps the SDK result onto the product's read model, resolving
// workspace display names (the SDK returns workspace IDs only).
func (s *service) contract(ctx context.Context, result sessioninsights.Result) contracts.Insights {
	names := s.workspaceNames(ctx)
	out := contracts.Insights{
		Days:              result.Days,
		From:              result.From,
		To:                result.To,
		GeneratedAt:       result.GeneratedAt,
		Truncated:         result.Truncated,
		TruncatedSessions: result.TruncatedSessions,
		Totals: contracts.InsightsTotals{
			Turns:            result.Totals.Turns,
			Steps:            result.Totals.Steps,
			PromptTokens:     result.Totals.Tokens.Prompt,
			CompletionTokens: result.Totals.Tokens.Completion,
			CachedTokens:     result.Totals.Tokens.Cached,
			TotalTokens:      result.Totals.Tokens.Total,
			ToolCalls:        result.Totals.ToolCalls,
			Sessions:         result.Totals.Sessions,
			Compactions:      result.Totals.Compactions,
			Spills:           result.Totals.Spills,
			CostUSD:          result.Totals.CostUSD,
			CostKnown:        result.Totals.CostKnown,
			PricedCalls:      result.Totals.PricedCalls,
			UnpricedCalls:    result.Totals.UnpricedCalls,
		},
		Daily:      make([]contracts.InsightsDay, len(result.Daily)),
		Models:     make([]contracts.InsightsModel, 0, len(result.Models)),
		Workspaces: make([]contracts.InsightsWorkspace, 0, len(result.Workspaces)),
		Sessions:   make([]contracts.InsightsSession, 0, len(result.Sessions)),
	}
	for index, day := range result.Daily {
		out.Daily[index] = contracts.InsightsDay{
			Day:              day.Day,
			Turns:            day.Turns,
			Steps:            day.Steps,
			ToolCalls:        day.ToolCalls,
			PromptTokens:     day.Tokens.Prompt,
			CompletionTokens: day.Tokens.Completion,
			CachedTokens:     day.Tokens.Cached,
			TotalTokens:      day.Tokens.Total,
			Sessions:         day.Sessions,
			CostUSD:          day.CostUSD,
			CostKnown:        day.CostKnown,
		}
	}
	for _, bucket := range result.Models {
		out.Models = append(out.Models, contracts.InsightsModel{
			Model:            bucket.Model,
			Calls:            bucket.Calls,
			PromptTokens:     bucket.Tokens.Prompt,
			CompletionTokens: bucket.Tokens.Completion,
			CachedTokens:     bucket.Tokens.Cached,
			TotalTokens:      bucket.Tokens.Total,
			CostUSD:          bucket.CostUSD,
			CostKnown:        bucket.CostKnown,
		})
	}
	for _, bucket := range result.Workspaces {
		out.Workspaces = append(out.Workspaces, contracts.InsightsWorkspace{
			WorkspaceID:      bucket.WorkspaceID,
			Name:             names[bucket.WorkspaceID],
			Sessions:         bucket.Sessions,
			PromptTokens:     bucket.Tokens.Prompt,
			CompletionTokens: bucket.Tokens.Completion,
			CachedTokens:     bucket.Tokens.Cached,
			TotalTokens:      bucket.Tokens.Total,
			CostUSD:          bucket.CostUSD,
			CostKnown:        bucket.CostKnown,
		})
	}
	for _, bucket := range result.Sessions {
		out.Sessions = append(out.Sessions, contracts.InsightsSession{
			SessionID:        bucket.SessionID,
			Title:            bucket.Title,
			WorkspaceID:      bucket.WorkspaceID,
			Model:            bucket.Model,
			UpdatedAt:        bucket.UpdatedAt,
			LastActivity:     bucket.LastActivity,
			Turns:            bucket.Turns,
			Steps:            bucket.Steps,
			ToolCalls:        bucket.ToolCalls,
			PromptTokens:     bucket.Tokens.Prompt,
			CompletionTokens: bucket.Tokens.Completion,
			CachedTokens:     bucket.Tokens.Cached,
			TotalTokens:      bucket.Tokens.Total,
			CostUSD:          bucket.CostUSD,
			CostKnown:        bucket.CostKnown,
		})
	}
	return out
}

// catalog loads the current model catalog (prices are read at aggregation time).
func (s *service) catalog() []contracts.ModelInfo {
	if s.settings == nil {
		return nil
	}
	models, err := contracts.LoadModelCatalog(s.settings)
	if err != nil {
		// Malformed catalog JSON is the modelcatalog module's to report; here it
		// means "no configured models", so no cost is shown.
		s.logf("load model catalog: %v", err)
		return nil
	}
	return models
}

// workspaceNames resolves workspace IDs to display names for the aggregation
// rows (the UI never reads the workspace service itself).
func (s *service) workspaceNames(ctx context.Context) map[string]string {
	if s.workspace == nil {
		return nil
	}
	list, err := s.workspace.List(ctx)
	if err != nil {
		s.logf("list workspaces: %v", err)
		return nil
	}
	names := make(map[string]string, len(list))
	for _, workspace := range list {
		names[workspace.ID] = workspace.Name
	}
	return names
}
