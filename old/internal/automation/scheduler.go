package automation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Store is the persistence surface the scheduler needs. *store.Store
// implements it directly.
type Store interface {
	ListAutomations() ([]store.Automation, error)
	SetAutomationNextRun(id string, next time.Time) (*store.Automation, error)
	MarkAutomationRun(id string, lastRun time.Time, nextRun *time.Time) (*store.Automation, error)
}

// *store.Store is the production implementation; this assertion keeps the
// scheduler and the store interface in sync.
var _ Store = (*store.Store)(nil)

// Config configures a Scheduler.
type Config struct {
	// Store is required.
	Store Store
	// OnFire runs one due task. It may be nil, in which case a fired task's
	// schedule still advances.
	OnFire func(store.Automation)
	// Now is the clock; nil means time.Now. Tests inject a fake clock.
	Now func() time.Time
	// Interval is how often Start polls; zero means one minute.
	Interval time.Duration
}

// Scheduler evaluates persisted automation tasks and calls OnFire for the
// ones that are due. Tick is deterministic: it takes the time explicitly, so
// a fake clock drives tests and Start only supplies the polling loop.
type Scheduler struct {
	Store    Store
	OnFire   func(store.Automation)
	Now      func() time.Time
	Interval time.Duration

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	wg      sync.WaitGroup
	lastErr error
}

// New validates cfg and returns a Scheduler.
func New(cfg Config) (*Scheduler, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("automation: scheduler needs a store")
	}
	return &Scheduler{
		Store:    cfg.Store,
		OnFire:   cfg.OnFire,
		Now:      cfg.Now,
		Interval: cfg.Interval,
	}, nil
}

// Start polls the store until ctx is cancelled or Stop is called. It is a
// no-op when the scheduler is already running.
func (s *Scheduler) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.stop = stop
	s.running = true
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	now := s.Now
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				at := time.Now()
				if now != nil {
					at = now()
				}
				s.Tick(at)
			}
		}
	}()
}

// Stop stops the polling loop and waits for it to exit. It is idempotent.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stop)
	s.stop = nil
	s.running = false
	s.mu.Unlock()
	s.wg.Wait()
}

// Running reports whether the polling loop is active.
func (s *Scheduler) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Tick fires every enabled task whose NextRun is at or before now, and returns
// the tasks it fired in deterministic order. Enabled tasks without a NextRun
// are scheduled from now. A task with an invalid cron expression is skipped
// and surfaces through LastError.
//
// Schedule expressions are wall-clock expressions in the caller's time zone:
// "@daily" means local midnight, not UTC midnight. The next run is therefore
// computed from now's location and only the persisted timestamps are
// normalized to UTC (the store keeps every instant in UTC).
func (s *Scheduler) Tick(now time.Time) []store.Automation {
	tasks, err := s.Store.ListAutomations()
	if err != nil {
		s.recordErr(fmt.Errorf("automation: list tasks: %w", err))
		return nil
	}

	sort.Slice(tasks, func(i, j int) bool {
		ni, nj := tasks[i].NextRun, tasks[j].NextRun
		switch {
		case ni == nil && nj == nil:
			return tasks[i].ID < tasks[j].ID
		case ni == nil:
			return false
		case nj == nil:
			return true
		case ni.Equal(*nj):
			return tasks[i].ID < tasks[j].ID
		default:
			return ni.Before(*nj)
		}
	})

	fired := make([]store.Automation, 0)
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		schedule, err := Parse(task.Cron)
		if err != nil {
			s.recordErr(fmt.Errorf("automation: task %q: %w", task.ID, err))
			continue
		}
		if task.NextRun == nil {
			next := schedule.Next(now).UTC()
			if _, err := s.Store.SetAutomationNextRun(task.ID, next); err != nil {
				s.recordErr(fmt.Errorf("automation: schedule task %q: %w", task.ID, err))
			}
			continue
		}
		if task.NextRun.After(now) {
			continue
		}

		if s.OnFire != nil {
			s.OnFire(task)
		}
		next := schedule.Next(now).UTC()
		if _, err := s.Store.MarkAutomationRun(task.ID, now.UTC(), &next); err != nil {
			s.recordErr(fmt.Errorf("automation: mark task %q run: %w", task.ID, err))
		}
		fired = append(fired, task)
	}
	return fired
}

// recordErr keeps the most recent scheduler error.
func (s *Scheduler) recordErr(err error) {
	s.mu.Lock()
	s.lastErr = err
	s.mu.Unlock()
}

// LastError returns the most recent list/schedule/record error, if any.
func (s *Scheduler) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}
