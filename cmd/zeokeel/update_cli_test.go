package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// updateFeed is a manifest+asset fixture for the runtime's GOOS/GOARCH.
type updateFeed struct {
	srv  *httptest.Server
	body []byte
	sha  string
}

// newUpdateFeed starts an httptest server whose manifest advertises version and
// one asset for the test platform. wrongSHA serves a deliberately mismatching
// checksum.
func newUpdateFeed(t *testing.T, version string, wrongSHA bool) *updateFeed {
	t.Helper()
	f := &updateFeed{body: []byte("new-binary-bytes")}
	sum := sha256.Sum256(f.body)
	f.sha = hex.EncodeToString(sum[:])
	if wrongSHA {
		f.sha = "deadbeef"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		manifest := map[string]any{
			"version": version,
			"notes":   "test notes",
			"assets": []map[string]any{{
				"goos": runtime.GOOS, "goarch": runtime.GOARCH,
				"url": f.srv.URL + "/asset", "sha256": f.sha, "size": len(f.body),
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manifest)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(f.body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// fakeUpdateReplacer records Replace calls (install tests must never touch the
// real test binary).
type fakeUpdateReplacer struct {
	mu    sync.Mutex
	calls [][2]string
}

func (f *fakeUpdateReplacer) CurrentExecutable() (string, error) { return "/opt/zeokeel/zeokeel", nil }

func (f *fakeUpdateReplacer) Replace(currentPath, stagedPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [2]string{currentPath, stagedPath})
	return nil
}

func (f *fakeUpdateReplacer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// useFakeReplacer swaps the production replacer for the duration of a test.
func useFakeReplacer(t *testing.T) *fakeUpdateReplacer {
	t.Helper()
	old := updateReplacer
	fake := &fakeUpdateReplacer{}
	updateReplacer = fake
	t.Cleanup(func() { updateReplacer = old })
	return fake
}

// setCurrentVersion overrides the injected build version for a test.
func setCurrentVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func parseUpdateJSON(t *testing.T, out []byte) updateResult {
	t.Helper()
	var result updateResult
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	return result
}

func TestRunUpdateCLIHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runUpdateCLI([]string{"--help"}, &out, &errb); code != 0 {
		t.Fatalf("update --help exit code = %d, want 0", code)
	}
	for _, want := range []string{"Usage: zeokeel update", "--check", "--download", "--install", "--json", "ZEOKEEL_UPDATE_FEED"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("update --help output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunUpdateCLIUsageErrors(t *testing.T) {
	cases := [][]string{
		{"--bogus"},
		{"--check", "--download"},
		{"--feed"},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		if code := runUpdateCLI(args, &out, &errb); code != 2 {
			t.Fatalf("runUpdateCLI(%v) exit code = %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Fatalf("runUpdateCLI(%v) wrote stdout: %q", args, out.String())
		}
	}
}

func TestRunUpdateCLINoFeed(t *testing.T) {
	t.Setenv("ZEOKEEL_UPDATE_FEED", "")
	t.Setenv("ZEOKEEL_DATA_DIR", filepath.Join(t.TempDir(), "zk-data"))
	var out, errb bytes.Buffer
	if code := runUpdateCLI(nil, &out, &errb); code != 1 {
		t.Fatalf("update without feed exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "no update feed configured") {
		t.Fatalf("stderr = %q", errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout written on feed error: %q", out.String())
	}
}

func TestRunUpdateCLICheckUpToDate(t *testing.T) {
	feed := newUpdateFeed(t, "1.0.0", false)
	setCurrentVersion(t, "1.0.0")
	t.Setenv("ZEOKEEL_UPDATE_FEED", feed.srv.URL+"/manifest.json")
	t.Setenv("ZEOKEEL_UPDATE_ALLOW_HTTP", "1")
	t.Setenv("ZEOKEEL_DATA_DIR", filepath.Join(t.TempDir(), "zk-data"))

	var out, errb bytes.Buffer
	if code := runUpdateCLI([]string{"--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errb.String())
	}
	result := parseUpdateJSON(t, out.Bytes())
	if result.Status != "up_to_date" || result.CurrentVersion != "1.0.0" {
		t.Fatalf("result = %+v, want up_to_date at 1.0.0", result)
	}
}

func TestRunUpdateCLICheckAvailable(t *testing.T) {
	feed := newUpdateFeed(t, "1.2.0", false)
	setCurrentVersion(t, "1.0.0")
	t.Setenv("ZEOKEEL_UPDATE_FEED", feed.srv.URL+"/manifest.json")
	t.Setenv("ZEOKEEL_UPDATE_ALLOW_HTTP", "1")
	t.Setenv("ZEOKEEL_DATA_DIR", filepath.Join(t.TempDir(), "zk-data"))

	var out, errb bytes.Buffer
	if code := runUpdateCLI([]string{"--check", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errb.String())
	}
	result := parseUpdateJSON(t, out.Bytes())
	if result.Status != "update_available" || result.LatestVersion != "1.2.0" || result.Notes != "test notes" {
		t.Fatalf("result = %+v, want update_available 1.2.0", result)
	}
	if result.Action != "check" {
		t.Fatalf("action = %q, want check (default)", result.Action)
	}
}

func TestRunUpdateCLIDownload(t *testing.T) {
	feed := newUpdateFeed(t, "1.2.0", false)
	setCurrentVersion(t, "1.0.0")
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_UPDATE_FEED", feed.srv.URL+"/manifest.json")
	t.Setenv("ZEOKEEL_UPDATE_ALLOW_HTTP", "1")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)

	var out, errb bytes.Buffer
	if code := runUpdateCLI([]string{"--download", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errb.String())
	}
	result := parseUpdateJSON(t, out.Bytes())
	if result.Status != "downloaded" || result.StagedPath == "" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasPrefix(result.StagedPath, filepath.Join(dataDir, "updates")+string(filepath.Separator)) {
		t.Fatalf("staged path %q outside %s/updates", result.StagedPath, dataDir)
	}
	got, err := os.ReadFile(result.StagedPath)
	if err != nil || string(got) != string(feed.body) {
		t.Fatalf("staged content = %q, err %v", got, err)
	}
}

func TestRunUpdateCLIInstall(t *testing.T) {
	feed := newUpdateFeed(t, "1.2.0", false)
	fake := useFakeReplacer(t)
	setCurrentVersion(t, "1.0.0")
	dataDir := filepath.Join(t.TempDir(), "zk-data")
	t.Setenv("ZEOKEEL_UPDATE_FEED", feed.srv.URL+"/manifest.json")
	t.Setenv("ZEOKEEL_UPDATE_ALLOW_HTTP", "1")
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)

	var out, errb bytes.Buffer
	if code := runUpdateCLI([]string{"--install", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d (stderr %q)", code, errb.String())
	}
	result := parseUpdateJSON(t, out.Bytes())
	if result.Status != "installed" || !result.Applied {
		t.Fatalf("result = %+v, want installed/applied", result)
	}
	if fake.callCount() != 1 {
		t.Fatalf("replacer calls = %d, want 1", fake.callCount())
	}
	if _, err := os.Stat(result.StagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged file still exists after install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "updates", "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("pending marker still exists after install: %v", err)
	}
}

func TestRunUpdateCLIChecksumMismatch(t *testing.T) {
	feed := newUpdateFeed(t, "1.2.0", true)
	setCurrentVersion(t, "1.0.0")
	t.Setenv("ZEOKEEL_UPDATE_FEED", feed.srv.URL+"/manifest.json")
	t.Setenv("ZEOKEEL_UPDATE_ALLOW_HTTP", "1")
	t.Setenv("ZEOKEEL_DATA_DIR", filepath.Join(t.TempDir(), "zk-data"))

	var out, errb bytes.Buffer
	if code := runUpdateCLI([]string{"--download"}, &out, &errb); code != 1 {
		t.Fatalf("checksum mismatch exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "checksum") {
		t.Fatalf("stderr = %q, want a checksum error", errb.String())
	}
}

func TestRunCLIUpdateDispatch(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCLI([]string{"update", "--help"}, &out, &errb); code != 0 {
		t.Fatalf("runCLI(update --help) exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Usage: zeokeel update") {
		t.Fatalf("output = %q", out.String())
	}
}

// TestExeReplacerContract pins the production replacer's failure behaviour: a
// failed staged move restores the original binary.
func TestExeReplacerContract(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "zeokeel")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}
	staged := filepath.Join(dir, "staged-missing")
	if err := (exeReplacer{}).Replace(current, staged); err == nil {
		t.Fatal("Replace with a missing staged file = nil error, want failure")
	}
	got, err := os.ReadFile(current)
	if err != nil || string(got) != "old" {
		t.Fatalf("current binary not restored after failed replace: %q, %v", got, err)
	}
}
