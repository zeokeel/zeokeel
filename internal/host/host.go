// Package host is ZeoKeel's process host.
//
// It owns everything the kernel and the UI root do not: the command line, the
// data directory, the settings and log files, the mygo application lifecycle
// (main thread, window, quit) and the headless product.
//
// The host is deliberately thin. For the desktop product it:
//
//  1. creates the kernel root context and provides the services modules
//     require (settings, log, and the uiroot UI services);
//  2. loads one embedded assembly on the main thread inside mygo's WhenReady
//     callback, so every UI module registers its contributions on the UI
//     thread;
//  3. validates the UI root, creates the window around uiroot's single view
//     function and wires window invalidation to it;
//  4. runs the event loop and unwinds in order on quit: close the assembly
//     (UI contributions unregister in reverse), close the window, dispose the
//     kernel root.
//
// For the headless product it loads the same registry with an assembly that has
// no UI modules, prints the instance states and waits for SIGINT/SIGTERM. It
// never touches mygo, uiroot or the display environment.
package host

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// Product identity and process defaults.
const (
	appName            = "ZeoKeel"
	dataDirEnv         = "ZEOKEEL_DATA_DIR"
	defaultDataDirName = ".zeokeel"

	// Settings keys the host owns. Modules use their own namespaces.
	settingDark     = "ui.theme.dark"
	settingLanguage = "ui.language"

	// Window geometry of the desktop product (mygo native window options).
	// The default window is the reading-first size: at 1512x944 the 960 DIP
	// transcript keeps comfortable margins and the composer/dock still leave a
	// useful conversation area. The state key is versioned so this larger
	// default applies once on an existing install (the framework restores the
	// saved geometry under the old key), while later user resizes persist.
	mainWindowWidth     = 1512
	mainWindowHeight    = 944
	mainWindowMinWidth  = 900
	mainWindowMinHeight = 600
	mainWindowStateKey  = "main.v2"
)

// Build identity of the running binary. Release builds fill these with
//
//	-ldflags "-X github.com/zeokeel/zeokeel/internal/host.Version=... \
//	          -X github.com/zeokeel/zeokeel/internal/host.Commit=... \
//	          -X github.com/zeokeel/zeokeel/internal/host.BuildTime=..."
//
// A plain `go build` / `go test` keeps the defaults.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Options is one process run's configuration. Zero values are meaningful:
// the product defaults to desktop, the data directory to $ZEOKEEL_DATA_DIR or
// ~/.zeokeel, and build identity to the ldflags values.
type Options struct {
	// Product selects the embedded assembly product (see assembly.Products).
	Product string
	// DataDir is the per-product data directory. Settings and logs live here.
	DataDir string
	// AppVersion, AppCommit and AppBuildTime are the running binary's build
	// identity; the host overlays them onto the appinfo module's config.
	AppVersion   string
	AppCommit    string
	AppBuildTime string
	// ACP runs the Agent Client Protocol stdio server instead of the desktop
	// or headless wait loop (editor integration).
	ACP bool
}

// normalize fills zero-valued options with their process defaults. Product
// accepts either the embedded name or its file name ("desktop" or
// "desktop.json").
func (o Options) normalize() Options {
	o.Product = strings.TrimSpace(o.Product)
	o.Product = strings.TrimSuffix(o.Product, ".json")
	if o.Product == "" {
		o.Product = assembly.ProductDesktop
	}
	if strings.TrimSpace(o.DataDir) == "" {
		o.DataDir = defaultDataDir()
	}
	if o.AppVersion == "" {
		o.AppVersion = Version
	}
	if o.AppCommit == "" {
		o.AppCommit = Commit
	}
	if o.AppBuildTime == "" {
		o.AppBuildTime = BuildTime
	}
	return o
}

// versionString is the single line printed by --version and written to the log
// header. It carries the runtime facts a bug report needs.
func versionString() string {
	return formatVersion(appName, Version, Commit, BuildTime)
}

// versionString is versionString for this run's resolved build identity; the
// host uses it in log headers so a programmatic run reports what it was told.
func (o Options) versionString() string {
	return formatVersion(appName, o.AppVersion, o.AppCommit, o.AppBuildTime)
}

func formatVersion(name, version, commit, buildTime string) string {
	return fmt.Sprintf("%s %s (commit %s, built %s, %s/%s, %s)",
		name, orDefault(version, "dev"), orDefault(commit, "unknown"),
		orDefault(buildTime, "unknown"), runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// defaultDataDir resolves the data directory: $ZEOKEEL_DATA_DIR when set, else
// ~/.zeokeel.
func defaultDataDir() string {
	if dir := strings.TrimSpace(os.Getenv(dataDirEnv)); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, defaultDataDirName)
	}
	return filepath.Join(".", defaultDataDirName)
}

// Main is the process entry point behind cmd/zeokeel. It never returns; it
// reports the process exit code instead so the thin main can call os.Exit.
func Main(args []string) int {
	return mainWithStreams(args, os.Stdout, os.Stderr)
}

// mainWithStreams is Main with injectable output streams (tests).
func mainWithStreams(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(appName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	// Print help ourselves: flag's default help goes to the error stream, and
	// --help is a success, not an error.
	flags.Usage = func() {}
	product := flags.String("product", assembly.ProductDesktop, "assembly product to run")
	headless := flags.Bool("headless", false, "run without a window (same as --product headless)")
	dataDir := flags.String("data-dir", "", "data directory")
	showVersion := flags.Bool("version", false, "print version information and exit")
	listProducts := flags.Bool("list-products", false, "print the embedded assembly products and exit")
	acpMode := flags.Bool("acp", false, "run the Agent Client Protocol (ACP) stdio server")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return 0
		}
		fmt.Fprint(stderr, usageText)
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n\n%s", appName, flags.Arg(0), usageText)
		return 2
	}
	if *listProducts {
		for _, name := range assembly.Products() {
			fmt.Fprintln(stdout, name)
		}
		return 0
	}
	if *showVersion {
		fmt.Fprintln(stdout, versionString())
		return 0
	}

	productSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "product" {
			productSet = true
		}
	})
	if *acpMode {
		if productSet && strings.TrimSuffix(strings.TrimSpace(*product), ".json") != assembly.ProductHeadless {
			fmt.Fprintf(stderr, "%s: --acp conflicts with --product %q\n\n%s", appName, *product, usageText)
			return 2
		}
		*headless = true
	}
	if *headless {
		if productSet && strings.TrimSuffix(strings.TrimSpace(*product), ".json") != assembly.ProductHeadless {
			fmt.Fprintf(stderr, "%s: --headless conflicts with --product %q\n\n%s", appName, *product, usageText)
			return 2
		}
		*product = assembly.ProductHeadless
	}

	opts := Options{
		Product:      *product,
		DataDir:      *dataDir,
		AppVersion:   Version,
		AppCommit:    Commit,
		AppBuildTime: BuildTime,
		ACP:          *acpMode,
	}
	if opts.ACP {
		ctx, stop := signal.NotifyContext(context.Background(), terminationSignals...)
		defer stop()
		if err := RunACP(ctx, opts, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", appName, err)
			return 1
		}
		return 0
	}
	if err := Run(opts); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", appName, err)
		return 1
	}
	return 0
}

const usageText = `ZeoKeel - a compile-time modular desktop agent.

Usage:
  zeokeel [flags]

Flags:
  --product <name>   assembly product to run (default "desktop")
  --headless         run without a window (same as --product headless)
  --acp              run the Agent Client Protocol stdio server (editor integration)
  --data-dir <dir>   data directory (default $ZEOKEEL_DATA_DIR or ~/.zeokeel)
  --version          print version information and exit
  --list-products    print the embedded assembly products and exit
  -h, --help         print this help and exit
`

// Run runs one product until it quits. It blocks: for the desktop product
// until the mygo event loop stops, for the headless product until SIGINT or
// SIGTERM.
func Run(opts Options) error {
	opts = opts.normalize()
	switch opts.Product {
	case assembly.ProductDesktop:
		return runDesktop(opts)
	case assembly.ProductHeadless:
		ctx, stop := signal.NotifyContext(context.Background(), terminationSignals...)
		defer stop()
		return runHeadless(ctx, opts, os.Stdout)
	default:
		return fmt.Errorf("no host for product %q (known products: %s)",
			opts.Product, strings.Join(assembly.Products(), ", "))
	}
}

// runHeadless loads the headless assembly (no uiroot, no window, no display
// environment), prints the loaded instance states and waits for ctx to be
// canceled before disposing the assembly and the kernel root.
func runHeadless(ctx context.Context, opts Options, out io.Writer) error {
	logger := NewLogger(opts.DataDir)
	defer func() { _ = logger.Close() }()

	settings, err := NewSettings(opts.DataDir)
	if err != nil {
		return err
	}
	if err := applyProxySettings(settings); err != nil {
		logger.Printf("%s: proxy settings: %v", appName, err)
	}
	// Install a scheduled update at startup (the headless product serves the
	// agent backend and should also apply updates); failures are logged, never
	// fatal, and keep the pending plan.
	applyPendingUpdateOnStartup(opts.DataDir, logger)
	root := cordis.Root()
	defer func() {
		if err := root.DisposeAll(); err != nil {
			logger.Printf("%s: dispose kernel root: %v", appName, err)
		}
	}()
	if err := provideServices(root, opts.DataDir, settings, logger, nil); err != nil {
		return err
	}
	moduleLister, err := provideModuleLister(root)
	if err != nil {
		return fmt.Errorf("%s: provide module lister: %w", appName, err)
	}

	loader, err := assembly.LoadWithBuildInfo(root, opts.Product, buildInfo(opts))
	if err != nil {
		return fmt.Errorf("%s: load product %q: %w", appName, opts.Product, err)
	}
	moduleLister.set(moduleInfos(loadedModulesFromInstances(loader.Instances())))
	logger.Printf("%s: %s product %q loaded", appName, opts.versionString(), opts.Product)
	fmt.Fprintf(out, "%s\n", opts.versionString())
	fmt.Fprintf(out, "product: %s (no window)\n", opts.Product)
	for _, instance := range loader.Instances() {
		fmt.Fprintf(out, "  instance %-10s module=%-22s state=%s\n",
			instance.ID(), instance.ModuleID(), instance.State())
	}
	for _, warning := range loader.Warnings() {
		fmt.Fprintf(out, "  warning: %s\n", warning)
	}
	fmt.Fprintln(out, "ready; waiting for SIGINT or SIGTERM")

	<-ctx.Done()
	fmt.Fprintln(out, "stopping")
	if err := loader.Close(); err != nil {
		return fmt.Errorf("%s: close assembly: %w", appName, err)
	}
	return nil
}

// RunACP loads the headless assembly and serves the Agent Client Protocol over
// the given streams (the process runs as an editor's agent backend).
func RunACP(ctx context.Context, opts Options, in io.Reader, out io.Writer) error {
	opts = opts.normalize()
	opts.ACP = true
	logger := NewLogger(opts.DataDir)
	defer func() { _ = logger.Close() }()

	settings, err := NewSettings(opts.DataDir)
	if err != nil {
		return err
	}
	root := cordis.Root()
	defer func() {
		if err := root.DisposeAll(); err != nil {
			logger.Printf("%s: dispose kernel root: %v", appName, err)
		}
	}()
	if err := provideServices(root, opts.DataDir, settings, logger, nil); err != nil {
		return err
	}
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, buildInfo(opts))
	if err != nil {
		return fmt.Errorf("%s: load product %q: %w", appName, assembly.ProductHeadless, err)
	}
	defer func() {
		if err := loader.Close(); err != nil {
			logger.Printf("%s: close assembly: %v", appName, err)
		}
	}()
	server, err := root.Require(contracts.KeyACP)
	if err != nil {
		return fmt.Errorf("%s: require ACP service: %w", appName, err)
	}
	logger.Printf("%s: ACP server ready over stdio", appName)
	return server.Serve(ctx, in, out)
}

// runDesktop runs the windowed product.
func runDesktop(opts Options) error {
	settings, err := NewSettings(opts.DataDir)
	if err != nil {
		return err
	}
	logger := NewLogger(opts.DataDir)
	defer func() { _ = logger.Close() }()
	logger.Printf("%s: starting %s, data dir %q, log %q",
		appName, opts.versionString(), opts.DataDir, logger.Path())
	mygo.App.SetName(appName)
	mygo.App.SetVersion(orDefault(opts.AppVersion, "dev"))

	// Apply the stored outbound proxy to the process environment before any
	// outbound HTTP happens (clients and child processes inherit it). A bad
	// setting is logged and never blocks startup.
	if err := applyProxySettings(settings); err != nil {
		logger.Printf("%s: proxy settings: %v", appName, err)
	}

	// Single-instance lock, before mygo starts the GUI backend and before Run:
	// RequestSingleInstanceLock forwards this launch's arguments to the first
	// instance and returns false here; the second process must exit without a
	// window. The lock is released by mygo on quit. The handshake needs the GUI
	// event loop, so it is not unit-tested (singleInstanceEnabled is).
	singleInstance := singleInstanceSetting(settings)
	if singleInstance && !mygo.App.RequestSingleInstanceLock() {
		logger.Printf("%s: another instance is already running; this launch exits", appName)
		return nil
	}

	// Install a scheduled update before the GUI and assembly start: the update
	// page wrote <dataDir>/updates/pending.json, the host replaces its own
	// executable here (it takes effect on the next start) and never blocks on a
	// failure (the plan is kept for a retry).
	applyPendingUpdateOnStartup(opts.DataDir, logger)

	// Best-effort XDG autostart entry sync (only the owning instance manages
	// the entry): the appearance page writes the setting; this keeps
	// <configHome>/autostart/zeokeel.desktop in step with it. A failure is
	// logged by syncAutostart and never blocks startup.
	_ = syncAutostart(settings, logger)

	// The UI root exists before the assembly loads: UI modules require its
	// services during activation. It owns the frame and every contribution
	// registry; the host only creates the window around its single view.
	uiRoot := uiroot.New(uiroot.Config{
		AppName:  appName,
		Dark:     settings.Get(settingDark, "false") == "true",
		Language: settings.Get(settingLanguage, "zh"),
		Settings: settings,
		// mygo localizes its own controls, menus and accessibility text
		// separately; mirror a runtime language switch into the framework so
		// those strings follow the product language (fix round SH-06).
		OnLanguageChange: func(lang string) {
			mygo.App.SetLocale(frameworkLocale(lang))
		},
	})
	root := cordis.Root()
	if err := provideServices(root, opts.DataDir, settings, logger, uiRoot); err != nil {
		_ = root.DisposeAll()
		return err
	}
	moduleLister, err := provideModuleLister(root)
	if err != nil {
		_ = root.DisposeAll()
		return fmt.Errorf("%s: provide module lister: %w", appName, err)
	}

	var (
		loader       *module.Loader
		window       *mygo.Window
		tray         trayHandle
		hotkeyOff    func()
		themeOff     func()
		startupErr   error
		shutdownOnce sync.Once
		closing      atomic.Bool
	)
	// shutdown is the one teardown path: unload the assembly (UI modules
	// unregister their contributions in reverse through the kernel), close the
	// window, persist host-owned UI preferences, dispose the kernel root. It is
	// idempotent so the quit listener and the post-Run fallback can both call it.
	shutdown := func() {
		shutdownOnce.Do(func() {
			closing.Store(true)
			logger.Printf("%s: shutting down", appName)
			uiRoot.SetInvalidator(nil)
			if loader != nil {
				if err := loader.Close(); err != nil {
					logger.Printf("%s: close assembly: %v", appName, err)
				}
				loader = nil
			}
			if themeOff != nil {
				themeOff()
				themeOff = nil
			}
			if hotkeyOff != nil {
				hotkeyOff()
				hotkeyOff = nil
			}
			if tray != nil {
				tray.Destroy()
				tray = nil
			}
			if window != nil && !window.IsDestroyed() {
				window.Close()
			}
			persistPreferences(settings, uiRoot, logger)
			if err := root.DisposeAll(); err != nil {
				logger.Printf("%s: dispose kernel root: %v", appName, err)
			}
		})
	}

	if singleInstance {
		// Later launches hand their command line to this instance; bring the
		// existing window to the front. The callback runs on the UI thread, and
		// the window may still be nil during startup, so both are guarded.
		mygo.App.OnSecondInstance(func(args []string, workingDir string) {
			logger.Printf("%s: second launch forwarded (args %v, cwd %q)", appName, args, workingDir)
			if window != nil && !window.IsDestroyed() {
				window.Show()
				window.Restore()
				window.Focus()
			}
		})
	}

	// A quit (Cmd+Q, tray/OS quit, SIGINT/SIGTERM) runs the teardown before
	// mygo closes the windows, so the assembly is unloaded while the UI thread
	// and the window still exist.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		logger.Printf("%s: quit requested", appName)
		shutdown()
	})
	// Closing the last window quits the product. The listener must exist for
	// mygo to report the close instead of starting a nested quit; during the
	// host's own teardown it stays quiet.
	mygo.App.OnWindowAllClosed(func() {
		if closing.Load() {
			return
		}
		logger.Printf("%s: last window closed", appName)
		mygo.App.Quit()
	})

	// The assembly loads here, on the main thread, only once mygo is ready:
	// every UI module activates and registers its contributions on the UI
	// thread, synchronously, before the first frame.
	mygo.App.WhenReady(func() {
		// The framework's own strings follow the product language from the
		// first frame (see framework_locale.go). App.SetLocale persists the tag
		// in the framework's own file, but the product stays the source of
		// truth: this line sets it from the stored ui.language setting on every
		// start, and OnLanguageChange (uiroot.Config) mirrors later switches.
		mygo.App.SetLocale(frameworkLocale(uiRoot.Locale().Language()))
		// Appearance mode: mygo is ready here, so Theme.IsDark and the desktop
		// appearance listener are available. System mode keeps following the
		// desktop; light/dark force the app theme.
		themeOff = applyThemeMode(uiRoot.Theme(), settings, mygo.Theme)
		defer func() {
			if recovered := recover(); recovered != nil {
				startupErr = fmt.Errorf("%s: startup panic: %v", appName, recovered)
				logger.Printf("%v", startupErr)
				mygo.App.Quit()
			}
		}()
		loaded, err := assembly.LoadWithBuildInfo(root, opts.Product, buildInfo(opts))
		if err != nil {
			startupErr = fmt.Errorf("%s: load product %q: %w", appName, opts.Product, err)
			logger.Printf("%v", startupErr)
			mygo.App.Quit()
			return
		}
		loader = loaded
		moduleLister.set(moduleInfos(loadedModulesFromInstances(loaded.Instances())))
		if err := uiRoot.Validate(); err != nil {
			startupErr = fmt.Errorf("%s: %w", appName, err)
			logger.Printf("%v", startupErr)
			mygo.App.Quit()
			return
		}
		window = mygo.NewWindow(mygo.WindowOptions{
			Title:     appName,
			Width:     mainWindowWidth,
			Height:    mainWindowHeight,
			MinWidth:  mainWindowMinWidth,
			MinHeight: mainWindowMinHeight,
			StateKey:  mainWindowStateKey,
			Content:   ui.View(uiRoot.View),
		})
		uiRoot.SetInvalidator(window.Invalidate)
		window.OnFileDrop(func(event *mygo.FileDropEvent) {
			if event != nil {
				uiRoot.FileDrop().Dispatch(event.Paths)
			}
		})
		// Tray actions: a fresh session (create + push the session screen) and
		// the settings overlay. ChatService/Navigator are required by the loaded
		// product; if either is missing the tray just omits that item.
		trayMenuActions := trayActions{}
		if chatService, chatErr := root.Require(contracts.KeyChat); chatErr != nil {
			logger.Printf("%s: tray actions unavailable: %v", appName, chatErr)
		} else {
			navigator := uiRoot.Navigator()
			trayMenuActions.NewSession = func() error {
				if _, err := chatService.NewSession(""); err != nil {
					return fmt.Errorf("create session: %w", err)
				}
				if err := navigator.Push("session"); err != nil {
					return fmt.Errorf("show session screen: %w", err)
				}
				return nil
			}
			trayMenuActions.OpenSettings = func() { navigator.ToggleSettings() }
		}
		// System tray (best effort): a session without a tray host, such as a
		// bare Xvfb display, fails NewTray; log it and keep running. With
		// close-to-tray enabled the close handler hides the window instead of
		// quitting; the teardown path (keepAlive false) still closes it.
		if created, trayErr := setupTrayWithActions(window, settings, func() bool { return !closing.Load() }, trayMenuActions, logger); trayErr != nil {
			logger.Printf("%s: system tray unavailable: %v", appName, trayErr)
		} else {
			tray = created
			logger.Printf("%s: system tray ready (close to tray: %t)", appName, closeToTrayEnabled(settings))
		}
		// Global hotkey (best effort): the appearance page stores the
		// accelerator; an invalid value or a shortcut owned by the desktop is
		// logged and skipped. Unregistered again on shutdown.
		if unregisterHotkey, hotkeyErr := setupGlobalHotkey(window, settings, mygoHotkeyRegistrar{}); hotkeyErr != nil {
			logger.Printf("%s: global hotkey unavailable: %v", appName, hotkeyErr)
		} else if unregisterHotkey != nil {
			hotkeyOff = unregisterHotkey
			logger.Printf("%s: global hotkey registered", appName)
		}
		logger.Printf("%s: %s product %q loaded: %d module instances, screens %v",
			appName, opts.versionString(), opts.Product, len(loader.Instances()),
			uiRoot.ScreenLister().Screens())
	})

	runErr := mygo.App.Run()
	shutdown()
	if startupErr != nil {
		return startupErr
	}
	if runErr != nil {
		return fmt.Errorf("%s: run: %w", appName, runErr)
	}
	return nil
}

// provideServices provides the process-host services on the kernel root before
// any assembly loads. uiRoot is nil for the headless product, which provides
// only settings and log.
func provideServices(root cordis.Context, dataDir string, settings *Settings, logger *Logger, uiRoot *uiroot.Root) error {
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		return fmt.Errorf("%s: provide data dir: %w", appName, err)
	}
	var settingsService contracts.SettingsService = settings
	if _, err := root.Provide(contracts.KeySettings, settingsService); err != nil {
		return fmt.Errorf("%s: provide settings: %w", appName, err)
	}
	var logService contracts.Logger = logger
	if _, err := root.Provide(contracts.KeyLogger, logService); err != nil {
		return fmt.Errorf("%s: provide log: %w", appName, err)
	}
	if uiRoot == nil {
		return nil
	}
	if _, err := root.Provide(contracts.KeyUIRuntime, uiRoot.Runtime()); err != nil {
		return fmt.Errorf("%s: provide ui runtime: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyNavigator, uiRoot.Navigator()); err != nil {
		return fmt.Errorf("%s: provide navigator: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyTheme, uiRoot.Theme()); err != nil {
		return fmt.Errorf("%s: provide theme: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyLocale, uiRoot.Locale()); err != nil {
		return fmt.Errorf("%s: provide locale: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyScreenLister, uiRoot.ScreenLister()); err != nil {
		return fmt.Errorf("%s: provide screen lister: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyFileDrop, contracts.FileDropService(uiRoot.FileDrop())); err != nil {
		return fmt.Errorf("%s: provide file drop: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyUserInteraction, contracts.UserInteraction(uiRoot.UserInteraction())); err != nil {
		return fmt.Errorf("%s: provide user interaction: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(uiRoot.ArtifactPresenter())); err != nil {
		return fmt.Errorf("%s: provide artifact presenter: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyPlanReviewer, contracts.PlanReviewer(uiRoot.PlanReviewer())); err != nil {
		return fmt.Errorf("%s: provide plan reviewer: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker())); err != nil {
		return fmt.Errorf("%s: provide goal tracker: %w", appName, err)
	}
	if _, err := root.Provide(contracts.KeyNotifier, contracts.Notifier(uiRoot.Notifier())); err != nil {
		return fmt.Errorf("%s: provide notifier: %w", appName, err)
	}
	return nil
}

// persistPreferences writes the host-owned UI preferences back to the settings
// file. It runs on the teardown path, where a brief file write on the main
// thread is acceptable.
func persistPreferences(settings *Settings, uiRoot *uiroot.Root, logger *Logger) {
	if err := settings.Set(settingDark, fmt.Sprintf("%t", uiRoot.Theme().Dark())); err != nil {
		logger.Printf("%s: persist theme: %v", appName, err)
	}
	if err := settings.Set(settingLanguage, uiRoot.Locale().Language()); err != nil {
		logger.Printf("%s: persist language: %v", appName, err)
	}
	// The appearance page already persists the switches; this keeps the
	// desktop entry in step with the final settings on the exit path too.
	_ = syncAutostart(settings, logger)
}

// buildInfo maps host options to the assembly's build-identity overlay.
func buildInfo(opts Options) assembly.BuildInfo {
	return assembly.BuildInfo{
		Version:   opts.AppVersion,
		Commit:    opts.AppCommit,
		BuildTime: opts.AppBuildTime,
	}
}

// orDefault returns value, or fallback when value is blank.
func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
