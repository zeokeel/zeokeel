// Package insights implements ZeoKeel's cross-session usage aggregation (DSH
// dsh-context P1 insights page).
//
// The backend folds the durable session event log (owned by sessionstore) into
// one windowed cross-session snapshot exposed through contracts.KeyInsights:
// token totals, cost (only when the model catalog prices the model), tool calls,
// the number of active sessions, one row per local calendar day, per model, per
// workspace and a leaderboard of the busiest sessions.
//
// Data-source rules:
//
//   - Only bounded reads: the store's Sessions() list plus one Last() per
//     session that was updated inside the window. It never scans the whole
//     event table and never runs on the UI thread (the service is called from a
//     background goroutine by the Insights screen).
//   - Usage is folded from `model_call` events (one per provider call) with a
//     fallback to `usage` events (one per completed turn) for sessions recorded
//     before per-call events existed. A session with model_call events uses only
//     those for tokens, so a turn is not counted twice.
//   - Cost is computed at read time from the model catalog's current prices, so
//     a price edit affects the next aggregation. A model without pricing never
//     reports a cost: the aggregate marks CostKnown=false and the UI renders
//     "—", never a fake zero.
//   - Days are local calendar days (the module's configured time zone, default
//     local, Asia/Shanghai in the product); the window includes today.
//   - Results are cached for a short TTL keyed by (days, session limit), and an
//     explicit ForceRefresh bypasses the cache.
package insights

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/insights"
	// Version is the module's semantic version.
	Version = "1.0.0"

	// DefaultEventsPerSession bounds the per-session event read. The newest
	// events are read (store.Last), which covers the long tail of a busy
	// conversation without scanning its whole log; a session that exceeds the
	// bound is reported as truncated instead of silently under-counted.
	DefaultEventsPerSession = 4000
	// MaxEventsPerSession bounds the configured per-session read.
	MaxEventsPerSession = 20000

	// DefaultCacheTTL is the aggregation cache lifetime when Config names none.
	DefaultCacheTTL = 15 * time.Second
	// MaxCacheTTL bounds the configured cache lifetime.
	MaxCacheTTL = 10 * time.Minute
	// maxCacheEntries bounds the in-memory cache (a handful of windows).
	maxCacheEntries = 8
)

// Config is the module's typed assembly config.
type Config struct {
	// EventsPerSession bounds how many durable events are read per session
	// (0 = 4000, clamped to [100, 20000]).
	EventsPerSession int `json:"eventsPerSession,omitempty"`
	// CacheTTLMs is the aggregation cache lifetime in milliseconds (0/unset
	// keeps the 15s default; a positive value is clamped to at most 10min).
	CacheTTLMs int `json:"cacheTtlMs,omitempty"`
	// TopSessions is the default session leaderboard size (0 = 8, clamped to
	// [1, 50]).
	TopSessions int `json:"topSessions,omitempty"`
	// TimeZone is the IANA time zone used to bucket days (empty = local time).
	TimeZone string `json:"timeZone,omitempty"`
}

// Module returns the insights aggregation kernel module. It requires the session
// store (Source) and the host logger; the settings store (model prices) and the
// workspace service (display names) are optional.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		store, err := ctx.Require(contracts.KeyStore)
		if err != nil {
			return fmt.Errorf("%s: require session store: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		settings, _ := ctx.Get(contracts.KeySettings)
		workspace, _ := ctx.Get(contracts.KeyWorkspace)
		service, err := newService(store, settings, workspace, logger, cfg)
		if err != nil {
			return fmt.Errorf("%s: %w", ModuleID, err)
		}
		if _, err := ctx.Provide(contracts.KeyInsights, contracts.InsightsService(service)); err != nil {
			return fmt.Errorf("%s: provide insights service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyInsights)).
		AddRequires(
			module.Requires(contracts.KeyStore),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel cross-session usage insights").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if cfg.EventsPerSession < 0 {
		return fmt.Errorf("%s: eventsPerSession must not be negative", ModuleID)
	}
	if cfg.CacheTTLMs < 0 {
		return fmt.Errorf("%s: cacheTtlMs must not be negative", ModuleID)
	}
	if cfg.TopSessions < 0 {
		return fmt.Errorf("%s: topSessions must not be negative", ModuleID)
	}
	if zone := strings.TrimSpace(cfg.TimeZone); zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			return fmt.Errorf("%s: invalid timeZone %q: %w", ModuleID, zone, err)
		}
	}
	return nil
}

// service is the contracts.InsightsService implementation.
type service struct {
	store     contracts.Store
	settings  contracts.SettingsService
	workspace contracts.WorkspaceService
	logger    contracts.Logger

	eventsPerSession int
	topSessions      int
	cacheTTL         time.Duration
	location         *time.Location
	// now is the clock (tests inject a fixed instant).
	now func() time.Time

	mu    sync.Mutex
	cache map[cacheKey]cacheEntry
}

var _ contracts.InsightsService = (*service)(nil)

// cacheKey identifies one cached aggregation window.
type cacheKey struct {
	days  int
	limit int
}

// cacheEntry is one cached aggregation result and when it was produced.
type cacheEntry struct {
	insights    contracts.Insights
	generatedAt time.Time
}

// newService resolves the module config and builds the service.
func newService(store contracts.Store, settings contracts.SettingsService,
	workspace contracts.WorkspaceService, logger contracts.Logger, cfg Config) (*service, error) {
	if store == nil {
		return nil, fmt.Errorf("session store is required")
	}
	eventsPerSession := cfg.EventsPerSession
	if eventsPerSession <= 0 {
		eventsPerSession = DefaultEventsPerSession
	}
	if eventsPerSession < 100 {
		eventsPerSession = 100
	}
	if eventsPerSession > MaxEventsPerSession {
		eventsPerSession = MaxEventsPerSession
	}
	topSessions := cfg.TopSessions
	if topSessions <= 0 {
		topSessions = contracts.InsightsDefaultSessionLimit
	}
	if topSessions > contracts.InsightsMaxSessionLimit {
		topSessions = contracts.InsightsMaxSessionLimit
	}
	cacheTTL := DefaultCacheTTL
	if cfg.CacheTTLMs > 0 {
		cacheTTL = time.Duration(cfg.CacheTTLMs) * time.Millisecond
		if cacheTTL > MaxCacheTTL {
			cacheTTL = MaxCacheTTL
		}
	}
	location := time.Local
	if zone := strings.TrimSpace(cfg.TimeZone); zone != "" {
		loaded, err := time.LoadLocation(zone)
		if err != nil {
			return nil, fmt.Errorf("invalid time zone %q: %w", zone, err)
		}
		location = loaded
	}
	return &service{
		store:            store,
		settings:         settings,
		workspace:        workspace,
		logger:           logger,
		eventsPerSession: eventsPerSession,
		topSessions:      topSessions,
		cacheTTL:         cacheTTL,
		location:         location,
		now:              time.Now,
		cache:            make(map[cacheKey]cacheEntry, maxCacheEntries),
	}, nil
}

// Insights implements contracts.InsightsService.
func (s *service) Insights(ctx context.Context, query contracts.InsightsQuery) (contracts.Insights, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	days := normalizeDays(query.Days)
	limit := s.sessionLimit(query.SessionLimit)
	key := cacheKey{days: days, limit: limit}
	if !query.ForceRefresh {
		if cached, ok := s.lookup(key); ok {
			return cached, nil
		}
	}
	result, err := s.aggregate(ctx, days, limit)
	if err != nil {
		return contracts.Insights{}, err
	}
	s.remember(key, result)
	return result, nil
}

// normalizeDays clamps a query window into the supported range.
func normalizeDays(days int) int {
	if days <= 0 {
		return contracts.InsightsDefaultDays
	}
	if days > contracts.InsightsMaxDays {
		return contracts.InsightsMaxDays
	}
	return days
}

// sessionLimit resolves the leaderboard size (query overrides the module
// default).
func (s *service) sessionLimit(limit int) int {
	if limit <= 0 {
		return s.topSessions
	}
	if limit > contracts.InsightsMaxSessionLimit {
		return contracts.InsightsMaxSessionLimit
	}
	return limit
}

// lookup returns a fresh cached result.
func (s *service) lookup(key cacheKey) (contracts.Insights, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[key]
	if !ok {
		return contracts.Insights{}, false
	}
	if s.now().Sub(entry.generatedAt) >= s.cacheTTL {
		delete(s.cache, key)
		return contracts.Insights{}, false
	}
	return entry.insights, true
}

// remember stores one result, evicting the oldest entry beyond the bound.
func (s *service) remember(key cacheKey, result contracts.Insights) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = make(map[cacheKey]cacheEntry, maxCacheEntries)
	}
	s.cache[key] = cacheEntry{insights: result, generatedAt: result.GeneratedAt}
	if len(s.cache) <= maxCacheEntries {
		return
	}
	oldestKey, oldestAt := cacheKey{}, time.Time{}
	for candidate, entry := range s.cache {
		if oldestAt.IsZero() || entry.generatedAt.Before(oldestAt) {
			oldestKey, oldestAt = candidate, entry.generatedAt
		}
	}
	delete(s.cache, oldestKey)
}

// logf logs through the module logger when one is configured.
func (s *service) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf("[insights] "+format, args...)
	}
}
