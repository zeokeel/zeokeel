// Package browser is ZeoKeel's local-browser capability (plan §9.3): it drives
// the user's own Chrome/Edge over the Chrome DevTools Protocol instead of
// running a cloud desktop. The window is visible by default so the user can
// watch and take over; headless is a setting. It requires nothing from other
// modules — a browser needs no inbox and no store — and provides KeyBrowser.
package browser

import (
	"context"
	"fmt"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/browser"

// Module is the browser descriptor. It provides contracts.KeyBrowser and has no
// requirements: with no endpoint configured it still loads and reports a
// disconnected status, so the product never fails to boot because a browser is
// not installed or not started yet.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		// With an endpoint configured and no transport injected, dial the real
		// CDP socket here. A browser that is not running must not block boot: the
		// failure is logged and the adapter serves a disconnected status.
		if cfg.Endpoint != "" && cfg.Transport == nil {
			dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			socket, dialErr := DialTransport(dialCtx, cfg.Endpoint, DialOptions{})
			cancel()
			if dialErr != nil {
				if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
					logger.Printf("browser: %v; starting without a browser connection", dialErr)
				}
				cfg.Endpoint = ""
			} else {
				cfg.Transport = socket
				if _, effectErr := ctx.Effect(func() cordis.Effect {
					return cordis.Effect{cordis.CleanupOf(func() { _ = socket.Close() })}
				}); effectErr != nil {
					_ = socket.Close()
					return fmt.Errorf("%s: own socket shutdown: %w", ModuleID, effectErr)
				}
			}
		}
		adapter, err := New(cfg)
		if err != nil {
			return fmt.Errorf("%s: open adapter: %w", ModuleID, err)
		}
		if _, err := ctx.Provide(contracts.KeyBrowser, contracts.BrowserService(adapter)); err != nil {
			return fmt.Errorf("%s: provide browser: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyBrowser)).
		WithName("ZeoKeel browser (CDP)")
}
