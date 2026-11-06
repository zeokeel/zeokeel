// Contract tests for the auto-update engine (TDD: red first, now green).
//
// These tests are the executable contract; they were written before the
// implementation and must not be weakened. The RED→GREEN history is recorded in
// .cache/fix-platform/updater-red.txt and .cache/fix-platform/updater-green.txt.
//
// Run:
//
//	go test -count=1 ./internal/updater

package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeReplacer records Replace calls and returns a configurable error. It is
// the seam that makes install-on-restart testable without touching a real
// executable.
type fakeReplacer struct {
	mu         sync.Mutex
	current    string
	replaceErr error
	calls      []string // "current -> staged"
}

func (f *fakeReplacer) CurrentExecutable() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current == "" {
		return "/opt/zeokeel/zeokeel", nil
	}
	return f.current, nil
}

func (f *fakeReplacer) Replace(currentPath, stagedPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replaceErr != nil {
		return f.replaceErr
	}
	f.calls = append(f.calls, currentPath+" -> "+stagedPath)
	return nil
}

func (f *fakeReplacer) replaceCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fixture is one manifest server: the manifest JSON plus asset paths -> bytes.
type fixture struct {
	srv   *httptest.Server
	bytes map[string][]byte // path -> body
	sums  map[string]string // path -> sha256 hex
}

// newFixture serves manifests and assets from one httptest server. The manifest
// URLs are absolute and point back at the same server, so tests exercise the
// real HTTP path end to end.
func newFixture(t *testing.T, build func(base string) Manifest, assets map[string][]byte) *fixture {
	t.Helper()
	f := &fixture{bytes: assets, sums: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(build(f.srv.URL))
	})
	for path, body := range assets {
		body := body
		p := path
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		})
		sum := sha256.Sum256(body)
		f.sums[p] = hex.EncodeToString(sum[:])
	}
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func testConfig(t *testing.T, feedURL string) Config {
	t.Helper()
	return Config{
		CurrentVersion:    "1.0.0",
		FeedURL:           feedURL,
		DataDir:           t.TempDir(),
		GOOS:              "linux",
		GOARCH:            "amd64",
		HTTPClient:        &http.Client{Timeout: 5 * time.Second},
		Replacer:          &fakeReplacer{},
		Now:               func() time.Time { return time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC) },
		AllowInsecureHTTP: true, // httptest servers are http
	}
}

// TestCheckFindsNewerVersion: a reachable manifest with a newer version selects
// this platform's asset and reports available=true.
func TestCheckFindsNewerVersion(t *testing.T) {
	var f *fixture
	f = newFixture(t, func(base string) Manifest {
		return Manifest{
			Version: "1.2.0",
			Notes:   "hello",
			Assets: []Asset{{
				GOOS: "linux", GOARCH: "amd64",
				URL:    base + "/asset/linux-amd64",
				SHA256: f.sums["/asset/linux-amd64"],
				Size:   int64(len(f.bytes["/asset/linux-amd64"])),
			}},
		}
	}, map[string][]byte{"/asset/linux-amd64": []byte("new binary")})
	cfg := testConfig(t, f.srv.URL+"/manifest.json")

	rel, available, err := Check(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !available {
		t.Fatalf("available = false, want true (1.0.0 -> 1.2.0)")
	}
	if rel == nil || rel.Version != "1.2.0" {
		t.Fatalf("release = %+v, want version 1.2.0", rel)
	}
	if rel.Asset.GOOS != "linux" || rel.Asset.GOARCH != "amd64" {
		t.Fatalf("asset = %+v, want linux/amd64", rel.Asset)
	}
}

// TestCheckRejectsOlderOrSame: same and older versions are not an update and
// must not be an error (the caller treats it as "up to date").
func TestCheckRejectsOlderOrSame(t *testing.T) {
	for _, version := range []string{"1.0.0", "0.9.9"} {
		version := version
		t.Run(version, func(t *testing.T) {
			f := newFixture(t, func(base string) Manifest {
				return Manifest{Version: version, Assets: []Asset{{
					GOOS: "linux", GOARCH: "amd64", URL: base + "/a", SHA256: "00",
				}}}
			}, map[string][]byte{"/a": []byte("x")})
			cfg := testConfig(t, f.srv.URL+"/manifest.json")

			rel, available, err := Check(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if available || rel != nil {
				t.Fatalf("available = %v, release = %+v; want no update", available, rel)
			}
		})
	}
}

// TestCheckDisabledWithoutFeed: no feed URL means auto-update is off, which is
// a distinct error so callers do not log a misleading failure.
func TestCheckDisabledWithoutFeed(t *testing.T) {
	cfg := testConfig(t, "")
	if _, _, err := Check(context.Background(), cfg); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}

// TestCheckRejectsInsecureFeed: http:// is refused unless explicitly allowed.
func TestCheckRejectsInsecureFeed(t *testing.T) {
	f := newFixture(t, func(base string) Manifest {
		return Manifest{Version: "9.9.9", Assets: []Asset{{GOOS: "linux", GOARCH: "amd64", URL: base + "/a", SHA256: "00"}}}
	}, map[string][]byte{"/a": []byte("x")})
	cfg := testConfig(t, f.srv.URL+"/manifest.json")
	cfg.AllowInsecureHTTP = false

	if _, _, err := Check(context.Background(), cfg); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("err = %v, want ErrInsecureURL", err)
	}
}

// TestCheckPicksPlatformAsset: among several assets the manifest's linux/amd64
// entry is chosen (and a missing entry is ErrNoAsset).
func TestCheckPicksPlatformAsset(t *testing.T) {
	f := newFixture(t, func(base string) Manifest {
		return Manifest{Version: "2.0.0", Assets: []Asset{
			{GOOS: "darwin", GOARCH: "arm64", URL: base + "/mac", SHA256: "11"},
			{GOOS: "linux", GOARCH: "amd64", URL: base + "/linux", SHA256: "22"},
			{GOOS: "windows", GOARCH: "amd64", URL: base + "/win", SHA256: "33"},
		}}
	}, map[string][]byte{"/mac": []byte("m"), "/linux": []byte("l"), "/win": []byte("w")})
	cfg := testConfig(t, f.srv.URL+"/manifest.json")

	rel, available, err := Check(context.Background(), cfg)
	if err != nil || !available {
		t.Fatalf("Check = (%+v, %v, %v), want available update", rel, available, err)
	}
	if rel.Asset.URL != f.srv.URL+"/linux" || rel.Asset.SHA256 != "22" {
		t.Fatalf("asset = %+v, want the linux/amd64 asset", rel.Asset)
	}

	missing := testConfig(t, f.srv.URL+"/manifest.json")
	missing.GOARCH = "riscv64"
	if _, _, err := Check(context.Background(), missing); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("missing platform err = %v, want ErrNoAsset", err)
	}
}

// TestParseManifest covers manifest decoding plus prerelease preservation:
// "1.2.0-rc.1" must survive parsing verbatim (no float trimming).
func TestParserHandlesPrerelease(t *testing.T) {
	raw := []byte(`{"version":"1.2.0-rc.1","notes":"rc","assets":[]}`)
	m, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Version != "1.2.0-rc.1" || m.Notes != "rc" {
		t.Fatalf("manifest = %+v", m)
	}
	if _, err := ParseManifest([]byte(`{`)); !errors.Is(err, ErrManifest) {
		t.Fatalf("broken JSON err = %v, want ErrManifest", err)
	}
	if _, err := ParseManifest([]byte(`{"version":"","assets":[]}`)); !errors.Is(err, ErrManifest) {
		t.Fatalf("empty version err = %v, want ErrManifest", err)
	}
}

// TestCompareVersionsTable pins the semver-ish ordering contract.
func TestCompareVersionsTable(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.10.0", -1},
		{"1.10.0", "1.2.0", 1},
		{"0.9.9", "1.0.0", -1},
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0", "1.2.0-rc.1", 1},
		{"1.2.0-rc.1", "1.2.0-rc.2", -1},
		{"1.2", "1.2.0", 0},
	}
	for _, tc := range cases {
		got, err := CompareVersions(tc.a, tc.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for _, bad := range []string{"", "x.y.z", "1..2"} {
		if _, err := CompareVersions(bad, "1.0.0"); err == nil {
			t.Fatalf("CompareVersions(%q) = nil error, want invalid-version error", bad)
		}
	}
}

// downloadFixture starts a manifest+asset server and returns a matching
// Release ready for Download.
func downloadFixture(t *testing.T) (Config, Release, []byte) {
	t.Helper()
	body := []byte("payload-bytes")
	var f *fixture
	f = newFixture(t, func(base string) Manifest {
		return Manifest{Version: "1.2.0", Assets: []Asset{{
			GOOS: "linux", GOARCH: "amd64", URL: base + "/asset", SHA256: f.sums["/asset"],
		}}}
	}, map[string][]byte{"/asset": body})
	cfg := testConfig(t, f.srv.URL+"/manifest.json")
	rel, _, err := Check(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return cfg, *rel, body
}

// TestDownloadWritesFileAndVerifiesSHA256: a verified download lands inside
// <DataDir>/updates, matches the bytes and is executable.
func TestDownloadWritesFileAndVerifiesSHA256(t *testing.T) {
	cfg, rel, body := downloadFixture(t)
	staged, err := Download(context.Background(), cfg, rel)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if staged == nil || staged.Version != "1.2.0" {
		t.Fatalf("staged = %+v", staged)
	}
	updates := stagingDir(cfg)
	if !strings.HasPrefix(staged.Path, updates+string(filepath.Separator)) {
		t.Fatalf("staged path %q is not inside %q", staged.Path, updates)
	}
	got, err := os.ReadFile(staged.Path)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("staged bytes = %q, want %q", got, body)
	}
	wantSum := sha256.Sum256(body)
	if staged.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("staged sha256 = %q", staged.SHA256)
	}
	info, err := os.Stat(staged.Path)
	if err != nil {
		t.Fatalf("stat staged: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("staged mode = %v, want owner-executable", info.Mode().Perm())
	}
}

// TestChecksumMismatchRejected: tampered bytes are rejected and nothing is left
// behind in the staging area (no half-written update to execute later).
func TestChecksumMismatchRejected(t *testing.T) {
	body := []byte("good-bytes")
	f := newFixture(t, func(base string) Manifest {
		return Manifest{Version: "1.2.0", Assets: []Asset{{
			GOOS: "linux", GOARCH: "amd64", URL: base + "/asset",
			SHA256: "deadbeef", // deliberately wrong
		}}}
	}, map[string][]byte{"/asset": body})
	cfg := testConfig(t, f.srv.URL+"/manifest.json")
	rel, _, err := Check(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	_, err = Download(context.Background(), cfg, *rel)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	var leftovers []string
	_ = filepath.Walk(stagingDir(cfg), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr == nil && info != nil && info.Mode().IsRegular() {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if len(leftovers) != 0 {
		t.Fatalf("staging leftovers after checksum mismatch: %v", leftovers)
	}
}

// TestInstallSchedulesReplaceOnRestart is the install-on-restart contract:
// stage a verified update, then on the "next start" the pending marker drives
// exactly one Replacer.Replace call and the staging area is cleaned up.
func TestInstallSchedulesReplaceOnRestart(t *testing.T) {
	cfg, rel, body := downloadFixture(t)
	staged, err := Download(context.Background(), cfg, rel)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if err := ScheduleInstall(cfg, *staged); err != nil {
		t.Fatalf("ScheduleInstall: %v", err)
	}

	replacer, ok := cfg.Replacer.(*fakeReplacer)
	if !ok {
		t.Fatalf("cfg.Replacer is not the fake")
	}
	applied, err := ApplyPendingInstall(cfg)
	if err != nil {
		t.Fatalf("ApplyPendingInstall: %v", err)
	}
	if !applied {
		t.Fatalf("applied = false, want true")
	}
	calls := replacer.replaceCalls()
	if len(calls) != 1 {
		t.Fatalf("Replace calls = %v, want exactly one", calls)
	}
	current, _ := replacer.CurrentExecutable()
	if !strings.Contains(calls[0], current) || !strings.Contains(calls[0], staged.Path) {
		t.Fatalf("Replace call %q, want current %q -> staged %q", calls[0], current, staged.Path)
	}
	if _, err := os.Stat(staged.Path); !os.IsNotExist(err) {
		t.Fatalf("staged file still exists after install: %v", err)
	}
	if len(body) == 0 {
		t.Fatalf("fixture body is empty")
	}
	// A second start with no pending update is a no-op.
	applied, err = ApplyPendingInstall(cfg)
	if err != nil || applied {
		t.Fatalf("second ApplyPendingInstall = (%v, %v), want (false, nil)", applied, err)
	}
	if got := len(replacer.replaceCalls()); got != 1 {
		t.Fatalf("Replace called %d times after no-op start, want 1", got)
	}
}

// TestInstallFailureKeepsPendingUpdate: a failed replace must keep the pending
// update so the next start can retry instead of losing the download.
func TestInstallFailureKeepsPendingUpdate(t *testing.T) {
	cfg, rel, _ := downloadFixture(t)
	staged, err := Download(context.Background(), cfg, rel)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if err := ScheduleInstall(cfg, *staged); err != nil {
		t.Fatalf("ScheduleInstall: %v", err)
	}
	replacer := cfg.Replacer.(*fakeReplacer)
	replacer.replaceErr = errors.New("permission denied")

	applied, err := ApplyPendingInstall(cfg)
	if applied {
		t.Fatalf("applied = true on a failed replace")
	}
	if !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("err = %v, want ErrInstallFailed", err)
	}
	if _, statErr := os.Stat(staged.Path); statErr != nil {
		t.Fatalf("staged file was removed after a failed install: %v", statErr)
	}

	// Retry on the next start succeeds once the replacer works again.
	replacer.replaceErr = nil
	applied, err = ApplyPendingInstall(cfg)
	if err != nil || !applied {
		t.Fatalf("retry ApplyPendingInstall = (%v, %v), want (true, nil)", applied, err)
	}
}

// TestApplyPendingInstallResumesInterruptedReplace covers the crash-recovery
// path: if the process dies after the staged file was renamed to
// "<path>.installing" but before Replace finished, the next start must resume
// from the ".installing" sibling instead of losing the update.
func TestApplyPendingInstallResumesInterruptedReplace(t *testing.T) {
	cfg, rel, _ := downloadFixture(t)
	staged, err := Download(context.Background(), cfg, rel)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if err := ScheduleInstall(cfg, *staged); err != nil {
		t.Fatalf("ScheduleInstall: %v", err)
	}
	installing := staged.Path + ".installing"
	if err := os.Rename(staged.Path, installing); err != nil {
		t.Fatalf("simulate interrupted replace: %v", err)
	}

	applied, err := ApplyPendingInstall(cfg)
	if err != nil {
		t.Fatalf("ApplyPendingInstall after interrupted replace: %v", err)
	}
	if !applied {
		t.Fatalf("applied = false, want true (resume interrupted replace)")
	}
	replacer := cfg.Replacer.(*fakeReplacer)
	calls := replacer.replaceCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], installing) {
		t.Fatalf("Replace calls = %v, want one call with the .installing path", calls)
	}
	for _, p := range []string{installing, stagingDir(cfg) + string(filepath.Separator) + "pending.json"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after recovery: %v", p, err)
		}
	}
}

// TestApplyPendingInstallSelfHealsMissingDownload covers a marker whose staged
// file disappeared (e.g. the user cleaned the data directory): startup must
// not fail, the marker is dropped.
func TestApplyPendingInstallSelfHealsMissingDownload(t *testing.T) {
	cfg, rel, _ := downloadFixture(t)
	staged, err := Download(context.Background(), cfg, rel)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if err := ScheduleInstall(cfg, *staged); err != nil {
		t.Fatalf("ScheduleInstall: %v", err)
	}
	if err := os.Remove(staged.Path); err != nil {
		t.Fatalf("remove staged: %v", err)
	}
	applied, err := ApplyPendingInstall(cfg)
	if err != nil || applied {
		t.Fatalf("ApplyPendingInstall = (%v, %v), want (false, nil) for self-heal", applied, err)
	}
	if _, err := os.Stat(pendingPath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("marker was not removed by self-heal: %v", err)
	}
}

// TestApplyPendingInstallSelfHealsCorruptMarker covers a truncated/corrupt
// pending marker: the app must still start (no error) and drop the marker.
func TestApplyPendingInstallSelfHealsCorruptMarker(t *testing.T) {
	cfg := testConfig(t, "")
	if err := os.MkdirAll(stagingDir(cfg), 0o700); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(pendingPath(cfg), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}
	applied, err := ApplyPendingInstall(cfg)
	if err != nil || applied {
		t.Fatalf("ApplyPendingInstall on corrupt marker = (%v, %v), want (false, nil)", applied, err)
	}
	if _, err := os.Stat(pendingPath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("corrupt marker was not removed: %v", err)
	}
}
