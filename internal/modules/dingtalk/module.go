package dingtalk

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/dingtalk"

// Module is the DingTalk adapter descriptor: it requires the inbox and provides
// contracts.KeyDingTalk. The access token and the app secret arrive through
// configuration; without a token the adapter reports unauthorized instead of
// pretending to be connected, and without a secret every callback is refused.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		adapter := NewAdapter(inboxService, cfg)
		if _, err := ctx.Provide(contracts.KeyDingTalk, contracts.DingTalkService(adapter)); err != nil {
			return fmt.Errorf("%s: provide dingtalk: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyDingTalk)).
		AddRequires(module.Requires(contracts.KeyInbox)).
		WithName("ZeoKeel DingTalk chat-bot adapter")
}
