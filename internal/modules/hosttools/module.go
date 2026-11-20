// Package hosttools is ZeoKeel's host-tools kernel module.
//
// It puts the product-side agent tools into the shared ZeoKeel Rig tool
// registry (provided by the toolkit module as hostcaps.KeyRegistry): ask_user,
// present, plan_review, exit_plan_mode and the DSH goal trio get_goal/
// create_goal/update_goal. ask_user, plan_review, exit_plan_mode and the goal
// trio are the official Rig host tools (github.com/zeokeel/zeokeel-rig/tools/
// hosttools) — the goal trio takes the product's ctx-carrying GoalRevisionStore
// adapter. present stays product-owned: ZeoKeel's present is a content artifact
// rather than a file declaration. The legacy Goal v1 `goal` tool is no longer
// registered: goals are per-session and durable behind contracts.GoalService. The legacy in-memory
// "team" stub was removed: the agent module registers the real rig team tools
// (spawn_teammate, list_agents, team_task_*, ...) against contracts.KeyTeam, so
// keeping the stub only misled the model into spawning members that never ran.
//
// Interactive products (the desktop app) register every tool. Non-interactive
// products (headless/ACP) cannot show a question or get a plan approved by a
// user, so they omit the plan-review pair entirely — a plan submitted there
// could never leave "reviewing" — and ask_user reports that no interactive UI
// is available instead of blocking the run.
//
// Registration is explicit and reversible: every tool is registered during
// activation and its unregistration is owned by a kernel effect, so disposing
// the module (or a failed activation of a later tool) removes every tool it
// added, leaving the shared registry exactly as it was.
package hosttools

import (
	"context"
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
	// Interactive says whether this product has a live user who can answer
	// questions, review plans and view artifacts in real time. When false,
	// ask_user reports that no interactive UI is available instead of blocking
	// a headless run, and the plan-review tools are not registered at all
	// (nobody could approve the plan, so a submission would stall the run).
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
// Presenter, PlanController and GoalStore. A nil capability in host disables
// only the tools that need it.
func ModuleWithHost(host *hostcaps.Host) *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		registry, err := ctx.Require(hostcaps.KeyRegistry)
		if err != nil {
			return err
		}
		capabilities := hostcaps.Host{}
		if host == nil {
			capabilities = *hostcaps.NewMemoryHost()
			// The desktop host provides a UserInteraction bridge; route the
			// product ask_user tool to it so the question renders in the UI and
			// the tool parks until the user answers. Headless products have no
			// bridge and keep the memory asker's no-interactive behavior.
			if interaction, ok := ctx.Get(contracts.KeyUserInteraction); ok {
				capabilities.Asker = hostcaps.AskerFunc(func(ctx context.Context, question string, options []string) (string, error) {
					return interaction.Ask(ctx, question, options)
				})
			}
			if presenter, ok := ctx.Get(contracts.KeyArtifactPresenter); ok {
				capabilities.Presenter = hostcaps.PresenterFunc(func(ctx context.Context, title, body string) (string, error) {
					return presenter.Present(ctx, title, body)
				})
			}
			if reviewer, ok := ctx.Get(contracts.KeyPlanReviewer); ok {
				capabilities.Plan = uiPlanController{reviewer: reviewer}
			}
		} else {
			capabilities = *host
		}
		if !cfg.Interactive {
			capabilities.Asker = nil
		}

		// The DSH goal trio talks to the per-session durable goal backend. The
		// service is resolved lazily at tool-call time: an assembly may load
		// this module before the goal backend, and a product without one keeps
		// the tools registered with a readable "no goal backend" tool error
		// instead of failing activation.
		goalStore := hostcaps.NewLazyGoalRevisionStore(func() contracts.GoalService {
			service, ok := ctx.Get(contracts.KeyGoal)
			if !ok || service == nil {
				return nil
			}
			return service
		})

		for _, tool := range newTools(capabilities, cfg.Interactive, goalStore) {
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
		AddRequires(module.Requires(hostcaps.KeyRegistry)).
		WithName("ZeoKeel host tools")
}

// uiPlanController adapts the host's contracts.PlanReviewer to the host-tools
// PlanController seam. Review has no context in that seam (the Rig adapter
// checks the caller's context before invoking it), so the adapter uses
// Background.
type uiPlanController struct {
	reviewer contracts.PlanReviewer
}

// State implements hostcaps.PlanController.
func (p uiPlanController) State() string { return p.reviewer.State() }

// Review implements hostcaps.PlanController.
func (p uiPlanController) Review(content string) (string, error) {
	return p.reviewer.Review(context.Background(), content)
}

// Exit implements hostcaps.PlanController.
func (p uiPlanController) Exit(action string) (string, error) {
	return p.reviewer.Exit(action)
}
