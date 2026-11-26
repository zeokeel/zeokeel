package feishu

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/feishu"

// Module is the Feishu (Lark) sender descriptor: it requires nothing but the
// credential store and provides contracts.KeyFeishu. App credentials arrive
// through configuration or the credential store; without them the adapter reports
// unauthorized instead of pretending to be connected.
//
// It deliberately does not require the inbox: Feishu is outbound only, so it must
// not be able to file a message (docs/research/channel-capabilities.md §6).
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		// Stored credentials fill whatever configuration left empty.
		if store, ok := ctx.Get(contracts.KeyCredentials); ok && store != nil {
			cfg = loadCredential(cfg, store)
		}
		adapter := NewAdapter(cfg)
		if _, err := ctx.Provide(contracts.KeyFeishu, contracts.FeishuService(adapter)); err != nil {
			return fmt.Errorf("%s: provide feishu: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyFeishu)).
		WithName("ZeoKeel Feishu (Lark) sender")
}
