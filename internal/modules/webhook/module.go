package webhook

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/webhook"

// Module is the webhook adapter descriptor: it requires the inbox (its only
// destination) and provides contracts.KeyWebhook for the HTTP layer.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		service := NewService(inboxService, cfg)
		if _, err := ctx.Provide(contracts.KeyWebhook, contracts.WebhookService(service)); err != nil {
			return fmt.Errorf("%s: provide webhook: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyWebhook)).
		AddRequires(module.Requires(contracts.KeyInbox)).
		WithName("ZeoKeel webhook ingress")
}
