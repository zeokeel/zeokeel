package webhook

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
const ModuleID = "zeokeel/webhook"

// Module is the webhook adapter descriptor: it requires the inbox (its only
// destination) and provides contracts.KeyWebhook for the HTTP layer.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		sourceDir := filepath.Join(dataDir, "webhook")
		service, err := Open(sourceDir, inboxService, cfg)
		if err != nil {
			// A corrupt source file must not brick the product and must not be
			// overwritten: move it aside, say so, keep the endpoint table empty.
			quarantined := fmt.Sprintf("sources.json.corrupt-%d", time.Now().Unix())
			_ = os.Rename(filepath.Join(sourceDir, "sources.json"), filepath.Join(sourceDir, quarantined))
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("webhook: corrupt source list moved to %s; no endpoints are configured", quarantined)
			}
			service = NewService(inboxService, cfg)
		}
		_ = errors.Is // keep the import for the wrapped errors above
		if _, err := ctx.Provide(contracts.KeyWebhook, contracts.WebhookService(service)); err != nil {
			return fmt.Errorf("%s: provide webhook: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyWebhook)).
		AddRequires(module.Requires(contracts.KeyInbox), module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel webhook ingress")
}
