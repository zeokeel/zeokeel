package agent

// Per-run workspace-root scoping: a run's tools must resolve file/shell/terminal
// /job operations against the session's project even while another workspace is
// current. The wrapper stamps every tool call's context with
// contracts.WithWorkspaceRoot; the toolkit's root-scoped proxies read it and
// dispatch to that root's build instead of the process-wide current workspace.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// scopedTool is a rig.Tool proxy that attaches the run's workspace root to the
// invocation context.
type scopedTool struct {
	inner rig.Tool
	root  string
}

func (t *scopedTool) Name() string           { return t.inner.Name() }
func (t *scopedTool) Description() string    { return t.inner.Description() }
func (t *scopedTool) Schema() map[string]any { return t.inner.Schema() }
func (t *scopedTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	return t.inner.Invoke(contracts.WithWorkspaceRoot(ctx, t.root), call)
}

// scopedNoticeTool keeps a wrapped tool's rig.NoticeSource capability alive so
// background-job completion notices still reach the model.
type scopedNoticeTool struct {
	*scopedTool
}

func (t *scopedNoticeTool) PendingNotices() []string {
	if source, ok := t.scopedTool.inner.(rig.NoticeSource); ok {
		return source.PendingNotices()
	}
	return nil
}

// withWorkspaceRoot wraps every tool with the run's workspace root. An empty
// root returns the tools unchanged: an unbound/headless run keeps the previous
// current-workspace behavior.
func withWorkspaceRoot(tools []rig.Tool, root string) []rig.Tool {
	if len(tools) == 0 || root == "" {
		return tools
	}
	wrapped := make([]rig.Tool, len(tools))
	for index, tool := range tools {
		if tool == nil {
			continue
		}
		scoped := &scopedTool{inner: tool, root: root}
		if _, ok := tool.(rig.NoticeSource); ok {
			wrapped[index] = &scopedNoticeTool{scopedTool: scoped}
			continue
		}
		wrapped[index] = scoped
	}
	return wrapped
}
