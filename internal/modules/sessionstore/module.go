package sessionstore

import (
	"fmt"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Module returns the SQLite session store kernel module.
//
// It requires the product data directory (contracts.KeyDataDir) and the host
// logger (contracts.KeyLogger), opens <dataDir>/zeokeel.db (or Config.Path),
// applies the idempotent schema and provides the database as
// contracts.KeyStore. The database is closed by a kernel cleanup effect when the
// module fiber unloads.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data directory: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		current, err := openStore(storeOptions{
			dataDir:     dataDir,
			path:        cfg.Path,
			busyTimeout: time.Duration(cfg.BusyTimeoutMs) * time.Millisecond,
			logger:      logger,
		})
		if err != nil {
			return err
		}
		// Register the cleanup before the service so unloading removes the
		// service (kernel-owned disposer) and then closes the database.
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				if closeErr := current.Close(); closeErr != nil {
					logger.Printf("%s: %v", ModuleID, closeErr)
				}
			})}
		}); err != nil {
			_ = current.Close()
			return fmt.Errorf("%s: register cleanup: %w", ModuleID, err)
		}
		var service contracts.Store = current
		if _, err := ctx.Provide(contracts.KeyStore, service); err != nil {
			_ = current.Close()
			return fmt.Errorf("%s: provide store: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyStore)).
		AddRequires(
			module.Requires(contracts.KeyDataDir),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel session store").
		WithVersion("1.0.0").
		WithValidate(func(cfg Config) error {
			if cfg.BusyTimeoutMs < 0 {
				return fmt.Errorf("%s: busyTimeoutMs must not be negative", ModuleID)
			}
			if strings.ContainsRune(cfg.Path, 0) {
				return fmt.Errorf("%s: path contains a NUL byte", ModuleID)
			}
			return nil
		})
}
