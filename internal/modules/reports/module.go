package reports

// Module wiring for the report backend. It needs nothing but a data directory:
// delivery is optional (a report is still a report when no channel is bound),
// so a missing dispatcher degrades to "kept, deferred" rather than failing
// activation.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/reports"

// DefaultSweepInterval is how often deferred reports are retried.
const DefaultSweepInterval = time.Minute

// Module starts the report backend and its retry loop.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		logger := func(format string, args ...any) {}
		if log, ok := ctx.Get(contracts.KeyLogger); ok && log != nil {
			logger = log.Printf
		}
		var notify Dispatcher
		if dispatcher, ok := ctx.Get(contracts.KeyChannelDispatch); ok && dispatcher != nil {
			notify = dispatcher
		}
		service := New(notify, loggerOf(ctx), cfg)
		if dataDir, ok := ctx.Get(contracts.KeyDataDir); ok && dataDir != "" {
			opened, err := Open(filepath.Join(dataDir, "reports"), notify, loggerOf(ctx), cfg)
			if err != nil {
				if !errors.Is(err, ErrStoreCorrupt) {
					return fmt.Errorf("%s: open store: %w", ModuleID, err)
				}
				quarantined := fmt.Sprintf("%s.corrupt-%d", StoreFileName, time.Now().Unix())
				_ = os.Rename(filepath.Join(dataDir, "reports", StoreFileName), filepath.Join(dataDir, "reports", quarantined))
				logger("[reports] corrupt store moved to %s; starting empty", quarantined)
			} else {
				service = opened
			}
		}
		if _, err := ctx.Provide(contracts.KeyReports, contracts.ReportService(service)); err != nil {
			return fmt.Errorf("%s: provide reports: %w", ModuleID, err)
		}
		if !cfg.enabled() {
			return nil
		}
		done := make(chan struct{})
		loopCtx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(done)
			ticker := time.NewTicker(cfg.sweepInterval())
			defer ticker.Stop()
			for {
				select {
				case <-loopCtx.Done():
					return
				case <-ticker.C:
					report, err := service.Sweep(loopCtx)
					if err != nil {
						logger("[reports] sweep: %v", err)
						continue
					}
					if report.Retried > 0 {
						logger("[reports] sweep: retried=%d delivered=%d digests=%d errors=%d",
							report.Retried, report.Delivered, report.Digests, len(report.Errors))
					}
				}
			}
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
		AddProvides(module.Provides(contracts.KeyReports)).
		WithName("ZeoKeel structured reports")
}

// loggerOf returns the kernel logger, or nil.
func loggerOf(ctx cordis.Context) contracts.Logger {
	if log, ok := ctx.Get(contracts.KeyLogger); ok && log != nil {
		return log
	}
	return nil
}
