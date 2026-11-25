package heartbeat

import (
	"context"
	"fmt"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/heartbeat"

// Module starts the autonomous loop: triage on one timer, every configured
// channel on another. Both are best-effort — a failure is recorded in the report
// and never stops the loop, because a stopped loop means the product is asleep
// while its owner thinks it is working.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		leadService, _ := ctx.Get(contracts.KeyLead)

		var channels []Channel
		if wechatService, ok := ctx.Get(contracts.KeyWeChat); ok && wechatService != nil {
			channels = append(channels, Channel{Name: "wechat", Poll: wechatService.PollOnce})
		}
		if emailService, ok := ctx.Get(contracts.KeyEmail); ok && emailService != nil {
			channels = append(channels, Channel{Name: "email", Poll: emailService.PollOnce})
		}

		service := New(cfg, leadService, channels, time.Now)
		if _, err := ctx.Provide(contracts.KeyHeartbeat, contracts.HeartbeatService(service)); err != nil {
			return fmt.Errorf("%s: provide heartbeat: %w", ModuleID, err)
		}

		// The loop runs for the life of the product; the kernel tears it down.
		done := make(chan struct{})
		loopCtx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(done)
			_ = service.Run(loopCtx)
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
		AddProvides(module.Provides(contracts.KeyHeartbeat)).
		WithName("ZeoKeel heartbeat loop")
}
