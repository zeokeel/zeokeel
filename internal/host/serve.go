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
//  3. nothing here touches mygo: no window, no tray, no event loop. The tray
//     shell is its own tiny process that reads gateway.json.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
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
	// uiRoot is nil: the gateway has no native UI. This provides the data dir,
	// settings and log only — the gateway adds the other five services below.
	if err := provideServices(root, opts.DataDir, settings, logger, nil); err != nil {
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

	err = gw.Serve(ctx)
	if err != nil && errors.Is(err, gateway.ErrAlreadyRunning) {
		// A second launch is not an error: report where the running gateway is
		// and let the process exit 0 (desktop second-instance behaviour).
		fmt.Fprintf(out, "%s: %v\n", appName, err)
		return nil
	}
	return err
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
