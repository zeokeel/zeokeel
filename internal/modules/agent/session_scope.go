package agent

// Per-run session scoping: a run's tools must know which conversation owns them
// even though the toolkit registers one process-wide tool instance. The wrapper
// stamps every tool call's context with contracts.WithAgentSession and the run's
// origin (contracts.WithAgentRunOrigin); session-aware toolkit surfaces (the
// shared task board, completion receipts, the DSH goal tools) read them and
// route to that session's own state, and goal mutations can enforce the "direct
// human turn" rule from the origin.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sessionScopedTool is a rig.Tool proxy that attaches the run's session ID and
// origin to the invocation context.
type sessionScopedTool struct {
	inner     rig.Tool
	sessionID string
	origin    string
}

func (t *sessionScopedTool) Name() string           { return t.inner.Name() }
func (t *sessionScopedTool) Description() string    { return t.inner.Description() }
func (t *sessionScopedTool) Schema() map[string]any { return t.inner.Schema() }
func (t *sessionScopedTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	ctx = contracts.WithAgentSession(ctx, t.sessionID)
	ctx = contracts.WithAgentRunOrigin(ctx, t.origin)
	return t.inner.Invoke(ctx, call)
}

// sessionScopedNoticeTool keeps a wrapped tool's rig.NoticeSource capability
// alive so background-job completion notices still reach the model.
type sessionScopedNoticeTool struct {
	*sessionScopedTool
}

func (t *sessionScopedNoticeTool) PendingNotices() []string {
	if source, ok := t.sessionScopedTool.inner.(rig.NoticeSource); ok {
		return source.PendingNotices()
	}
	return nil
}

// withAgentSession wraps every tool with the run's session ID and no explicit
// origin (the legacy form used by callers without a run origin).
func withAgentSession(tools []rig.Tool, sessionID string) []rig.Tool {
	return withAgentScope(tools, sessionID, "")
}

// withAgentScope wraps every tool with the run's session ID and origin. An empty
// session ID and empty origin return the tools unchanged (headless probes and
// direct service calls keep the process-wide behavior).
func withAgentScope(tools []rig.Tool, sessionID, origin string) []rig.Tool {
	if len(tools) == 0 || (sessionID == "" && origin == "") {
		return tools
	}
	wrapped := make([]rig.Tool, len(tools))
	for index, tool := range tools {
		if tool == nil {
			wrapped[index] = tool
			continue
		}
		scoped := &sessionScopedTool{inner: tool, sessionID: sessionID, origin: origin}
		if _, ok := tool.(rig.NoticeSource); ok {
			wrapped[index] = &sessionScopedNoticeTool{sessionScopedTool: scoped}
			continue
		}
		wrapped[index] = scoped
	}
	return wrapped
}
