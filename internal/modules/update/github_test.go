package update

// Tests for the keyless GitHub Releases source: latest-release parsing (tag
// v-prefix, notes), platform asset selection by release-workflow names,
// checksums asset parsing/digest fallback, error paths, config defaults and
// module activation with githubRepo.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// githubLatestPath is the REST endpoint the fake API serves.
const githubLatestPath = "/repos/zeokeel/zeokeel/releases/latest"

// githubTestAsset is one asset of a fake GitHub release.
type githubTestAsset struct {
	Name   string
	Data   []byte
	Digest string
	Size   int64
	Status int
}

// githubTestRelease is one fake GET /releases/latest response plus the files
// its assets point at.
type githubTestRelease struct {
	Status int
	Body   string
	Tag    string
	Notes  string
	Assets []githubTestAsset
}

// githubReleaseServer serves the latest-release payload and the assets it
// links (under /files/<name>).
func githubReleaseServer(t *testing.T, release githubTestRelease) *httptest.Server {
	t.Helper()
	byName := make(map[string]githubTestAsset, len(release.Assets))
	for _, asset := range release.Assets {
		byName[asset.Name] = asset
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == githubLatestPath:
			if release.Status != 0 {
				w.WriteHeader(release.Status)
				return
			}
			if release.Body != "" {
				_, _ = io.WriteString(w, release.Body)
				return
			}
			payload := githubRelease{TagName: release.Tag, Body: release.Notes}
			for _, asset := range release.Assets {
				size := asset.Size
				if size == 0 {
					size = int64(len(asset.Data))
				}
				payload.Assets = append(payload.Assets, githubReleaseAsset{
					Name:               asset.Name,
					BrowserDownloadURL: server.URL + "/files/" + url.PathEscape(asset.Name),
					Size:               size,
					Digest:             asset.Digest,
				})
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Errorf("marshal fake release: %v", err)
				http.Error(w, "encode", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(encoded)
		case strings.HasPrefix(r.URL.Path, "/files/"):
			name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/files/"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			asset, ok := byName[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if asset.Status != 0 {
				w.WriteHeader(asset.Status)
				return
			}
			_, _ = w.Write(asset.Data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// githubService builds a GitHub-backed service pointed at a fake API.
func githubService(t *testing.T, server *httptest.Server, version string) *service {
	t.Helper()
	return newTestService(t, Config{
		GitHubRepo:        "zeokeel/zeokeel",
		APIBaseURL:        server.URL,
		AllowInsecureHTTP: true,
	}, version)
}

// runtimeGitHubAssetName is the release-workflow asset name for this test
// binary's platform (the assets the release workflow publishes).
func runtimeGitHubAssetName(version string) string {
	switch runtime.GOOS {
	case "windows":
		return "zeokeel-setup-" + version + "-" + runtime.GOARCH + ".exe"
	case "darwin":
		return "zeokeel-" + version + "-" + runtime.GOARCH + ".dmg"
	default:
		return "zeokeel-" + version + "-linux-" + runtime.GOARCH + ".AppImage"
	}
}

func TestNewServiceGitHubDefaults(t *testing.T) {
	service := newService(Config{GitHubRepo: " zeokeel/zeokeel ", APIBaseURL: "https://ghe.example.com/api/v3/"}, contracts.AppInfo{}, &fakeLogger{})
	if service.githubRepo != "zeokeel/zeokeel" {
		t.Fatalf("githubRepo = %q", service.githubRepo)
	}
	if service.apiBaseURL != "https://ghe.example.com/api/v3" {
		t.Fatalf("apiBaseURL = %q, want the trailing slash trimmed", service.apiBaseURL)
	}
	if service.checksumsAsset != defaultChecksumsAsset {
		t.Fatalf("checksumsAsset = %q, want %q", service.checksumsAsset, defaultChecksumsAsset)
	}
	defaults := newService(Config{}, contracts.AppInfo{}, nil)
	if defaults.apiBaseURL != defaultGitHubAPIBaseURL {
		t.Fatalf("default apiBaseURL = %q, want %q", defaults.apiBaseURL, defaultGitHubAPIBaseURL)
	}
	if defaults.checksumsAsset != "SHA256SUMS.txt" {
		t.Fatalf("default checksumsAsset = %q", defaults.checksumsAsset)
	}
}

func TestCheckGitHubLatestRelease(t *testing.T) {
	assetData := []byte("zeokeel appimage content")
	sum := sha256Hex(assetData)
	wrongDigest := "sha256:" + strings.Repeat("ff", 32)
	assetName := runtimeGitHubAssetName("2.0.0")
	checksums := strings.ToUpper(sum) + "  ./" + assetName + "\n"
	release := githubTestRelease{
		Tag:   "v2.0.0",
		Notes: "## 这个版本更新了什么\n\n- keyless GitHub Releases",
		Assets: []githubTestAsset{
			// Wrong digest: the checksums entry must win.
			{Name: assetName, Data: assetData, Digest: wrongDigest},
			{Name: "zeokeel-2.0.0.dmg", Data: []byte("dmg")},
			{Name: "SHA256SUMS.txt", Data: []byte(checksums)},
		},
	}
	service := githubService(t, githubReleaseServer(t, release), "1.0.0")

	info, err := service.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.CurrentVersion != "1.0.0" || info.LatestVersion != "2.0.0" || !info.Available {
		t.Fatalf("info = %+v, want an available 1.0.0 -> 2.0.0 update", info)
	}
	if info.Notes != release.Notes {
		t.Fatalf("notes = %q, want the release body", info.Notes)
	}
	if !strings.HasSuffix(info.URL, "/files/"+assetName) {
		t.Fatalf("URL = %q, want the chosen asset", info.URL)
	}
	if info.SHA256 != sum {
		t.Fatalf("SHA256 = %q, want the checksums entry %q (not the digest)", info.SHA256, sum)
	}
	if info.Size != int64(len(assetData)) {
		t.Fatalf("Size = %d, want %d", info.Size, len(assetData))
	}
}

func TestCheckGitHubVersionComparison(t *testing.T) {
	assetName := runtimeGitHubAssetName("1.2.0")
	data := []byte("asset")
	sum := sha256Hex(data)
	release := githubTestRelease{
		Tag: "v1.2.0",
		Assets: []githubTestAsset{
			{Name: assetName, Data: data},
			{Name: "SHA256SUMS.txt", Data: []byte(sum + "  " + assetName + "\n")},
		},
	}
	server := githubReleaseServer(t, release)
	cases := []struct {
		current       string
		wantAvailable bool
	}{
		{"1.1.0", true},
		{"1.2.0", false},
		{"1.3.0", false},
		{"1.2.0-rc.1", true},
		{"dev", true},
	}
	for _, testCase := range cases {
		service := githubService(t, server, testCase.current)
		info, err := service.Check(context.Background())
		if err != nil {
			t.Fatalf("Check(current %q): %v", testCase.current, err)
		}
		if info.LatestVersion != "1.2.0" || info.Available != testCase.wantAvailable {
			t.Fatalf("Check(current %q) = %+v, want available=%v", testCase.current, info, testCase.wantAvailable)
		}
	}
}

func TestCheckGitHubUpToDateWithoutAsset(t *testing.T) {
	// The newest release may predate this platform's asset naming (or carry no
	// assets at all). When the running version is not older, that is "up to
	// date", not a check failure.
	release := githubTestRelease{Tag: "v1.2.0", Notes: "notes"}
	service := githubService(t, githubReleaseServer(t, release), "v1.2.0")
	info, err := service.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Available || info.LatestVersion != "1.2.0" || info.Notes != "notes" {
		t.Fatalf("info = %+v, want an up-to-date 1.2.0 result", info)
	}
	if info.URL != "" || info.SHA256 != "" {
		t.Fatalf("up-to-date result must not name an asset: %+v", info)
	}

	// A newer release still requires the platform asset and its checksum.
	service = githubService(t, githubReleaseServer(t, release), "1.0.0")
	if _, err := service.Check(context.Background()); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("Check for a newer release without an asset = %v, want ErrNoAsset", err)
	}
}

func TestGitHubAssetSelection(t *testing.T) {
	asset := func(names ...string) []githubReleaseAsset {
		assets := make([]githubReleaseAsset, 0, len(names))
		for _, name := range names {
			assets = append(assets, githubReleaseAsset{Name: name})
		}
		return assets
	}
	cases := []struct {
		name   string
		goos   string
		goarch string
		assets []githubReleaseAsset
		want   string
		wantOK bool
	}{
		{
			name:   "windows picks the setup installer over tar.gz and portable exe",
			goos:   "windows",
			goarch: "amd64",
			assets: asset("zeokeel-1.0.0-windows-amd64.tar.gz", "zeokeel-1.0.0-windows-amd64.exe", "zeokeel-setup-1.0.0-amd64.exe"),
			want:   "zeokeel-setup-1.0.0-amd64.exe",
			wantOK: true,
		},
		{
			name:   "windows prefers the setup installer regardless of asset order",
			goos:   "windows",
			goarch: "amd64",
			assets: asset("zeokeel-setup-1.0.0-amd64.exe", "zeokeel-1.0.0-windows-amd64.exe"),
			want:   "zeokeel-setup-1.0.0-amd64.exe",
			wantOK: true,
		},
		{
			name:   "windows falls back to a portable exe when no setup installer is published",
			goos:   "windows",
			goarch: "arm64",
			assets: asset("zeokeel-1.0.0-windows-arm64.exe", "zeokeel-1.0.0-windows-arm64.tar.gz"),
			want:   "zeokeel-1.0.0-windows-arm64.exe",
			wantOK: true,
		},
		{
			name:   "windows never selects a tar.gz archive",
			goos:   "windows",
			goarch: "amd64",
			assets: asset("zeokeel-1.0.0-windows-amd64.tar.gz"),
			wantOK: false,
		},
		{
			name:   "windows matching is case-insensitive",
			goos:   "windows",
			goarch: "amd64",
			assets: asset("ZeoKeel-Setup-1.0.0-AMD64.EXE"),
			want:   "ZeoKeel-Setup-1.0.0-AMD64.EXE",
			wantOK: true,
		},
		{
			name:   "linux prefers AppImage",
			goos:   "linux",
			goarch: "arm64",
			assets: asset("zeokeel-1.0.0-linux-arm64.tar.gz", "zeokeel-1.0.0-linux-arm64.AppImage"),
			want:   "zeokeel-1.0.0-linux-arm64.AppImage",
			wantOK: true,
		},
		{
			name:   "linux falls back to tar.gz for older releases",
			goos:   "linux",
			goarch: "amd64",
			assets: asset("zeokeel-1.0.0-linux-amd64.tar.gz", "zeokeel-1.0.0-linux-arm64.AppImage"),
			want:   "zeokeel-1.0.0-linux-amd64.tar.gz",
			wantOK: true,
		},
		{
			name:   "darwin picks the per-arch dmg over the tar.gz archive",
			goos:   "darwin",
			goarch: "arm64",
			assets: asset("zeokeel-1.0.0-darwin-arm64.tar.gz", "zeokeel-1.0.0-arm64.dmg"),
			want:   "zeokeel-1.0.0-arm64.dmg",
			wantOK: true,
		},
		{
			name:   "darwin falls back to a universal dmg",
			goos:   "darwin",
			goarch: "arm64",
			assets: asset("zeokeel-1.0.0.dmg", "zeokeel-1.0.0-darwin-arm64.tar.gz"),
			want:   "zeokeel-1.0.0.dmg",
			wantOK: true,
		},
		{
			// Linux cross-builds ship the .app as a tar.gz (no macOS runner);
			// it is the fallback when the release has no dmg.
			name:   "darwin falls back to the cross-built app archive",
			goos:   "darwin",
			goarch: "arm64",
			assets: asset("zeokeel-1.0.0-darwin-arm64.tar.gz"),
			want:   "zeokeel-1.0.0-darwin-arm64.tar.gz",
			wantOK: true,
		},
		{
			name:   "unsupported platform",
			goos:   "plan9",
			goarch: "mips",
			assets: asset("zeokeel-1.0.0-linux-amd64.tar.gz"),
			wantOK: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := pickGitHubAsset(testCase.assets, testCase.goos, testCase.goarch)
			if ok != testCase.wantOK {
				t.Fatalf("pickGitHubAsset(%s/%s) = %+v/%v, want ok=%v", testCase.goos, testCase.goarch, got, ok, testCase.wantOK)
			}
			if ok && got.Name != testCase.want {
				t.Fatalf("pickGitHubAsset(%s/%s) = %q, want %q", testCase.goos, testCase.goarch, got.Name, testCase.want)
			}
		})
	}
}

func TestGitHubChecksumParsing(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	other := strings.Repeat("cd", 32)
	cases := []struct {
		name    string
		body    string
		asset   string
		want    string
		wantErr bool
	}{
		{
			name:  "two spaces form",
			body:  other + "  other.tar.gz\n" + sum + "  app.tar.gz\n",
			asset: "app.tar.gz",
			want:  sum,
		},
		{
			name:  "star form",
			body:  sum + " *app.tar.gz\n",
			asset: "app.tar.gz",
			want:  sum,
		},
		{
			name:  "dot-slash prefix",
			body:  sum + "  ./app.tar.gz\n",
			asset: "app.tar.gz",
			want:  sum,
		},
		{
			name:  "uppercase hex is normalized",
			body:  strings.ToUpper(sum) + "  app.tar.gz\n",
			asset: "app.tar.gz",
			want:  sum,
		},
		{
			name:    "missing entry",
			body:    sum + "  other.tar.gz\n",
			asset:   "app.tar.gz",
			wantErr: true,
		},
		{
			name:  "malformed lines are skipped",
			body:  "not a checksum line\n" + sum + "  app.tar.gz\n",
			asset: "app.tar.gz",
			want:  sum,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := parseChecksums([]byte(testCase.body), testCase.asset)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("parseChecksums(%q) = %q, want an error", testCase.body, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseChecksums(%q): %v", testCase.body, err)
			}
			if got != testCase.want {
				t.Fatalf("parseChecksums(%q) = %q, want %q", testCase.body, got, testCase.want)
			}
		})
	}
}

func TestCheckGitHubDigestFallback(t *testing.T) {
	data := []byte("windows archive bytes")
	sum := sha256Hex(data)
	assetName := runtimeGitHubAssetName("2.0.0")
	release := githubTestRelease{
		Tag: "v2.0.0",
		Assets: []githubTestAsset{
			// No checksums asset: GitHub's own digest field is the fallback.
			{Name: assetName, Data: data, Digest: "sha256:" + strings.ToUpper(sum)},
		},
	}
	service := githubService(t, githubReleaseServer(t, release), "1.0.0")
	info, err := service.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.LatestVersion != "2.0.0" || info.SHA256 != sum {
		t.Fatalf("info = %+v, want the GitHub digest as sha256", info)
	}
}

func TestCheckGitHubMissingChecksum(t *testing.T) {
	assetName := runtimeGitHubAssetName("2.0.0")
	baseAssets := []githubTestAsset{{Name: assetName, Data: []byte("bytes")}}
	cases := []struct {
		name   string
		assets []githubTestAsset
	}{
		{name: "no checksums asset and no digest", assets: baseAssets},
		{
			name: "checksums asset lacks the entry",
			assets: append([]githubTestAsset{
				{Name: "SHA256SUMS.txt", Data: []byte(strings.Repeat("ab", 32) + "  other.tar.gz\n")},
			}, baseAssets...),
		},
		{
			name: "checksums fetch fails and no digest",
			assets: append([]githubTestAsset{
				{Name: "SHA256SUMS.txt", Data: []byte("unused"), Status: http.StatusInternalServerError},
			}, baseAssets...),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := githubService(t, githubReleaseServer(t, githubTestRelease{Tag: "v2.0.0", Assets: testCase.assets}), "1.0.0")
			_, err := service.Check(context.Background())
			if !errors.Is(err, ErrManifest) {
				t.Fatalf("Check = %v, want ErrManifest", err)
			}
			if !strings.Contains(err.Error(), "sha256") {
				t.Fatalf("Check error = %v, want it to mention sha256", err)
			}
		})
	}
}

func TestCheckGitHubChecksumsFetchFailureFallsBackToDigest(t *testing.T) {
	data := []byte("archive bytes")
	sum := sha256Hex(data)
	assetName := runtimeGitHubAssetName("2.0.0")
	release := githubTestRelease{
		Tag: "v2.0.0",
		Assets: []githubTestAsset{
			{Name: "SHA256SUMS.txt", Data: []byte("unused"), Status: http.StatusBadGateway},
			{Name: assetName, Data: data, Digest: "sha256:" + sum},
		},
	}
	service := githubService(t, githubReleaseServer(t, release), "1.0.0")
	info, err := service.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.SHA256 != sum {
		t.Fatalf("SHA256 = %q, want the digest fallback %q", info.SHA256, sum)
	}
}

func TestCheckGitHubErrors(t *testing.T) {
	assetName := runtimeGitHubAssetName("2.0.0")
	sum := strings.Repeat("ab", 32)
	valid := githubTestRelease{
		Tag: "v2.0.0",
		Assets: []githubTestAsset{
			{Name: assetName, Data: []byte("bytes")},
			{Name: "SHA256SUMS.txt", Data: []byte(sum + "  " + assetName + "\n")},
		},
	}
	t.Run("non-200", func(t *testing.T) {
		service := githubService(t, githubReleaseServer(t, githubTestRelease{Status: http.StatusBadGateway}), "1.0.0")
		_, err := service.Check(context.Background())
		if !errors.Is(err, ErrManifest) || !strings.Contains(err.Error(), "502") {
			t.Fatalf("Check = %v, want an ErrManifest with the status", err)
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		service := githubService(t, githubReleaseServer(t, githubTestRelease{Body: "{not json"}), "1.0.0")
		if _, err := service.Check(context.Background()); !errors.Is(err, ErrManifest) {
			t.Fatalf("Check = %v, want ErrManifest", err)
		}
	})
	t.Run("empty assets", func(t *testing.T) {
		service := githubService(t, githubReleaseServer(t, githubTestRelease{Tag: "v2.0.0"}), "1.0.0")
		if _, err := service.Check(context.Background()); !errors.Is(err, ErrNoAsset) {
			t.Fatalf("Check = %v, want ErrNoAsset", err)
		}
	})
	t.Run("missing tag", func(t *testing.T) {
		release := valid
		release.Tag = ""
		service := githubService(t, githubReleaseServer(t, release), "1.0.0")
		if _, err := service.Check(context.Background()); !errors.Is(err, ErrManifest) {
			t.Fatalf("Check = %v, want ErrManifest", err)
		}
	})
	t.Run("invalid tag", func(t *testing.T) {
		release := valid
		release.Tag = "vnot-a-version"
		service := githubService(t, githubReleaseServer(t, release), "1.0.0")
		if _, err := service.Check(context.Background()); !errors.Is(err, ErrManifest) {
			t.Fatalf("Check = %v, want ErrManifest", err)
		}
	})
	t.Run("insecure api base", func(t *testing.T) {
		config := Config{GitHubRepo: "zeokeel/zeokeel", APIBaseURL: "http://api.example.com"}
		service := newTestService(t, config, "1.0.0")
		if _, err := service.Check(context.Background()); !errors.Is(err, ErrInsecureURL) {
			t.Fatalf("Check = %v, want ErrInsecureURL", err)
		}
	})
	t.Run("invalid repo", func(t *testing.T) {
		config := Config{GitHubRepo: "not-a-repo", APIBaseURL: "https://api.example.com"}
		service := newTestService(t, config, "1.0.0")
		if _, err := service.Check(context.Background()); !errors.Is(err, ErrManifest) {
			t.Fatalf("Check = %v, want ErrManifest", err)
		}
	})
	t.Run("checksums too large", func(t *testing.T) {
		release := valid
		release.Assets = []githubTestAsset{
			{Name: assetName, Data: []byte("bytes")},
			{Name: "SHA256SUMS.txt", Data: []byte(strings.Repeat(" ", maxChecksumsBytes+1))},
		}
		service := githubService(t, githubReleaseServer(t, release), "1.0.0")
		if _, err := service.Check(context.Background()); !errors.Is(err, ErrManifest) {
			t.Fatalf("Check = %v, want ErrManifest", err)
		}
	})
}

func TestModuleActivationProvidesGitHubUpdateService(t *testing.T) {
	assetName := runtimeGitHubAssetName("2.0.0")
	data := []byte("release bytes")
	sum := sha256Hex(data)
	server := githubReleaseServer(t, githubTestRelease{
		Tag: "v2.0.0",
		Assets: []githubTestAsset{
			{Name: assetName, Data: data},
			{Name: "SHA256SUMS.txt", Data: []byte(sum + "  " + assetName + "\n")},
		},
	})

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
		Config: map[string]any{
			"githubRepo":        "zeokeel/zeokeel",
			"apiBaseURL":        server.URL,
			"allowInsecureHTTP": true,
			"timeoutMs":         2000,
		},
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
	if !info.Available || info.LatestVersion != "2.0.0" || info.SHA256 != sum {
		t.Fatalf("info = %+v, want the GitHub 2.0.0 update", info)
	}
}

func TestGitHubDigestParsing(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	cases := map[string]string{
		"sha256:" + sum:                      sum,
		"SHA256:" + sum:                      sum,
		"sha256:" + strings.Repeat("zz", 32): "",
		"sha512:" + sum:                      "",
		"":                                   "",
		sum:                                  "",
	}
	for digest, want := range cases {
		if got := githubDigestSHA256(digest); got != want {
			t.Fatalf("githubDigestSHA256(%q) = %q, want %q", digest, got, want)
		}
	}
}

func TestGitHubLatestReleaseURL(t *testing.T) {
	service := newService(Config{GitHubRepo: "zeokeel/zeokeel", APIBaseURL: "https://api.github.com/"}, contracts.AppInfo{Version: "1.0.0"}, nil)
	endpoint, err := service.githubLatestReleaseURL()
	if err != nil {
		t.Fatalf("githubLatestReleaseURL: %v", err)
	}
	if endpoint != "https://api.github.com/repos/zeokeel/zeokeel/releases/latest" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	invalid := []string{"", "zeokeel", "/zeokeel", "zeokeel/", "a/b/c"}
	for _, repo := range invalid {
		bad := newService(Config{GitHubRepo: repo}, contracts.AppInfo{}, nil)
		if _, err := bad.githubLatestReleaseURL(); err == nil {
			t.Fatalf("githubLatestReleaseURL(%q) accepted an invalid repo", repo)
		}
	}
}

func TestGitHubInsecureChecksumURLIsPolicyError(t *testing.T) {
	// The checksum URL is validated like the API/asset URLs: an http checksum
	// URL must surface ErrInsecureURL even when the release asset carries a
	// usable GitHub digest (no silent downgrade).
	sum := strings.Repeat("ab", 32)
	service := newService(Config{
		GitHubRepo: "zeokeel/zeokeel",
		APIBaseURL: "https://api.github.com",
	}, contracts.AppInfo{Version: "1.0.0"}, nil)
	assets := []githubReleaseAsset{{
		Name:               "SHA256SUMS.txt",
		BrowserDownloadURL: "http://insecure.example/SHA256SUMS.txt",
	}}
	chosen := githubReleaseAsset{Name: "zeokeel-2.0.0-linux-amd64.tar.gz", Digest: "sha256:" + sum}
	if _, err := service.resolveGitHubSHA256(context.Background(), assets, chosen); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("resolveGitHubSHA256(insecure checksums) = %v, want ErrInsecureURL", err)
	}
}
