package users

// The users module: it opens the roster under the product data directory and
// publishes it as contracts.KeyUsers. The gateway (roster page + action checks),
// the bot surface (Owner resolution) and the memory reviewer require it instead
// of reading the document themselves.

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
const ModuleID = "zeokeel/users"

// DataSubdir is the directory the store uses inside the product data directory:
// <dataDir>/users/users.json.
const DataSubdir = "users"

// Config is the module's (empty) configuration: the roster lives entirely in the
// product data directory, and a configurable directory would only invite
// permission-bearing records into a place the product does not back up.
type Config struct{}

// Module is the roster descriptor (plan M6): it requires the product data
// directory (a member outlives every session and task it is attributed to) and
// provides contracts.KeyUsers.
//
// A corrupt store does not brick the product: the store already reports
// contracts.ErrUserStoreCorrupt with a usable roster (the seeded owner), and the
// module quarantines the file as users.json.corrupt-<unix> before the store
// writes again, so the bytes stay available for diagnosis. Quarantine is the
// precondition for writing: if the rename fails, activation fails loudly rather
// than letting a later Put overwrite the only copy of the evidence — the roster
// is a permission surface, and losing its history is not recoverable.
//
// It deliberately does NOT enforce the role table at runtime. contracts.Allowed
// is a pure function; the callers that act (gateway, task board, memory review)
// consult it. Binding a user to a channel identity, per-user memory isolation
// and budget attribution are the rest of M6 and read these records.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, _ Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		service, err := Open(filepath.Join(dataDir, DataSubdir))
		if err != nil {
			if !errors.Is(err, contracts.ErrUserStoreCorrupt) {
				return fmt.Errorf("%s: open user store: %w", ModuleID, err)
			}
			quarantined := fmt.Sprintf("%s.corrupt-%d", FileName, time.Now().Unix())
			if renameErr := os.Rename(service.Path(), filepath.Join(filepath.Dir(service.Path()), quarantined)); renameErr != nil {
				return fmt.Errorf("%s: quarantine corrupt user store: %w", ModuleID, renameErr)
			}
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("users: corrupt user store moved to %s; starting with the seeded owner and no other members", quarantined)
			}
		}
		if _, err := ctx.Provide(contracts.KeyUsers, contracts.UserService(service)); err != nil {
			return fmt.Errorf("%s: provide users: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyUsers)).
		AddRequires(module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel users (成员与角色)")
}
