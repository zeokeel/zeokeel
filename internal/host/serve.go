package host

// The gateway product (--product serve): the backend of the browser client.
//
// It is the headless assembly plus the gateway control plane, with no window
// and no uiroot.Root (docs/TRANSPORT.md §2.2). Three things differ from the
// headless product and each one is load-bearing:
//
//  1. the entry tree is assembly.ServeEntries(): headless entries with the
//     approvals policy unpinned from "auto" (otherwise the browser would never
//     receive an approval request) and host-tools made interactive (otherwise
//     ask_user stays permanently unavailable);
//  2. the gateway provides the five host services the headless assembly never
//     gets, because provideServices returns early when uiRoot is nil: user
//     interaction, artifact presenter, plan reviewer, notifier and foreground
//     session (TRANSPORT §7.2);
//  3. the tray is optional and best effort: the gateway starts first, then the
//     mygo event loop (main goroutine) hosts only the tray icon, whose "show"
//     opens the browser client. A tray that cannot start (no display, no tray
//     host) is a warning; --no-tray skips it entirely (TRANSPORT §2.2).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/gateway"
)

// runServe runs the gateway product until ctx is canceled.
//
// The instance lock, port negotiation and gateway.json all live in the gateway
// package: a second launch returns gateway.ErrAlreadyRunning and exits 0 after
// printing the running instance's URL, which is the desktop second-instance
// behaviour (TRANSPORT §2.4).
func runServe(ctx context.Context, opts Options, out io.Writer) error {
	logger := NewLogger(opts.DataDir)
	defer func() { _ = logger.Close() }()

	settings, err := NewSettings(opts.DataDir)
	if err != nil {
		return err
	}
	if err := applyProxySettings(settings); err != nil {
		logger.Printf("%s: proxy settings: %v", appName, err)
	}
	// The gateway runs the same long-lived agent backend, so a scheduled update
	// installs here too (failures are logged and keep the pending plan).
	applyPendingUpdateOnStartup(opts.DataDir, opts.AppVersion, logger)

	root := cordis.Root()
	defer func() {
		if err := root.DisposeAll(); err != nil {
			logger.Printf("%s: dispose kernel root: %v", appName, err)
		}
	}()
	// The gateway host provides the data dir, settings and log; the gateway adds
	// the five window-owned services itself (TRANSPORT §7.2).
	if err := provideServices(root, opts.DataDir, settings, logger); err != nil {
		return err
	}
	gw, err := gateway.New(root, gateway.Options{
		DataDir: opts.DataDir,
		Port:    opts.Port,
		Product: assembly.ProductServe,
		Version: opts.AppVersion,
		Logger:  logger,
		Out:     out,
	})
	if err != nil {
		return err
	}
	// Provided before the assembly loads: the host-tools module reads these
	// during activation and registers its capabilities from what it finds.
	if err := gw.Provide(root); err != nil {
		return err
	}
	moduleLister, err := provideModuleLister(root)
	if err != nil {
		return fmt.Errorf("%s: provide module lister: %w", appName, err)
	}

	entries, err := assembly.ServeEntries()
	if err != nil {
		return fmt.Errorf("%s: prepare serve entries: %w", appName, err)
	}
	loader, err := assembly.LoadEntriesWithBuildInfo(root, entries, buildInfo(opts))
	if err != nil {
		return fmt.Errorf("%s: load product %q: %w", appName, assembly.ProductServe, err)
	}
	defer func() {
		if err := loader.Close(); err != nil {
			logger.Printf("%s: close assembly: %v", appName, err)
		}
	}()
	moduleLister.set(moduleInfos(loadedModulesFromInstances(loader.Instances())))
	// Same reason as the headless product: open the default workspace so a run
	// started from the browser does not work against nothing.
	ensureDefaultWorkspace(root, opts.DataDir, logger)

	logger.Printf("%s: %s product %q loaded", appName, opts.versionString(), opts.Product)
	fmt.Fprintf(out, "%s\n", opts.versionString())
	fmt.Fprintf(out, "product: %s (gateway; no window, open the URL below in a browser)\n", assembly.ProductServe)
	for _, instance := range loader.Instances() {
		fmt.Fprintf(out, "  instance %-10s module=%-22s state=%s\n",
			instance.ID(), instance.ModuleID(), instance.State())
	}
	for _, warning := range loader.Warnings() {
		fmt.Fprintf(out, "  warning: %s\n", warning)
	}

	// The gateway owns the service: start it before the tray so a tray that
	// cannot start never costs the gateway. serveErr carries its verdict.
	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- gw.Serve(serveCtx) }()

	if !trayEnabled(opts.NoTray) {
		// --no-tray, or a Linux session without a display: pure gateway. Wait for
		// the service to end instead of starting the mygo event loop.
		err = <-serveErr
	} else {
		err = runServeWithTray(ctx, opts, root, gw, settings, logger, serveErr, cancelServe)
	}
	if err != nil && errors.Is(err, gateway.ErrAlreadyRunning) {
		// A second launch is not an error: report where the running gateway is
		// and let the process exit 0 (desktop second-instance behaviour).
		fmt.Fprintf(out, "%s: %v\n", appName, err)
		return nil
	}
	return err
}

// runServeWithTray runs the gateway (already started in a goroutine) alongside
// the tray shell, on the main goroutine. Whoever ends first ends the process:
// the tray quitting cancels the gateway, and the gateway ending quits the tray.
func runServeWithTray(ctx context.Context, opts Options, root cordis.Context, gw *gateway.Gateway,
	settings *Settings, logger *Logger, serveErr <-chan error, cancelServe context.CancelFunc) error {
	// The tray mirrors the live counters the desktop read through uiroot: running
	// conversations and pending approvals, both from the in-process services.
	status := func() (running, approvals int) {
		if chat, ok := root.Get(contracts.KeyChat); ok && chat != nil {
			for _, session := range chat.Sessions() {
				if session.Running {
					running++
				}
			}
		}
		if service, ok := root.Get(contracts.KeyApprovals); ok && service != nil {
			approvals = len(service.Pending())
		}
		return running, approvals
	}
	english := func() bool {
		if service, ok := root.Get(contracts.KeyLocale); ok && service != nil {
			return service.Language() == "en"
		}
		return false
	}
	trayCtx, stopTray := context.WithCancel(ctx)
	defer stopTray()

	// Watch the gateway: when it ends, the tray must end too.
	gatewayEnded := make(chan error, 1)
	go func() {
		err := <-serveErr
		gatewayEnded <- err
		stopTray()
	}()

	runErr := startTrayShell(trayCtx, opts.DataDir, settings, status, english, func() {
		cancelServe()
	}, logger)
	if runErr != nil {
		// The event loop itself failed to start (no display backend, no tray
		// host): the gateway keeps serving. This is the documented degradation,
		// so it is a warning, not an error, and the service keeps running until
		// the process context ends.
		logger.Printf("%s: tray shell unavailable (%v); the gateway keeps serving", appName, runErr)
		err := <-gatewayEnded
		if err != nil && !errors.Is(err, gateway.ErrAlreadyRunning) {
			return err
		}
		return nil
	}
	// The tray ended through its quit item: stop the gateway and wait for it.
	cancelServe()
	err := <-gatewayEnded
	if err != nil && !errors.Is(err, gateway.ErrAlreadyRunning) {
		return err
	}
	return nil
}

// parseServePort validates the --port flag value: a port in 1..65535, or 0 for
// the documented default range.
func parseServePort(value int) (int, error) {
	if value < 0 || value > 65535 {
		return 0, fmt.Errorf("%s: --port %d is out of range (0-65535)", appName, value)
	}
	return value, nil
}

// servePortConflict reports the flag combinations that cannot carry --port.
// product is the resolved product name (the desktop default when --product was
// not given), so "--port 1234" alone is rejected instead of silently starting
// the desktop product on a port it never listens to.
func servePortConflict(product string, acp, headless bool, port int) error {
	if port == 0 {
		return nil
	}
	if acp || headless {
		return fmt.Errorf("%s: --port only applies to --product %s", appName, assembly.ProductServe)
	}
	if strings.TrimSuffix(strings.TrimSpace(product), ".json") != assembly.ProductServe {
		return fmt.Errorf("%s: --port only applies to --product %s", appName, assembly.ProductServe)
	}
	return nil
}
