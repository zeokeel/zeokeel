package feishu

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/feishu"

// Module is the Feishu (Lark) adapter descriptor: it requires the inbox and
// provides contracts.KeyFeishu. App credentials and the encrypt key arrive
// through configuration (the settings/credential store persists them later);
// without them the adapter reports unauthorized instead of pretending to be
// connected.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		adapter := NewAdapter(inboxService, cfg)
		if _, err := ctx.Provide(contracts.KeyFeishu, contracts.FeishuService(adapter)); err != nil {
			return fmt.Errorf("%s: provide feishu: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyFeishu)).
		AddRequires(module.Requires(contracts.KeyInbox)).
		WithName("ZeoKeel Feishu (Lark) adapter")
}
