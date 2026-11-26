package dingtalk

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/dingtalk"

// Module is the DingTalk sender descriptor: it requires nothing but the
// credential store and provides contracts.KeyDingTalk. The access token and the
// robot code arrive through configuration or the credential store; the adapter
// reports unauthorized without a token and refuses to pretend to be connected
// while either half is missing (the send body always carries robotCode, so a
// token alone cannot deliver anything).
//
// It deliberately does not require the inbox: DingTalk is outbound only, so it
// must not be able to file a message (docs/research/channel-capabilities.md §6).
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		// Stored credentials fill whatever configuration left empty.
		if store, ok := ctx.Get(contracts.KeyCredentials); ok && store != nil {
			cfg = loadCredential(cfg, store)
		}
		adapter := NewAdapter(cfg)
		if _, err := ctx.Provide(contracts.KeyDingTalk, contracts.DingTalkService(adapter)); err != nil {
			return fmt.Errorf("%s: provide dingtalk: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyDingTalk)).
		WithName("ZeoKeel DingTalk sender")
}
