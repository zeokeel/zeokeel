package wechat

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/wechat"

// Module is the WeChat adapter descriptor: it requires the inbox and provides
// contracts.KeyWeChat. The login token arrives through configuration (the QR
// flow persists it later); without one the adapter reports unauthorized instead
// of pretending to be connected.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		adapter := NewAdapter(inboxService, cfg)
		if _, err := ctx.Provide(contracts.KeyWeChat, contracts.WeChatService(adapter)); err != nil {
			return fmt.Errorf("%s: provide wechat: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyWeChat)).
		AddRequires(module.Requires(contracts.KeyInbox)).
		WithName("ZeoKeel WeChat (ClawBot/iLink) adapter")
}
