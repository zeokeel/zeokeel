package skilldraft

import (
	"errors"
	"fmt"
	"path/filepath"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/skilldraft"

// Config is the (empty) typed assembly config. The store always lives in
// <dataDir>/skilldraft: drafts are per-user data, and a configurable directory
// would only invite them into a place the product does not back up.
type Config struct{}

// Module is the skill draft descriptor (plan G12): it requires the product data
// directory (a draft outlives the task that proposed it) and provides the
// propose/list/decide half of "从任务沉淀技能".
//
// It deliberately does NOT provide the skills editor: accepting a draft records a
// human decision and keeps the Markdown, and writing <dataDir>/skills/<slug>/
// SKILL.md stays the skills module's job, so a draft can never install itself.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, _ Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		service, err := Open(filepath.Join(dataDir, "skilldraft"))
		if err != nil {
			if !errors.Is(err, contracts.ErrSkillDraftStoreCorrupt) {
				return fmt.Errorf("%s: open draft store: %w", ModuleID, err)
			}
			// A corrupt draft file must not brick the product: proposals are
			// regenerable, and the file is left in place so an operator can look
			// at it. The first new draft replaces it.
			if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
				logger.Printf("skilldraft: corrupt draft store ignored (%v); starting with no drafts", err)
			}
		}
		if _, err := ctx.Provide(contracts.KeySkillDrafts, contracts.SkillDraftsService(service)); err != nil {
			return fmt.Errorf("%s: provide skill drafts: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeySkillDrafts)).
		AddRequires(module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel skill drafts (从任务沉淀技能)")
}
