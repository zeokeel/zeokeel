package toolkit

// Per-call timeout for MCP tool calls (plan M2.5 batch A, G14 #6: "每 server
// 超时 + 有限重试" — the half that matters first is that one slow server cannot
// drag a whole turn).
//
// Startup is already bounded (mcpStartupOverallTimeout) and servers can carry
// TimeoutMs, but a tools/call had no product-level bound at the tool boundary
// and, just as importantly, no readable failure: a server that accepts the
// request and never answers stalls the turn until the transport's own
// diagnostic 30s default, and the caller only sees a bare context deadline.
// This file adds the missing half:
//
//   - every manager tool the controller puts in the shared registry is wrapped,
//     so one call cannot outlive its server's budget;
//   - the budget is the row's TimeoutMs when set, else mcpDefaultCallTimeout
//     (the same value is also handed to the Rig client, so the transport cannot
//     fire first with an untyped deadline);
//   - expiry returns *MCPCallTimeoutError (server + tool + budget) instead of a
//     bare context error, so the tool result reads "server S did not answer
//     tool T within X" and the turn continues with the next step;
//   - expiry does NOT tear the server down: only that request's context is
//     cancelled, which makes the Rig transport forget the pending id and leaves
//     the connection (child process / HTTP session) intact, so the next call to
//     the same server works. TestMCPCallTimeoutKeepsServerUsableAfterExpiry
//     pins that with a fake slow server.
//
// A timed-out call is deliberately not retried here: retry/backoff is a
// separate item and retrying a wedged server would only make the turn slower.

import (
	"context"
	"errors"
	"fmt"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
)

// mcpDefaultCallTimeout is the per-call budget for an MCP tool when the server
// row does not set TimeoutMs.
//
// Why 60s:
//   - it is deliberately larger than the Rig SDK's mcp.DefaultTimeout (30s),
//     which was sized for protocol chatter (initialize / tools/list / ping) and
//     is too tight for a tool that does real work;
//   - it fits inside what a user will wait for one step of a streaming turn, so
//     a wedged server costs one visibly failed call instead of an apparently
//     frozen run (the M2.5 acceptance criterion);
//   - a server that legitimately needs longer (browser automation, large
//     crawls) says so with its own TimeoutMs, which always wins.
const mcpDefaultCallTimeout = 60 * time.Second

// mcpServerCallTimeout resolves one server's per-call budget: the configured
// TimeoutMs when set, else mcpDefaultCallTimeout.
func mcpServerCallTimeout(server MCPServer) time.Duration {
	if server.TimeoutMs > 0 {
		return time.Duration(server.TimeoutMs) * time.Millisecond
	}
	return mcpDefaultCallTimeout
}

// MCPCallTimeoutError reports that one MCP tool call exceeded its per-call
// budget. It is a typed error so a caller can tell "this server is slow" (the
// connection is fine, other servers and calls are unaffected) from a protocol
// failure, and so the transcript can name the server and the tool instead of
// showing a bare context deadline.
//
// It deliberately does not unwrap to context.DeadlineExceeded: the run itself
// is still alive, and the runtime classifies a DeadlineExceeded run error as a
// whole-run timeout (internal/modules/agent/agent.go). Use errors.As to detect
// this error.
type MCPCallTimeoutError struct {
	// Server is the configured (display) name of the MCP server.
	Server string
	// Tool is the model-visible tool name (mcp__<server>__<tool>).
	Tool string
	// Timeout is the budget that expired.
	Timeout time.Duration
}

// Error implements error.
func (e *MCPCallTimeoutError) Error() string {
	return fmt.Sprintf("mcp: server %q did not answer tool %q within %s (the server is still connected; other calls are unaffected)",
		e.Server, e.Tool, e.Timeout)
}

// mcpCallTimeoutTool wraps one MCP tool with its server's per-call budget. Only
// Invoke is bounded; Name/Description/Schema delegate unchanged so the registry,
// tool_search and the approval surface keep seeing the real tool.
type mcpCallTimeoutTool struct {
	inner rig.Tool
	// server is the configured display name used in the timeout error.
	server string
	// tool is the model-visible tool name used in the timeout error.
	tool string
	// timeout is the per-call budget; a non-positive value disables the wrapper.
	timeout time.Duration
}

var _ rig.Tool = (*mcpCallTimeoutTool)(nil)

// Name implements rig.Tool.
func (t *mcpCallTimeoutTool) Name() string { return t.inner.Name() }

// Description implements rig.Tool.
func (t *mcpCallTimeoutTool) Description() string { return t.inner.Description() }

// Schema implements rig.Tool.
func (t *mcpCallTimeoutTool) Schema() map[string]any { return t.inner.Schema() }

// Invoke implements rig.Tool: it runs the call under the per-call budget and
// reports an expired budget as *MCPCallTimeoutError.
//
// The bound is enforced through the call context, which both MCP transports
// honor (stdio returns ctx.Err() from its select, HTTP builds the request with
// WithContext), so the pending request is abandoned cleanly instead of leaking
// and the connection stays usable.
func (t *mcpCallTimeoutTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	if t.inner == nil {
		return rig.ToolResult{}, errors.New("mcp: timeout wrapper has no inner tool")
	}
	if t.timeout <= 0 {
		return t.inner.Invoke(ctx, call)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	result, err := t.inner.Invoke(callCtx, call)
	if err == nil {
		return result, nil
	}
	// Only our own budget counts as a per-call timeout. A caller-level deadline
	// or cancellation (or a genuine protocol error) passes through unchanged, so
	// the run keeps the original meaning of its own context: if the caller's
	// deadline expires first we return before our budget elapses.
	if callCtx.Err() != nil && errors.Is(err, context.DeadlineExceeded) && time.Since(started) >= t.timeout {
		return rig.ToolResult{IsError: true}, &MCPCallTimeoutError{
			Server:  t.server,
			Tool:    t.tool,
			Timeout: t.timeout,
		}
	}
	return result, err
}

// withMCPCallTimeout wraps one MCP tool with a per-call budget. A nil tool or a
// non-positive budget returns the tool unchanged.
func withMCPCallTimeout(tool rig.Tool, server string, timeout time.Duration) rig.Tool {
	if tool == nil || timeout <= 0 {
		return tool
	}
	return &mcpCallTimeoutTool{
		inner:   tool,
		server:  server,
		tool:    tool.Name(),
		timeout: timeout,
	}
}
