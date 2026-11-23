package host

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/assembly"
)

func TestOptionsNormalize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(dataDirEnv, dir)

	normalized := Options{}.normalize()
	if normalized.Product != assembly.ProductServe {
		t.Fatalf("default product = %q, want %q", normalized.Product, assembly.ProductServe)
	}
	if normalized.DataDir != dir {
		t.Fatalf("default data dir = %q, want $%s = %q", normalized.DataDir, dataDirEnv, dir)
	}
	if normalized.AppVersion != Version || normalized.AppCommit != Commit || normalized.AppBuildTime != BuildTime {
		t.Fatalf("default build identity = %q/%q/%q, want the ldflags values",
			normalized.AppVersion, normalized.AppCommit, normalized.AppBuildTime)
	}

	explicit := Options{
		Product:      assembly.ProductHeadless,
		DataDir:      "/tmp/explicit",
		AppVersion:   "1.2.3",
		AppCommit:    "abc",
		AppBuildTime: "now",
	}.normalize()
	if explicit.Product != assembly.ProductHeadless || explicit.DataDir != "/tmp/explicit" ||
		explicit.AppVersion != "1.2.3" || explicit.AppCommit != "abc" || explicit.AppBuildTime != "now" {
		t.Fatalf("explicit options were overwritten: %+v", explicit)
	}
}

func TestMainVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := mainWithStreams([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--version exit code = %d, want 0 (stderr %q)", code, stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{appName, "commit", runtime.GOOS, runtime.GOARCH, runtime.Version()} {
		if !strings.Contains(got, want) {
			t.Fatalf("--version output = %q, want it to mention %q", got, want)
		}
	}
}

func TestMainListProducts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := mainWithStreams([]string{"--list-products"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--list-products exit code = %d, want 0 (stderr %q)", code, stderr.String())
	}
	lines := strings.Fields(stdout.String())
	// serve has no assembly JSON of its own (it reuses headless entries), so it
	// is enumerated by AllProducts rather than by the embedded file list.
	want := []string{assembly.ProductHeadless, assembly.ProductServe}
	if len(lines) != len(want) {
		t.Fatalf("--list-products output = %q, want %v", stdout.String(), want)
	}
	for i, name := range want {
		if lines[i] != name {
			t.Fatalf("--list-products output = %q, want %v", stdout.String(), want)
		}
	}
}

// TestServePortFlagValidation covers the --port wiring: it only applies to the
// gateway product and must reject out-of-range values instead of silently
// falling back to the default range.
func TestServePortFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		// --product defaults to serve now, so a bare --port is valid and is not
		// part of this table.
		{name: "headless product", args: []string{"--headless", "--port", "1234"}},
		{name: "desktop product", args: []string{"--product", "desktop", "--port", "1234"}},
		{name: "no-tray on headless", args: []string{"--product", "headless", "--no-tray"}},
		{name: "no-tray on acp", args: []string{"--acp", "--no-tray"}},
		{name: "out of range", args: []string{"--product", "serve", "--port", "70000"}},
		{name: "negative", args: []string{"--product", "serve", "--port", "-2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := mainWithStreams(c.args, &stdout, &stderr); code != 2 {
				t.Fatalf("exit code = %d, want 2 (stdout %q, stderr %q)", code, stdout.String(), stderr.String())
			}
			want := "--port"
			if strings.Contains(strings.Join(c.args, " "), "--no-tray") {
				want = "--no-tray"
			}
			if !strings.Contains(stderr.String(), want) {
				t.Fatalf("stderr = %q, want it to explain %s", stderr.String(), want)
			}
		})
	}
}

func TestMainHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := mainWithStreams([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--help exit code = %d, want 0", code)
	}
	for _, want := range []string{"--product", "--headless", "--data-dir", "--version", "--list-products", "--port", "--no-tray"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("--help output = %q, want it to document %q", stdout.String(), want)
		}
	}
}

func TestMainUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown flag", args: []string{"--no-such-flag"}, want: "flag provided but not defined"},
		{name: "unexpected argument", args: []string{"extra"}, want: "unexpected argument"},
		{name: "headless conflicts with product", args: []string{"--headless", "--product", "desktop"}, want: "conflicts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := mainWithStreams(test.args, &stdout, &stderr); code != 2 {
				t.Fatalf("%s exit code = %d, want 2", test.name, code)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("%s stderr = %q, want it to mention %q", test.name, stderr.String(), test.want)
			}
		})
	}
}

func TestRunRejectsUnknownProduct(t *testing.T) {
	t.Setenv(dataDirEnv, t.TempDir())
	var stderr bytes.Buffer
	code := mainWithStreams([]string{"--product", "mobile"}, new(bytes.Buffer), &stderr)
	if code != 1 {
		t.Fatalf("unknown product exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no host for product") {
		t.Fatalf("unknown product stderr = %q, want the routing error", stderr.String())
	}
}

// TestHeadlessRun loads the real headless assembly on a root that has only the
// host services, then shuts down gracefully. It proves the headless product
// needs no display environment and no uiroot.
func TestHeadlessRun(t *testing.T) {
	dataDir := t.TempDir()
	opts := Options{
		Product:      assembly.ProductHeadless,
		DataDir:      dataDir,
		AppVersion:   "9.8.7",
		AppCommit:    "cafebabe",
		AppBuildTime: "2026-11-06T12:00:00Z",
	}.normalize()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // shutdown as soon as the assembly has loaded
	var out bytes.Buffer
	if err := runHeadless(ctx, opts, &out); err != nil {
		t.Fatalf("runHeadless: %v", err)
	}

	output := out.String()
	for _, want := range []string{
		"product: headless",
		"instance appinfo",
		"zeokeel/appinfo",
		"state=active",
		"9.8.7",
		"ready; waiting for SIGINT or SIGTERM",
		"stopping",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("headless output = %q, want it to mention %q", output, want)
		}
	}

	// The process log file was created under the data directory.
	logPath := filepath.Join(dataDir, "logs", LogFileName)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read headless log %q: %v", logPath, err)
	}
	if !strings.Contains(string(data), "headless") {
		t.Fatalf("headless log = %q, want the product start line", data)
	}
	// The headless product opens the host's one default workspace before it
	// reports ready: the folder exists and the log says which one is open.
	if _, err := os.Stat(filepath.Join(dataDir, defaultWorkspaceDirName)); err != nil {
		t.Fatalf("headless default workspace folder: %v", err)
	}
	if !strings.Contains(string(data), "opened the default workspace folder") {
		t.Fatalf("headless log = %q, want the default-workspace line", data)
	}
}

func TestVersionString(t *testing.T) {
	opts := Options{AppVersion: "2.0.0", AppCommit: "abcdef", AppBuildTime: "yesterday"}
	got := opts.versionString()
	for _, want := range []string{appName, "2.0.0", "abcdef", "yesterday", runtime.GOOS, runtime.GOARCH} {
		if !strings.Contains(got, want) {
			t.Fatalf("versionString() = %q, want it to mention %q", got, want)
		}
	}
	if lines := strings.Count(got, "\n"); lines != 0 {
		t.Fatalf("versionString() = %q, want a single line", got)
	}
}

func TestDefaultDataDirEnvOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom-data")
	t.Setenv(dataDirEnv, "  "+dir+"  ")
	if got := defaultDataDir(); got != dir {
		t.Fatalf("defaultDataDir() = %q, want the trimmed $%s %q", got, dataDirEnv, dir)
	}
	if got := defaultDataDir(); strings.TrimSpace(got) != got {
		t.Fatalf("defaultDataDir() kept surrounding spaces: %q", got)
	}
}
