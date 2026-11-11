package contracts

// Run-scoped agent session context. A tool call executes inside one agent run;
// per-session surfaces (the shared task board, notice receipts) need to know
// which session owns that run. The agent module stamps every tool invocation's
// context with the run's session ID (like the workspace root), so a process-wide
// tool implementation can route to the session's own state without changing the
// rig.Tool contract.

import "context"

// agentSessionContextKey is the context key type for one run's session ID.
type agentSessionContextKey struct{}

// WithAgentSession returns a context carrying the agent session of the run the
// tool call belongs to. An empty session ID returns the context unchanged.
func WithAgentSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, agentSessionContextKey{}, sessionID)
}

// AgentSessionFromContext returns the run's session ID ("" when the call is not
// inside a session-scoped run, e.g. a direct service invocation or a headless
// probe).
func AgentSessionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(agentSessionContextKey{}).(string)
	return value
}
