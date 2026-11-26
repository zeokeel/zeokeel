// Package heartbeat is the product's autonomous loop: with no browser open it
// keeps triaging the inbox and keeps reading every configured channel. It is
// deliberately dumb — one pass, recorded failures, no retry policy of its own —
// because each adapter already owns its backoff and every mistake here is a
// mistake that repeats forever.
package heartbeat

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Channel is one adapter the loop polls.
type Channel struct {
	// Name labels failures ("wechat", "email", ...).
	Name string
	// Poll performs one poll and reports how many messages it ingested.
	Poll func(ctx context.Context) (int, error)
}

// Config is the module's typed assembly config.
type Config struct {
	// Enabled starts the loop (default true).
	Enabled *bool `json:"enabled,omitempty"`
	// LeadInterval is how often triage runs (default 30s).
	LeadInterval time.Duration `json:"leadInterval,omitempty"`
	// ChannelInterval is how often channels are polled (default 60s).
	ChannelInterval time.Duration `json:"channelInterval,omitempty"`
	// MinInterval overrides the cadence floor (tests use it; 0 = the product
	// default of 5s). It exists so the floor can be tested without sleeping 5s.
	MinInterval time.Duration `json:"-"`
}

func (c Config) enabled() bool { return c.Enabled == nil || *c.Enabled }

func (c Config) normalized() Config {
	if c.LeadInterval <= 0 {
		c.LeadInterval = 30 * time.Second
	}
	if c.ChannelInterval <= 0 {
		c.ChannelInterval = 60 * time.Second
	}
	return c
}

// Service implements contracts.HeartbeatService.
type Service struct {
	cfg      Config
	lead     contracts.LeadService
	channels []Channel
	now      func() time.Time

	mu       sync.Mutex
	lastTick time.Time
	last     contracts.HeartbeatReport
	running  bool
	// wake asks the running loop to re-read its configuration.
	wake chan struct{}
}

// New wires the loop (now defaults to time.Now; nil lead is allowed for tests
// that only exercise channels).
func New(cfg Config, lead contracts.LeadService, channels []Channel, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{cfg: cfg.normalized(), lead: lead, channels: channels, now: now, wake: make(chan struct{}, 1)}
}

// Tick implements contracts.HeartbeatService.
func (s *Service) Tick(ctx context.Context) (contracts.HeartbeatReport, error) {
	if err := ctx.Err(); err != nil {
		return contracts.HeartbeatReport{}, err
	}
	report := contracts.HeartbeatReport{At: s.now()}

	if s.lead != nil {
		leadReport, err := s.lead.RunOnce(ctx)
		report.LeadRuns = 1
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("lead: %v", err))
		} else {
			report.Dispatched = leadReport.Dispatched
			report.Ignored = leadReport.Ignored
			report.Errors = append(report.Errors, leadReport.Errors...)
		}
	}

	for _, channel := range s.channels {
		if channel.Poll == nil {
			continue
		}
		ingested, err := channel.Poll(ctx)
		report.Polls++
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", channel.Name, err))
			continue
		}
		report.NewMessages += ingested
	}

	s.mu.Lock()
	s.lastTick = report.At
	s.last = report
	s.mu.Unlock()
	return report, nil
}

// Status implements contracts.HeartbeatService.
func (s *Service) Status(ctx context.Context) (contracts.HeartbeatStatus, error) {
	if err := ctx.Err(); err != nil {
		return contracts.HeartbeatStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return contracts.HeartbeatStatus{
		Running: s.running, LeadInterval: s.cfg.LeadInterval, ChannelInterval: s.cfg.ChannelInterval,
		LastTick: s.lastTick, LastReport: s.last,
	}, nil
}

// Run ticks until the context ends, re-reading its configuration whenever
// Configure asks it to. Two independent schedules (lead, channels) share one
// wait: the loop sleeps until whichever fires first, and a Configure shortens or
// lengthens that wait immediately. It returns nil on cancellation (a stop is not
// a failure).
func (s *Service) Run(ctx context.Context) error {
	nextLead := s.now().Add(s.configSnapshot().LeadInterval)
	nextChannel := s.now().Add(s.configSnapshot().ChannelInterval)

	for {
		cfg := s.configSnapshot()
		s.setRunning(cfg.enabled())
		if !cfg.enabled() {
			// Disabled: wait for a Configure or the context. Returning here would
			// make enable-at-runtime impossible.
			select {
			case <-ctx.Done():
				s.setRunning(false)
				return nil
			case <-s.wake:
			}
			nextLead = s.now().Add(s.configSnapshot().LeadInterval)
			nextChannel = s.now().Add(s.configSnapshot().ChannelInterval)
			continue
		}

		wait := nextLead.Sub(s.now())
		if channelWait := nextChannel.Sub(s.now()); channelWait < wait {
			wait = channelWait
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.setRunning(false)
			return nil
		case <-s.wake:
			timer.Stop()
			cfg = s.configSnapshot()
			nextLead = s.now().Add(cfg.LeadInterval)
			nextChannel = s.now().Add(cfg.ChannelInterval)
		case <-timer.C:
			now := s.now()
			if !now.Before(nextLead) {
				s.runLeadOnly(ctx)
				nextLead = s.now().Add(s.configSnapshot().LeadInterval)
			}
			if !now.Before(nextChannel) {
				s.runChannelsOnly(ctx)
				nextChannel = s.now().Add(s.configSnapshot().ChannelInterval)
			}
		}
	}
}

// Configure implements contracts.HeartbeatService.
func (s *Service) Configure(ctx context.Context, update contracts.HeartbeatConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The floor read takes the same mutex, so read it before locking.
	floor := s.minInterval()
	s.mu.Lock()
	next := s.cfg
	if update.Enabled != nil {
		next.Enabled = update.Enabled
	}
	if update.LeadInterval != 0 {
		if update.LeadInterval < floor {
			s.mu.Unlock()
			return fmt.Errorf("heartbeat: lead interval %s is below the %s minimum", update.LeadInterval, floor)
		}
		if update.LeadInterval > 24*time.Hour {
			s.mu.Unlock()
			return fmt.Errorf("heartbeat: lead interval %s is above 24h", update.LeadInterval)
		}
		next.LeadInterval = update.LeadInterval
	}
	if update.ChannelInterval != 0 {
		if update.ChannelInterval < floor {
			s.mu.Unlock()
			return fmt.Errorf("heartbeat: channel interval %s is below the %s minimum", update.ChannelInterval, floor)
		}
		if update.ChannelInterval > 24*time.Hour {
			s.mu.Unlock()
			return fmt.Errorf("heartbeat: channel interval %s is above 24h", update.ChannelInterval)
		}
		next.ChannelInterval = update.ChannelInterval
	}
	s.cfg = next
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// minInterval is the cadence floor (product default, or the test override).
func (s *Service) minInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.MinInterval > 0 {
		return s.cfg.MinInterval
	}
	return contracts.HeartbeatMinInterval
}

func (s *Service) configSnapshot() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Service) setRunning(running bool) {
	s.mu.Lock()
	s.running = running
	s.mu.Unlock()
}

func (s *Service) runLeadOnly(ctx context.Context) {
	if s.lead == nil {
		return
	}
	report, err := s.lead.RunOnce(ctx)
	update := contracts.HeartbeatReport{At: s.now(), LeadRuns: 1}
	if err != nil {
		update.Errors = append(update.Errors, fmt.Sprintf("lead: %v", err))
	} else {
		update.Dispatched, update.Ignored, update.Errors = report.Dispatched, report.Ignored, report.Errors
	}
	s.record(update)
}

func (s *Service) runChannelsOnly(ctx context.Context) {
	update := contracts.HeartbeatReport{At: s.now()}
	for _, channel := range s.channels {
		if channel.Poll == nil {
			continue
		}
		ingested, err := channel.Poll(ctx)
		update.Polls++
		if err != nil {
			update.Errors = append(update.Errors, fmt.Sprintf("%s: %v", channel.Name, err))
			continue
		}
		update.NewMessages += ingested
	}
	s.record(update)
}

func (s *Service) record(report contracts.HeartbeatReport) {
	s.mu.Lock()
	s.lastTick = report.At
	s.last = report
	s.mu.Unlock()
}

var _ contracts.HeartbeatService = (*Service)(nil)
