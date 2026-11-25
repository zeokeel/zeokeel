package inbox

import (
	"fmt"

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
		service := NewService(cfg)
		if _, err := ctx.Provide(contracts.KeyInbox, contracts.InboxService(service)); err != nil {
			return fmt.Errorf("%s: provide inbox service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyInbox)).
		WithName("ZeoKeel inbox")
}
