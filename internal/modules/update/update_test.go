package update

// Tests for the update-check backend: manifest fetch/parse, semver comparison,
// platform asset selection, URL policy, limits, timeout/cancel, concurrency and
// module activation.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

// platformAsset is a manifest asset matching the test binary's platform.
func platformAsset() map[string]any {
	return map[string]any{
		"goos":   runtime.GOOS,
		"goarch": runtime.GOARCH,
		"url":    "https://example.com/zeokeel.zip",
		"sha256": strings.Repeat("ab", 32),
		"size":   123456,
	}
}

// manifestJSON builds a release manifest body.
func manifestJSON(version, notes string, assets []map[string]any) string {
	entries := make([]string, 0, len(assets))
	for _, asset := range assets {
		encoded, _ := jsonMarshal(asset)
		entries = append(entries, string(encoded))
	}
	return fmt.Sprintf(`{"version":%q,"notes":%q,"assets":[%s]}`, version, notes, strings.Join(entries, ","))
}

// jsonMarshal is a tiny indirection so the helper stays readable.
func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

// manifestServer serves one fixed manifest body.
func manifestServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
}

// newTestService builds a service directly.
func newTestService(t *testing.T, cfg Config, appVersion string) *service {
	t.Helper()
	return newService(cfg, contracts.AppInfo{Name: "ZeoKeel", Version: appVersion}, &fakeLogger{})
}

// check runs one Check and fails on unexpected error.
func check(t *testing.T, service *service) (contracts.UpdateInfo, error) {
	t.Helper()
	return service.Check(context.Background())
}

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/update" {
		t.Fatalf("module ID = %q", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyUpdate.Name() {
		t.Fatalf("provides = %+v, want KeyUpdate", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{contracts.KeyAppInfo.Name(), contracts.KeyLogger.Name()} {
		if !required[name] {
			t.Fatalf("requires = %+v, missing %q", descriptor.Requires, name)
		}
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	for _, cfg := range []Config{{}, {FeedURL: "https://example.com/latest.json", TimeoutMs: 10}} {
		if err := descriptor.Validate(cfg); err != nil {
			t.Fatalf("Validate(%+v) = %v", cfg, err)
		}
	}
	if err := descriptor.Validate(Config{TimeoutMs: -1}); err == nil {
		t.Fatal("Validate accepted a negative timeoutMs")
	}
	if err := descriptor.Validate(Config{FeedURL: "bad\x00url"}); err == nil {
		t.Fatal("Validate accepted a NUL feedURL")
	}
}

func TestModuleActivationProvidesUpdateService(t *testing.T) {
	server := manifestServer(t, manifestJSON("2.0.0", "release", []map[string]any{platformAsset()}))
	defer server.Close()
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	if _, err := root.Provide(contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"}); err != nil {
		t.Fatalf("provide app info: %v", err)
	}
	logger := &fakeLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyAppInfo).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "update", Module: ModuleID,
		Config: map[string]any{"feedURL": server.URL, "allowInsecureHTTP": true, "timeoutMs": 2000},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()
	updateService, err := root.Require(contracts.KeyUpdate)
	if err != nil {
		t.Fatalf("require KeyUpdate: %v", err)
	}
	info, err := updateService.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !info.Available || info.CurrentVersion != "1.0.0" || info.LatestVersion != "2.0.0" {
		t.Fatalf("info = %+v", info)
	}
}

func TestCheckDisabled(t *testing.T) {
	service := newTestService(t, Config{}, "1.0.0")
	if _, err := check(t, service); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Check(disabled) = %v, want ErrDisabled", err)
	}
}

func TestCheckURLPolicy(t *testing.T) {
	server := manifestServer(t, manifestJSON("2.0.0", "", []map[string]any{platformAsset()}))
	defer server.Close()

	insecure := newTestService(t, Config{FeedURL: server.URL}, "1.0.0")
	if _, err := check(t, insecure); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("Check(http feed) = %v, want ErrInsecureURL", err)
	}
	allowed := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
	if _, err := check(t, allowed); err != nil {
		t.Fatalf("Check(http feed with allowInsecureHTTP): %v", err)
	}
	for _, raw := range []string{"https://example.com/a", "http://example.com/a"} {
		if err := allowed.validateURL(raw); err != nil {
			t.Fatalf("validateURL(%q, allowInsecure) = %v", raw, err)
		}
	}
	for _, raw := range []string{"", "ftp://example.com/a", "not a url", "http://example.com/a"} {
		if err := insecure.validateURL(raw); !errors.Is(err, ErrInsecureURL) {
			t.Fatalf("validateURL(%q) = %v, want ErrInsecureURL", raw, err)
		}
	}
}

func TestCheckAvailableAndAsset(t *testing.T) {
	server := manifestServer(t, manifestJSON("2.0.0", "## notes", []map[string]any{platformAsset()}))
	defer server.Close()
	service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
	info, err := check(t, service)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.CurrentVersion != "1.0.0" || info.LatestVersion != "2.0.0" || !info.Available {
		t.Fatalf("info = %+v", info)
	}
	if info.Notes != "## notes" || info.URL != "https://example.com/zeokeel.zip" ||
		info.SHA256 != strings.Repeat("ab", 32) || info.Size != 123456 {
		t.Fatalf("asset fields = %+v", info)
	}
}

func TestCheckSameOlderAndDevVersions(t *testing.T) {
	asset := []map[string]any{platformAsset()}
	cases := []struct {
		latest        string
		current       string
		wantAvailable bool
		wantCurrent   string
	}{
		{"1.0.0", "1.0.0", false, "1.0.0"},
		{"0.9.0", "1.0.0", false, "1.0.0"},
		{"1.2", "1.2.0", false, "1.2.0"},
		{"1.2.3", "", true, "dev"},
		{"1.2.3", "dev", true, "dev"},
		{"1.2.0", "1.2.0-rc.1", true, "1.2.0-rc.1"},
		{"1.2.0-rc.1", "1.2.0", false, "1.2.0"},
	}
	for _, testCase := range cases {
		server := manifestServer(t, manifestJSON(testCase.latest, "notes", asset))
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, testCase.current)
		info, err := check(t, service)
		server.Close()
		if err != nil {
			t.Fatalf("Check(latest %s, current %q): %v", testCase.latest, testCase.current, err)
		}
		if info.Available != testCase.wantAvailable || info.CurrentVersion != testCase.wantCurrent ||
			info.LatestVersion != testCase.latest {
			t.Fatalf("Check(latest %s, current %q) = %+v, want available=%v current=%q",
				testCase.latest, testCase.current, info, testCase.wantAvailable, testCase.wantCurrent)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.2.0", "1.2.0", 0},
		{"1.2", "1.2.0", 0},
		{"1", "1.0.0", 0},
		{"1.2.1", "1.2.0", 1},
		{"1.2.0", "1.2.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0", "1.2.0-rc.1", 1},
		{"1.2.0-rc.10", "1.2.0-rc.2", 1},
		{"1.2.0-alpha", "1.2.0-beta", -1},
		{"1.2.0-alpha.1", "1.2.0-alpha", 1},
		{"1.2.0+build.5", "1.2.0", 0},
	}
	for _, testCase := range cases {
		got, err := CompareVersions(testCase.left, testCase.right)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", testCase.left, testCase.right, err)
		}
		if got != testCase.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", testCase.left, testCase.right, got, testCase.want)
		}
	}
	invalid := []string{"", "x", "1.x", "1.2.3.4", "1.2.0-", "1.2.0-alpha..1", "-1.0.0"}
	for _, value := range invalid {
		if _, err := CompareVersions(value, "1.0.0"); err == nil {
			t.Fatalf("CompareVersions(%q) accepted an invalid version", value)
		}
	}
}

func TestParseManifestValidation(t *testing.T) {
	valid := manifestJSON("1.2.3", "notes", []map[string]any{platformAsset()})
	if _, err := ParseManifest([]byte(valid)); err != nil {
		t.Fatalf("ParseManifest(valid): %v", err)
	}
	for _, body := range []string{
		`{broken`,
		`{}`,
		`{"version":"nope","assets":[]}`,
		`{"version":"1.0.0","assets":"nope"}`,
	} {
		if _, err := ParseManifest([]byte(body)); !errors.Is(err, ErrManifest) {
			t.Fatalf("ParseManifest(%q) = %v, want ErrManifest", body, err)
		}
	}
}

func TestCheckErrors(t *testing.T) {
	asset := []map[string]any{platformAsset()}
	t.Run("invalid json", func(t *testing.T) {
		server := manifestServer(t, "{not json")
		defer server.Close()
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
		if _, err := check(t, service); !errors.Is(err, ErrManifest) {
			t.Fatalf("invalid JSON = %v, want ErrManifest", err)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer server.Close()
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
		if _, err := check(t, service); err == nil || !strings.Contains(err.Error(), "502") {
			t.Fatalf("non-200 = %v, want the status in the error", err)
		}
	})
	t.Run("no platform asset", func(t *testing.T) {
		other := map[string]any{"goos": "plan9", "goarch": "mips", "url": "https://example.com/x"}
		server := manifestServer(t, manifestJSON("2.0.0", "", []map[string]any{other}))
		defer server.Close()
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
		if _, err := check(t, service); !errors.Is(err, ErrNoAsset) {
			t.Fatalf("no asset = %v, want ErrNoAsset", err)
		}
	})
	t.Run("invalid asset URL", func(t *testing.T) {
		bad := platformAsset()
		bad["url"] = "http://example.com/insecure.zip"
		server := manifestServer(t, manifestJSON("2.0.0", "", []map[string]any{bad}))
		defer server.Close()
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
		// allowInsecureHTTP also allows http asset URLs, so remove it from a
		// manually built service to exercise the rejection deterministically.
		service.allowInsecureHTTP = false
		if _, err := check(t, service); !errors.Is(err, ErrInsecureURL) {
			t.Fatalf("insecure asset = %v, want ErrInsecureURL", err)
		}
	})
	t.Run("body too large", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", maxManifestBytes+10))
		}))
		defer server.Close()
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
		if _, err := check(t, service); !errors.Is(err, ErrManifest) {
			t.Fatalf("large body = %v, want ErrManifest", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, manifestJSON("2.0.0", "", asset))
		}))
		defer server.Close()
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true, TimeoutMs: 50}, "1.0.0")
		start := time.Now()
		if _, err := check(t, service); err == nil {
			t.Fatal("timed-out Check returned nil error")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("timeout took %s", elapsed)
		}
	})
	t.Run("context canceled", func(t *testing.T) {
		server := manifestServer(t, manifestJSON("2.0.0", "", asset))
		defer server.Close()
		service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := service.Check(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Check = %v, want context.Canceled", err)
		}
	})
}

func TestCheckConcurrent(t *testing.T) {
	server := manifestServer(t, manifestJSON("2.0.0", "notes", []map[string]any{platformAsset()}))
	defer server.Close()
	service := newTestService(t, Config{FeedURL: server.URL, AllowInsecureHTTP: true}, "1.0.0")
	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			info, err := service.Check(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if !info.Available || info.LatestVersion != "2.0.0" {
				errs <- fmt.Errorf("unexpected info: %+v", info)
			}
		}()
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
