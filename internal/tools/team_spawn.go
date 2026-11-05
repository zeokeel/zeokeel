package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/agentteam"
	"github.com/zeokeel/zeokeel/internal/runtime"
)

// TeamSpawnConfig configures the asynchronous team lifecycle tools.
//
// Manager is the shared team manager. It usually is the same manager passed to
// TeamConfig so spawn_team, list_teams, team_status, close_team and wait_team
// observe the members created by list_subagent_models, send_message and
// interrupt_agent. A nil Manager creates a private manager, which is useful for
// tests and for a toolset that owns its own team; runtime.New can bind the
// parent Agent later through runtime.SubagentRunnerBinder.
type TeamSpawnConfig struct {
	Manager *agentteam.Manager
}

const (
	// teamSpawnMaxTasks caps one spawn_team call.
	teamSpawnMaxTasks = 8
	// teamSpawnMaxOutput caps one lifecycle result (64 KiB).
	teamSpawnMaxOutput = DefaultMaxOutputBytes
	// teamSpawnDefaultWait is wait_team's timeout when none is given.
	teamSpawnDefaultWait = 30 * time.Second
	// teamSpawnMaxWait is the largest accepted wait_team timeout.
	teamSpawnMaxWait = time.Hour
)

const spawnTeamDescription = `Create one asynchronous team member for each task.

Call it with a JSON object:
  {"tasks":["review the parser","write the migration plan"],"agent":"worker"}

"tasks" is required and accepts 1 through 8 non-blank strings. Every task
becomes its own member running in parallel in the shared workspace. "agent" is
optional and names the child agent ("subagent" when empty). The call returns
immediately:
{"kind":"team_spawn","agent":"worker","count":2,"members":[{id,name,status,
task,...},...]}. Use the returned id (or a unique name) as the "target" of
team_status, wait_team, send_message and interrupt_agent. The members keep
running after this tool returns.`

const listTeamsDescription = `List every asynchronous team member and its lifecycle status.

Call it with an empty JSON object:
  {}

The result is {"kind":"team_list","count":N,"members":[{id,name,status,task,
current_task,output,error,turns,queue_len,...}]}. "status" is one of queued,
running, completed, failed or interrupted. Use a member id (or a unique agent
name) as the "target" of team_status, wait_team, send_message and
interrupt_agent.`

const teamStatusDescription = `Read the current detail of one asynchronous team member.

Call it with a JSON object:
  {"target":"agent-1"}

"target" is the member id returned by spawn_team or list_teams; a unique agent
name is also accepted. The result is {"kind":"team_status","target":...,
"member":{id,name,status,task,current_task,output,error,turns,queue_len,...}}.
An unknown or ambiguous target is an error.`

const closeTeamDescription = `Stop every asynchronous team member.

Call it with an empty JSON object:
  {}

All queued messages are dropped and every running turn is canceled. The call
waits for the stopped members to settle and is idempotent: calling it again on
an idle team succeeds. The result is {"kind":"team_close","closed":N,
"stopped":K,"members":[...]}, where "closed" is the team size, "stopped" is the
number of running turns this call canceled, and "members" are the settled
snapshots. The manager stays usable for a later spawn_team.`

const waitTeamDescription = `Wait until team members settle or the timeout elapses.

Call it with a JSON object:
  {"target":"agent-1","timeout_seconds":30}

"target" is optional: when it is present the call waits for that member; when
it is omitted the call waits for every member that is running when the wait
starts. "timeout_seconds" is optional (default 30, maximum 3600) and may be a
fraction. The call never starts or wakes a member. It returns
{"kind":"team_wait","timed_out":false,"timeout_seconds":30,"status":"completed",
"member":{...},"members":[]} for a targeted wait, or ...{"members":[...]} for a
whole-team wait. A timed-out wait returns "timed_out":true with the current
snapshots instead of an error.`

// teamSpawnToolBase is embedded by every lifecycle tool. Its
// SetSubagentRunner method is promoted, so runtime.New can bind the parent
// Agent even though the tools were registered before it existed.
type teamSpawnToolBase struct {
	mgr *agentteam.Manager
}

// SetSubagentRunner implements runtime.SubagentRunnerBinder.
func (b *teamSpawnToolBase) SetSubagentRunner(runner runtime.SubagentRunner) {
	if b == nil || b.mgr == nil {
		return
	}
	b.mgr.SetRunner(runner)
}

// NewTeamSpawnTools returns spawn_team, list_teams, team_status, close_team
// and wait_team. The tools are constructors only: the parent registers them and
// binds the subagent runner.
func NewTeamSpawnTools(cfg TeamSpawnConfig) []runtime.Tool {
	mgr := cfg.Manager
	if mgr == nil {
		mgr = agentteam.New(agentteam.Config{})
	}
	base := teamSpawnToolBase{mgr: mgr}
	return []runtime.Tool{
		&spawnTeamTool{teamSpawnToolBase: base},
		&listTeamsTool{teamSpawnToolBase: base},
		&teamStatusTool{teamSpawnToolBase: base},
		&closeTeamTool{teamSpawnToolBase: base},
		&waitTeamTool{teamSpawnToolBase: base},
	}
}

// teamSpawnManager returns the shared manager or a clear configuration error.
func teamSpawnManager(mgr *agentteam.Manager, tool string) (*agentteam.Manager, error) {
	if mgr == nil {
		return nil, fmt.Errorf("%s: no team manager configured", tool)
	}
	return mgr, nil
}

// teamSpawnMembers normalizes a nil member slice to an empty JSON array.
func teamSpawnMembers(members []agentteam.Member) []agentteam.Member {
	if members == nil {
		return []agentteam.Member{}
	}
	return members
}

// teamSpawnResult is the spawn_team result.
type teamSpawnResult struct {
	Kind    string             `json:"kind"`
	Agent   string             `json:"agent"`
	Count   int                `json:"count"`
	Members []agentteam.Member `json:"members"`
}

// spawnTeamTool implements spawn_team.
type spawnTeamTool struct {
	teamSpawnToolBase
}

func (t *spawnTeamTool) Name() string        { return "spawn_team" }
func (t *spawnTeamTool) Description() string { return spawnTeamDescription }

func (t *spawnTeamTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("spawn_team: no team manager configured")
	}
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	var in struct {
		Tasks []string `json:"tasks"`
		Agent string   `json:"agent"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	if len(in.Tasks) == 0 {
		return "", errors.New(`spawn_team: "tasks" is required and must contain at least one task`)
	}
	if len(in.Tasks) > teamSpawnMaxTasks {
		return "", fmt.Errorf("spawn_team: at most %d tasks are allowed per call (got %d)", teamSpawnMaxTasks, len(in.Tasks))
	}
	agent := strings.TrimSpace(in.Agent)
	depth := runtime.SubagentDepth(ctx) + 1
	// Members are scoped to the turn's session: Stop cancels a session's
	// asynchronous members, so the manager must know who spawned them.
	sessionID := runtime.SessionIDFromContext(ctx)
	reqs := make([]agentteam.SpawnRequest, 0, len(in.Tasks))
	for i, task := range in.Tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			return "", fmt.Errorf("spawn_team: tasks[%d] is empty", i)
		}
		reqs = append(reqs, agentteam.SpawnRequest{
			Task: task, Agent: agent, Depth: depth, SessionID: sessionID,
		})
	}
	mgr, err := teamSpawnManager(t.mgr, t.Name())
	if err != nil {
		return "", err
	}
	members, err := mgr.SpawnBatch(ctx, reqs)
	if err != nil {
		return "", fmt.Errorf("spawn_team: %w", err)
	}
	effectiveAgent := agent
	if len(members) > 0 {
		effectiveAgent = members[0].Name
	} else if effectiveAgent == "" {
		effectiveAgent = "subagent"
	}
	return boundedToolJSON(teamSpawnResult{
		Kind:    "team_spawn",
		Agent:   effectiveAgent,
		Count:   len(members),
		Members: teamSpawnMembers(members),
	}, teamSpawnMaxOutput, t.Name())
}

// teamListResult is the list_teams result.
type teamListResult struct {
	Kind    string             `json:"kind"`
	Count   int                `json:"count"`
	Members []agentteam.Member `json:"members"`
}

// listTeamsTool implements list_teams.
type listTeamsTool struct {
	teamSpawnToolBase
}

func (t *listTeamsTool) Name() string        { return "list_teams" }
func (t *listTeamsTool) Description() string { return listTeamsDescription }

func (t *listTeamsTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("list_teams: no team manager configured")
	}
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	if err := decodeArgs(t.Name(), args, &struct{}{}); err != nil {
		return "", err
	}
	mgr, err := teamSpawnManager(t.mgr, t.Name())
	if err != nil {
		return "", err
	}
	members := teamSpawnMembers(mgr.Snapshot())
	return boundedToolJSON(teamListResult{
		Kind:    "team_list",
		Count:   len(members),
		Members: members,
	}, teamSpawnMaxOutput, t.Name())
}

// teamStatusResult is the team_status result.
type teamStatusResult struct {
	Kind   string           `json:"kind"`
	Target string           `json:"target"`
	Member agentteam.Member `json:"member"`
}

// teamStatusTool implements team_status.
type teamStatusTool struct {
	teamSpawnToolBase
}

func (t *teamStatusTool) Name() string        { return "team_status" }
func (t *teamStatusTool) Description() string { return teamStatusDescription }

func (t *teamStatusTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("team_status: no team manager configured")
	}
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	var in struct {
		Target string `json:"target"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		return "", errors.New(`team_status: "target" is required`)
	}
	mgr, err := teamSpawnManager(t.mgr, t.Name())
	if err != nil {
		return "", err
	}
	member, err := mgr.Member(target)
	if err != nil {
		return "", fmt.Errorf("team_status: %w", err)
	}
	return boundedToolJSON(teamStatusResult{
		Kind:   "team_status",
		Target: target,
		Member: member,
	}, teamSpawnMaxOutput, t.Name())
}

// teamCloseResult is the close_team result.
type teamCloseResult struct {
	Kind    string             `json:"kind"`
	Closed  int                `json:"closed"`
	Stopped int                `json:"stopped"`
	Members []agentteam.Member `json:"members"`
}

// closeTeamTool implements close_team.
type closeTeamTool struct {
	teamSpawnToolBase
}

func (t *closeTeamTool) Name() string        { return "close_team" }
func (t *closeTeamTool) Description() string { return closeTeamDescription }

func (t *closeTeamTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("close_team: no team manager configured")
	}
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	if err := decodeArgs(t.Name(), args, &struct{}{}); err != nil {
		return "", err
	}
	mgr, err := teamSpawnManager(t.mgr, t.Name())
	if err != nil {
		return "", err
	}
	stopped := mgr.CloseMembers()
	members := teamSpawnMembers(mgr.Snapshot())
	return boundedToolJSON(teamCloseResult{
		Kind:    "team_close",
		Closed:  len(members),
		Stopped: stopped,
		Members: members,
	}, teamSpawnMaxOutput, t.Name())
}

// teamWaitResult is the wait_team result. Exactly one of Member (targeted wait)
// and Members (whole-team wait) is populated.
type teamWaitResult struct {
	Kind           string             `json:"kind"`
	Target         string             `json:"target,omitempty"`
	TimedOut       bool               `json:"timed_out"`
	TimeoutSeconds float64            `json:"timeout_seconds"`
	Status         agentteam.Status   `json:"status,omitempty"`
	Member         *agentteam.Member  `json:"member,omitempty"`
	Members        []agentteam.Member `json:"members"`
}

// waitTeamTool implements wait_team.
type waitTeamTool struct {
	teamSpawnToolBase
}

func (t *waitTeamTool) Name() string        { return "wait_team" }
func (t *waitTeamTool) Description() string { return waitTeamDescription }

func (t *waitTeamTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil {
		return "", errors.New("wait_team: no team manager configured")
	}
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	var in struct {
		Target         string   `json:"target"`
		TimeoutSeconds *float64 `json:"timeout_seconds"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	timeout := teamSpawnDefaultWait
	if in.TimeoutSeconds != nil {
		seconds := *in.TimeoutSeconds
		if seconds < 0 {
			return "", errors.New("wait_team: timeout_seconds must not be negative")
		}
		if seconds > teamSpawnMaxWait.Seconds() {
			return "", fmt.Errorf("wait_team: timeout_seconds must be at most %.0f", teamSpawnMaxWait.Seconds())
		}
		timeout = time.Duration(seconds * float64(time.Second))
	}
	mgr, err := teamSpawnManager(t.mgr, t.Name())
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	target := strings.TrimSpace(in.Target)
	if target != "" {
		member, err := mgr.Wait(waitCtx, target)
		timedOut := false
		if err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				return "", fmt.Errorf("wait_team: %w", err)
			}
			timedOut = true
			member, err = mgr.Member(target)
			if err != nil {
				return "", fmt.Errorf("wait_team: %w", err)
			}
		}
		return boundedToolJSON(teamWaitResult{
			Kind:           "team_wait",
			Target:         target,
			TimedOut:       timedOut,
			TimeoutSeconds: timeout.Seconds(),
			Status:         member.Status,
			Member:         &member,
			Members:        []agentteam.Member{},
		}, teamSpawnMaxOutput, t.Name())
	}

	timedOut := false
	for _, member := range mgr.Snapshot() {
		if member.Status != agentteam.StatusRunning {
			continue
		}
		if _, err := mgr.Wait(waitCtx, member.ID); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				timedOut = true
				break
			}
			return "", fmt.Errorf("wait_team: %w", err)
		}
	}
	members := teamSpawnMembers(mgr.Snapshot())
	return boundedToolJSON(teamWaitResult{
		Kind:           "team_wait",
		TimedOut:       timedOut,
		TimeoutSeconds: timeout.Seconds(),
		Members:        members,
	}, teamSpawnMaxOutput, t.Name())
}
