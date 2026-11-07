package agent

// Team orchestration backend: the agent service exposes contracts.TeamService
// over Rig's team.Manager, and Module registers the manager's model-facing tools
// into the shared toolkit with duplicate names skipped.

import (
	"context"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"
	rigteam "github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// teamMaxDepth keeps team members from starting further orchestrators.
	teamMaxDepth = 1
	// teamMaxConcurrent bounds a member's live subagents.
	teamMaxConcurrent = 4
)

// teamToolNames returns every model-facing orchestration tool name this module
// registers (native team_*, legacy aliases, DSH spawn/list/wait, shared task
// board). Both the team and synchronous-subagent runners exclude them.
func teamToolNames() []string {
	names := append([]string(nil), rigteam.ToolNames()...)
	names = append(names, rigteam.LegacyToolNames()...)
	names = append(names,
		rigteam.ToolSpawnTeammate,
		rigteam.ToolListAgents,
		rigteam.ToolWaitAgent,
		rigteam.ToolSubagent,
		rigteam.ToolSubagentFork,
		"team_task_create",
		"team_task_update",
	)
	return names
}

// teamExcludedTools is the orchestration-tool set a team member must never
// receive: the synchronous subagent names plus every team/legacy/DSH tool this
// module registers, so a member cannot recursively start teammates.
func teamExcludedTools() []string {
	names := append([]string(nil), subagentExcludedTools()...)
	seen := make(map[string]bool, len(names)+32)
	out := make([]string, 0, len(names)+32)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range names {
		add(name)
	}
	for _, name := range teamToolNames() {
		add(name)
	}
	return out
}

// dynamicTeamRunner builds a fresh Rig subagent runner for each spawn using the
// service's current provider/config. The configuration is deliberately resolved
// at spawn time so a provider injection, model switch or settings change applies
// to the next teammate.
type dynamicTeamRunner struct {
	service *service
}

// Start implements rig.SubagentRunner.
func (r *dynamicTeamRunner) Start(ctx context.Context, spec rig.SubagentSpec) (rig.Subagent, error) {
	cfg, active := r.service.subagentParent()
	provider, err := r.service.newProvider(cfg, r.service.sessionEffort(active))
	if err != nil {
		return nil, fmt.Errorf("agent: team member %q: create provider: %w", strings.TrimSpace(spec.Name), err)
	}
	parent := rig.AgentSpec{
		Provider:      provider,
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		MaxIterations: cfg.MaxIterations,
		Tools:         r.service.registry.Tools(),
		Gate:          r.service.gate(),
		History:       r.service.historyView(),
		Logger:        r.service.logger,
	}
	runner, err := rigagent.NewRunner(parent, rigagent.RunnerOptions{
		MaxDepth:      teamMaxDepth,
		MaxConcurrent: teamMaxConcurrent,
		ExcludedTools: teamExcludedTools(),
	})
	if err != nil {
		return nil, fmt.Errorf("agent: team member %q: create runner: %w", strings.TrimSpace(spec.Name), err)
	}
	return runner.Start(ctx, spec)
}

// teamManagerLocked returns the service's team manager, creating it on first
// use. The caller holds s.mu.
func (s *service) teamManagerLocked() *rigteam.Manager {
	if s.teamManager == nil {
		s.teamManager = rigteam.NewManager(&dynamicTeamRunner{service: s})
	}
	return s.teamManager
}

// teamManager returns the service's team manager.
func (s *service) teamManagerService() *rigteam.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.teamManagerLocked()
}

// teamService adapts the agent service to contracts.TeamService. The adapter
// exists because contracts.ChatService already owns Send(sessionID, prompt);
// TeamService has a different Send(ctx, id, message) signature.
type teamService struct {
	service *service
}

var _ contracts.TeamService = (*teamService)(nil)

// newTeamService returns the kernel-provided team service for svc.
func newTeamService(svc *service) *teamService { return &teamService{service: svc} }

// manager returns the underlying Rig team manager.
func (t *teamService) manager() *rigteam.Manager { return t.service.teamManagerService() }

// Members implements contracts.TeamService: the live teammates, oldest first.
func (t *teamService) Members() []contracts.TeamMember {
	manager := t.manager()
	if manager == nil {
		return nil
	}
	raw := manager.Members()
	members := make([]contracts.TeamMember, 0, len(raw))
	for _, member := range raw {
		converted := contracts.TeamMember{
			ID:      member.ID,
			Name:    member.Name,
			Status:  member.Status,
			Created: member.Created,
		}
		if member.Last != nil {
			converted.Last = member.Last.Output
		}
		members = append(members, converted)
	}
	return members
}

// Spawn implements contracts.TeamService: start a named teammate. Its initial
// run proceeds asynchronously; Wait/Send read and steer it.
func (t *teamService) Spawn(ctx context.Context, name, prompt, model string) (contracts.TeamMember, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return contracts.TeamMember{}, fmt.Errorf("agent: team member prompt is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	member, err := t.manager().Spawn(ctx, rig.SubagentSpec{
		Name:   strings.TrimSpace(name),
		Prompt: prompt,
		Model:  strings.TrimSpace(model),
	})
	if err != nil {
		return contracts.TeamMember{}, err
	}
	converted := contracts.TeamMember{
		ID:      member.ID,
		Name:    member.Name,
		Status:  member.Status,
		Created: member.Created,
	}
	if member.Last != nil {
		converted.Last = member.Last.Output
	}
	return converted, nil
}

// SendAsync implements contracts.TeamService.
func (t *teamService) SendAsync(id, message string) error {
	return t.manager().SendAsync(id, message)
}

// Send implements contracts.TeamService.
func (t *teamService) Send(ctx context.Context, id, message string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := t.manager().Send(ctx, id, message)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

// Wait implements contracts.TeamService.
func (t *teamService) Wait(ctx context.Context, id string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := t.manager().Wait(ctx, id)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

// Interrupt implements contracts.TeamService.
func (t *teamService) Interrupt(id string) error {
	return t.manager().Interrupt(id)
}

// Close implements contracts.TeamService.
func (t *teamService) Close(id string) error {
	return t.manager().Close(id)
}

// newTeamTools builds every model-facing team tool group for the manager:
// native team_* tools, legacy aliases and the DSH-compatible spawn/list/wait
// set. The caller filters duplicate names before registering them.
func newTeamTools(manager *rigteam.Manager) []rig.Tool {
	if manager == nil {
		return nil
	}
	tools := manager.Tools()
	tools = append(tools, manager.LegacyTools(nil)...)
	tools = append(tools, rigteam.DSHTools(manager)...)
	return tools
}
