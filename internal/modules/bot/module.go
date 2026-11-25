package bot

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
const ModuleID = "zeokeel/bots"

// DataSubdir is the directory the store uses inside the product data directory:
// <dataDir>/bots/bots.json.
const DataSubdir = "bots"

// Config is the module's (empty) configuration: the store lives entirely in the
// product data directory, and a configurable directory would only invite bot
// records into a place the product does not back up.
type Config struct{}

// Module is the bot store descriptor (plan G1): it requires the product data
// directory (a bot outlives the session it was created in) and provides
// contracts.KeyBots.
//
// A corrupt store does not brick the product: the store already reports
// contracts.ErrBotStoreCorrupt with an empty, usable store (built-in templates
// still present), and the module quarantines the file as
// bots.json.corrupt-<unix> before the store writes again, so the bytes stay
// available for diagnosis. Quarantine is the precondition for writing: if the
// rename fails, activation fails loudly rather than letting a later Create
// overwrite the only copy of the evidence.
//
// It deliberately does NOT bind bots to sessions, dispatch work to them or grant
// tools at runtime — that is the next step, and it reads these records.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, _ Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		service, err := Open(filepath.Join(dataDir, DataSubdir))
		if err != nil {
			if !errors.Is(err, contracts.ErrBotStoreCorrupt) {
				return fmt.Errorf("%s: open bot store: %w", ModuleID, err)
			}
			quarantined := fmt.Sprintf("%s.corrupt-%d", FileName, time.Now().Unix())
			if renameErr := os.Rename(service.Path(), filepath.Join(filepath.Dir(service.Path()), quarantined)); renameErr != nil {
				return fmt.Errorf("%s: quarantine corrupt bot store: %w", ModuleID, renameErr)
			}
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("bot: corrupt bot store moved to %s; starting with the built-in templates and no bots", quarantined)
			}
		}
		if _, err := ctx.Provide(contracts.KeyBots, contracts.BotService(service)); err != nil {
			return fmt.Errorf("%s: provide bots: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyBots)).
		AddRequires(module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel bots (岗位模板与实例)")
}
