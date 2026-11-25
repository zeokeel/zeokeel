package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// HeartbeatStatus is what the settings/health surface shows about the loop that
// makes the product work without a browser open: the last pass, how often it
// runs, and whether it is running at all.
type HeartbeatStatus struct {
	// Running reports whether the background loop is started.
	Running bool
	// LeadInterval / ChannelInterval are the configured periods.
	LeadInterval    time.Duration
	ChannelInterval time.Duration
	// LastTick is the time of the last pass (zero = never).
	LastTick time.Time
	// LastReport is the result of the last pass.
	LastReport HeartbeatReport
}

// HeartbeatReport is one pass: how much triage work and how many channel polls
// happened, plus the failures. A failure is recorded, never propagated as a
// reason to stop the loop — one broken channel must not freeze triage.
type HeartbeatReport struct {
	At time.Time
	// LeadRuns is the number of triage passes performed (0 or 1).
	LeadRuns int
	// Dispatched / Ignored come from the triage pass.
	Dispatched int
	Ignored    int
	// Polls counts channel adapters polled; NewMessages counts what they ingested.
	Polls       int
	NewMessages int
	// Errors are one-line failures ("wechat: unavailable: ...").
	Errors []string
}

// HeartbeatConfig is the runtime control surface: the settings screen can turn
// the loop off or change its cadence without restarting the product.
type HeartbeatConfig struct {
	Enabled         *bool
	LeadInterval    time.Duration
	ChannelInterval time.Duration
}

// HeartbeatService is the product's autonomous loop.
type HeartbeatService interface {
	// Tick performs one pass immediately (the loop calls this on its timer).
	Tick(ctx context.Context) (HeartbeatReport, error)
	// Status reports the loop state.
	Status(ctx context.Context) (HeartbeatStatus, error)
	// Configure changes the loop while it runs. A zero field keeps the current
	// value; intervals below HeartbeatMinInterval are refused.
	Configure(ctx context.Context, cfg HeartbeatConfig) error
}

// HeartbeatMinInterval is the shortest cadence the loop accepts: anything faster
// hammers every channel and every triage pass for no new information.
const HeartbeatMinInterval = 5 * time.Second

// KeyHeartbeat is provided by the heartbeat module.
var KeyHeartbeat = cordis.NewServiceKey[HeartbeatService]("zeokeel/heartbeat")
