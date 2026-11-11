package agent

// Team activity read model. Every model-facing team tool is wrapped so one
// bounded activity row is recorded per invocation (spawn/send/wait/interrupt/
// close and the shared task-board calls). Run lifecycle rows come from
// team_state.go; this file keeps the tool surface observable in the inspector's
// Agents detail timeline.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	rigteam "github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// trackedTeamTool wraps one model-facing team tool: it keeps the inner tool's
// name/schema/description and its optional completion-notice capability, and
// records one bounded activity row after each invocation.
type trackedTeamTool struct {
	inner   rig.Tool
	tracker *teamTracker
	manager *rigteam.Manager
}

var (
	_ rig.Tool         = (*trackedTeamTool)(nil)
	_ rig.NoticeSource = (*trackedTeamTool)(nil)
)

// Name implements rig.Tool.
func (t *trackedTeamTool) Name() string { return t.inner.Name() }

// Description implements rig.Tool.
func (t *trackedTeamTool) Description() string { return t.inner.Description() }

// Schema implements rig.Tool.
func (t *trackedTeamTool) Schema() map[string]any { return t.inner.Schema() }

// PendingNotices implements rig.NoticeSource. The rig manager's completion
// notices are drained and dropped here: ZeoKeel now delivers teammate
// completions through the durable child-session receipts (child_notices.go),
// which are session-scoped and survive between turns. Draining keeps the
// manager's queue bounded; job/tool notices use other tools and are untouched.
func (t *trackedTeamTool) PendingNotices() []string {
	if source, ok := t.inner.(rig.NoticeSource); ok {
		_ = source.PendingNotices()
	}
	return nil
}

// Invoke implements rig.Tool.
func (t *trackedTeamTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	result, err := t.inner.Invoke(ctx, call)
	t.record(call, result, err)
	return result, err
}

// record appends one bounded activity row for the invocation and reconciles the
// manager's members first, so a spawn/interrupt/close is attributed to the
// member it just changed.
func (t *trackedTeamTool) record(call rig.ToolCall, result rig.ToolResult, err error) {
	if t.tracker == nil {
		return
	}
	if t.manager != nil {
		t.tracker.reconcile(t.manager.Members())
	}
	name := t.Name()
	kind := teamToolActivityKind(name)
	ref := teamToolRef(call.Arguments)
	text := teamToolActivityText(name, call.Arguments, ref)
	failed := err != nil || result.IsError
	if err != nil {
		text += "：" + truncateTeamText(err.Error(), 120)
	} else if result.IsError {
		text += "：" + collapseTeamText(result.Content, 120)
	}
	if kind == contracts.TeamActivitySpawn && !failed {
		// The run tracker records a successful spawn as soon as the subagent
		// attaches (it owns the real member id and the run lifecycle); recording
		// it here too duplicated the first timeline row. A failed spawn has no
		// tracker run, so its activity still comes from this wrapper.
		return
	}
	run := t.tracker.runForRef(ref)
	memberID := t.tracker.claimMemberID(run)
	t.tracker.addActivity(run, memberID, kind, text, failed)
}

// teamToolActivityKind maps a tool name to its activity kind.
func teamToolActivityKind(name string) string {
	switch name {
	case rigteam.ToolSpawnTeammate, rigteam.ToolSpawn:
		return contracts.TeamActivitySpawn
	case rigteam.ToolSend:
		return contracts.TeamActivitySend
	case rigteam.ToolWait, rigteam.ToolWaitAgent:
		return contracts.TeamActivityWait
	case rigteam.ToolInterrupt:
		return contracts.TeamActivityInterrupt
	case rigteam.ToolClose:
		return contracts.TeamActivityClose
	default:
		return contracts.TeamActivityTool
	}
}

// teamToolRef extracts the member reference (id/target/name) of a team tool
// call; "" when the tool carries none (task-board and list calls).
func teamToolRef(arguments string) string {
	args := teamToolArgs(arguments)
	for _, key := range []string{"target", "id", "name", "agent", "member"} {
		if value := strings.TrimSpace(teamArgString(args, key)); value != "" {
			return value
		}
	}
	return ""
}

// teamToolActivityText renders the one-line activity summary of a team tool
// call, bounded to the activity text budget.
func teamToolActivityText(name, arguments, ref string) string {
	args := teamToolArgs(arguments)
	switch teamToolActivityKind(name) {
	case contracts.TeamActivitySpawn:
		teammate := strings.TrimSpace(teamArgString(args, "name"))
		prompt := firstTeamArg(args, "prompt", "task", "description")
		if prompt == "" {
			return "创建队友「" + teammate + "」"
		}
		return fmt.Sprintf("创建队友「%s」：%s", teammate, prompt)
	case contracts.TeamActivitySend:
		message := firstTeamArg(args, "message", "prompt")
		if ref == "" {
			return fmt.Sprintf("发送消息：%s", message)
		}
		return fmt.Sprintf("发送消息给「%s」：%s", ref, message)
	case contracts.TeamActivityWait:
		if ref == "" {
			return "等待团队变化"
		}
		return fmt.Sprintf("等待「%s」的当前运行", ref)
	case contracts.TeamActivityInterrupt:
		return fmt.Sprintf("中断「%s」", ref)
	case contracts.TeamActivityClose:
		return fmt.Sprintf("关闭「%s」", ref)
	default:
		subject := firstTeamArg(args, "subject", "action", "status")
		if subject == "" {
			return name
		}
		return name + "：" + subject
	}
}

// teamToolArgs decodes a tool call's JSON arguments into a generic map; an
// unparsable or empty argument list yields an empty map.
func teamToolArgs(arguments string) map[string]any {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" || arguments == "null" {
		return map[string]any{}
	}
	args := make(map[string]any)
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return map[string]any{}
	}
	return args
}

// teamArgString returns one string argument, or "".
func teamArgString(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// firstTeamArg returns the first non-empty string argument among keys, bounded
// for the activity text.
func firstTeamArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := teamArgString(args, key); value != "" {
			return collapseTeamText(value, 120)
		}
	}
	return ""
}

// runForRef resolves one member reference (id, target or unique display name)
// to its wrapped run, claimed or not. It is used to attribute tool activities.
func (t *teamTracker) runForRef(ref string) *trackedSubagent {
	ref = strings.TrimSpace(ref)
	if t == nil || ref == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for memberID, run := range t.claims {
		if memberID == ref {
			return run
		}
	}
	for _, run := range t.runs {
		if run.name == ref {
			run.mu.Lock()
			skip := run.released || run.closed
			run.mu.Unlock()
			if skip {
				continue
			}
			return run
		}
	}
	return nil
}

// TeamActivities implements contracts.TeamActivityLister: the bounded team
// activity table, oldest first, with member IDs resolved to the unified
// child-session identity.
func (t *teamService) TeamActivities() []contracts.TeamActivity {
	activities := t.teamTracker().activities()
	for index := range activities {
		memberID := activities[index].MemberID
		if memberID == "" {
			continue
		}
		childID := t.teamTracker().childIDForMember(memberID)
		if childID == "" && t.service.childSessions != nil {
			// A closed member no longer has a claim; its persisted child session
			// still maps the manager reference to the unified identity.
			if record, ok := t.service.childSessions.byBackendRef(memberID, "", contracts.ChildAgentTeammate); ok {
				childID = record.ID
			}
		}
		if childID != "" {
			activities[index].MemberID = childID
		}
	}
	return activities
}
