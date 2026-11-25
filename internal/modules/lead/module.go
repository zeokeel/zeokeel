package lead

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/lead"

// Module is the triage-pass descriptor: it joins the inbox and the triage
// service and provides KeyLead. Rules come from configuration; a model-backed
// planner can replace RulePlanner without touching the loop.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		inboxService, err := ctx.Require(contracts.KeyInbox)
		if err != nil {
			return fmt.Errorf("%s: require inbox: %w", ModuleID, err)
		}
		triageService, err := ctx.Require(contracts.KeyTriage)
		if err != nil {
			return fmt.Errorf("%s: require triage: %w", ModuleID, err)
		}
		service := NewService(inboxService, triageService, RulePlanner{}, cfg)
		if _, err := ctx.Provide(contracts.KeyLead, contracts.LeadService(service)); err != nil {
			return fmt.Errorf("%s: provide lead: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyLead)).
		AddRequires(module.Requires(contracts.KeyInbox), module.Requires(contracts.KeyTriage)).
		WithName("ZeoKeel lead triage pass")
}
