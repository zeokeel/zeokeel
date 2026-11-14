package toolkit

// ID-routed execution tools.
//
// Root-scoped builds are retained for the module lifetime, so a job or terminal
// created under one (root, permission, sandbox) build must stay reachable after
// the workspace or the policy changes. The model-facing proxies therefore:
//
//   - dispatch NEW work (job_start/terminal_open) to the build of the call's
//     workspace root, i.e. the current policy;
//   - remember which build created each job_id/session_id;
//   - dispatch follow-up calls (job_output/wait/kill, terminal_send/read/signal/
//     close) by that id to its owner build, falling back to the current build
//     for unknown ids so Rig's own "unknown job"/"unknown session" errors still
//     surface;
//   - merge job_list/terminal_list across every retained build of the caller's
//     workspace root, keeping Rig's text format and count accurate.
//
// When a durable store is wired, the same routing also records execution
// lifecycle events (execution/job_started, execution/job_finished,
// execution/terminal_opened, execution/terminal_closed) for the session stamped
// into the tool context. Event failures never affect the tool result.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/jobs"
	"github.com/zeokeel/zeokeel-rig/tools/terminal"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Execution tool categories.
var (
	jobExecutionTools = map[string]bool{
		jobs.ToolJobStart:  true,
		jobs.ToolJobOutput: true,
		jobs.ToolJobWait:   true,
		jobs.ToolJobKill:   true,
		jobs.ToolJobList:   true,
	}
	terminalExecutionTools = map[string]bool{
		terminal.ToolTerminalOpen:   true,
		terminal.ToolTerminalSend:   true,
		terminal.ToolTerminalRead:   true,
		terminal.ToolTerminalSignal: true,
		terminal.ToolTerminalClose:  true,
		terminal.ToolTerminalList:   true,
	}
)

// isExecutionTool reports whether a tool name routes through the ID registry.
func (g *rebindGroup) isExecutionTool(name string) bool {
	name = strings.TrimSpace(name)
	return jobExecutionTools[name] || terminalExecutionTools[name]
}

// invokeExecutionTool dispatches one job/terminal tool call.
func (g *rebindGroup) invokeExecutionTool(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	name := strings.TrimSpace(call.Name)
	switch {
	case name == jobs.ToolJobStart:
		scoped := g.currentForContext(ctx)
		result, err := invokeRootScoped(ctx, scoped, call)
		if err == nil && !result.IsError {
			g.recordJobStarted(ctx, scoped, call, result)
		}
		return result, err
	case name == terminal.ToolTerminalOpen:
		scoped := g.currentForContext(ctx)
		result, err := invokeRootScoped(ctx, scoped, call)
		if err == nil && !result.IsError {
			g.recordTerminalOpened(ctx, scoped, call, result)
		}
		return result, err
	case name == jobs.ToolJobList:
		return g.mergeJobLists(ctx)
	case name == terminal.ToolTerminalList:
		return g.mergeTerminalLists(ctx)
	case name == jobs.ToolJobOutput || name == jobs.ToolJobWait || name == jobs.ToolJobKill:
		id := executionArgument(call.Arguments, "job_id")
		return invokeRootScoped(ctx, g.executionOwner(id, false), call)
	case name == terminal.ToolTerminalSend || name == terminal.ToolTerminalRead ||
		name == terminal.ToolTerminalSignal || name == terminal.ToolTerminalClose:
		id := executionArgument(call.Arguments, "sessionId", "session_id", "id")
		scoped := g.executionOwner(id, true)
		result, err := invokeRootScoped(ctx, scoped, call)
		if name == terminal.ToolTerminalClose && err == nil && !result.IsError {
			g.recordTerminalClosed(ctx, scoped, id)
		}
		return result, err
	default:
		return invokeRootScoped(ctx, g.currentForContext(ctx), call)
	}
}

// currentForContext returns the build for this call's workspace root, i.e. the
// current policy for new work.
func (g *rebindGroup) currentForContext(ctx context.Context) *rootScoped {
	if ctx != nil {
		if root := strings.TrimSpace(contracts.WorkspaceRootFromContext(ctx)); root != "" {
			return g.forRootValue(root)
		}
	}
	return g.forRoot()
}

// executionOwner returns the build owning one job/terminal id, falling back to
// the current build for unknown ids (Rig then reports its own unknown-id error).
func (g *rebindGroup) executionOwner(id string, terminalSession bool) *rootScoped {
	id = strings.TrimSpace(id)
	if id != "" {
		g.mu.Lock()
		var owner *rootScoped
		if terminalSession {
			owner = g.terminalOwners[id]
		} else {
			owner = g.jobOwners[id]
		}
		g.mu.Unlock()
		if owner != nil {
			return owner
		}
	}
	return g.forRoot()
}

// contextRoot is the workspace root the caller is working in (empty when no
// workspace is open).
func (g *rebindGroup) contextRoot(ctx context.Context) string {
	if ctx != nil {
		if root := strings.TrimSpace(contracts.WorkspaceRootFromContext(ctx)); root != "" {
			return root
		}
	}
	if g.rootOf != nil {
		return strings.TrimSpace(g.rootOf())
	}
	return ""
}

// buildsForRoot returns every retained build of one root (creation order), which
// is the set whose jobs/terminals belong to that workspace.
func (g *rebindGroup) buildsForRoot(root string) []*rootScoped {
	root = strings.TrimSpace(root)
	builds := g.buildsSnapshot()
	out := make([]*rootScoped, 0, len(builds))
	seen := make(map[*rootScoped]bool, len(builds))
	for _, scoped := range builds {
		if scoped == nil || seen[scoped] || scoped.root != root {
			continue
		}
		seen[scoped] = true
		out = append(out, scoped)
	}
	return out
}

// mergeJobLists renders job_list across every build of the caller's root in
// Rig's original format ("jobs: <n>" plus one "- ..." row per job).
func (g *rebindGroup) mergeJobLists(ctx context.Context) (rig.ToolResult, error) {
	builds := g.buildsForRoot(g.contextRoot(ctx))
	if len(builds) == 0 {
		// No retained build for the caller's workspace root: nothing belongs to
		// it yet (another root's jobs must never leak into this list).
		return rig.ToolResult{Content: "no jobs"}, nil
	}
	if len(builds) == 1 {
		return invokeRootScoped(ctx, builds[0], rig.ToolCall{Name: jobs.ToolJobList, Arguments: "{}"})
	}
	rows := make([]string, 0, 8)
	for _, scoped := range builds {
		result, err := invokeRootScoped(ctx, scoped, rig.ToolCall{Name: jobs.ToolJobList, Arguments: "{}"})
		if err != nil || result.IsError {
			continue
		}
		for _, line := range strings.Split(result.Content, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "- ") {
				rows = append(rows, strings.TrimRight(line, "\r"))
			}
		}
	}
	if len(rows) == 0 {
		return rig.ToolResult{Content: "no jobs"}, nil
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "jobs: %d\n", len(rows))
	for _, row := range rows {
		builder.WriteString(row)
		builder.WriteString("\n")
	}
	return rig.ToolResult{Content: builder.String()}, nil
}

// mergeTerminalLists renders terminal_list across every build of the caller's
// root: Rig's per-session blocks concatenated with a blank line.
func (g *rebindGroup) mergeTerminalLists(ctx context.Context) (rig.ToolResult, error) {
	builds := g.buildsForRoot(g.contextRoot(ctx))
	if len(builds) == 0 {
		return rig.ToolResult{Content: "no terminal sessions"}, nil
	}
	if len(builds) == 1 {
		return invokeRootScoped(ctx, builds[0], rig.ToolCall{Name: terminal.ToolTerminalList, Arguments: "{}"})
	}
	blocks := make([]string, 0, 8)
	for _, scoped := range builds {
		result, err := invokeRootScoped(ctx, scoped, rig.ToolCall{Name: terminal.ToolTerminalList, Arguments: "{}"})
		if err != nil || result.IsError {
			continue
		}
		for _, block := range strings.Split(result.Content, "\n\n") {
			block = strings.TrimSpace(block)
			if block == "" || block == "no terminal sessions" {
				continue
			}
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return rig.ToolResult{Content: "no terminal sessions"}, nil
	}
	// Deterministic order by the session id line Rig prints first in each block.
	sort.SliceStable(blocks, func(i, j int) bool {
		return firstLine(blocks[i]) < firstLine(blocks[j])
	})
	return rig.ToolResult{Content: strings.Join(blocks, "\n\n")}, nil
}

// firstLine returns the first line of a block.
func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

// executionArgument extracts one string argument from a tool call.
func executionArgument(arguments string, keys ...string) string {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// executionResultID extracts the value of one "key: value" line from a tool
// result (job_start -> "job_id: ...", terminal_open -> "session_id: ...").
func executionResultID(content, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// --- durable execution lifecycle events ------------------------------------

// executionEvents writes execution lifecycle events to the optional durable
// store. Failures are logged and never affect the tool result.
type executionEvents struct {
	store contracts.Store
	logf  func(string, ...any)
}

// append writes one event for the session stamped into a tool call context.
func (e *executionEvents) append(ctx context.Context, sessionID, eventType string, payload map[string]any) {
	if e == nil || e.store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		if e.logf != nil {
			e.logf("[toolkit] marshal %s: %v", eventType, err)
		}
		return
	}
	if err := e.store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: sessionID,
		Type:      eventType,
		Time:      time.Now(),
		Payload:   encoded,
	}); err != nil && e.logf != nil {
		e.logf("[toolkit] append %s: %v", eventType, err)
	}
}

// sessionOf returns the agent session stamped into the tool call context.
func sessionOf(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(contracts.AgentSessionFromContext(ctx))
}

// recordJobStarted remembers the job's owner build and records the audit event.
func (g *rebindGroup) recordJobStarted(ctx context.Context, scoped *rootScoped, call rig.ToolCall, result rig.ToolResult) {
	id := executionResultID(result.Content, "job_id")
	if id == "" || scoped == nil {
		return
	}
	sessionID := sessionOf(ctx)
	g.mu.Lock()
	if g.jobOwners == nil {
		g.jobOwners = make(map[string]*rootScoped)
	}
	g.jobOwners[id] = scoped
	if sessionID != "" {
		g.jobSessions[id] = sessionID
	}
	g.mu.Unlock()
	if g.events == nil {
		return
	}
	g.events.append(ctx, sessionID, contracts.ExecutionJobStarted, map[string]any{
		"id":      id,
		"command": executionArgument(call.Arguments, "command"),
		"cwd":     executionArgument(call.Arguments, "cwd"),
		"root":    scoped.root,
		"mode":    string(scoped.mode),
	})
}

// recordTerminalOpened remembers the terminal's owner build and records the
// audit event (transport comes from the open result, not the arguments).
func (g *rebindGroup) recordTerminalOpened(ctx context.Context, scoped *rootScoped, call rig.ToolCall, result rig.ToolResult) {
	id := executionResultID(result.Content, "session_id")
	if id == "" || scoped == nil {
		return
	}
	sessionID := sessionOf(ctx)
	g.mu.Lock()
	if g.terminalOwners == nil {
		g.terminalOwners = make(map[string]*rootScoped)
	}
	g.terminalOwners[id] = scoped
	if sessionID != "" {
		g.terminalSessions[id] = sessionID
	}
	g.mu.Unlock()
	if g.events == nil {
		return
	}
	g.events.append(ctx, sessionID, contracts.ExecutionTerminalOpened, map[string]any{
		"id":        id,
		"cwd":       executionArgument(call.Arguments, "cwd"),
		"transport": executionResultID(result.Content, "transport"),
		"root":      scoped.root,
		"mode":      string(scoped.mode),
	})
}

// recordJobFinished records the audit event for one settled job notice and drops
// its routing entry. The notice does not carry an exit code (Rig only exposes it
// through a job_output read, which would consume the model's output cursor), so
// the event records the DSH terminal state and kind.
func (g *rebindGroup) recordJobFinished(notice jobs.Notice) {
	id := strings.TrimSpace(notice.JobID)
	if id == "" {
		return
	}
	g.mu.Lock()
	sessionID := g.jobSessions[id]
	// Keep the owner entry: finished jobs stay retained by Rig's manager (with
	// their output/exit code) until module unload, so a later job_output/list by
	// id must still route to the manager that owns them. Only the session tag is
	// dropped after the finish event was attributed.
	delete(g.jobSessions, id)
	g.mu.Unlock()
	if g.events == nil {
		return
	}
	g.events.append(context.Background(), sessionID, contracts.ExecutionJobFinished, map[string]any{
		"id":    id,
		"state": string(notice.Status),
		"kind":  notice.Kind,
	})
}

// recordTerminalClosed records the audit event for a closed terminal session and
// drops its routing entry.
func (g *rebindGroup) recordTerminalClosed(ctx context.Context, scoped *rootScoped, id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	g.mu.Lock()
	sessionID := g.terminalSessions[id]
	if sessionID == "" {
		sessionID = sessionOf(ctx)
	}
	delete(g.terminalOwners, id)
	delete(g.terminalSessions, id)
	g.mu.Unlock()
	if g.events == nil {
		return
	}
	g.events.append(ctx, sessionID, contracts.ExecutionTerminalClosed, map[string]any{
		"id":   id,
		"root": scopedRoot(scoped),
	})
}

// scopedRoot returns one build's root ("" for nil).
func scopedRoot(scoped *rootScoped) string {
	if scoped == nil {
		return ""
	}
	return scoped.root
}
