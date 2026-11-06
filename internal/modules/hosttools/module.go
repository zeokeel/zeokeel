// Package hosttools is ZeoKeel's host-tools kernel module.
//
// It puts the product-side agent tools into the shared ZeoKeel Rig tool
// registry (provided by the toolkit module as contracts.KeyToolkit): ask_user,
// present, plan_review, exit_plan_mode, goal and team. These tools are not part
// of the pure toolset SDK because they need process capabilities — a UI to ask
// the user, an artifact presenter, plan-review state, a goal store and a team
// manager (see internal/hosttools).
//
// Registration is explicit and reversible: every tool is registered during
// activation and its unregistration is owned by a kernel effect, so disposing
// the module (or a failed activation of a later tool) removes every tool it
// added, leaving the shared registry exactly as it was.
package hosttools

import (
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	hostcaps "github.com/zeokeel/zeokeel/internal/hosttools"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/hosttools"

// Config is the module's typed assembly config.
type Config struct {
	// Interactive says whether this product can ask the user questions (and
	// show plan reviews / artifacts) in real time. When false, ask_user reports
	// that no interactive UI is available instead of blocking a headless run.
	Interactive bool `json:"interactive"`
}

// Module returns the host-tools descriptor backed by a fresh in-memory
// capability set per activation. It is registered in every product; the
// assembly config decides whether the product is interactive.
func Module() *module.Descriptor[Config] {
	return ModuleWithHost(nil)
}

// ModuleWithHost is Module with injected host capabilities. A nil host means
// "fresh in-memory capabilities per activation" (the production default);
// tests and products with real UI/agent integration pass their own Asker,
// Presenter, PlanController, GoalStore and TeamManager. A nil capability in
// host disables only the tools that need it.
func ModuleWithHost(host *hostcaps.Host) *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		registry, err := ctx.Require(contracts.KeyToolkit)
		if err != nil {
			return err
		}
		capabilities := hostcaps.Host{}
		if host == nil {
			capabilities = *hostcaps.NewMemoryHost()
		} else {
			capabilities = *host
		}
		if !cfg.Interactive {
			capabilities.Asker = nil
		}

		for _, tool := range newTools(capabilities) {
			if err := registry.Register(tool); err != nil {
				return fmt.Errorf("%s: register %q: %w", ModuleID, tool.Name(), err)
			}
			name := tool.Name()
			if _, err := ctx.Effect(func() cordis.Effect {
				return cordis.Effect{cordis.CleanupOf(func() { registry.Unregister(name) })}
			}); err != nil {
				registry.Unregister(name)
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyToolkit)).
		WithName("ZeoKeel host tools")
}
