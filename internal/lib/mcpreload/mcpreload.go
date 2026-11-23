// Package mcpreload triggers the toolkit's MCP hot reload after a workspace
// switch. Connectors and their disabled switches are workspace-scoped, so the
// tool/plugin surface must follow the new workspace immediately instead of
// waiting for a restart. The rebuild performs network handshakes, so the trigger
// is asynchronous and a switch never blocks on it.
package mcpreload

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Trigger asks the toolkit to rebuild the MCP connections for the current
// workspace. A composition without the reloader (older test seams, headless
// runs without the toolkit) is a no-op.
func Trigger(ctx cordis.Context) {
	reloader, ok := ctx.Get(contracts.KeyMCPReloader)
	if !ok || reloader == nil {
		return
	}
	go func() {
		_, _ = reloader.Reload(context.Background())
	}()
}
