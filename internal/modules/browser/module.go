// Package browser is ZeoKeel's local-browser capability (plan §9.3): it drives
// the user's own Chrome/Edge over the Chrome DevTools Protocol instead of
// running a cloud desktop. The window is visible by default so the user can
// watch and take over; headless is a setting. It requires nothing from other
// modules — a browser needs no inbox and no store — and provides KeyBrowser.
// When the host toolkit registry is present it also contributes the three
// model-facing tools (browser_open, browser_title, browser_screenshot) defined
// in tools.go, so the agent can open and inspect pages; without a registry the
// service is still provided and simply has no tool face.
package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
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
		// The tool registry is optional: a product assembled without the toolkit
		// still gets the browser service, it just has no model-facing tool face.
		if registry, ok := ctx.Get(hosttools.KeyRegistry); ok && registry != nil {
			registerBrowserTools(ctx, registry, adapter)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyBrowser)).
		WithName("ZeoKeel browser (CDP)")
}

// registerBrowserTools publishes the browser tool face on the host tool
// registry. Registration is best-effort: a tool that cannot be registered is
// skipped (the service stays provided), and the module's unload removes every
// tool it added, so a reloaded module cannot leave stale tools behind.
func registerBrowserTools(ctx cordis.Context, registry *rigtools.Registry, service contracts.BrowserService) {
	for _, tool := range browserTools(service) {
		registerBrowserTool(ctx, registry, tool)
	}
}

// registerBrowserTool registers one tool, replacing a same-named tool that some
// other assembly layer already claimed. The module's tools are bound to the live
// browser service, so ours wins over a built-in placeholder with the same name
// (the same rule the automation module applies to its schedule_* tools).
func registerBrowserTool(ctx cordis.Context, registry *rigtools.Registry, tool rig.Tool) {
	name := tool.Name()
	registerErr := registry.Register(tool)
	if registerErr != nil && strings.Contains(registerErr.Error(), "duplicate tool name") {
		registry.Unregister(name)
		registerErr = registry.Register(tool)
	}
	if registerErr != nil {
		logBrowserf(ctx, "%s: register tool %q: %v", ModuleID, name, registerErr)
		return
	}
	if _, effectErr := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(func() {
			registry.Unregister(name)
		})}
	}); effectErr != nil {
		registry.Unregister(name)
		logBrowserf(ctx, "%s: own tool %q cleanup: %v", ModuleID, name, effectErr)
	}
}

// logBrowserf logs through the host logger when one is present. The browser
// module has no logger requirement (a browser-less product must boot), so a
// registration problem is reported and otherwise dropped.
func logBrowserf(ctx cordis.Context, format string, args ...any) {
	if logger, ok := ctx.Get(contracts.KeyLogger); ok && logger != nil {
		logger.Printf(format, args...)
	}
}
