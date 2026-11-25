package lead

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
const ModuleID = "zeokeel/lead"

// Module is the triage-pass descriptor: it joins the inbox and the triage
// service, and requires the product data directory because the operator's rules
// must survive a restart (a pass with no rules silently skips everything). Rules
// come from rules.json; a model-backed planner can replace RulePlanner without
// touching the loop.
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
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		service, err := Open(filepath.Join(dataDir, "lead"), inboxService, triageService, cfg)
		if err != nil {
			if !errors.Is(err, ErrLeadStoreCorrupt) {
				return fmt.Errorf("%s: open rule store: %w", ModuleID, err)
			}
			// A corrupt rule set must not brick the product and must not be
			// overwritten silently: move it aside and keep serving with no rules
			// (an empty set skips every item, it never dispatches on a guess).
			quarantined := fmt.Sprintf("%s.corrupt-%d", ruleStoreName, time.Now().Unix())
			_ = os.Rename(
				filepath.Join(dataDir, "lead", ruleStoreName),
				filepath.Join(dataDir, "lead", quarantined),
			)
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("lead: corrupt rule store moved to %s; starting with no rules", quarantined)
			}
		}
		if _, err := ctx.Provide(contracts.KeyLead, contracts.LeadService(service)); err != nil {
			return fmt.Errorf("%s: provide lead: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyLead)).
		AddRequires(
			module.Requires(contracts.KeyInbox),
			module.Requires(contracts.KeyTriage),
			module.Requires(contracts.KeyDataDir),
		).
		WithName("ZeoKeel lead triage pass")
}
