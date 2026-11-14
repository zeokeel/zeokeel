package contracts

// Cross-session usage insights contract (DSH dsh-context P1).
//
// The insights backend owns the aggregation: it reads the durable session log
// through contracts.KeyStore (bounded per-session reads, cached/pre-aggregated)
// and folds every accounted session event into one cross-session snapshot:
//
//   - totals: turns/steps, prompt/completion/cached tokens, cost, tool calls and
//     the number of sessions with recorded activity in the window;
//   - one row per local calendar day of the window (7 or 30 days);
//   - one row per model and per workspace;
//   - a leaderboard of the busiest sessions.
//
// Cost is computed at read time from the model catalog's per-million prices, so
// a price edit is reflected on the next aggregation. A model without pricing
// never reports a cost: CostKnown is false and the caller must render the row as
// unknown, not as zero (an unknown price must not look free). The window is
// bucketed in the backend's local time zone (days start at local midnight).
//
// UI modules only read this contract; they never import Rig or the backend.

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

const (
	// InsightsDaysWeek is the default aggregation window (7 local calendar
	// days including today).
	InsightsDaysWeek = 7
	// InsightsDaysMonth is the extended aggregation window (30 local calendar
	// days including today).
	InsightsDaysMonth = 30
	// InsightsDefaultDays is the window used when a query names none.
	InsightsDefaultDays = InsightsDaysWeek
	// InsightsDefaultSessionLimit is the session leaderboard size used when a
	// query names none.
	InsightsDefaultSessionLimit = 8
	// InsightsMaxSessionLimit bounds the session leaderboard size.
	InsightsMaxSessionLimit = 50
	// InsightsMaxDays bounds the aggregation window.
	InsightsMaxDays = 90
)

// InsightsQuery selects one aggregation window. The zero value asks for the
// default week and leaderboard size.
type InsightsQuery struct {
	// Days is the window length in local calendar days (7 = week, 30 = month;
	// <= 0 uses InsightsDefaultDays, values above InsightsMaxDays are clamped).
	Days int
	// SessionLimit bounds the session leaderboard (<= 0 uses
	// InsightsDefaultSessionLimit).
	SessionLimit int
	// ForceRefresh bypasses the backend's short-lived cache. A UI uses it for
	// the explicit refresh action; normal page loads read the cache.
	ForceRefresh bool
}

// InsightsTotals is the window-wide accounting of every session.
type InsightsTotals struct {
	// Turns is the number of completed turns (one durable `usage` event per
	// turn; turns still running have no usage event yet).
	Turns int
	// Steps is the number of provider calls (one durable `model_call` event per
	// call).
	Steps int
	// PromptTokens, CompletionTokens and CachedTokens are the provider-reported
	// token sums. CachedTokens is a subset of PromptTokens.
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	// TotalTokens is the provider-reported total, or Prompt+Completion for
	// events that did not report a total.
	TotalTokens int
	// ToolCalls is the number of recorded tool calls.
	ToolCalls int
	// Sessions is the number of sessions with recorded activity in the window.
	Sessions int
	// Compactions and Spills count the context audit events in the window.
	Compactions int
	Spills      int
	// CostUSD is the estimated cost of the priced token-bearing calls. It is a
	// lower bound when UnpricedCalls is non-zero.
	CostUSD float64
	// CostKnown reports that every token-bearing call in the window was priced.
	CostKnown bool
	// PricedCalls and UnpricedCalls count the token-bearing calls by whether
	// their model had pricing configured. A UI shows the cost when PricedCalls
	// is non-zero, and marks it partial when UnpricedCalls is non-zero.
	PricedCalls   int
	UnpricedCalls int
}

// InsightsDay is one local calendar day of the window.
type InsightsDay struct {
	// Day is the day's local midnight (the bucket identity).
	Day time.Time
	// Turns, Steps and ToolCalls count the day's activity.
	Turns     int
	Steps     int
	ToolCalls int
	// PromptTokens, CompletionTokens, CachedTokens and TotalTokens are the
	// day's token sums.
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	TotalTokens      int
	// Sessions is the number of distinct sessions active that day.
	Sessions int
	// CostUSD is the day's estimated cost for priced calls, and CostKnown
	// reports whether every token-bearing call that day was priced.
	CostUSD   float64
	CostKnown bool
}

// InsightsModel is one model's accounting in the window.
type InsightsModel struct {
	// Model is the model identifier (may be empty for legacy `usage` events
	// recorded before per-call model attribution existed).
	Model string
	// Calls is the number of provider calls.
	Calls int
	// PromptTokens, CompletionTokens, CachedTokens and TotalTokens are the
	// model's token sums.
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	TotalTokens      int
	// CostUSD is the model's estimated cost when the catalog prices it;
	// CostKnown is false for a model without pricing.
	CostUSD   float64
	CostKnown bool
}

// InsightsWorkspace is one workspace's accounting in the window. Sessions with
// no workspace association are grouped under the empty WorkspaceID.
type InsightsWorkspace struct {
	// WorkspaceID is the durable workspace ID (empty for unassociated
	// sessions).
	WorkspaceID string
	// Name is the workspace's display name when the backend could resolve it
	// (empty when unknown).
	Name string
	// Sessions is the number of distinct sessions of the workspace active in
	// the window.
	Sessions int
	// PromptTokens, CompletionTokens, CachedTokens and TotalTokens are the
	// workspace's token sums.
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	TotalTokens      int
	// CostUSD is the workspace's estimated cost for priced calls, and
	// CostKnown reports whether every token-bearing call was priced.
	CostUSD   float64
	CostKnown bool
}

// InsightsSession is one session of the leaderboard.
type InsightsSession struct {
	// SessionID is the durable session ID; opening it is the row's action.
	SessionID string
	// Title is the session's durable display title (may be empty).
	Title string
	// WorkspaceID is the session's workspace (empty when unassociated).
	WorkspaceID string
	// Model is the session's most-called model in the window (empty when
	// unknown).
	Model string
	// UpdatedAt is the session's durable updated time.
	UpdatedAt time.Time
	// LastActivity is the newest accounted event's time.
	LastActivity time.Time
	// Turns, Steps and ToolCalls count the session's activity in the window.
	Turns     int
	Steps     int
	ToolCalls int
	// PromptTokens, CompletionTokens, CachedTokens and TotalTokens are the
	// session's token sums.
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	TotalTokens      int
	// CostUSD is the session's estimated cost for priced calls, and CostKnown
	// reports whether every token-bearing call was priced.
	CostUSD   float64
	CostKnown bool
}

// Insights is one cross-session aggregation result.
type Insights struct {
	// Days is the effective window length in local calendar days.
	Days int
	// From is the local midnight the window starts at (inclusive); To is the
	// local midnight after the last day (exclusive).
	From time.Time
	To   time.Time
	// GeneratedAt is when the aggregation ran (the UI's freshness anchor).
	GeneratedAt time.Time
	// Totals is the window-wide accounting.
	Totals InsightsTotals
	// Daily has one entry per day of the window, oldest first (zero-activity
	// days included).
	Daily []InsightsDay
	// Models is sorted by total tokens, descending.
	Models []InsightsModel
	// Workspaces is sorted by total tokens, descending.
	Workspaces []InsightsWorkspace
	// Sessions is the leaderboard, sorted by total tokens, descending.
	Sessions []InsightsSession
	// Truncated reports that at least one session had more events than the
	// backend's per-session read bound, so older in-window events may be
	// missing. The UI must say so instead of presenting a complete total.
	Truncated bool
	// TruncatedSessions is the number of sessions that hit the read bound.
	TruncatedSessions int
}

// InsightsService aggregates the durable session log across sessions. The
// backend module `zeokeel/insights` provides it; it is optional for products
// without a session store (the UI then shows an explanatory empty state).
type InsightsService interface {
	// Insights folds every accounted session event of the query's window into
	// one cross-session result. It is safe to call from any goroutine: the
	// aggregation reads the store (bounded per session) and the result is
	// cached for a short TTL, so a UI calls it from a background goroutine and
	// posts the result back to its thread.
	Insights(ctx context.Context, query InsightsQuery) (Insights, error)
}

// KeyInsights is provided by the insights backend module and consumed by the
// Insights screen.
var KeyInsights = cordis.NewServiceKey[InsightsService]("zeokeel/insights")
