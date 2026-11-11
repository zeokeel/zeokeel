package agent

// Team orchestration backend: the agent service exposes contracts.TeamService
// over Rig's team.Manager, and Module registers the manager's model-facing tools
// into the shared toolkit with duplicate names skipped.
//
// The rig manager only reports member availability, so this package wraps every
// spawned rig.Subagent (team_state.go) to track the real run lifecycle, keeps a
// bounded activity read model (team_activity.go) and asks the chat subscribers
// for a frame after a team change. The inspector then shows truthful
// running/idle/done/failed badges instead of drawing "active" as "running".

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
// to the next teammate. Every started subagent is wrapped by the team tracker so
// its run lifecycle and completion are visible to the product.
type dynamicTeamRunner struct {
	service *service
	tracker *teamTracker
}

// Start implements rig.SubagentRunner.
func (r *dynamicTeamRunner) Start(ctx context.Context, spec rig.SubagentSpec) (rig.Subagent, error) {
	cfg, active := r.service.subagentParent(contracts.AgentSessionFromContext(ctx))
	// A per-member model override runs with that model's own provider connection
	// (its catalog row's BaseURL/APIKey), falling back to the parent session's
	// connection when the model list has no such row.
	cfg = r.service.configForModelOverride(cfg, spec.Model)
	// Members start from the same model-visible surface as their parent run
	// (disabled tools, preset allowlist and runtime mode whitelist all apply);
	// the orchestration tools are then removed so a member cannot recurse.
	childTools := withoutToolNames(r.service.toolsForSession(active), teamExcludedTools())
	// Team members get the same complete prompt as a normal run, with guidance
	// and mode line rendered for exactly the surface they receive. Their tools
	// are scoped to the parent session's workspace root (same as subagents).
	workspaceRoot := r.service.sessionWorkspaceRoot(active)
	cfg.SystemPrompt = r.service.buildRunSystemPrompt(ctx, active, cfg, toolNameSet(childTools), workspaceRoot)
	provider, err := r.service.newProvider(cfg, r.service.thinkingParamsForSession(active, cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("agent: team member %q: create provider: %w", strings.TrimSpace(spec.Name), err)
	}
	parent := rig.AgentSpec{
		Provider:      provider,
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		MaxIterations: cfg.MaxIterations,
		Tools:         withWorkspaceRoot(withAgentSession(childTools, active), workspaceRoot),
		Gate:          r.service.gate(active),
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
	subagent, err := runner.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if r.tracker == nil {
		return subagent, nil
	}
	return r.tracker.track(subagent, spec, cfg.Model, active), nil
}

// teamManagerLocked returns the service's team manager, creating it on first
// use. The caller holds s.mu. The tracker is registered for both the service
// and the manager: the module factory builds the manager and the adapter in two
// separate calls, so neither can carry the other's pointer.
func (s *service) teamManagerLocked() *rigteam.Manager {
	if s.teamManager == nil {
		tracker := teamTrackerForService(s)
		s.teamManager = rigteam.NewManager(&dynamicTeamRunner{service: s, tracker: tracker})
		teamTrackers.Store(s.teamManager, tracker)
	}
	return s.teamManager
}

// teamManager returns the service's team manager.
func (s *service) teamManagerService() *rigteam.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.teamManagerLocked()
}

// notifyTeamChange asks the chat subscribers of every session for a frame after
// a team change: the team is process-wide, so every open inspector may need to
// repaint even when another session is active. It must not be called while
// holding s.mu (the subscribers may hop to the UI thread).
func (s *service) notifyTeamChange() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	if len(ids) == 0 {
		s.notify("")
		return
	}
	for _, id := range ids {
		s.notify(id)
	}
}

// teamTrackerForService returns the run/activity tracker shared by the service,
// its runner and its manager, creating and registering it on first use.
func teamTrackerForService(s *service) *teamTracker {
	return teamTrackerFor(s, func() *teamTracker {
		return newTeamTracker(s.notifyTeamChange, s.childSessions)
	})
}

// teamService adapts the agent service to contracts.TeamService. The adapter
// exists because contracts.ChatService already owns Send(sessionID, prompt);
// TeamService has a different Send(ctx, id, message) signature.
type teamService struct {
	service *service
	tracker *teamTracker
}

var (
	_ contracts.TeamService        = (*teamService)(nil)
	_ contracts.TeamActivityLister = (*teamService)(nil)
)

// newTeamService returns the kernel-provided team service for svc.
func newTeamService(svc *service) *teamService {
	return &teamService{service: svc, tracker: teamTrackerForService(svc)}
}

// manager returns the underlying Rig team manager.
func (t *teamService) manager() *rigteam.Manager { return t.service.teamManagerService() }

// teamTracker returns the service tracker, creating it when the adapter was
// built before the manager (tests may construct the adapter directly).
func (t *teamService) teamTracker() *teamTracker {
	if t.tracker == nil {
		t.tracker = teamTrackerForService(t.service)
	}
	return t.tracker
}

// Members implements contracts.TeamService: the live teammates first, enriched
// with the truthful run state, model, timing, usage and the bounded recent
// activity of each member. Every live row carries its durable child-session ID
// (the same identity the child-agent view and the restored history rows use), so
// the two team/subagent surfaces address one child, not two records.
func (t *teamService) Members() []contracts.TeamMember {
	manager := t.manager()
	if manager == nil {
		return nil
	}
	raw := manager.Members()
	snapshots := t.teamTracker().reconcile(raw)
	members := make([]contracts.TeamMember, 0, len(raw))
	for _, member := range raw {
		converted := teamMemberFromRig(member)
		if snapshot, ok := snapshots[member.ID]; ok {
			applyRunSnapshot(&converted, snapshot)
		}
		childID := t.childIDForMember(member)
		converted.ID = childID
		// The activity read model carries the same unified identity: a row that
		// was attributed to the manager reference is re-attributed to the child
		// session, so the team overlay and the child timeline share one ID.
		for index := range converted.Activity {
			if converted.Activity[index].MemberID == member.ID {
				converted.Activity[index].MemberID = childID
			}
		}
		members = append(members, converted)
	}
	// Members() is the UI's per-frame read path, so it must never write the
	// durable store (no I/O in build): the tracker persists run snapshots from
	// the spawn/send/watch goroutines, Close persists the closed state, and the
	// module disposal persists every live member.
	return t.appendHistoricalMembers(members)
}

// childIDForMember resolves the durable child-session identity of one live
// manager member. The tracker binds it at spawn; a member the tracker never saw
// (a manager built directly in focused unit tests) keeps the manager reference.
func (t *teamService) childIDForMember(member rigteam.Member) string {
	if childID := t.teamTracker().childIDForMember(member.ID); childID != "" {
		return childID
	}
	if store := t.service.childSessions; store != nil {
		if record, ok := store.byBackendRef(member.ID, "", contracts.ChildAgentTeammate); ok {
			return record.ID
		}
	}
	return member.ID
}

// appendHistoricalMembers appends the restored teammate records that no live
// member supersedes, so a reopened app still lists the team that ran.
func (t *teamService) appendHistoricalMembers(live []contracts.TeamMember) []contracts.TeamMember {
	if t.service.childSessions == nil {
		return live
	}
	records := t.service.childSessions.records()
	if len(records) == 0 {
		return live
	}
	out := append([]contracts.TeamMember(nil), live...)
	for _, record := range records {
		if record.Kind != contracts.ChildAgentTeammate {
			continue
		}
		if t.liveMemberSupersedes(record, live) {
			continue
		}
		out = append(out, teamMemberFromChildSession(record))
	}
	return out
}

// liveMemberSupersedes reports whether a live member already covers record: the
// same child-session ID, or the same manager reference inside one parent session
// (a restart reuses manager references, and a claim may be bound after the row
// was already built, so both identities are checked).
func (t *teamService) liveMemberSupersedes(record childSessionRecord, live []contracts.TeamMember) bool {
	if record.ID == "" {
		return false
	}
	sessionMatches := func(member contracts.TeamMember) bool {
		return record.ParentSessionID == "" || member.SessionID == "" || member.SessionID == record.ParentSessionID
	}
	for _, member := range live {
		if !sessionMatches(member) {
			continue
		}
		if member.ID == record.ID || (record.BackendRef != "" && member.ID == record.BackendRef) {
			return true
		}
	}
	tracker := t.teamTracker()
	for _, member := range t.manager().Members() {
		if record.BackendRef != "" && member.ID != record.BackendRef {
			continue
		}
		if childID := tracker.childIDForMember(member.ID); childID != "" && childID == record.ID {
			return true
		}
	}
	return false
}

// memberSnapshot returns one live member converted to the contract, with the
// child-session ID bound to it (for the close/shutdown archiving path).
func (t *teamService) memberSnapshot(memberID string) (contracts.TeamMember, string, bool) {
	if t == nil || memberID == "" {
		return contracts.TeamMember{}, "", false
	}
	raw := t.manager().Members()
	snapshots := t.teamTracker().reconcile(raw)
	for _, member := range raw {
		if member.ID != memberID {
			continue
		}
		converted := teamMemberFromRig(member)
		if snapshot, ok := snapshots[member.ID]; ok {
			applyRunSnapshot(&converted, snapshot)
		}
		return converted, t.childIDForMember(member), true
	}
	return contracts.TeamMember{}, "", false
}

// archiveTeamMembersOnShutdown persists every live member before the module
// disposes the manager: a member still running when the app quits is recorded as
// interrupted instead of vanishing.
func (s *service) archiveTeamMembersOnShutdown() {
	if s == nil || s.childSessions == nil {
		return
	}
	manager := s.teamManagerService()
	raw := manager.Members()
	if len(raw) == 0 {
		return
	}
	tracker := teamTrackerForService(s)
	snapshots := tracker.reconcile(raw)
	for _, member := range raw {
		converted := teamMemberFromRig(member)
		if snapshot, ok := snapshots[member.ID]; ok {
			applyRunSnapshot(&converted, snapshot)
		}
		switch converted.RunState {
		case contracts.TeamRunDone, contracts.TeamRunFailed:
		default:
			// No completed run, or a run still in flight when the app quit: the
			// child did not survive, so the store records an interruption.
			converted.RunState = contracts.TeamRunInterrupted
		}
		childID := tracker.childIDForMember(member.ID)
		if childID == "" {
			childID = s.childSessions.newID()
		}
		s.childSessions.upsert(childSessionFromTeamMember(
			converted, converted.SessionID, tracker.memberPrompt(member.ID), childID, member.ID))
	}
}

// resolveChildRef resolves a durable child-session ID to its live manager
// reference ("" when no live member owns it). Manager references and names pass
// through unchanged so legacy callers keep working.
func (t *teamService) resolveChildRef(id string) (memberID, name string) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", ""
	}
	if memberID, name := t.resolveMemberRef(trimmed); memberID != "" {
		return memberID, name
	}
	resolved := t.teamTracker().memberForChild(trimmed)
	if resolved == "" {
		return "", ""
	}
	for _, member := range t.manager().Members() {
		if member.ID == resolved {
			return member.ID, member.Name
		}
	}
	return resolved, ""
}

// teamMemberFromRig converts one manager member snapshot to the contract.
func teamMemberFromRig(member rigteam.Member) contracts.TeamMember {
	converted := contracts.TeamMember{
		ID:      member.ID,
		Name:    member.Name,
		Status:  member.Status,
		Created: member.Created,
	}
	if member.Last != nil {
		converted.Last = member.Last.Output
	}
	return converted
}

// applyRunSnapshot fills the run-state half of one contract member.
func applyRunSnapshot(member *contracts.TeamMember, snapshot teamRunSnapshot) {
	if member == nil {
		return
	}
	member.RunState = snapshot.RunState
	member.Error = snapshot.Error
	member.Model = snapshot.Model
	member.Started = snapshot.Started
	member.Finished = snapshot.Finished
	member.UsageTokens = snapshot.UsageTokens
	member.Iterations = snapshot.Iterations
	member.SessionID = snapshot.SessionID
	if len(snapshot.Activity) > 0 {
		member.Activity = append([]contracts.TeamActivity(nil), snapshot.Activity...)
	}
	if strings.TrimSpace(member.Last) == "" && strings.TrimSpace(snapshot.LastOutput) != "" {
		// The manager records Last only on Wait/Send; the tracker observed the
		// initial background run, so its answer fills the hole.
		member.Last = snapshot.LastOutput
	}
	if member.RunState == contracts.TeamRunRunning && strings.TrimSpace(member.Error) == "" {
		member.Last = ""
	}
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
	converted := teamMemberFromRig(member)
	if snapshot, ok := t.teamTracker().reconcile(t.manager().Members())[member.ID]; ok {
		applyRunSnapshot(&converted, snapshot)
	}
	converted.ID = t.childIDForMember(member)
	t.teamTracker().notifyChange()
	return converted, nil
}

// SendAsync implements contracts.TeamService.
func (t *teamService) SendAsync(id, message string) error {
	ref := t.managerRef(id)
	err := t.manager().SendAsync(ref, message)
	if err != nil {
		t.teamTracker().addActivity(nil, "", contracts.TeamActivityError,
			"发送消息失败："+err.Error(), true)
	}
	t.teamTracker().notifyChange()
	return err
}

// Send implements contracts.TeamService.
func (t *teamService) Send(ctx context.Context, id, message string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := t.manager().Send(ctx, t.managerRef(id), message)
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
	memberID, name := t.resolveChildRef(id)
	if run := t.teamTracker().runForRef(memberID); run != nil || name != "" {
		t.teamTracker().addActivity(run, memberID, contracts.TeamActivityWait,
			fmt.Sprintf("等待「%s」的当前运行", teamDisplayName(name, id)), false)
	}
	ref := t.managerRef(id)
	result, err := t.manager().Wait(ctx, ref)
	if err != nil {
		return "", err
	}
	// After a follow-up SendAsync the manager's Wait can still report the first
	// run's result; prefer the latest completed answer when one is recorded.
	if latest, ok, lastErr := t.manager().Last(ref); lastErr == nil && ok && latest.Output != "" {
		return latest.Output, nil
	}
	return result.Output, nil
}

// Interrupt implements contracts.TeamService.
func (t *teamService) Interrupt(id string) error {
	err := t.manager().Interrupt(t.managerRef(id))
	if err != nil {
		t.teamTracker().addActivity(nil, "", contracts.TeamActivityError,
			"中断失败："+err.Error(), true)
	}
	t.teamTracker().reconcile(t.manager().Members())
	t.teamTracker().notifyChange()
	return err
}

// Close implements contracts.TeamService.
func (t *teamService) Close(id string) error {
	memberID, name := t.resolveChildRef(id)
	ref := t.managerRef(id)
	run := t.teamTracker().runForRef(memberID)
	// Capture the member before the manager drops it: a closed teammate stays in
	// the durable child-session store (same identity), read-only and visible in
	// both the team and child-agent surfaces.
	target, childID, hasTarget := t.memberSnapshot(memberID)
	if err := t.manager().Close(ref); err != nil {
		t.teamTracker().addActivity(run, memberID, contracts.TeamActivityError,
			"关闭失败："+err.Error(), true)
		t.teamTracker().notifyChange()
		return err
	}
	t.teamTracker().addActivity(run, memberID, contracts.TeamActivityClose,
		fmt.Sprintf("关闭「%s」", teamDisplayName(name, id)), false)
	if memberID != "" {
		t.teamTracker().release(memberID)
	}
	t.teamTracker().reconcile(t.manager().Members())
	if run != nil && t.teamTracker() != nil && t.service.childSessions != nil {
		// Closing a running teammate marked the tracked run interrupted (with a
		// finish time) inside manager.Close; persist that state instead of the
		// stale pre-close snapshot so the interrupted receipt can fire.
		t.teamTracker().persistRun(run)
	} else if hasTarget && t.service.childSessions != nil {
		if childID == "" {
			childID = t.service.childSessions.newID()
		}
		t.service.childSessions.upsert(childSessionFromTeamMember(
			target, target.SessionID, t.teamTracker().memberPrompt(memberID), childID, memberID))
	}
	t.teamTracker().notifyChange()
	return nil
}

// managerRef resolves a durable child-session ID to the manager reference used
// by the rig team manager; manager references and names pass through unchanged.
func (t *teamService) managerRef(id string) string {
	if memberID, _ := t.resolveChildRef(id); memberID != "" {
		return memberID
	}
	return id
}

// resolveMemberRef resolves a member reference (id, target or unique name) to
// the manager's member ID and display name; both "" when unknown.
func (t *teamService) resolveMemberRef(ref string) (id, name string) {
	manager := t.manager()
	if manager == nil {
		return "", ""
	}
	ref = strings.TrimSpace(ref)
	for _, member := range manager.Members() {
		if member.ID == ref || (ref != "" && member.Name == ref) {
			return member.ID, member.Name
		}
	}
	return "", ""
}

// teamDisplayName returns the first non-empty trimmed name.
func teamDisplayName(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// newTeamTools builds every model-facing team tool group for the manager:
// native team_* tools, legacy aliases and the DSH-compatible spawn/list/wait
// set. Each tool is wrapped so its invocation lands in the bounded team
// activity read model (the run lifecycle itself is tracked in team_state.go).
func newTeamTools(manager *rigteam.Manager) []rig.Tool {
	if manager == nil {
		return nil
	}
	tools := manager.Tools()
	tools = append(tools, manager.LegacyTools(nil)...)
	tools = append(tools, rigteam.DSHTools(manager)...)
	tracker := teamTrackerFor(manager, nil)
	if tracker == nil {
		return tools
	}
	wrapped := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		wrapped = append(wrapped, &trackedTeamTool{inner: tool, tracker: tracker, manager: manager})
	}
	return wrapped
}
