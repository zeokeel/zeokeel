package audit

// Module wiring for the automatic audit loop.
//
// Placement matters: the loop needs the chat service (agent) and the lease layer
// (tasklease), so it is registered after both. It provides KeyAudit and nothing
// else: a caller that wants the loop to run a sweep now (the diagnostics surface)
// asks it; the loop itself runs on a ticker owned here.

import (
	"context"
	"fmt"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/audit"

// Config is the module's typed assembly config.
type Config struct {
	// Enabled starts the loop (default true). A deployment that wants manual
	// audits only turns it off and calls Sweep from its own surface.
	Enabled *bool `json:"enabled,omitempty"`
	// Interval overrides contracts.AuditInterval (tests and operators).
	Interval time.Duration `json:"interval,omitempty"`
}

func (c Config) enabled() bool { return c.Enabled == nil || *c.Enabled }

func (c Config) interval() time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	return contracts.AuditInterval
}

// Module starts the audit loop over the review queue.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return fmt.Errorf("%s: require chat: %w", ModuleID, err)
		}
		lease, err := ctx.Require(contracts.KeyTaskLease)
		if err != nil {
			return fmt.Errorf("%s: require task lease: %w", ModuleID, err)
		}
		// The three read halves the loop drives on. They are asserted here, at
		// activation, so a backend missing one fails loudly instead of running a
		// loop that can only log "not supported" every 5 seconds.
		queue, ok := lease.(contracts.TaskReviewQueue)
		if !ok {
			return fmt.Errorf("%s: task lease does not implement the review queue", ModuleID)
		}
		reviews, ok := lease.(contracts.TaskReviewLog)
		if !ok {
			return fmt.Errorf("%s: task lease does not implement the review log", ModuleID)
		}
		rework, ok := lease.(Rework)
		if !ok {
			return fmt.Errorf("%s: task lease does not implement the rework counter", ModuleID)
		}

		deps := Deps{Chat: chat, Queue: queue, Reviews: reviews, Rework: rework}
		if board, ok := ctx.Get(contracts.KeyTaskBoard); ok && board != nil {
			deps.Board = board
		}
		if dispatcher, ok := ctx.Get(contracts.KeyChannelDispatch); ok && dispatcher != nil {
			deps.Notifier = dispatcher
		}
		if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
			deps.Logger = logger
		}

		service := New(deps)
		if _, err := ctx.Provide(contracts.KeyAudit, contracts.AuditService(service)); err != nil {
			return fmt.Errorf("%s: provide audit: %w", ModuleID, err)
		}

		done := make(chan struct{})
		loopCtx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(done)
			runLoop(loopCtx, service, cfg)
		}()
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
				}
			})}
		}); err != nil {
			cancel()
			return fmt.Errorf("%s: own loop shutdown: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyAudit)).
		AddRequires(
			module.Requires(contracts.KeyChat),
			module.Requires(contracts.KeyTaskLease),
		).
		WithName("ZeoKeel automatic audit loop")
}

// runLoop sweeps the review queue until the module is torn down. One failed
// sweep is logged and retried on the next tick: a transient store error must not
// stop the loop that exists to keep tasks moving.
func runLoop(ctx context.Context, service *Service, cfg Config) {
	if !cfg.enabled() {
		return
	}
	ticker := time.NewTicker(cfg.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report, err := service.Sweep(ctx)
			if err != nil {
				if logger := service.deps.Logger; logger != nil {
					logger.Printf("[audit] sweep: %v", err)
				}
				continue
			}
			if report.AuditorsStarted > 0 || report.Redispatches > 0 || report.Escalated > 0 || len(report.Errors) > 0 {
				if logger := service.deps.Logger; logger != nil {
					logger.Printf("[audit] sweep: queued=%d auditors=%d reworks=%d escalated=%d errors=%d",
						report.Queued, report.AuditorsStarted, report.Redispatches, report.Escalated, len(report.Errors))
				}
			}
		}
	}
}
