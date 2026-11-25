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
}

// New wires the loop (now defaults to time.Now; nil lead is allowed for tests
// that only exercise channels).
func New(cfg Config, lead contracts.LeadService, channels []Channel, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{cfg: cfg.normalized(), lead: lead, channels: channels, now: now}
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

// Run ticks until the context ends: triage on LeadInterval, channels on
// ChannelInterval. It returns nil on cancellation (a stop is not a failure).
func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.enabled() {
		return nil
	}
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	leadTick := time.NewTicker(s.cfg.LeadInterval)
	defer leadTick.Stop()
	channelTick := time.NewTicker(s.cfg.ChannelInterval)
	defer channelTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-leadTick.C:
			s.runLeadOnly(ctx)
		case <-channelTick.C:
			s.runChannelsOnly(ctx)
		}
	}
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
