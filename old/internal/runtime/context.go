package runtime

import "context"

// sessionIDContextKey carries the session id through the eino call chain so
// the provider adapter can attach it to Provider requests.
type sessionIDContextKey struct{}

func withSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

func sessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sessionID, _ := ctx.Value(sessionIDContextKey{}).(string)
	return sessionID
}

// SessionIDFromContext returns the session id the runtime attached to a tool
// invocation's context, or "" when the call is outside a session. Tools use it
// to keep per-session state instead of sharing one list across every session.
func SessionIDFromContext(ctx context.Context) string { return sessionIDFromContext(ctx) }

// WithSessionID returns a context carrying sessionID, for tools and tests that
// invoke outside the agent loop.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return withSessionID(ctx, sessionID)
}
