package insights

// The aggregation fold: every accounted durable session event of the window is
// folded once into day/model/workspace/session buckets. The fold is bounded by
// the window (events older than the window start are ignored) and by the
// per-session read bound (the store returns the newest events; a session that
// hits the bound is reported as truncated).
//
// Accounting rules (kept in one place so the totals cannot drift):
//
//   - tokens: one contribution per `model_call` (per provider call). Sessions
//     with no `model_call` in the window fall back to their `usage` events (one
//     per completed turn); a session that has any `model_call` uses only those,
//     so a turn is never counted twice.
//   - turns: one per `usage` event (a running turn has none yet).
//   - steps: one per `model_call` event.
//   - tool calls: one per `tool_call` event.
//   - cost: every token-bearing contribution is priced with the model catalog's
//     current per-million prices. A contribution whose model has no pricing is
//     counted as unpriced, never as free.
//   - days: local calendar days of the configured time zone.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Durable event types recorded by the agent backend (observability.go/usage.go).
const (
	eventRunStart        = "run_start"
	eventContextSnapshot = "context_snapshot"
	eventModelCall       = "model_call"
	eventUsage           = "usage"
	eventToolCall        = "tool_call"
	eventCompact         = "compact"
	eventSpill           = "spill"
)

// modelCallPayload mirrors the durable model_call payload (one per provider
// call; the per-call token truth).
type modelCallPayload struct {
	Iteration        int    `json:"iteration"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CachedTokens     int    `json:"cachedTokens"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
	ContextTokens    int    `json:"contextTokens"`
}

// usagePayload mirrors the durable usage payload (one per completed turn; the
// fallback token source for sessions recorded before per-call events existed).
type usagePayload struct {
	PromptTokens     int   `json:"promptTokens"`
	CompletionTokens int   `json:"completionTokens"`
	TotalTokens      int   `json:"totalTokens"`
	CachedTokens     int   `json:"cachedTokens"`
	Iterations       int   `json:"iterations"`
	DurationMS       int64 `json:"durationMs"`
	ContextTokens    int   `json:"contextTokens"`
}

// modelNamePayload covers the events that only carry the run's model name
// (run_start, context_snapshot).
type modelNamePayload struct {
	Model string `json:"model"`
}

// aggregate runs one aggregation over the store.
func (s *service) aggregate(ctx context.Context, days, limit int) (contracts.Insights, error) {
	now := s.now()
	local := now.In(s.location)
	from := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.location).
		AddDate(0, 0, -(days - 1))
	to := from.AddDate(0, 0, days)
	acc := newAccumulator(days, from, to, s.location, s.catalog())
	metas, err := s.store.Sessions(ctx)
	if err != nil {
		return contracts.Insights{}, fmt.Errorf("insights: list sessions: %w", err)
	}
	for _, meta := range metas {
		if err := ctx.Err(); err != nil {
			return contracts.Insights{}, err
		}
		// A session whose durable updated time predates the window has no
		// event inside it (every durable write bumps updated_at), so skipping
		// it keeps the read bounded without losing in-window data.
		if !meta.UpdatedAt.IsZero() && meta.UpdatedAt.Before(from) {
			continue
		}
		events, err := s.store.Last(ctx, meta.ID, s.eventsPerSession)
		if err != nil {
			return contracts.Insights{}, fmt.Errorf("insights: read events of session %q: %w", meta.ID, err)
		}
		acc.addSession(meta, events, s.eventsPerSession)
	}
	return acc.finish(now, limit, s.workspaceNames(ctx)), nil
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

// tokenSample is one priced token contribution.
type tokenSample struct {
	model      string
	prompt     int
	completion int
	cached     int
	total      int
}

// accumulator folds the window's events into buckets.
type accumulator struct {
	days     int
	from     time.Time
	to       time.Time
	location *time.Location
	catalog  []contracts.ModelInfo

	totals totalsAcc
	daily  []dayAcc

	models     map[string]*modelAcc
	workspaces map[string]*workspaceAcc
	sessions   map[string]*sessionAcc
	// activeSessions is the set of sessions with recorded activity (the
	// window's "sessions" total).
	activeSessions map[string]struct{}

	// truncated counts the sessions whose event read hit the per-session bound
	// while the oldest read event was still inside the window (older in-window
	// events may be missing).
	truncated int
}

// totalsAcc accumulates the window-wide numbers.
type totalsAcc struct {
	turns, steps, toolCalls           int
	prompt, completion, cached, total int
	compactions, spills               int
	cost                              float64
	priced, unpriced                  int
}

// dayAcc is one local calendar day's bucket.
type dayAcc struct {
	day        time.Time
	turns      int
	steps      int
	toolCalls  int
	prompt     int
	completion int
	cached     int
	total      int
	cost       float64
	priced     int
	unpriced   int
	sessions   map[string]struct{}
}

// modelAcc is one model's bucket.
type modelAcc struct {
	model      string
	calls      int
	prompt     int
	completion int
	cached     int
	total      int
	cost       float64
	priced     int
	unpriced   int
}

// workspaceAcc is one workspace's bucket.
type workspaceAcc struct {
	id         string
	sessions   map[string]struct{}
	prompt     int
	completion int
	cached     int
	total      int
	cost       float64
	priced     int
	unpriced   int
}

// sessionAcc is one session's bucket.
type sessionAcc struct {
	id           string
	title        string
	workspaceID  string
	updatedAt    time.Time
	lastActivity time.Time
	turns        int
	steps        int
	toolCalls    int
	prompt       int
	completion   int
	cached       int
	total        int
	cost         float64
	priced       int
	unpriced     int
	modelCalls   map[string]int
	lastModel    string
}

// newAccumulator builds the empty hourglass for one window.
func newAccumulator(days int, from, to time.Time, loc *time.Location,
	catalog []contracts.ModelInfo) *accumulator {
	acc := &accumulator{
		days:           days,
		from:           from,
		to:             to,
		location:       loc,
		catalog:        catalog,
		daily:          make([]dayAcc, days),
		models:         make(map[string]*modelAcc),
		workspaces:     make(map[string]*workspaceAcc),
		sessions:       make(map[string]*sessionAcc),
		activeSessions: make(map[string]struct{}),
	}
	for index := range acc.daily {
		acc.daily[index] = dayAcc{
			day:      from.AddDate(0, 0, index),
			sessions: make(map[string]struct{}),
		}
	}
	return acc
}

// addSession folds one session's durable events (newest bound events, ascending
// order) into the window.
func (acc *accumulator) addSession(meta contracts.SessionMeta, events []rig.SessionEvent, bound int) {
	if len(events) == 0 {
		return
	}
	// The store returned the newest bound events: when the oldest returned one
	// is already inside the window, older in-window events may be missing.
	if bound > 0 && len(events) == bound {
		if oldest := events[0].Time; !oldest.IsZero() && !oldest.Before(acc.from) {
			acc.truncated++
		}
	}
	// The most recent model mentioned anywhere in the read window is the
	// attribution fallback for a legacy usage event whose run started before
	// the read window.
	fallbackModel := ""
	for index := len(events) - 1; index >= 0; index-- {
		if model := eventModel(events[index]); model != "" {
			fallbackModel = model
			break
		}
	}
	hasModelCalls := false
	for _, event := range events {
		if event.Type == eventModelCall && acc.inWindow(event.Time) {
			hasModelCalls = true
			break
		}
	}
	currentModel := fallbackModel
	for _, event := range events {
		at := event.Time
		if !acc.inWindow(at) {
			continue
		}
		switch event.Type {
		case eventRunStart, eventContextSnapshot:
			if model := eventModel(event); model != "" {
				currentModel = model
			}
		case eventModelCall:
			var payload modelCallPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			model := strings.TrimSpace(payload.Model)
			if model == "" {
				model = currentModel
			}
			if model != "" {
				currentModel = model
			}
			session := acc.sessionFor(meta)
			session.steps++
			acc.totals.steps++
			if day := acc.dayAt(at); day != nil {
				day.steps++
			}
			acc.modelFor(model).calls++
			if session.modelCalls == nil {
				session.modelCalls = make(map[string]int, 2)
			}
			session.modelCalls[model]++
			if model != "" {
				session.lastModel = model
			}
			acc.markActive(session, at)
			acc.credit(session, at, tokenSample{
				model:      model,
				prompt:     payload.PromptTokens,
				completion: payload.CompletionTokens,
				cached:     payload.CachedTokens,
				total:      payload.TotalTokens,
			})
		case eventUsage:
			var payload usagePayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			session := acc.sessionFor(meta)
			session.turns++
			acc.totals.turns++
			if day := acc.dayAt(at); day != nil {
				day.turns++
			}
			acc.markActive(session, at)
			if hasModelCalls {
				// The per-call events already account for this turn's tokens.
				continue
			}
			model := currentModel
			if model == "" {
				model = fallbackModel
			}
			if session.modelCalls == nil {
				session.modelCalls = make(map[string]int, 2)
			}
			session.modelCalls[model]++
			if model != "" {
				session.lastModel = model
			}
			// A legacy usage event carries the turn's provider-call count, so a
			// session without per-call events still reports its steps.
			iterations := payload.Iterations
			if iterations <= 0 {
				iterations = 1
			}
			session.steps += iterations
			acc.totals.steps += iterations
			if day := acc.dayAt(at); day != nil {
				day.steps += iterations
			}
			acc.modelFor(model).calls += iterations
			acc.credit(session, at, tokenSample{
				model:      model,
				prompt:     payload.PromptTokens,
				completion: payload.CompletionTokens,
				cached:     payload.CachedTokens,
				total:      payload.TotalTokens,
			})
		case eventToolCall:
			session := acc.sessionFor(meta)
			session.toolCalls++
			acc.totals.toolCalls++
			if day := acc.dayAt(at); day != nil {
				day.toolCalls++
			}
			acc.markActive(session, at)
		case eventCompact:
			session := acc.sessionFor(meta)
			acc.totals.compactions++
			acc.markActive(session, at)
		case eventSpill:
			session := acc.sessionFor(meta)
			acc.totals.spills++
			acc.markActive(session, at)
		}
	}
}

// inWindow reports whether at belongs to the window (both boundaries are local
// midnights; the end is exclusive).
func (acc *accumulator) inWindow(at time.Time) bool {
	return !at.IsZero() && !at.Before(acc.from) && at.Before(acc.to)
}

// dayAt returns the local-day bucket of at (nil when outside the window).
func (acc *accumulator) dayAt(at time.Time) *dayAcc {
	local := at.In(acc.location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, acc.location)
	// Round: a DST transition makes a "day" 23 or 25 hours long.
	index := int(math.Round(start.Sub(acc.from).Hours() / 24))
	if index < 0 || index >= len(acc.daily) {
		return nil
	}
	return &acc.daily[index]
}

// sessionFor returns (creating on first use) the session bucket.
func (acc *accumulator) sessionFor(meta contracts.SessionMeta) *sessionAcc {
	if existing, ok := acc.sessions[meta.ID]; ok {
		return existing
	}
	session := &sessionAcc{id: meta.ID, title: meta.Title, workspaceID: meta.WorkspaceID, updatedAt: meta.UpdatedAt}
	acc.sessions[meta.ID] = session
	return session
}

// modelFor returns (creating on first use) the model bucket.
func (acc *accumulator) modelFor(model string) *modelAcc {
	if existing, ok := acc.models[model]; ok {
		return existing
	}
	bucket := &modelAcc{model: model}
	acc.models[model] = bucket
	return bucket
}

// workspaceFor returns (creating on first use) the workspace bucket.
func (acc *accumulator) workspaceFor(id string) *workspaceAcc {
	if existing, ok := acc.workspaces[id]; ok {
		return existing
	}
	bucket := &workspaceAcc{id: id, sessions: make(map[string]struct{})}
	acc.workspaces[id] = bucket
	return bucket
}

// markActive records that a session had activity at at.
func (acc *accumulator) markActive(session *sessionAcc, at time.Time) {
	if session.lastActivity.IsZero() || at.After(session.lastActivity) {
		session.lastActivity = at
	}
	acc.activeSessions[session.id] = struct{}{}
	if day := acc.dayAt(at); day != nil {
		day.sessions[session.id] = struct{}{}
	}
	acc.workspaceFor(session.workspaceID).sessions[session.id] = struct{}{}
}

// credit adds one token sample to every bucket it belongs to, including the
// priced/unpriced accounting.
func (acc *accumulator) credit(session *sessionAcc, at time.Time, sample tokenSample) {
	total := sample.total
	if total <= 0 {
		total = sample.prompt + sample.completion
	}
	if sample.prompt < 0 {
		sample.prompt = 0
	}
	if sample.completion < 0 {
		sample.completion = 0
	}
	if sample.cached < 0 {
		sample.cached = 0
	}
	if total < 0 {
		total = 0
	}
	day := acc.dayAt(at)
	model := acc.modelFor(sample.model)
	workspace := acc.workspaceFor(session.workspaceID)

	acc.totals.prompt += sample.prompt
	acc.totals.completion += sample.completion
	acc.totals.cached += sample.cached
	acc.totals.total += total
	session.prompt += sample.prompt
	session.completion += sample.completion
	session.cached += sample.cached
	session.total += total
	if day != nil {
		day.prompt += sample.prompt
		day.completion += sample.completion
		day.cached += sample.cached
		day.total += total
	}
	model.prompt += sample.prompt
	model.completion += sample.completion
	model.cached += sample.cached
	model.total += total
	workspace.prompt += sample.prompt
	workspace.completion += sample.completion
	workspace.cached += sample.cached
	workspace.total += total

	cost, priced := acc.price(sample)
	if sample.prompt+sample.completion > 0 {
		if priced {
			acc.totals.priced++
			session.priced++
			model.priced++
			workspace.priced++
			if day != nil {
				day.priced++
			}
		} else {
			acc.totals.unpriced++
			session.unpriced++
			model.unpriced++
			workspace.unpriced++
			if day != nil {
				day.unpriced++
			}
		}
	}
	if !priced {
		return
	}
	acc.totals.cost += cost
	session.cost += cost
	model.cost += cost
	workspace.cost += cost
	if day != nil {
		day.cost += cost
	}
}

// price estimates one sample's cost from the catalog's current prices.
// priced=false means the model has no pricing (never a free call).
func (acc *accumulator) price(sample tokenSample) (float64, bool) {
	model, ok := contracts.FindModelInfo(acc.catalog, sample.model)
	if !ok || !model.HasPricing() {
		return 0, false
	}
	cost, priced := model.Cost(sample.prompt, sample.cached, sample.completion)
	if !priced {
		return 0, false
	}
	return cost, true
}

// eventModel extracts the model name of an event that carries one.
func eventModel(event rig.SessionEvent) string {
	switch event.Type {
	case eventRunStart, eventContextSnapshot:
		var payload modelNamePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return ""
		}
		return strings.TrimSpace(payload.Model)
	case eventModelCall:
		var payload modelCallPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return ""
		}
		return strings.TrimSpace(payload.Model)
	default:
		return ""
	}
}

// finish converts the buckets into the contract result.
func (acc *accumulator) finish(now time.Time, limit int, workspaceNames map[string]string) contracts.Insights {
	result := contracts.Insights{
		Days:              acc.days,
		From:              acc.from,
		To:                acc.to,
		GeneratedAt:       now,
		Truncated:         acc.truncated > 0,
		TruncatedSessions: acc.truncated,
		Totals: contracts.InsightsTotals{
			Turns:            acc.totals.turns,
			Steps:            acc.totals.steps,
			PromptTokens:     acc.totals.prompt,
			CompletionTokens: acc.totals.completion,
			CachedTokens:     acc.totals.cached,
			TotalTokens:      acc.totals.total,
			ToolCalls:        acc.totals.toolCalls,
			Sessions:         len(acc.activeSessions),
			Compactions:      acc.totals.compactions,
			Spills:           acc.totals.spills,
			CostUSD:          acc.totals.cost,
			CostKnown:        acc.totals.priced > 0 && acc.totals.unpriced == 0,
			PricedCalls:      acc.totals.priced,
			UnpricedCalls:    acc.totals.unpriced,
		},
		Daily:      make([]contracts.InsightsDay, len(acc.daily)),
		Models:     make([]contracts.InsightsModel, 0, len(acc.models)),
		Workspaces: make([]contracts.InsightsWorkspace, 0, len(acc.workspaces)),
		Sessions:   make([]contracts.InsightsSession, 0, len(acc.sessions)),
	}
	for index := range acc.daily {
		result.Daily[index] = acc.daily[index].contract()
	}
	for _, bucket := range acc.models {
		result.Models = append(result.Models, contracts.InsightsModel{
			Model:            bucket.model,
			Calls:            bucket.calls,
			PromptTokens:     bucket.prompt,
			CompletionTokens: bucket.completion,
			CachedTokens:     bucket.cached,
			TotalTokens:      bucket.total,
			CostUSD:          bucket.cost,
			CostKnown:        bucket.priced > 0 && bucket.unpriced == 0,
		})
	}
	sort.Slice(result.Models, func(i, j int) bool {
		if result.Models[i].TotalTokens != result.Models[j].TotalTokens {
			return result.Models[i].TotalTokens > result.Models[j].TotalTokens
		}
		return result.Models[i].Model < result.Models[j].Model
	})
	for _, bucket := range acc.workspaces {
		result.Workspaces = append(result.Workspaces, contracts.InsightsWorkspace{
			WorkspaceID:      bucket.id,
			Name:             workspaceNames[bucket.id],
			Sessions:         len(bucket.sessions),
			PromptTokens:     bucket.prompt,
			CompletionTokens: bucket.completion,
			CachedTokens:     bucket.cached,
			TotalTokens:      bucket.total,
			CostUSD:          bucket.cost,
			CostKnown:        bucket.priced > 0 && bucket.unpriced == 0,
		})
	}
	sort.Slice(result.Workspaces, func(i, j int) bool {
		if result.Workspaces[i].TotalTokens != result.Workspaces[j].TotalTokens {
			return result.Workspaces[i].TotalTokens > result.Workspaces[j].TotalTokens
		}
		return result.Workspaces[i].WorkspaceID < result.Workspaces[j].WorkspaceID
	})
	for _, bucket := range acc.sessions {
		result.Sessions = append(result.Sessions, contracts.InsightsSession{
			SessionID:        bucket.id,
			Title:            bucket.title,
			WorkspaceID:      bucket.workspaceID,
			Model:            bucket.mostCalledModel(),
			UpdatedAt:        bucket.updatedAt,
			LastActivity:     bucket.lastActivity,
			Turns:            bucket.turns,
			Steps:            bucket.steps,
			ToolCalls:        bucket.toolCalls,
			PromptTokens:     bucket.prompt,
			CompletionTokens: bucket.completion,
			CachedTokens:     bucket.cached,
			TotalTokens:      bucket.total,
			CostUSD:          bucket.cost,
			CostKnown:        bucket.priced > 0 && bucket.unpriced == 0,
		})
	}
	sort.Slice(result.Sessions, func(i, j int) bool {
		left, right := result.Sessions[i], result.Sessions[j]
		if left.TotalTokens != right.TotalTokens {
			return left.TotalTokens > right.TotalTokens
		}
		if left.Steps != right.Steps {
			return left.Steps > right.Steps
		}
		if !left.LastActivity.Equal(right.LastActivity) {
			return left.LastActivity.After(right.LastActivity)
		}
		return left.SessionID < right.SessionID
	})
	if limit > 0 && len(result.Sessions) > limit {
		result.Sessions = result.Sessions[:limit]
	}
	return result
}

// contract converts a day bucket.
func (day dayAcc) contract() contracts.InsightsDay {
	return contracts.InsightsDay{
		Day:              day.day,
		Turns:            day.turns,
		Steps:            day.steps,
		ToolCalls:        day.toolCalls,
		PromptTokens:     day.prompt,
		CompletionTokens: day.completion,
		CachedTokens:     day.cached,
		TotalTokens:      day.total,
		Sessions:         len(day.sessions),
		CostUSD:          day.cost,
		CostKnown:        day.priced > 0 && day.unpriced == 0,
	}
}

// mostCalledModel reports the session's most-called model (the latest model
// breaks a tie; empty when unknown).
func (session *sessionAcc) mostCalledModel() string {
	best, bestCount := "", 0
	for model, count := range session.modelCalls {
		if count > bestCount || (count == bestCount && model < best) {
			best, bestCount = model, count
		}
	}
	if best != "" {
		return best
	}
	return session.lastModel
}
