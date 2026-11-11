package agent

// Unified child-agent read model (contracts.ChildAgentService).
//
// The session shell presents subagents and team members as child conversations
// of the active session: one header breadcrumb, one content switch, one way
// back to the parent. The two backends stay as they are:
//
//   - a synchronous subagent delegation is one blocking run tracked by the
//     bounded subagent activity registry (subagent_activity.go); it is one-shot
//     and cannot be steered;
//   - a teammate is a durable rig team member tracked by team_state.go /
//     team_activity.go; it is continuable.
//
// This file is the single adapter the session UI consumes, so the two kinds
// render through one code path and a future durable child-session backend only
// has to change this mapping. Both source snapshots are private copies; nothing
// here aliases the registries or the Rig runs.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// childAgentsService implements contracts.ChildAgentService over the service's
// subagent activity registry and its team service. It is safe for concurrent
// use: every source read is either a bounded snapshot or already locked.
type childAgentsService struct {
	service *service
	team    *teamService
}

var _ contracts.ChildAgentService = (*childAgentsService)(nil)

// newChildAgentsService returns the unified child-agent view for svc. team is
// the same adapter KeyTeam provides, so the run state and the activity timeline
// stay consistent with the team inspector.
func newChildAgentsService(svc *service, team *teamService) *childAgentsService {
	return &childAgentsService{service: svc, team: team}
}

// ChildAgents implements contracts.ChildAgentService: the subagent delegations
// of the selected parent session and the process-wide team members, merged and
// ordered oldest first.
//
// An empty parentSessionID means the service's active session. Teammates are
// process-wide (the rig team manager has no parent-session binding), so they
// are listed for every scope with an empty ChildAgent.ParentSessionID: the
// session UI shows the team from the active conversation, but never claims a
// teammate belongs to a session the backend did not record.
func (c *childAgentsService) ChildAgents(parentSessionID string) []contracts.ChildAgent {
	if c == nil {
		return nil
	}
	scope := strings.TrimSpace(parentSessionID)
	if scope == "" {
		scope = c.activeSessionID()
	}
	agents := c.subagentAgents(scope)
	agents = append(agents, c.teammateAgents()...)
	agents = append(agents, c.archivedAgents(scope)...)
	if len(agents) == 0 {
		return nil
	}
	sort.SliceStable(agents, func(i, j int) bool {
		if !agents[i].Started.Equal(agents[j].Started) {
			return agents[i].Started.Before(agents[j].Started)
		}
		return agents[i].ID < agents[j].ID
	})
	return agents
}

// ChildAgent implements contracts.ChildAgentService: one child agent by its
// stable row ID ("subagent-<n>" or "member-<n>"), independent of the parent
// session scope.
func (c *childAgentsService) ChildAgent(id string) (contracts.ChildAgent, bool) {
	if c == nil {
		return contracts.ChildAgent{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return contracts.ChildAgent{}, false
	}
	if c.service != nil {
		for _, activity := range c.service.SubagentActivities() {
			if activity.ID == id {
				return childAgentFromSubagentActivity(activity), true
			}
		}
	}
	if agent, ok := c.liveTeammateAgent(id); ok {
		return agent, true
	}
	if record, ok := c.archivedMember(id); ok {
		return childAgentFromArchivedMember(record), true
	}
	if record, ok := c.archivedSubagent(id); ok {
		return childAgentFromArchivedSubagent(record), true
	}
	return contracts.ChildAgent{}, false
}

// SendChildAgent implements contracts.ChildAgentService: only continuable
// teammates accept a follow-up; a one-shot subagent cannot be resumed.
func (c *childAgentsService) SendChildAgent(ctx context.Context, id, message string) (string, error) {
	if c == nil {
		return "", errors.New("agent: child agent service is unavailable")
	}
	child, ok := c.ChildAgent(id)
	if !ok {
		return "", fmt.Errorf("agent: child agent %q not found", strings.TrimSpace(id))
	}
	if child.Historical {
		return "", fmt.Errorf("agent: child agent %q is a finished historical record and cannot be continued", child.ID)
	}
	if child.Kind != contracts.ChildAgentTeammate {
		return "", fmt.Errorf("agent: child agent %q is one-shot and cannot receive follow-up messages", child.ID)
	}
	if c.team == nil {
		return "", fmt.Errorf("agent: team backend is unavailable for child agent %q", child.ID)
	}
	return c.team.Send(ctx, child.ID, message)
}

// activeSessionID returns the session the UI currently shows, or "" when the
// service has none (headless embedders, fresh tests).
func (c *childAgentsService) activeSessionID() string {
	if c.service == nil {
		return ""
	}
	return c.service.ActiveSession()
}

// subagentAgents maps the synchronous delegation snapshots of one parent
// session. The registry records one snapshot per delegation, not an event
// timeline (that lives in the parent transcript), so the mapped Activity stays
// empty rather than inventing entries; usage is not recorded there either.
func (c *childAgentsService) subagentAgents(scope string) []contracts.ChildAgent {
	if c.service == nil {
		return nil
	}
	activities := c.service.SubagentActivities()
	if len(activities) == 0 {
		return nil
	}
	agents := make([]contracts.ChildAgent, 0, len(activities))
	for _, activity := range activities {
		if strings.TrimSpace(activity.SessionID) != scope {
			continue
		}
		agents = append(agents, childAgentFromSubagentActivity(activity))
	}
	if len(agents) == 0 {
		return nil
	}
	return agents
}

// teammateAgents maps the live team members, each with the team activity rows
// filtered by member ID. The team service already reconciles the truthful run
// state, model, timing and usage; only the spawn prompt (kept by the run
// tracker) is added here.
func (c *childAgentsService) teammateAgents() []contracts.ChildAgent {
	if c.team == nil {
		return nil
	}
	members := c.team.Members()
	if len(members) == 0 {
		return nil
	}
	// The team adapter implements the optional activity read model next to
	// contracts.TeamService; read it through the interface the inspector uses.
	activities := contracts.TeamActivityLister(c.team).TeamActivities()
	byMember := make(map[string][]contracts.ChildAgentActivity, len(members))
	for _, activity := range activities {
		memberID := strings.TrimSpace(activity.MemberID)
		if memberID == "" {
			// A team-level event (an unattributed tool call) is not part of a
			// member's child conversation.
			continue
		}
		byMember[memberID] = append(byMember[memberID], contracts.ChildAgentActivity{
			ID:    activity.ID,
			Kind:  activity.Kind,
			Text:  activity.Text,
			Time:  activity.Time,
			Error: activity.Error,
		})
	}
	tracker := c.team.teamTracker()
	agents := make([]contracts.ChildAgent, 0, len(members))
	for _, member := range members {
		if member.Historical {
			// Restored history rows are not live teammates: they are added by
			// archivedAgents so the manager-backed ones keep their reference IDs.
			continue
		}
		agent := childAgentFromTeamMember(member)
		agent.ParentSessionID = member.SessionID
		agent.Prompt = tracker.memberPrompt(member.ID)
		agent.Activity = byMember[member.ID]
		agents = append(agents, agent)
	}
	return agents
}

// ClearChildAgentHistory implements the optional contracts.ChildHistoryMaintainer:
// the development driver resets the retained history between scripted scenes.
func (c *childAgentsService) ClearChildAgentHistory() {
	if history := c.history(); history != nil {
		history.clear()
	}
}

// history returns the service's durable child-agent history (nil when the
// module runs without a data directory).
func (c *childAgentsService) history() *childHistoryStore {
	if c == nil || c.service == nil {
		return nil
	}
	return c.service.childHistory
}

// liveTeammateAgent returns one live (non-historical) teammate by reference.
func (c *childAgentsService) liveTeammateAgent(id string) (contracts.ChildAgent, bool) {
	for _, agent := range c.teammateAgents() {
		if agent.ID == id {
			return agent, true
		}
	}
	return contracts.ChildAgent{}, false
}

// archivedMember returns one restored teammate record by its history key.
func (c *childAgentsService) archivedMember(id string) (childHistoryMember, bool) {
	history := c.history()
	if history == nil {
		return childHistoryMember{}, false
	}
	for _, record := range history.memberRecords() {
		if record.Key == id {
			return record, true
		}
	}
	return childHistoryMember{}, false
}

// archivedSubagent returns one restored delegation record by its history key.
func (c *childAgentsService) archivedSubagent(id string) (childHistorySubagent, bool) {
	history := c.history()
	if history == nil {
		return childHistorySubagent{}, false
	}
	for _, record := range history.subagentRecords() {
		if record.Key == id {
			return record, true
		}
	}
	return childHistorySubagent{}, false
}

// archivedAgents maps the durable history rows of one session scope. Records
// that a live row still supersedes (the same backend child is listed by the
// running read model) are skipped so a member that is both finished and live
// never renders twice.
func (c *childAgentsService) archivedAgents(scope string) []contracts.ChildAgent {
	history := c.history()
	if history == nil {
		return nil
	}
	live := make(map[string]string)
	if c.service != nil {
		for _, activity := range c.service.SubagentActivities() {
			live[activity.ID] = activity.SessionID
		}
	}
	if c.team != nil {
		for _, member := range c.team.Members() {
			if member.Historical {
				continue
			}
			live[member.ID] = member.SessionID
		}
	}
	superseded := func(record childHistoryMember) bool {
		session, ok := live[record.OriginalID]
		if !ok {
			return false
		}
		return session == "" || record.SessionID == "" || session == record.SessionID
	}
	agents := make([]contracts.ChildAgent, 0, len(history.memberRecords())+len(history.subagentRecords()))
	for _, record := range history.memberRecords() {
		if !historyScopeMatches(record.SessionID, scope) || superseded(record) {
			continue
		}
		agents = append(agents, childAgentFromArchivedMember(record))
	}
	for _, record := range history.subagentRecords() {
		if !historyScopeMatches(record.SessionID, scope) {
			continue
		}
		if session, ok := live[record.OriginalID]; ok &&
			(historyScopeMatches(session, record.SessionID) || session == "" || record.SessionID == "") {
			continue
		}
		agents = append(agents, childAgentFromArchivedSubagent(record))
	}
	return agents
}

// historyScopeMatches reports whether a persisted record belongs to scope: an
// unattributed session or an empty scope matches everything (legacy records and
// headless callers).
func historyScopeMatches(sessionID, scope string) bool {
	if scope == "" || sessionID == "" {
		return true
	}
	return sessionID == scope
}

// childAgentFromSubagentActivity converts one synchronous delegation snapshot.
// Cancelled delegations are "interrupted" (the child was stopped, not broken)
// and their detail is empty, so neither Output nor Error is filled.
func childAgentFromSubagentActivity(activity contracts.SubagentActivity) contracts.ChildAgent {
	agent := contracts.ChildAgent{
		ID:              activity.ID,
		ParentSessionID: activity.SessionID,
		Name:            activity.Name,
		Kind:            contracts.ChildAgentSubagent,
		Mode:            contracts.ChildAgentOneShot,
		Status:          childAgentStatusFromSubagent(activity.Status),
		Model:           activity.Model,
		Prompt:          activity.Prompt,
		Started:         activity.Started,
		Finished:        activity.Finished,
	}
	switch activity.Status {
	case contracts.SubagentStatusDone:
		agent.Output = activity.Detail
	case contracts.SubagentStatusError:
		agent.Error = activity.Detail
	}
	return agent
}

// childAgentStatusFromSubagent maps one SubagentStatus* value. The registry
// only stores running/finished rows, so the default is the in-flight state.
func childAgentStatusFromSubagent(status string) string {
	switch status {
	case contracts.SubagentStatusDone:
		return contracts.ChildAgentCompleted
	case contracts.SubagentStatusError:
		return contracts.ChildAgentFailed
	case contracts.SubagentStatusCancelled:
		return contracts.ChildAgentInterrupted
	default:
		return contracts.ChildAgentRunning
	}
}

// childAgentFromTeamMember converts one team member snapshot. A failed latest
// run keeps its error and never shows the previous answer as Output; a running
// member shows neither.
func childAgentFromTeamMember(member contracts.TeamMember) contracts.ChildAgent {
	agent := contracts.ChildAgent{
		ID:   member.ID,
		Name: member.Name,
		Kind: contracts.ChildAgentTeammate,
		Mode: contracts.ChildAgentContinuable,

		Status:      childAgentStatusFromTeamRun(member.RunState),
		Model:       member.Model,
		Error:       member.Error,
		Started:     member.Started,
		Finished:    member.Finished,
		UsageTokens: member.UsageTokens,
		Iterations:  member.Iterations,
	}
	if agent.Status != contracts.ChildAgentRunning && strings.TrimSpace(member.Error) == "" {
		agent.Output = member.Last
	}
	return agent
}

// childAgentStatusFromTeamRun maps one TeamRun* value to the unified status.
// A closed member (removed from the manager, normally absent from Members) is
// terminal and not a failure, so it maps like an interrupted run; an unknown or
// empty state maps to idle (available, no run in flight).
func childAgentStatusFromTeamRun(runState string) string {
	switch runState {
	case contracts.TeamRunRunning:
		return contracts.ChildAgentRunning
	case contracts.TeamRunDone:
		return contracts.ChildAgentCompleted
	case contracts.TeamRunFailed:
		return contracts.ChildAgentFailed
	case contracts.TeamRunInterrupted, contracts.TeamRunClosed:
		return contracts.ChildAgentInterrupted
	default:
		return contracts.ChildAgentIdle
	}
}

// memberPrompt returns the bounded spawn prompt of the run claimed by memberID;
// "" when the member has no tracked run. It copies the prompt out under the run
// lock, never aliases the tracker.
func (t *teamTracker) memberPrompt(memberID string) string {
	if t == nil {
		return ""
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return ""
	}
	t.mu.Lock()
	run := t.claims[memberID]
	t.mu.Unlock()
	if run == nil {
		return ""
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.prompt
}
