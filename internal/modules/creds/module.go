package creds

// The creds module: it opens the credential store under the product data
// directory and publishes it as contracts.KeyCredentials. Channel adapters
// (feishu, wechat, dingtalk, ...) require it instead of carrying secrets in
// their own configuration.

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
const ModuleID = "zeokeel/credentials"

// DataSubdir is the directory the store uses inside the product data
// directory: <dataDir>/credentials/credentials.json.
const DataSubdir = "credentials"

// Config is the module's (empty) configuration: the store lives entirely in the
// product data directory, so there is nothing to tune and nothing secret to put
// in an assembly document.
type Config struct{}

// Module is the credential store descriptor: it requires the product data
// directory (the store must survive a restart) and provides
// contracts.KeyCredentials.
//
// A corrupt store does not brick the product: the store already reports
// ErrStoreCorrupt with an empty usable set, and the module quarantines the file
// as credentials.json.corrupt-<unix> before the store writes again, so the bad
// bytes stay available for diagnosis. Quarantine is the precondition for
// writing — if the rename fails, activation fails loudly rather than letting a
// later Set overwrite the only copy of the evidence.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		logf := func(format string, args ...any) {
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf(format, args...)
			}
		}
		service, err := openStore(dataDir, logf)
		if err != nil {
			return fmt.Errorf("%s: open store: %w", ModuleID, err)
		}
		if _, err := ctx.Provide(contracts.KeyCredentials, contracts.CredentialStore(service)); err != nil {
			return fmt.Errorf("%s: provide credentials: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyCredentials)).
		AddRequires(module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel credential store")
}

// openStore opens <dataDir>/credentials and applies the module's resilience
// policy: a corrupt document is quarantined (never deleted) and a fresh store is
// opened in its place.
func openStore(dataDir string, logf func(string, ...any)) (*Service, error) {
	dir := filepath.Join(dataDir, DataSubdir)
	service, err := Open(dir)
	if err == nil {
		return service, nil
	}
	if !errors.Is(err, ErrStoreCorrupt) {
		return nil, err
	}
	quarantinePath := fmt.Sprintf("%s.corrupt-%d", service.Path(), time.Now().Unix())
	logf("creds: credential store is corrupt (%v); quarantining it as %s and starting empty; adapters must be reconnected", err, quarantinePath)
	if renameErr := os.Rename(service.Path(), quarantinePath); renameErr != nil {
		return nil, fmt.Errorf("quarantine corrupt store %s: %w", service.Path(), renameErr)
	}
	fresh, retryErr := Open(dir)
	if retryErr != nil {
		return nil, retryErr
	}
	return fresh, nil
}
