package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/updater"
)

// fakeUpdateReplacer is the app-level updater.Replacer seam for tests: it never
// touches the real executable.
type fakeUpdateReplacer struct {
	currentPath  string
	currentErr   error
	replaceCalls int32
	replaceErr   error
}

func (f *fakeUpdateReplacer) CurrentExecutable() (string, error) {
	if f.currentErr != nil {
		return "", f.currentErr
	}
	if f.currentPath == "" {
		return "/opt/zeokeel/zeokeel", nil
	}
	return f.currentPath, nil
}

func (f *fakeUpdateReplacer) Replace(currentPath, stagedPath string) error {
	atomic.AddInt32(&f.replaceCalls, 1)
	if f.replaceErr != nil {
		return f.replaceErr
	}
	if currentPath == "" || stagedPath == "" {
		return errors.New("fake replacer: empty path")
	}
	return nil
}

func (f *fakeUpdateReplacer) calls() int { return int(atomic.LoadInt32(&f.replaceCalls)) }

// updateFeed is one httptest release feed with a matching asset.
type updateFeed struct {
	server    *httptest.Server
	version   string
	payload   []byte
	badSHA    bool
	assetGets int32
	feedGets  int32
}

// newUpdateFeed serves /feed.json and /asset.
func newUpdateFeed(t *testing.T, version string, payload []byte, badSHA bool) *updateFeed {
	t.Helper()
	feed := &updateFeed{version: version, payload: payload, badSHA: badSHA}
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&feed.assetGets, 1)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(feed.payload)
	})
	mux.HandleFunc("/feed.json", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&feed.feedGets, 1)
		sum := sha256.Sum256(feed.payload)
		sha := hex.EncodeToString(sum[:])
		if feed.badSHA {
			sha = "0000000000000000000000000000000000000000000000000000000000000000"
		}
		manifest := map[string]any{
			"version": feed.version,
			"notes":   "test release",
			"assets": []map[string]any{{
				"goos":   runtime.GOOS,
				"goarch": runtime.GOARCH,
				"url":    feed.server.URL + "/asset",
				"sha256": sha,
				"size":   len(feed.payload),
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manifest)
	})
	feed.server = httptest.NewServer(mux)
	t.Cleanup(feed.server.Close)
	return feed
}

// updateTestApp returns the app + store with the update data dir and feed set.
func updateTestApp(t *testing.T, feedURL string) (*app, *storeForUpdate) {
	t.Helper()
	t.Setenv("ZEOKEEL_UPDATE_ALLOW_HTTP", "1")
	prevVersion := BuildVersion
	BuildVersion = "1.2.3"
	t.Cleanup(func() { BuildVersion = prevVersion })
	a, st := newSettingsTestApp(t)
	if feedURL != "" {
		if err := st.SetSetting(updateFeedURLKey, feedURL); err != nil {
			t.Fatalf("SetSetting(update.feed_url): %v", err)
		}
	}
	// a.updateDataDir() follows the live store's data directory.
	return a, &storeForUpdate{st: st, dir: a.updateDataDir()}
}

// storeForUpdate bundles the store and the data dir a test configured.
type storeForUpdate struct {
	st  *store.Store
	dir string
}

func TestDownloadAndInstallUpdateNoFeed(t *testing.T) {
	a, _ := updateTestApp(t, "")
	_, err := a.DownloadAndInstallUpdate(context.Background(), nil)
	if !errors.Is(err, updater.ErrDisabled) {
		t.Fatalf("DownloadAndInstallUpdate without feed = %v, want updater.ErrDisabled", err)
	}
}

func TestDownloadAndInstallUpdateDownloadsAndApplies(t *testing.T) {
	feed := newUpdateFeed(t, "9.9.9", []byte("new-binary-payload"), false)
	a, _ := updateTestApp(t, feed.server.URL+"/feed.json")
	a.updateReplacer = &fakeUpdateReplacer{}
	var downloaded, total int64
	result, err := a.DownloadAndInstallUpdate(context.Background(), func(d, tt int64) {
		downloaded, total = d, tt
	})
	if err != nil {
		t.Fatalf("DownloadAndInstallUpdate: %v", err)
	}
	if result == nil || !result.Staged || !result.Applied {
		t.Fatalf("result = %+v, want staged+applied", result)
	}
	if result.ReleaseVersion != "9.9.9" {
		t.Fatalf("result version = %q, want 9.9.9", result.ReleaseVersion)
	}
	if downloaded == 0 || total != int64(len(feed.payload)) {
		t.Fatalf("progress = (%d, %d), want (%d, %d)", downloaded, total, len(feed.payload), len(feed.payload))
	}
	if a.updateReplacer.(*fakeUpdateReplacer).calls() != 1 {
		t.Fatalf("replace calls = %d, want 1", a.updateReplacer.(*fakeUpdateReplacer).calls())
	}
	// The applied install consumes the pending marker and the staged file (the
	// replicer moves it over the running executable).
	if marker := filepath.Join(a.updateDataDir(), "updates", "pending.json"); fileExists(marker) {
		t.Fatalf("pending marker still present after an applied install: %s", marker)
	}
	if result.StagedPath == "" {
		t.Fatal("result did not record the staged download path")
	}
}

func TestDownloadAndInstallUpdateChecksumFailureKeepsBinary(t *testing.T) {
	feed := newUpdateFeed(t, "9.9.9", []byte("corrupted"), true)
	a, _ := updateTestApp(t, feed.server.URL+"/feed.json")
	fake := &fakeUpdateReplacer{}
	a.updateReplacer = fake
	if _, err := a.DownloadAndInstallUpdate(context.Background(), nil); !errors.Is(err, updater.ErrChecksumMismatch) {
		t.Fatalf("checksum failure = %v, want updater.ErrChecksumMismatch", err)
	}
	if fake.calls() != 0 {
		t.Fatalf("replace calls = %d, want 0 (a bad download must not touch the binary)", fake.calls())
	}
	if marker := filepath.Join(a.updateDataDir(), "updates", "pending.json"); fileExists(marker) {
		t.Fatalf("checksum failure left a pending install marker: %s", marker)
	}
}

func TestDownloadAndInstallUpdateUpToDate(t *testing.T) {
	// The build version and the manifest version match: nothing to download.
	feed := newUpdateFeed(t, "1.2.3", []byte("same-version"), false)
	a, _ := updateTestApp(t, feed.server.URL+"/feed.json")
	fake := &fakeUpdateReplacer{}
	a.updateReplacer = fake
	result, err := a.DownloadAndInstallUpdate(context.Background(), nil)
	if err != nil {
		t.Fatalf("DownloadAndInstallUpdate: %v", err)
	}
	if result == nil || !result.UpToDate || result.Staged || result.Applied {
		t.Fatalf("result = %+v, want up-to-date (no staged/applied)", result)
	}
	if fake.calls() != 0 {
		t.Fatalf("replace calls = %d, want 0", fake.calls())
	}
}

func TestApplyPendingUpdateOnStartup(t *testing.T) {
	feed := newUpdateFeed(t, "9.9.9", []byte("staged-stale-payload"), false)
	_ = feed
	a, info := updateTestApp(t, "")
	cfg := updater.Config{CurrentVersion: "1.2.3", DataDir: info.dir}
	stagingDir := filepath.Join(info.dir, "updates")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stagedPath := filepath.Join(stagingDir, "zeokeel-9.9.9-linux-amd64")
	if err := os.WriteFile(stagedPath, []byte("staged-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("staged-binary")
	sum := sha256.Sum256(payload)
	if err := updater.ScheduleInstall(cfg, updater.Staged{
		Version: "9.9.9",
		Path:    stagedPath,
		SHA256:  hex.EncodeToString(sum[:]),
	}); err != nil {
		t.Fatalf("ScheduleInstall: %v", err)
	}
	fake := &fakeUpdateReplacer{}
	a.updateReplacer = fake
	applied, err := a.applyPendingUpdate()
	if err != nil || !applied {
		t.Fatalf("applyPendingUpdate = %v, %v; want applied", applied, err)
	}
	if fake.calls() != 1 {
		t.Fatalf("replace calls = %d, want 1", fake.calls())
	}
	if again, err := a.applyPendingUpdate(); err != nil || again {
		t.Fatalf("second applyPendingUpdate = %v, %v; want false, nil (marker consumed)", again, err)
	}
}

func TestApplyPendingUpdateCorruptMarkerSelfHeals(t *testing.T) {
	a, info := updateTestApp(t, "")
	marker := filepath.Join(info.dir, "updates", "pending.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeUpdateReplacer{}
	a.updateReplacer = fake
	applied, err := a.applyPendingUpdate()
	if err != nil || applied {
		t.Fatalf("corrupt marker applyPendingUpdate = %v, %v; want false, nil", applied, err)
	}
	if fileExists(marker) {
		t.Fatal("corrupt pending marker was not removed")
	}
	if fake.calls() != 0 {
		t.Fatalf("replace calls = %d, want 0", fake.calls())
	}
}

func TestDownloadAndInstallUpdateReplaceFailureKeepsStaged(t *testing.T) {
	feed := newUpdateFeed(t, "9.9.9", []byte("install-will-fail"), false)
	a, _ := updateTestApp(t, feed.server.URL+"/feed.json")
	fake := &fakeUpdateReplacer{replaceErr: fmt.Errorf("replace: permission denied")}
	a.updateReplacer = fake
	result, err := a.DownloadAndInstallUpdate(context.Background(), nil)
	if err == nil || !errors.Is(err, updater.ErrInstallFailed) {
		t.Fatalf("replace failure = %v, want updater.ErrInstallFailed", err)
	}
	if result == nil || !result.Staged || result.Applied {
		t.Fatalf("result = %+v, want staged (kept for next start) but not applied", result)
	}
	if !fileExists(result.StagedPath) {
		t.Fatalf("staged download was not kept for the next start: %s", result.StagedPath)
	}
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
