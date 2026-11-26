package workflow

// Module wiring for the stage-based workflow backend.
//
// Placement: it needs the chat service (to start a stage's session) and the task
// board (to create the stage's task), so it is registered after both. It provides
// KeyWorkflow only; the advance loop is owned here and runs on a ticker.

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
const ModuleID = "zeokeel/workflow"

// Module starts the workflow backend: durable definitions, runs, and the advance
// loop that dispatches stages as their dependencies complete.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		board, err := ctx.Require(contracts.KeyTaskBoard)
		if err != nil {
			return fmt.Errorf("%s: require task board: %w", ModuleID, err)
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return fmt.Errorf("%s: require chat: %w", ModuleID, err)
		}
		dispatcher, ok := chat.(contracts.WorkflowDispatcher)
		if !ok {
			return fmt.Errorf("%s: chat service cannot dispatch stages", ModuleID)
		}
		logger := func(format string, args ...any) {}
		if log, ok := ctx.Get(contracts.KeyLogger); ok && log != nil {
			logger = log.Printf
		}

		service := New(board, dispatcher, cfg)
		if dataDir, ok := ctx.Get(contracts.KeyDataDir); ok && dataDir != "" {
			opened, err := Open(filepath.Join(dataDir, "workflow"), board, dispatcher, cfg)
			if err != nil {
				if !errors.Is(err, ErrStoreCorrupt) {
					return fmt.Errorf("%s: open store: %w", ModuleID, err)
				}
				// A corrupt document must not brick the product and must not be
				// overwritten silently: move it aside and keep serving with no
				// definitions (the runs it described are gone with it, visibly).
				quarantined := fmt.Sprintf("%s.corrupt-%d", StoreFileName, time.Now().Unix())
				_ = os.Rename(filepath.Join(dataDir, "workflow", StoreFileName), filepath.Join(dataDir, "workflow", quarantined))
				logger("[workflow] corrupt store moved to %s; starting empty", quarantined)
			} else {
				service = opened
			}
		}

		// A stage that names tools needs the lease layer to record the binding;
		// it is optional here so an assembly without the lease can still run
		// stages that do not name any.
		if lease, ok := ctx.Get(contracts.KeyTaskLease); ok && lease != nil {
			if binder, ok := lease.(ToolBinder); ok {
				service.WithToolBinder(binder)
			}
		}

		if _, err := ctx.Provide(contracts.KeyWorkflow, contracts.WorkflowService(service)); err != nil {
			return fmt.Errorf("%s: provide workflow: %w", ModuleID, err)
		}

		done := make(chan struct{})
		loopCtx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(done)
			runLoop(loopCtx, service, cfg, logger)
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
		AddProvides(module.Provides(contracts.KeyWorkflow)).
		AddRequires(
			module.Requires(contracts.KeyTaskBoard),
			module.Requires(contracts.KeyChat),
		).
		WithName("ZeoKeel workflow (staged pipelines)")
}

// runLoop advances every run until the module is torn down. A failed pass is
// logged and retried on the next tick: a transient store or board error must not
// stop the loop that exists to keep runs moving.
func runLoop(ctx context.Context, service *Service, cfg Config, logger func(string, ...any)) {
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
			report, err := service.Advance(ctx)
			if err != nil {
				logger("[workflow] advance: %v", err)
				continue
			}
			if report.Dispatched > 0 || report.Completed > 0 || len(report.Errors) > 0 {
				logger("[workflow] advance: runs=%d dispatched=%d completed=%d errors=%d",
					report.Runs, report.Dispatched, report.Completed, len(report.Errors))
			}
		}
	}
}
