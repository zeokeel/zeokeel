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
	// The autostart entry follows the stored setting; without this call the
	// settings switch wrote a value nothing ever read (AUDIT-2026-11-24 A-7).
	if err := syncAutostart(settings, logger); err != nil {
		logger.Printf("%s: autostart: %v", appName, err)
	}
	// Later toggles in the settings screen apply immediately.
	settings.WatchAutostart(func() {
		if err := syncAutostart(settings, logger); err != nil {
			logger.Printf("%s: autostart: %v", appName, err)
		}
	})
	// A marker left by a crash between "install now" and the install must not
	// install anything on a start where nobody asked.
	clearStaleInstallMarker(opts.DataDir, logger)
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
		DataDir:          opts.DataDir,
		Port:             opts.Port,
		Product:          assembly.ProductServe,
		Version:          opts.AppVersion,
		Logger:           logger,
		Out:              out,
		AutoCheckUpdates: true,
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

	// The OS notification sink lives in this process (the tray shell), so the
	// gateway's System notifications reach the notification center here.
	installSystemNotifier(notifierFrom(root), settings, logger)

	// Pure gateway: the process has no UI of its own any more (the browser
	// client is the window), so serving is the whole job. Wait for the service
	// to end.
	err = <-serveErr
	// The gateway stops before the installer runs: that is the whole point of
	// the marker handoff (a running executable cannot replace itself, and the
	// listener and data directory must be free). Failures are logged and keep
	// the plan for the next start.
	maybeInstallRequestedUpdate(opts.DataDir, logger)

	if err != nil && errors.Is(err, gateway.ErrAlreadyRunning) {
		// A second launch is not an error: report where the running gateway is
		// and let the process exit 0 (desktop second-instance behaviour).
		fmt.Fprintf(out, "%s: %v\n", appName, err)
		return nil
	}
	return err
}

// notifierFrom reads the gateway's notifier service off the kernel root.
func notifierFrom(root cordis.Context) contracts.Notifier {
	notifier, ok := root.Get(contracts.KeyNotifier)
	if !ok {
		return nil
	}
	return notifier
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
