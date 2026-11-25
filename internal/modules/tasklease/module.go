package tasklease

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/tasklease"

// Module is the task lease descriptor: it requires the task board and provides
// the claim/heartbeat/evidence/review layer over it.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		board, err := ctx.Require(contracts.KeyTaskBoard)
		if err != nil {
			return fmt.Errorf("%s: require task board: %w", ModuleID, err)
		}
		service := NewService(board, cfg)
		if _, err := ctx.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(service)); err != nil {
			return fmt.Errorf("%s: provide task lease: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyTaskLease)).
		AddRequires(module.Requires(contracts.KeyTaskBoard)).
		WithName("ZeoKeel task lease (claim, evidence, review)")
}
