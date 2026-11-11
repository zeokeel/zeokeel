package contracts

import (
	"context"
	"strings"
)

// workspaceRootKey carries the workspace root one run's tool calls must use.
//
// The toolkit's root-scoped tools normally follow the workspace that is
// current at call time, but several sessions (and automation runs) can execute
// concurrently while each belongs to a different project. The agent therefore
// wraps a run's tools with WithWorkspaceRoot(session root); the toolkit's
// dispatch then resolves the file/shell/terminal/job tools against that root
// instead of the process-wide current workspace.
type workspaceRootKey struct{}

// WithWorkspaceRoot returns a context carrying root as the run's workspace
// root. An empty root is a no-op (the toolkit keeps following the current
// workspace), so callers can attach the helper unconditionally.
func WithWorkspaceRoot(ctx context.Context, root string) context.Context {
	root = strings.TrimSpace(root)
	if root == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workspaceRootKey{}, root)
}

// WorkspaceRootFromContext returns the run's workspace root override ("" when
// the call follows the current workspace).
func WorkspaceRootFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(workspaceRootKey{}).(string)
	return strings.TrimSpace(value)
}
