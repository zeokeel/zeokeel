package replyrelay

import (
	"context"
	"fmt"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/replyrelay"

// Module starts the relay loop. It joins the inbox and the approval action
// service: the two halves that already existed, wired so a chat message can
// answer a pending request.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		actions, err := ctx.Require(contracts.KeyApprovalAction)
		if err != nil {
			return fmt.Errorf("%s: require approval action: %w", ModuleID, err)
		}
		service := New(inboxService, actions, cfg)
		if _, err := ctx.Provide(contracts.KeyReplyRelay, contracts.ReplyRelayService(service)); err != nil {
			return fmt.Errorf("%s: provide relay: %w", ModuleID, err)
		}

		done := make(chan struct{})
		loopCtx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(done)
			runLoop(loopCtx, service, cfg)
		}()
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
				}
			})}
		}); err != nil {
			cancel()
			return fmt.Errorf("%s: own loop shutdown: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyReplyRelay)).
		AddRequires(module.Requires(contracts.KeyInbox), module.Requires(contracts.KeyApprovalAction)).
		WithName("ZeoKeel chat-reply approval relay")
}

func runLoop(ctx context.Context, service *Service, cfg Config) {
	if !cfg.enabled() {
		return
	}
	ticker := time.NewTicker(contracts.ReplyRelayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = service.Relay(ctx)
		}
	}
}
