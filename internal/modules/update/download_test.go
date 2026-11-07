package update

// Tests for the download half of the update service (contracts.UpdateDownloader):
// staged path/verification, checksum/size failures, HTTP/manifest errors,
// cancellation, path-traversal-safe staging, same-version overwrite and
// concurrent downloads.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// assetEntry is one platform-matching manifest asset.
func assetEntry(url, sha string, size int64) map[string]any {
	return map[string]any{
		"goos":   runtime.GOOS,
		"goarch": runtime.GOARCH,
		"url":    url,
		"sha256": sha,
		"size":   size,
	}
}

// downloadFeed serves a manifest whose asset URL points back at the same test
// server, and serves data for any path ending in the asset name.
func downloadFeed(t *testing.T, version string, data []byte, sha string, size int64, assetStatus int) (string, string) {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manifest.json":
			assetURL := server.URL + "/files/app.zip"
			_, _ = io.WriteString(w, manifestJSON(version, "", []map[string]any{assetEntry(assetURL, sha, size)}))
		case strings.HasSuffix(r.URL.Path, "/app.zip"):
			if assetStatus != 0 {
				w.WriteHeader(assetStatus)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/manifest.json", server.URL
}

// dataDirService builds a download-capable service staged under a temp dir.
func dataDirService(t *testing.T, feedURL string, allowInsecure bool) (*service, string) {
	t.Helper()
	service := newTestService(t, Config{FeedURL: feedURL, AllowInsecureHTTP: allowInsecure}, "1.0.0")
	dataDir := t.TempDir()
	service.dataDir = dataDir
	return service, dataDir
}

// updatesDir returns <dataDir>/updates.
func updatesDir(dataDir string) string { return filepath.Join(dataDir, "updates") }

// regularFiles lists the regular files in dir (missing dir = empty).
func regularFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	return files
}

func TestDownloadStagesVerifiedAsset(t *testing.T) {
	data := []byte("zeokeel release binary content")
	sha := sha256Hex(data)
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha, int64(len(data)), 0)
	service, dataDir := dataDirService(t, feedURL, true)

	download, err := service.Download(context.Background())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if download.Version != "2.0.0" {
		t.Fatalf("Version = %q, want 2.0.0", download.Version)
	}
	if download.SHA256 != sha {
		t.Fatalf("SHA256 = %q, want %q", download.SHA256, sha)
	}
	if download.Size != int64(len(data)) {
		t.Fatalf("Size = %d, want %d", download.Size, len(data))
	}
	stagedDir := updatesDir(dataDir)
	if filepath.Dir(download.Path) != stagedDir {
		t.Fatalf("staged path = %q, want inside %q", download.Path, stagedDir)
	}
	if base := filepath.Base(download.Path); !strings.Contains(base, "2.0.0") || !strings.HasSuffix(base, "app.zip") {
		t.Fatalf("staged name = %q, want <version>-<asset>", base)
	}
	stored, err := os.ReadFile(download.Path)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(stored) != string(data) {
		t.Fatalf("staged content differs from the asset (%d vs %d bytes)", len(stored), len(data))
	}
	info, err := os.Stat(download.Path)
	if err != nil {
		t.Fatalf("stat staged file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("staged mode = %o, want 0644", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(stagedDir)
	if err != nil {
		t.Fatalf("stat updates dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("updates dir mode = %o, want 0755", dirInfo.Mode().Perm())
	}
}

func TestDownloadOverwritesSameVersion(t *testing.T) {
	data := []byte("v2 binary")
	sha := sha256Hex(data)
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha, int64(len(data)), 0)
	service, dataDir := dataDirService(t, feedURL, true)

	first, err := service.Download(context.Background())
	if err != nil {
		t.Fatalf("first Download: %v", err)
	}
	second, err := service.Download(context.Background())
	if err != nil {
		t.Fatalf("second Download: %v", err)
	}
	if second.Path != first.Path {
		t.Fatalf("same-version download path changed: %q -> %q", first.Path, second.Path)
	}
	if files := regularFiles(t, updatesDir(dataDir)); len(files) != 1 {
		t.Fatalf("updates dir = %v, want the same-version file staged once", files)
	}
}

func TestDownloadChecksumMismatchCleansUp(t *testing.T) {
	data := []byte("content")
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha256Hex([]byte("different")), int64(len(data)), 0)
	service, dataDir := dataDirService(t, feedURL, true)

	_, err := service.Download(context.Background())
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("Download = %v, want ErrDownload", err)
	}
	if files := regularFiles(t, updatesDir(dataDir)); len(files) != 0 {
		t.Fatalf("updates dir = %v, want no staged file after a checksum failure", files)
	}
}

func TestDownloadSizeMismatchCleansUp(t *testing.T) {
	data := []byte("content")
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha256Hex(data), int64(len(data)+1), 0)
	service, dataDir := dataDirService(t, feedURL, true)

	_, err := service.Download(context.Background())
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("Download = %v, want ErrDownload", err)
	}
	if files := regularFiles(t, updatesDir(dataDir)); len(files) != 0 {
		t.Fatalf("updates dir = %v, want no staged file after a size failure", files)
	}
}

func TestDownloadMissingSHA(t *testing.T) {
	data := []byte("content")
	feedURL, _ := downloadFeed(t, "2.0.0", data, "", int64(len(data)), 0)
	service, dataDir := dataDirService(t, feedURL, true)

	_, err := service.Download(context.Background())
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("Download = %v, want ErrDownload for a manifest without sha256", err)
	}
	if files := regularFiles(t, updatesDir(dataDir)); len(files) != 0 {
		t.Fatalf("updates dir = %v, want no staged file", files)
	}
}

func TestDownloadErrors(t *testing.T) {
	data := []byte("content")
	sha := sha256Hex(data)

	t.Run("asset status", func(t *testing.T) {
		feedURL, _ := downloadFeed(t, "2.0.0", data, sha, int64(len(data)), http.StatusBadGateway)
		service, _ := dataDirService(t, feedURL, true)
		if _, err := service.Download(context.Background()); !errors.Is(err, ErrDownload) {
			t.Fatalf("Download = %v, want ErrDownload", err)
		}
	})

	t.Run("insecure feed", func(t *testing.T) {
		feedURL, _ := downloadFeed(t, "2.0.0", data, sha, int64(len(data)), 0)
		service, _ := dataDirService(t, feedURL, false)
		if _, err := service.Download(context.Background()); !errors.Is(err, ErrInsecureURL) {
			t.Fatalf("Download = %v, want ErrInsecureURL", err)
		}
	})

	t.Run("no matching asset", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"version":"2.0.0","assets":[{"goos":"plan9","goarch":"mips","url":"https://example.com/x","sha256":"`+sha+`"}]}`)
		}))
		defer server.Close()
		service, _ := dataDirService(t, server.URL, true)
		if _, err := service.Download(context.Background()); !errors.Is(err, ErrNoAsset) {
			t.Fatalf("Download = %v, want ErrNoAsset", err)
		}
	})

	t.Run("bad manifest", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "not json")
		}))
		defer server.Close()
		service, _ := dataDirService(t, server.URL, true)
		if _, err := service.Download(context.Background()); !errors.Is(err, ErrManifest) {
			t.Fatalf("Download = %v, want ErrManifest", err)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		service, _ := dataDirService(t, "", true)
		if _, err := service.Download(context.Background()); !errors.Is(err, ErrDisabled) {
			t.Fatalf("Download = %v, want ErrDisabled", err)
		}
	})
}

func TestDownloadWithoutDataDir(t *testing.T) {
	data := []byte("content")
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha256Hex(data), int64(len(data)), 0)
	service := newTestService(t, Config{FeedURL: feedURL, AllowInsecureHTTP: true}, "1.0.0")
	_, err := service.Download(context.Background())
	if !errors.Is(err, ErrNoDataDir) {
		t.Fatalf("Download without dataDir = %v, want ErrNoDataDir", err)
	}
}

func TestDownloadCanceledContext(t *testing.T) {
	data := []byte("content")
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha256Hex(data), int64(len(data)), 0)
	service, dataDir := dataDirService(t, feedURL, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.Download(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Download(cancelled) = %v, want context.Canceled", err)
	}
	if files := regularFiles(t, updatesDir(dataDir)); len(files) != 0 {
		t.Fatalf("updates dir = %v, want no staged file after cancellation", files)
	}
}

func TestDownloadStagesAssetBaseNameOnly(t *testing.T) {
	data := []byte("traversal content")
	sha := sha256Hex(data)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			// The asset path carries parent-directory segments (encoded and
			// raw); only its base name may name the staged file.
			assetURL := server.URL + "/%2e%2e/%2e%2e/evil.zip"
			_, _ = io.WriteString(w, manifestJSON("2.0.0", "", []map[string]any{assetEntry(assetURL, sha, int64(len(data)))}))
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	service, dataDir := dataDirService(t, server.URL+"/manifest.json", true)

	download, err := service.Download(context.Background())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if filepath.Dir(download.Path) != updatesDir(dataDir) {
		t.Fatalf("staged path = %q, want inside %q", download.Path, updatesDir(dataDir))
	}
	if base := filepath.Base(download.Path); base != "2.0.0-evil.zip" {
		t.Fatalf("staged base = %q, want 2.0.0-evil.zip", base)
	}
}

func TestDownloadConcurrent(t *testing.T) {
	data := []byte("concurrent content")
	sha := sha256Hex(data)
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha, int64(len(data)), 0)
	service, dataDir := dataDirService(t, feedURL, true)

	var group sync.WaitGroup
	results := make([]contracts.UpdateDownload, 2)
	errs := make([]error, 2)
	for index := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			results[index], errs[index] = service.Download(context.Background())
		}(index)
	}
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Download #%d: %v", index, err)
		}
	}
	if results[0].Path != results[1].Path {
		t.Fatalf("concurrent downloads staged different paths: %q vs %q", results[0].Path, results[1].Path)
	}
	stored, err := os.ReadFile(results[0].Path)
	if err != nil || string(stored) != string(data) {
		t.Fatalf("staged content = %q/%v", stored, err)
	}
	if files := regularFiles(t, updatesDir(dataDir)); len(files) != 1 {
		t.Fatalf("updates dir = %v, want one staged file", files)
	}
}

func TestModuleActivationProvidesDownloader(t *testing.T) {
	data := []byte("module wiring content")
	sha := sha256Hex(data)
	feedURL, _ := downloadFeed(t, "2.0.0", data, sha, int64(len(data)), 0)
	dataDir := t.TempDir()

	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	if _, err := root.Provide(contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"}); err != nil {
		t.Fatalf("provide app info: %v", err)
	}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(&fakeLogger{})); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyAppInfo).AddBuiltin(contracts.KeyLogger).AddBuiltin(contracts.KeyDataDir)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "update", Module: ModuleID,
		Config: map[string]any{"feedURL": feedURL, "allowInsecureHTTP": true},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	service, err := root.Require(contracts.KeyUpdate)
	if err != nil {
		t.Fatalf("require KeyUpdate: %v", err)
	}
	downloader, ok := service.(contracts.UpdateDownloader)
	if !ok {
		t.Fatalf("KeyUpdate service %T does not implement contracts.UpdateDownloader", service)
	}
	download, err := downloader.Download(context.Background())
	if err != nil {
		t.Fatalf("Download through the module: %v", err)
	}
	if filepath.Dir(download.Path) != updatesDir(dataDir) {
		t.Fatalf("module download staged outside <dataDir>/updates: %q", download.Path)
	}
	required := make(map[string]bool, len(Module().Requires))
	for _, requirement := range Module().Requires {
		required[requirement.Name] = requirement.Optional
	}
	optional, present := required[contracts.KeyDataDir.Name()]
	if !present || !optional {
		t.Fatalf("module requires = %+v, want an optional %s", Module().Requires, contracts.KeyDataDir.Name())
	}
}

var _ = fmt.Sprintf
