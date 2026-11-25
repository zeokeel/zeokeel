package teamnotes

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/teamnotes"

// Module is the team note board descriptor: it provides KeyTeamNotes and
// requires nothing hard. The memory store is an optional dependency because a
// product without memory must still boot — it just reports that notes are
// unavailable instead of dropping them silently.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		var memory contracts.MemoryService
		if value, ok := ctx.Get(contracts.KeyMemoryService); ok && value != nil {
			memory = value
		}
		service := New(memory, cfg)
		if _, err := ctx.Provide(contracts.KeyTeamNotes, contracts.TeamNotesService(service)); err != nil {
			return fmt.Errorf("%s: provide team notes: %w", ModuleID, err)
		}
		if memory == nil {
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("teamnotes: no memory service in this product; shared notes are unavailable")
			}
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyTeamNotes)).
		AddOptionalRequires(module.Requires(contracts.KeyMemoryService)).
		WithName("ZeoKeel team notes")
}
