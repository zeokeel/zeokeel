package tasklease

import (
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
const ModuleID = "zeokeel/tasklease"

// Module is the task lease descriptor: it requires the task board and the
// product data directory (a live claim must survive a restart, and a claim that
// expired while the process was down must hand its task back to the board), and
// provides the claim/heartbeat/evidence/review layer.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		board, err := ctx.Require(contracts.KeyTaskBoard)
		if err != nil {
			return fmt.Errorf("%s: require task board: %w", ModuleID, err)
		}
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		logger := func(format string, args ...any) {}
		if log, ok := ctx.Get(contracts.KeyLogger); ok && log != nil {
			logger = log.Printf
		}
		if cfg.OnDirectClose == nil {
			// The unaudited-close row is written by the review log; this line is
			// the operator's copy of it, not a second record.
			cfg.OnDirectClose = func(taskID, actor, source string) {
				logger("[tasklease] task %s closed via %s by %s without a lease review", taskID, source, actor)
			}
		}
		service, err := Open(filepath.Join(dataDir, "tasklease"), board, cfg)
		if err != nil {
			if !errors.Is(err, ErrLeaseStoreCorrupt) {
				return fmt.Errorf("%s: open lease store: %w", ModuleID, err)
			}
			// A corrupt lease table must not brick the product and must not be
			// overwritten silently: move it aside and keep serving with no live
			// claims (the board still shows the work, so nothing is lost).
			quarantined := fmt.Sprintf("%s.corrupt-%d", leaseStoreName, time.Now().Unix())
			_ = os.Rename(
				filepath.Join(dataDir, "tasklease", leaseStoreName),
				filepath.Join(dataDir, "tasklease", quarantined),
			)
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("tasklease: corrupt lease store moved to %s; starting with no live leases", quarantined)
			}
		}
		// Every path that can move a task into a terminal state must leave an
		// audit row, and the lease layer owns that log. The board learns about
		// this service here, at activation, because the dependency runs the other
		// way (the lease requires the board).
		if setter, ok := board.(contracts.TaskCloseObserverSetter); ok {
			setter.SetTaskCloseObserver(service)
		}
		if _, err := ctx.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(service)); err != nil {
			return fmt.Errorf("%s: provide task lease: %w", ModuleID, err)
		}
		// The held-send half is its own key: a consumer that only parks outbound
		// messages should not have to implement thirteen lease methods.
		if _, err := ctx.Provide(contracts.KeyTaskSendHolder, contracts.TaskSendHolder(service)); err != nil {
			return fmt.Errorf("%s: provide held sends: %w", ModuleID, err)
		}
		if _, err := ctx.Provide(contracts.KeyTaskRequester, contracts.TaskRequester(service)); err != nil {
			return fmt.Errorf("%s: provide task requester: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyTaskLease), module.Provides(contracts.KeyTaskSendHolder), module.Provides(contracts.KeyTaskRequester)).
		AddRequires(module.Requires(contracts.KeyTaskBoard), module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel task lease (claim, evidence, review)")
}
