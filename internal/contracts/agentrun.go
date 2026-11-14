package contracts

// Run-scoped agent origin context. A tool call executes inside one agent run;
// goal mutations and session provenance need to know who started that run: a
// direct human turn, the engine-side goal continuation driver, a scheduled
// automation run or a delegated child. The agent module stamps every tool
// invocation's context with the run origin (like the session ID and the
// workspace root), so a process-wide tool implementation can enforce DSH's
// "direct human turn" rules without changing the rig.Tool contract.

import "context"

// Agent run origins (the values AgentRunOriginFromContext returns).
const (
	// AgentRunOriginUser is a run started by a direct human turn: the user sent
	// a message (or retried one) in this session.
	AgentRunOriginUser = "user"
	// AgentRunOriginGoal is a run started by the engine-side goal continuation
	// driver.
	AgentRunOriginGoal = "goal"
	// AgentRunOriginAutomation is a run started by a scheduled automation task
	// (the automation backend records the session origin setting).
	AgentRunOriginAutomation = "automation"
	// AgentRunOriginSubagent is a run of a delegated child (synchronous
	// subagent, teammate or team child).
	AgentRunOriginSubagent = "subagent"
)

// agentRunOriginContextKey is the context key type for one run's origin.
type agentRunOriginContextKey struct{}

// WithAgentRunOrigin returns a context carrying the origin of the run the tool
// call belongs to. An empty origin returns the context unchanged (callers
// outside any agent run keep the legacy behavior).
func WithAgentRunOrigin(ctx context.Context, origin string) context.Context {
	if origin == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, agentRunOriginContextKey{}, origin)
}

// AgentRunOriginFromContext returns the run's origin ("" when the call is not
// inside an agent run, e.g. a direct service invocation or a headless probe).
func AgentRunOriginFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(agentRunOriginContextKey{}).(string)
	return value
}

// IsDirectHumanAgentRun reports whether origin is a direct human turn (or an
// unstamped host call, which is the human/host path rather than an autonomous
// one). DSH requires a direct human turn for goal create/edit/pause/resume.
func IsDirectHumanAgentRun(origin string) bool {
	return origin == "" || origin == AgentRunOriginUser
}
