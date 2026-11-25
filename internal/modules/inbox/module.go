package inbox

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
const ModuleID = "zeokeel/inbox"

// Module is the inbox descriptor. It provides contracts.KeyInbox and requires
// nothing: the inbox is the outermost entry point, so it must boot before (and
// independently of) every consumer.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		service, err := Open(filepath.Join(dataDir, "inbox"), cfg)
		if err != nil {
			if !errors.Is(err, ErrCorruptStore) {
				return fmt.Errorf("%s: open store: %w", ModuleID, err)
			}
			// A corrupt queue must not brick the product and must not be
			// overwritten silently: move it aside, say so, start empty.
			quarantined := fmt.Sprintf("inbox.json.corrupt-%d", time.Now().Unix())
			_ = os.Rename(filepath.Join(dataDir, "inbox", "inbox.json"), filepath.Join(dataDir, "inbox", quarantined))
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("inbox: corrupt store moved to %s; starting with an empty queue", quarantined)
			}
			service = NewService(cfg)
		}
		if _, err := ctx.Provide(contracts.KeyInbox, contracts.InboxService(service)); err != nil {
			return fmt.Errorf("%s: provide inbox service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyInbox)).
		AddRequires(module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel inbox")
}
