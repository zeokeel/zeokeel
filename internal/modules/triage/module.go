package triage

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/triage"

// Config is the (empty) typed assembly config.
type Config struct{}

// Module is the triage descriptor: it requires the inbox and the task board and
// provides the only service that may turn an inbox item into work.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, _ Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		board, err := ctx.Require(contracts.KeyTaskBoard)
		if err != nil {
			return fmt.Errorf("%s: require task board: %w", ModuleID, err)
		}
		service := NewService(inboxService, board)
		if _, err := ctx.Provide(contracts.KeyTriage, contracts.TriageService(service)); err != nil {
			return fmt.Errorf("%s: provide triage: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyTriage)).
		AddRequires(module.Requires(contracts.KeyInbox), module.Requires(contracts.KeyTaskBoard)).
		WithName("ZeoKeel triage (inbox → task board)")
}
