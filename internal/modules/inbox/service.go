// Package inbox implements the product's single external entry point: every
// channel (WeChat, DingTalk, Feishu, email, webhooks, cron, teammate proposals)
// normalizes an inbound event into a contracts.InboxItem, and only the Lead
// triages it into work. Nothing else may turn an external event into a task.
//
// The service is in-memory for now (slice 1): it owns the *rules* that are hard
// to get right — dedupe, per-source rate budget, capacity, atomic triage — so
// the durable half can be added behind the same contract without changing
// callers.
package inbox

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Defaults are chosen to be safe for a single machine: 2000 pending items is
// far more than a human can triage, and 60 events per source per minute keeps a
// busy channel from drowning the Lead.
const (
	defaultDedupeWindow = 24 * time.Hour
	defaultMaxPerMinute = 60
	defaultMaxItems     = 2000
	defaultDefaultTTL   = 7 * 24 * time.Hour
	defaultListLimit    = 200
)

// Config is the module's typed assembly config. Every field has a safe zero
// value; NewService fills the defaults.
type Config struct {
	// Now overrides the clock (tests); nil means time.Now.
	Now func() time.Time `json:"-"`
	// DedupeWindow is how long the same dedupe key keeps merging.
	DedupeWindow time.Duration `json:"dedupeWindow"`
	// MaxPerMinute is the per-source inbound budget.
	MaxPerMinute int `json:"maxPerMinute"`
	// MaxItems caps the pending list.
	MaxItems int `json:"maxItems"`
	// DefaultTTL hides untriaged items after this long (0 = never expire).
	DefaultTTL time.Duration `json:"defaultTTL"`
	// ListLimit is the default page size for List.
	ListLimit int `json:"listLimit"`
}

func (c Config) normalized() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.DedupeWindow <= 0 {
		c.DedupeWindow = defaultDedupeWindow
	}
	if c.MaxPerMinute <= 0 {
		c.MaxPerMinute = defaultMaxPerMinute
	}
	if c.MaxItems <= 0 {
		c.MaxItems = defaultMaxItems
	}
	if c.DefaultTTL < 0 {
		c.DefaultTTL = 0
	} else if c.DefaultTTL == 0 {
		c.DefaultTTL = defaultDefaultTTL
	}
	if c.ListLimit <= 0 {
		c.ListLimit = defaultListLimit
	}
	return c
}

// Service is the in-memory inbox. It is safe for concurrent use; every read
// returns copies so a caller can never mutate stored state by accident.
type Service struct {
	mu    sync.Mutex
	cfg   Config
	items map[string]*contracts.InboxItem
	// dedup maps source\x00key to the live item id.
	dedup map[string]string
	// rate keeps arrival timestamps per source inside the current minute.
	rate map[string][]time.Time
	seq  int64
	// path is the durable document ("", when the service is memory-only).
	path string
}

// NewService builds the service from cfg, filling defaults.
func NewService(cfg Config) *Service {
	return &Service{
		cfg:   cfg.normalized(),
		items: make(map[string]*contracts.InboxItem),
		dedup: make(map[string]string),
		rate:  make(map[string][]time.Time),
	}
}

func dedupSlot(source, key string) string { return source + "\x00" + key }

func cloneItem(item *contracts.InboxItem) contracts.InboxItem {
	out := *item
	if item.TaskIDs != nil {
		out.TaskIDs = append([]string(nil), item.TaskIDs...)
	}
	return out
}

// Add implements contracts.InboxService.
func (s *Service) Add(ctx context.Context, item contracts.InboxItem) (contracts.InboxItem, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.InboxItem{}, false, err
	}
	if item.Source == "" || item.DedupKey == "" {
		return contracts.InboxItem{}, false, contracts.ErrInboxInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Now()

	// The rate budget is checked before dedupe so a flood of duplicates cannot
	// bypass it, and it is per source so one noisy channel cannot starve others.
	budget := s.rate[item.Source]
	cutoff := now.Add(-time.Minute)
	kept := budget[:0]
	for _, at := range budget {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= s.cfg.MaxPerMinute {
		s.rate[item.Source] = kept
		return contracts.InboxItem{}, false, contracts.ErrInboxRateLimited
	}
	s.rate[item.Source] = append(kept, now)

	// Dedupe: the same thing happening again inside the window folds into the
	// existing row instead of becoming a second piece of work.
	if id, ok := s.dedup[dedupSlot(item.Source, item.DedupKey)]; ok {
		if existing, ok := s.items[id]; ok && now.Sub(existing.LastSeenAt) <= s.cfg.DedupeWindow {
			previous := *existing
			existing.Count++
			existing.LastSeenAt = now
			existing.Body = item.Body
			if item.Title != "" {
				existing.Title = item.Title
			}
			if item.Priority > existing.Priority {
				existing.Priority = item.Priority
			}
			if item.Ref != "" {
				existing.Ref = item.Ref
			}
			if err := s.persistLocked(); err != nil {
				*existing = previous // keep memory and disk in agreement
				return contracts.InboxItem{}, false, err
			}
			return cloneItem(existing), true, nil
		}
	}

	if len(s.items) >= s.cfg.MaxItems {
		return contracts.InboxItem{}, false, contracts.ErrInboxFull
	}

	stored := item
	stored.Count = 1
	stored.Status = contracts.InboxStatusNew
	stored.ReceivedAt = now
	stored.LastSeenAt = now
	if stored.ExpiresAt.IsZero() && s.cfg.DefaultTTL > 0 {
		stored.ExpiresAt = now.Add(s.cfg.DefaultTTL)
	}
	if stored.TaskIDs != nil {
		stored.TaskIDs = append([]string(nil), stored.TaskIDs...)
	}

	s.seq++
	stored.ID = fmt.Sprintf("inbox-%06d", s.seq)
	s.items[stored.ID] = &stored
	s.dedup[dedupSlot(stored.Source, stored.DedupKey)] = stored.ID
	if err := s.persistLocked(); err != nil {
		delete(s.items, stored.ID)
		delete(s.dedup, dedupSlot(stored.Source, stored.DedupKey))
		s.seq--
		return contracts.InboxItem{}, false, err
	}
	return cloneItem(&stored), false, nil
}

// List implements contracts.InboxService.
func (s *Service) List(ctx context.Context, filter contracts.InboxFilter) ([]contracts.InboxItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Now()
	statuses := filter.Statuses
	if len(statuses) == 0 {
		statuses = []string{contracts.InboxStatusNew, contracts.InboxStatusTriaged}
	}
	wanted := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		wanted[status] = true
	}

	out := make([]contracts.InboxItem, 0, len(s.items))
	for _, stored := range s.items {
		if !wanted[stored.Status] {
			continue
		}
		if filter.Source != "" && stored.Source != filter.Source {
			continue
		}
		if !filter.IncludeExpired && !stored.ExpiresAt.IsZero() && stored.ExpiresAt.Before(now) {
			continue
		}
		out = append(out, cloneItem(stored))
	}

	// Higher priority first, then the most recently seen — the order a human
	// reads a queue in.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})

	limit := filter.Limit
	if limit <= 0 {
		limit = s.cfg.ListLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Get implements contracts.InboxService.
func (s *Service) Get(ctx context.Context, id string) (contracts.InboxItem, error) {
	if err := ctx.Err(); err != nil {
		return contracts.InboxItem{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.items[id]
	if !ok {
		return contracts.InboxItem{}, fmt.Errorf("%w: %s", contracts.ErrInboxUnknownItem, id)
	}
	return cloneItem(stored), nil
}

// Triage implements contracts.InboxService. It validates every id and the
// action first, then applies, so a bad batch never leaves a half-applied state.
func (s *Service) Triage(ctx context.Context, ids []string, action string, taskIDs []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	var status string
	switch action {
	case contracts.InboxActionTriaged:
		status = contracts.InboxStatusTriaged
	case contracts.InboxActionTask:
		status = contracts.InboxStatusTask
	case contracts.InboxActionIgnored:
		status = contracts.InboxStatusIgnored
	case contracts.InboxActionArchived:
		status = contracts.InboxStatusArchived
	default:
		return 0, contracts.ErrInboxUnknownAction
	}
	if action == contracts.InboxActionTask && len(taskIDs) == 0 {
		return 0, contracts.ErrInboxInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		if _, ok := s.items[id]; !ok {
			return 0, fmt.Errorf("%w: %s", contracts.ErrInboxUnknownItem, id)
		}
	}

	previous := make(map[string]contracts.InboxItem, len(ids))
	updated := 0
	for _, id := range ids {
		stored := s.items[id]
		previous[id] = *stored
		stored.Status = status
		if action == contracts.InboxActionTask {
			stored.TaskIDs = append([]string(nil), taskIDs...)
		}
		updated++
	}
	if err := s.persistLocked(); err != nil {
		for id, snapshot := range previous {
			*s.items[id] = snapshot
		}
		return 0, err
	}
	return updated, nil
}

// Stats implements contracts.InboxService.
func (s *Service) Stats(ctx context.Context) (contracts.InboxStats, error) {
	if err := ctx.Err(); err != nil {
		return contracts.InboxStats{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stats := contracts.InboxStats{Total: len(s.items)}
	for _, stored := range s.items {
		switch stored.Status {
		case contracts.InboxStatusNew:
			stats.New++
		case contracts.InboxStatusTriaged:
			stats.Triaged++
		}
	}
	return stats, nil
}

// compile-time proof that the service satisfies the contract the gateway and
// the triage loop consume.
var _ contracts.InboxService = (*Service)(nil)
