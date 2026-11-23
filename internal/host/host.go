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

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
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
	// Port is the first port the gateway product tries; 0 means the documented
	// default range (gateway.DefaultPort..+9, then any free port). It is
	// meaningless for the other products.
	Port int
	// NoTray skips the tray shell of the gateway product: a pure server. A
	// display-less Linux session skips it anyway (silently), so this flag is for
	// a machine with a display that must not show an icon.
	NoTray bool
}

// normalize fills zero-valued options with their process defaults. Product
// accepts either the embedded name or its file name ("serve" or "serve.json").
func (o Options) normalize() Options {
	o.Product = strings.TrimSpace(o.Product)
	o.Product = strings.TrimSuffix(o.Product, ".json")
	if o.Product == "" {
		o.Product = assembly.ProductServe
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
	// Hidden update-helper modes run before flag parsing and before mygo is
	// initialized: the updater spawns this same executable to wait for the old
	// process to quit and then start the installer / reopen the new app without
	// any shell quoting. They never open a window or touch the single-instance
	// lock.
	if handled, code := runUpdateHelper(args); handled {
		return code
	}
	return mainWithStreams(args, os.Stdout, os.Stderr)
}

// mainWithStreams is Main with injectable output streams (tests).
func mainWithStreams(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(appName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	// Print help ourselves: flag's default help goes to the error stream, and
	// --help is a success, not an error.
	flags.Usage = func() {}
	product := flags.String("product", assembly.ProductServe, "assembly product to run")
	headless := flags.Bool("headless", false, "run without a window (same as --product headless)")
	dataDir := flags.String("data-dir", "", "data directory")
	showVersion := flags.Bool("version", false, "print version information and exit")
	listProducts := flags.Bool("list-products", false, "print the embedded assembly products and exit")
	acpMode := flags.Bool("acp", false, "run the Agent Client Protocol (ACP) stdio server")
	servePort := flags.Int("port", 0, "first port for --product serve (default 9932, then +1..+9, then any free port)")
	noTray := flags.Bool("no-tray", false, "run --product serve without the system tray (pure gateway)")

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
		for _, name := range assembly.AllProducts() {
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

	port, err := parseServePort(*servePort)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n\n%s", err, usageText)
		return 2
	}
	if err := servePortConflict(*product, *acpMode, *headless, port); err != nil {
		fmt.Fprintf(stderr, "%s\n\n%s", err, usageText)
		return 2
	}
	if *noTray && (strings.TrimSuffix(strings.TrimSpace(*product), ".json") != assembly.ProductServe || *acpMode || *headless) {
		fmt.Fprintf(stderr, "%s: --no-tray only applies to --product %s\n\n%s", appName, assembly.ProductServe, usageText)
		return 2
	}
	opts := Options{
		Product:      *product,
		NoTray:       *noTray,
		DataDir:      *dataDir,
		AppVersion:   Version,
		AppCommit:    Commit,
		AppBuildTime: BuildTime,
		ACP:          *acpMode,
		Port:         port,
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
  --product <name>   assembly product to run (default "serve")
  --headless         run without a window (same as --product headless)
  --acp              run the Agent Client Protocol stdio server (editor integration)
  --port <port>      first gateway port for --product serve (default 9932)
  --no-tray          run --product serve without the system tray (pure gateway)
  --data-dir <dir>   data directory (default $ZEOKEEL_DATA_DIR or ~/.zeokeel)
  --version          print version information and exit
  --list-products    print the embedded assembly products and exit
  -h, --help         print this help and exit
`

// Run runs one product until it quits. It blocks until the product's
// context ends (SIGINT/SIGTERM for the gateway and headless products).
//
// The windowed "desktop" product was retired with the native UI; the name is
// kept as a readable error so an old script fails with an instruction instead
// of an unknown-product message.
// retiredProductDesktop is the removed windowed product's name. It is not an
// assembly product any more; the constant exists so the CLI can answer a stale
// `--product desktop` with an instruction.
const retiredProductDesktop = "desktop"

func Run(opts Options) error {
	opts = opts.normalize()
	switch opts.Product {
	case retiredProductDesktop:
		// The windowed product was retired when the browser client became the
		// main surface (docs/plans/web-client-pivot.md). Fail loudly instead of
		// starting something the build no longer contains.
		return fmt.Errorf("the desktop product is gone; run --product %s and open the printed URL in a browser",
			assembly.ProductServe)
	case assembly.ProductHeadless:
		ctx, stop := signal.NotifyContext(context.Background(), terminationSignals...)
		defer stop()
		return runHeadless(ctx, opts, os.Stdout)
	case assembly.ProductServe:
		ctx, stop := signal.NotifyContext(context.Background(), terminationSignals...)
		defer stop()
		return runServe(ctx, opts, os.Stdout)
	default:
		return fmt.Errorf("no host for product %q (known products: %s)",
			opts.Product, strings.Join(assembly.AllProducts(), ", "))
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
	applyPendingUpdateOnStartup(opts.DataDir, opts.AppVersion, logger)
	root := cordis.Root()
	defer func() {
		if err := root.DisposeAll(); err != nil {
			logger.Printf("%s: dispose kernel root: %v", appName, err)
		}
	}()
	if err := provideServices(root, opts.DataDir, settings, logger); err != nil {
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
	// The headless product has no window, but it serves the same agent tools:
	// open the single default workspace before reporting ready, so a run
	// without a current folder does not work against nothing.
	ensureDefaultWorkspace(root, opts.DataDir, logger)
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
	if err := provideServices(root, opts.DataDir, settings, logger); err != nil {
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

// provideServices provides the process-host services on the kernel root before
// any assembly loads. It is the gateway/headless host: the window-owned
// services (user interaction, artifacts, plans, notifications, foreground
// session, theme, locale) are provided by whoever runs the browser transport —
// for --product serve that is gateway.Provide (TRANSPORT §7.2).
func provideServices(root cordis.Context, dataDir string, settings *Settings, logger *Logger) error {
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
	return nil
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
