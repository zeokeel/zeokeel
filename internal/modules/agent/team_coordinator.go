package agent

// Team coordinator bridge: the truthful teammate run state machine is the SDK's
// host-agnostic coordinator (rig agent/team). This file adapts the product's rig
// team manager and child-session store to the coordinator's Host/Store seams and
// keeps the product's Chinese activity vocabulary and durable child records.
//
// Lane split: TeamService.Spawn starts a member through the coordinator (the
// coordinator's Host spawns it in the rig team manager, so the model-facing team
// tools keep seeing it). Spawns made directly by the model-facing tools bypass
// the coordinator (the rig manager owns them); team_state.go keeps the legacy
// run wrapper for that lane. Members() merges both lanes.

import (
	"context"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagentteam "github.com/zeokeel/zeokeel-rig/agent/team"
	rigteam "github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// coordinatorSpawnKey marks the context of a manager spawn initiated by the
// coordinator's Host: the dynamic runner then starts the raw subagent (the
// coordinator owns the run state) instead of the legacy external tracker.
type coordinatorSpawnKey struct{}

// isCoordinatorSpawn reports whether ctx marks a coordinator-driven spawn.
func isCoordinatorSpawn(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(coordinatorSpawnKey{}).(bool)
	return marked
}

// coordinatorStore marks the durable home of the coordinator's read model: the
// child-session store keeps one record per teammate, so Load is intentionally
// empty (restored rows are historical and come from the child-session store).
type coordinatorStore struct{ tracker *teamTracker }

func (s coordinatorStore) Load(context.Context) (rigagentteam.TeamState, error) {
	return rigagentteam.TeamState{}, nil
}

func (s coordinatorStore) Save(ctx context.Context, state rigagentteam.TeamState) error {
	if s.tracker != nil {
		s.tracker.persistCoordinatorState(state)
	}
	return nil
}

// coordinatorHost adapts the rig team manager to rig agent/team.Host.
type coordinatorHost struct{ tracker *teamTracker }

func (h *coordinatorHost) manager() *rigteam.Manager {
	if h == nil || h.tracker == nil {
		return nil
	}
	return h.tracker.teamManager()
}

// Spawn implements rigagentteam.Host.
func (h *coordinatorHost) Spawn(ctx context.Context, spec rigagentteam.SpawnSpec) (string, error) {
	manager := h.manager()
	if manager == nil {
		return "", rigagentteam.ErrNoHost
	}
	spawnCtx := context.WithValue(ctx, coordinatorSpawnKey{}, true)
	member, err := manager.Spawn(spawnCtx, rig.SubagentSpec{
		Name: strings.TrimSpace(spec.Name), Prompt: spec.Prompt, Model: strings.TrimSpace(spec.Model),
	})
	if err != nil {
		return "", err
	}
	h.tracker.bindCoordinatorMember(member.ID, spec)
	return member.ID, nil
}

// Send implements rigagentteam.Host.
func (h *coordinatorHost) Send(ctx context.Context, id, message string) (rigagentteam.Result, error) {
	manager := h.manager()
	if manager == nil {
		return rigagentteam.Result{}, rigagentteam.ErrNoHost
	}
	result, err := manager.Send(ctx, id, message)
	return coordinatorResult(result), err
}

// Wait implements rigagentteam.Host.
func (h *coordinatorHost) Wait(ctx context.Context, id string) (rigagentteam.Result, error) {
	manager := h.manager()
	if manager == nil {
		return rigagentteam.Result{}, rigagentteam.ErrNoHost
	}
	result, err := manager.Wait(ctx, id)
	return coordinatorResult(result), err
}

// Cancel implements rigagentteam.Host.
func (h *coordinatorHost) Cancel(_ context.Context, id string) error {
	manager := h.manager()
	if manager == nil {
		return rigagentteam.ErrNoHost
	}
	return manager.Interrupt(id)
}

// Snapshot implements rigagentteam.Host.
func (h *coordinatorHost) Snapshot(_ context.Context, id string) (rigagentteam.MemberSnapshot, bool) {
	manager := h.manager()
	if manager == nil {
		return rigagentteam.MemberSnapshot{}, false
	}
	for _, member := range manager.Members() {
		if member.ID != id {
			continue
		}
		return rigagentteam.MemberSnapshot{
			Status: member.Status, Running: member.Status == rigteam.StatusActive,
		}, true
	}
	return rigagentteam.MemberSnapshot{}, false
}

// coordinatorResult converts a rig subagent result to the coordinator model.
func coordinatorResult(result rig.SubagentResult) rigagentteam.Result {
	return rigagentteam.Result{
		Output:      result.Output,
		UsageTokens: result.Usage.TotalTokens,
		Iterations:  result.Iterations,
	}
}

// coordinator returns the service's coordinator (nil for a bare tracker).
func (t *teamTracker) coordinator() *rigagentteam.Coordinator {
	if t == nil {
		return nil
	}
	return t.coord
}

// teamManager resolves the rig manager the coordinator's Host drives.
func (t *teamTracker) teamManager() *rigteam.Manager {
	if t == nil || t.managerFn == nil {
		return nil
	}
	return t.managerFn()
}

// bindCoordinatorMember binds the durable child-session identity and spawn
// activity of one coordinator-started teammate.
func (t *teamTracker) bindCoordinatorMember(memberID string, spec rigagentteam.SpawnSpec) {
	if t == nil || strings.TrimSpace(memberID) == "" {
		return
	}
	childID := t.sessions.newID()
	if t.sessions == nil {
		childID = childSessionIDPrefix + randomHex(4)
	}
	t.mu.Lock()
	if t.coordChildIDs == nil {
		t.coordChildIDs = make(map[string]string)
	}
	if t.coordPrompts == nil {
		t.coordPrompts = make(map[string]string)
	}
	t.coordChildIDs[memberID] = childID
	prompt := collapseTeamText(spec.Prompt, 120)
	t.coordPrompts[memberID] = prompt
	t.mu.Unlock()
	t.addMemberActivity(memberID, contracts.TeamActivitySpawn,
		fmt.Sprintf("创建队友「%s」：%s", spec.Name, prompt), false)
}

// coordinatorTeammate resolves a member reference (manager id, child-session id
// or unique display name) to the coordinator's teammate.
func (t *teamTracker) coordinatorTeammate(ref string) (rigagentteam.Teammate, bool) {
	coordinator := t.coordinator()
	if coordinator == nil {
		return rigagentteam.Teammate{}, false
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return rigagentteam.Teammate{}, false
	}
	if memberID := t.memberForChild(ref); memberID != "" {
		ref = memberID
	}
	return coordinator.Teammate(ref)
}

// coordinatorMemberID resolves one member reference (manager id, child-session
// id or unique display name) to the coordinator-owned member id, so tool
// activity can be attributed even when no legacy wrapped run exists.
func (t *teamTracker) coordinatorMemberID(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if memberID := t.memberForChild(ref); memberID != "" {
		return memberID
	}
	if teammate, ok := t.coordinatorTeammate(ref); ok {
		return teammate.ID
	}
	return ""
}

// coordinatorSnapshot returns the coordinator's read model (zero for a bare
// tracker).
func (t *teamTracker) coordinatorSnapshot() rigagentteam.TeamState {
	if coordinator := t.coordinator(); coordinator != nil {
		return coordinator.Snapshot()
	}
	return rigagentteam.TeamState{}
}

// runStateFromCoordinator folds the SDK status onto the contracts run-state
// vocabulary (the string values are identical today; the mapping keeps the
// dependency one-way).
func runStateFromCoordinator(status rigagentteam.Status) string {
	switch string(status) {
	case string(rigagentteam.StatusRunning):
		return contracts.TeamRunRunning
	case string(rigagentteam.StatusDone):
		return contracts.TeamRunDone
	case string(rigagentteam.StatusFailed):
		return contracts.TeamRunFailed
	case string(rigagentteam.StatusInterrupted):
		return contracts.TeamRunInterrupted
	case string(rigagentteam.StatusClosed):
		return contracts.TeamRunClosed
	default:
		return contracts.TeamRunIdle
	}
}

// teamMemberFromCoordinator converts one coordinator teammate to the contracts
// member read model, filling its bounded activity rows.
func (t *teamTracker) teamMemberFromCoordinator(teammate rigagentteam.Teammate) contracts.TeamMember {
	converted := contracts.TeamMember{
		ID:          t.childIDForMember(teammate.ID),
		Name:        teammate.Name,
		Status:      rigteam.StatusActive,
		Created:     teammate.Created,
		RunState:    runStateFromCoordinator(teammate.Status),
		Error:       teammate.Error,
		Model:       teammate.Model,
		Started:     teammate.Started,
		Finished:    teammate.Finished,
		UsageTokens: teammate.UsageTokens,
		Iterations:  teammate.Iterations,
		SessionID:   teammate.ParentID,
	}
	if converted.ID == "" {
		converted.ID = teammate.ID
	}
	if converted.RunState == contracts.TeamRunRunning {
		converted.Last = ""
	} else {
		converted.Last = teammate.LastOutput
	}
	converted.Activity = t.activityForMember(teammate.ID)
	return converted
}

// persistCoordinatorState persists every coordinator member as a child-session
// record and synthesizes the product's run-lifecycle activity rows from the
// status transitions (the coordinator itself owns the run state).
func (t *teamTracker) persistCoordinatorState(state rigagentteam.TeamState) {
	if t == nil {
		return
	}
	for _, teammate := range state.Members {
		t.recordCoordinatorTransitions(teammate)
	}
	for _, teammate := range state.Members {
		t.persistCoordinatorMember(teammate, "", false)
	}
}

// recordCoordinatorTransitions appends the Chinese lifecycle rows for the
// status change of one teammate since the previous persisted state.
func (t *teamTracker) recordCoordinatorTransitions(teammate rigagentteam.Teammate) {
	t.mu.Lock()
	if t.coordStatuses == nil {
		t.coordStatuses = make(map[string]string)
	}
	previous := t.coordStatuses[teammate.ID]
	t.coordStatuses[teammate.ID] = string(teammate.Status)
	t.mu.Unlock()

	wasRunning := previous == string(rigagentteam.StatusRunning)
	switch teammate.Status {
	case rigagentteam.StatusDone:
		if previous == "" {
			return
		}
		t.addMemberActivity(teammate.ID, contracts.TeamActivityFinish,
			fmt.Sprintf("「%s」已完成：%s", teammate.Name, collapseTeamText(teammate.LastOutput, 120)), false)
	case rigagentteam.StatusFailed:
		if previous == "" {
			return
		}
		t.addMemberActivity(teammate.ID, contracts.TeamActivityError,
			fmt.Sprintf("「%s」运行失败：%s", teammate.Name, teammate.Error), true)
	case rigagentteam.StatusInterrupted:
		if wasRunning {
			t.addMemberActivity(teammate.ID, contracts.TeamActivityInterrupt,
				fmt.Sprintf("「%s」的当前运行已停止", teammate.Name), false)
		}
	case rigagentteam.StatusClosed:
		if wasRunning {
			t.addMemberActivity(teammate.ID, contracts.TeamActivityInterrupt,
				fmt.Sprintf("「%s」的当前运行已停止", teammate.Name), false)
		}
		t.addMemberActivity(teammate.ID, contracts.TeamActivityClose,
			fmt.Sprintf("关闭「%s」", teammate.Name), false)
	}
}

// persistCoordinatorMember upserts the child-session record of one coordinator
// teammate. statusOverride forces the persisted status (shutdown archiving).
func (t *teamTracker) persistCoordinatorMember(teammate rigagentteam.Teammate, statusOverride string, finishedNow bool) {
	if t == nil || t.sessions == nil || strings.TrimSpace(teammate.ID) == "" {
		return
	}
	childID := t.childIDForMember(teammate.ID)
	if childID == "" {
		return
	}
	status := childStatusFromRunState(runStateFromCoordinator(teammate.Status))
	if statusOverride != "" {
		status = statusOverride
	}
	finished := teammate.Finished
	if finishedNow && finished.IsZero() {
		finished = time.Now()
	}
	record := childSessionRecord{
		ID:              childID,
		ParentSessionID: teammate.ParentID,
		BackendRef:      teammate.ID,
		Kind:            contracts.ChildAgentTeammate,
		Name:            teammate.Name,
		Model:           teammate.Model,
		Prompt:          t.memberPrompt(teammate.ID),
		Status:          status,
		Output:          teammate.LastOutput,
		Error:           teammate.Error,
		Started:         teammate.Started,
		Finished:        finished,
		UsageTokens:     teammate.UsageTokens,
		Iterations:      teammate.Iterations,
		Activity:        childSessionActivityFromTeam(t.activityForMember(teammate.ID)),
	}
	t.sessions.upsert(record)
}
