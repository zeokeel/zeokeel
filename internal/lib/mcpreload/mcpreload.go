// Package mcpreload triggers the toolkit's MCP hot reload after a workspace
// switch. Connectors and their disabled switches are workspace-scoped, so the
// tool/plugin surface must follow the new workspace immediately instead of
// waiting for a restart. The rebuild performs network handshakes, so the trigger
// is asynchronous and a switch never blocks on it.
package mcpreload

import (
	"context"
	"strings"

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
		if _, err := reloader.Reload(context.Background()); err != nil {
			reportFailure(ctx, err)
		}
	}()
}

// reportFailure surfaces a failed workspace-switch reload: the log line always
// lands, and the host notifier raises a localized error toast when present.
// Success stays silent (the switch itself already succeeded).
func reportFailure(ctx cordis.Context, err error) {
	if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
		logger.Printf("[mcp] workspace switch reload failed: %v", err)
	}
	notifier, ok := ctx.Get(contracts.KeyNotifier)
	if !ok || notifier == nil {
		return
	}
	message := "MCP 连接器重载失败：" + err.Error()
	if locale, ok := ctx.Get(contracts.KeyLocale); ok && locale != nil &&
		strings.EqualFold(strings.TrimSpace(locale.Language()), "en") {
		message = "MCP connectors failed to reload: " + err.Error()
	}
	notifier.Notify(contracts.NotifyError, message)
}
