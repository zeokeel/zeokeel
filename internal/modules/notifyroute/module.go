package notifyroute

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/notifyroute"

// Config is the (empty) typed assembly config.
type Config struct{}

// Module is the notification router descriptor: it provides KeyNotifyRoute and
// requires nothing (the channel adapters consume it, not the other way round).
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, _ Config) error {
		service := NewService(nil)
		if _, err := ctx.Provide(contracts.KeyNotifyRoute, contracts.NotifyRouterService(service)); err != nil {
			return fmt.Errorf("%s: provide notify route: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyNotifyRoute)).
		WithName("ZeoKeel notification routing")
}
