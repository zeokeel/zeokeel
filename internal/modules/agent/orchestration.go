package agent

// Orchestration tools (DSH parity): the rig `workflow` and `ralph` tools wired
// to the product's dynamic subagent runner, plus the shared delegation budget.
//
// Why they are registered here and not by the toolkit module: both tools only
// need a rig.SubagentRunner, and the product's runner has to be rebuilt per
// spawn from live state (provider injection, model switch, settings, workspace,
// approval gate) — exactly the pattern the synchronous `subagent` tool and the
// team manager already use. A single dynamic runner serves workflow steps and
// ralph rounds, with the orchestration tools excluded from the child surface so
// neither can recurse.
//
// ralph is experimental/opt-in in DSH. The product has no per-tool feature
// switch to invent one for, so it is registered with the same mechanism as
// workflow and constrained by the mechanisms that already exist: the system
// prompt guidance tells the model to use it only when the user explicitly asks
// for Ralph, `tools.disabled` removes it for good, agent presets can leave it
// out of their allowlist, and the runtime modes (plan/minimal/creator) deny it
// because it is not on their whitelists. workflow, by contrast, is a normal
// capability and only constrained by the explicit-request guidance.

import (
	"context"
	"fmt"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	rigralph "github.com/zeokeel/zeokeel-rig/tools/ralph"
	rigworkflow "github.com/zeokeel/zeokeel-rig/tools/workflow"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// workflowToolName is the model-visible workflow tool name.
	workflowToolName = rigworkflow.ToolName
	// ralphToolName is the model-visible ralph tool name.
	ralphToolName = rigralph.ToolName
	// orchestrationMaxDepth keeps a workflow step / ralph round (and every other
	// delegated child) from starting grandchildren.
	orchestrationMaxDepth = 1
	// orchestrationMaxConcurrent honours workflow's maximum parallel group size
	// (rigworkflow.MaxMaxParallel) so a full parallel group can run at once.
	orchestrationMaxConcurrent = rigworkflow.MaxMaxParallel
)

// delegatedIterationBudget is the explicit model/tool round budget every
// delegated child (sync subagent, team member, workflow step, ralph round)
// receives. Rig v0.19.0 defaults to unlimited rounds (0), so the product's
// "0 means unlimited" main-run policy passes through unchanged and a child is
// not silently limited. A configured limit applies to children too.
func delegatedIterationBudget(cfg contracts.ChatConfig) int {
	return effectiveMaxIterations(cfg.MaxIterations)
}

// delegatedParentSpec builds the shared rig parent spec for one delegated child.
// Every child runner starts from the same model-visible surface (disabled tools,
// preset allowlist, runtime mode already resolved by toolsForSession), the same
// provider/model connection, the same workspace-scoped tools, approval gate,
// history and context budget. workspaceRoot is the parent session's project.
func (s *service) delegatedParentSpec(sessionID, workspaceRoot string, childTools []rig.Tool,
	cfg contracts.ChatConfig, provider rig.Provider, origin string) rig.AgentSpec {
	spec := rig.AgentSpec{
		Provider:      provider,
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		MaxIterations: delegatedIterationBudget(cfg),
		Tools:         withWorkspaceRoot(withAgentScope(childTools, sessionID, origin), workspaceRoot),
		Gate:          s.gate(sessionID),
		History:       s.historyView(),
		Logger:        s.logger,
	}
	s.withContextBudget(&spec, cfg, provider)
	return spec
}

// dynamicOrchestrationRunner builds a fresh Rig subagent runner for one workflow
// step or ralph round, resolving the configuration at spawn time (provider,
// model, settings, workspace). It deliberately does not track its children in
// the team inspector: orchestration children are anonymous one-shot runs, not
// teammates.
type dynamicOrchestrationRunner struct {
	service *service
}

var _ rig.SubagentRunner = (*dynamicOrchestrationRunner)(nil)

// Start implements rig.SubagentRunner.
func (r *dynamicOrchestrationRunner) Start(ctx context.Context, spec rig.SubagentSpec) (rig.Subagent, error) {
	cfg, active := r.service.subagentParent(contracts.AgentSessionFromContext(ctx))
	cfg = r.service.configForModelOverride(cfg, spec.Model)
	// A workflow step / ralph round starts from the same model-visible surface as
	// the parent run, minus every orchestration tool (it must never recurse).
	childTools := withoutToolNames(r.service.toolsForSession(active), subagentExcludedTools())
	workspaceRoot := r.service.sessionWorkspaceRoot(active)
	cfg.SystemPrompt = r.service.buildRunSystemPrompt(ctx, active, cfg, toolNameSet(childTools), workspaceRoot)
	provider, err := r.service.newProvider(cfg, r.service.thinkingParamsForSession(active, cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("agent: orchestration child %q: create provider: %w", strings.TrimSpace(spec.Name), err)
	}
	parent := r.service.delegatedParentSpec(active, workspaceRoot, childTools, cfg, provider, contracts.AgentRunOriginSubagent)
	runner, err := rigagent.NewRunner(parent, rigagent.RunnerOptions{
		MaxDepth:      orchestrationMaxDepth,
		MaxConcurrent: orchestrationMaxConcurrent,
		ExcludedTools: subagentExcludedTools(),
	})
	if err != nil {
		return nil, fmt.Errorf("agent: orchestration child %q: create runner: %w", strings.TrimSpace(spec.Name), err)
	}
	return runner.Start(ctx, spec)
}

// newOrchestrationTools builds the workflow/ralph tools over one shared dynamic
// runner.
func newOrchestrationTools(svc *service) []rig.Tool {
	runner := &dynamicOrchestrationRunner{service: svc}
	return []rig.Tool{
		rigworkflow.New(runner),
		rigralph.New(runner),
	}
}

// registerOrchestrationTools registers the model-facing workflow/ralph tools
// into the shared toolkit registry and owns their unregistration through kernel
// effects (a failed later registration rolls their names back as well).
func registerOrchestrationTools(ctx cordis.Context, registry *rigtools.Registry, svc *service) error {
	// A host (or a test harness) may already provide a tool with one of these
	// names (the toolkit module and embedders share one registry): keep that
	// registration instead of failing activation, exactly like the team tools.
	known := make(map[string]bool, len(registry.Specs()))
	for _, spec := range registry.Specs() {
		known[spec.Name] = true
	}
	for _, tool := range newOrchestrationTools(svc) {
		name := strings.TrimSpace(tool.Name())
		if name == "" {
			continue
		}
		if known[name] {
			svc.logger.Printf("[agent] skip orchestration tool %s: duplicate registry name", name)
			continue
		}
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("agent: register %q: %w", name, err)
		}
		known[name] = true
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() { registry.Unregister(name) })}
		}); err != nil {
			registry.Unregister(name)
			return err
		}
	}
	return nil
}
