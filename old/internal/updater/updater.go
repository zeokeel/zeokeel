package updater

// Auto-update engine: check → download+verify → stage → install-on-restart.
// The executable contract lives in the package doc (doc.go) and in
// updater_test.go; this file is the implementation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Sentinel errors. Callers classify failures with errors.Is.
var (
	// ErrNotImplemented reports that a function is part of the TDD scaffold and
	// has no implementation yet. Reserved: the implementation no longer returns
	// it, but keep it exported so callers written against the RED round still
	// compile.
	ErrNotImplemented = errors.New("updater: not implemented yet")
	// ErrDisabled reports that no update feed is configured (auto-update off).
	ErrDisabled = errors.New("updater: no update feed configured")
	// ErrInsecureURL reports a non-https feed/asset URL while insecure HTTP is
	// not explicitly allowed.
	ErrInsecureURL = errors.New("updater: update URLs must use https")
	// ErrManifest reports a malformed or unsupported release manifest.
	ErrManifest = errors.New("updater: invalid release manifest")
	// ErrNoAsset reports a manifest without an asset for this GOOS/GOARCH.
	ErrNoAsset = errors.New("updater: no asset for this platform")
	// ErrChecksumMismatch reports downloaded bytes whose SHA-256 differs from
	// the manifest. The download is discarded.
	ErrChecksumMismatch = errors.New("updater: download checksum mismatch")
	// ErrInstallFailed reports a failed staged install/replace. A pending
	// update is kept for the next start when possible.
	ErrInstallFailed = errors.New("updater: install failed")
)

// DefaultHTTPTimeout bounds manifest and asset requests when Config.HTTPClient
// is nil.
const DefaultHTTPTimeout = 60 * time.Second

// Config is one update run's configuration.
type Config struct {
	// CurrentVersion is the running build's version (main.version from
	// -ldflags; "0.1.0-dev" on a plain go build).
	CurrentVersion string
	// FeedURL is the release manifest URL (https by default).
	FeedURL string
	// DataDir is the ZeoKeel data directory; staging lives under
	// <DataDir>/updates.
	DataDir string
	// GOOS/GOARCH select the manifest asset. Empty means the runtime's values.
	GOOS   string
	GOARCH string
	// HTTPClient overrides the default client (tests inject httptest clients).
	HTTPClient *http.Client
	// Replacer is the platform seam that swaps the running executable on the
	// next start. Tests inject a fake; production uses the installer package.
	Replacer Replacer
	// Now overrides the clock (tests).
	Now func() time.Time
	// AllowInsecureHTTP permits http:// feed/asset URLs. Tests only; a release
	// build must leave it false.
	AllowInsecureHTTP bool
}

// Manifest is the release feed document.
type Manifest struct {
	Version string  `json:"version"`
	Notes   string  `json:"notes,omitempty"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable build inside a manifest.
type Asset struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

// Release is the checked update: the manifest version plus the asset that
// matches this platform.
type Release struct {
	Version string
	Notes   string
	Asset   Asset
}

// Staged is a verified download waiting for install-on-restart.
type Staged struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// Replacer is the platform abstraction for swapping the running executable on
// restart. Implementations must be atomic (write a sibling file + rename) and
// must not run on Windows while the old binary is locked without scheduling a
// move-on-reboot (see packaging/windows/INSTALLER-PLAN.md).
type Replacer interface {
	// CurrentExecutable reports the path of the running binary.
	CurrentExecutable() (string, error)
	// Replace swaps currentPath with the verified stagedPath.
	Replace(currentPath, stagedPath string) error
}

// Limits that keep a malicious/broken feed from exhausting memory or disk.
const (
	maxManifestBytes = 1 << 20   // 1 MiB manifest cap
	maxAssetBytes    = 512 << 20 // 512 MiB asset cap
)

// installStagingDirName is the subdirectory of DataDir holding downloads and
// the pending marker.
const installStagingDirName = "updates"

// pendingFileName is the marker that schedules an install for the next start.
const pendingFileName = "pending.json"

// installingSuffix marks a staged file mid-replace (crash recovery).
const installingSuffix = ".installing"

// pendingInstall is the on-disk marker written by ScheduleInstall.
type pendingInstall struct {
	Staged
	ScheduledAt time.Time `json:"scheduled_at,omitempty"`
}

// stagingDir returns <DataDir>/updates.
func stagingDir(cfg Config) string {
	return filepath.Join(cfg.DataDir, installStagingDirName)
}

// pendingPath returns the pending marker path.
func pendingPath(cfg Config) string {
	return filepath.Join(stagingDir(cfg), pendingFileName)
}

// ParseManifest decodes and validates a release manifest document.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return Manifest{}, fmt.Errorf("%w: missing version", ErrManifest)
	}
	if _, err := parseVersion(m.Version); err != nil {
		return Manifest{}, fmt.Errorf("%w: version %q: %v", ErrManifest, m.Version, err)
	}
	return m, nil
}

// Check fetches the manifest, selects this platform's asset and reports whether
// a newer version is available.
func Check(ctx context.Context, cfg Config) (*Release, bool, error) {
	if strings.TrimSpace(cfg.FeedURL) == "" {
		return nil, false, ErrDisabled
	}
	if err := checkURL(cfg.FeedURL, cfg.AllowInsecureHTTP); err != nil {
		return nil, false, err
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.FeedURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("%w: build manifest request: %v", ErrManifest, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("updater: fetch manifest: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read error wins

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("%w: manifest status %s", ErrManifest, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("updater: read manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return nil, false, fmt.Errorf("%w: manifest too large", ErrManifest)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, false, err
	}

	goos, goarch := cfg.GOOS, cfg.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	asset, ok := pickAsset(manifest.Assets, goos, goarch)
	if !ok {
		return nil, false, fmt.Errorf("%w: %s/%s", ErrNoAsset, goos, goarch)
	}

	current := strings.TrimSpace(cfg.CurrentVersion)
	if current == "" {
		current = "0.0.0"
	}
	cmp, cmpErr := CompareVersions(current, manifest.Version)
	if cmpErr != nil {
		// An unparseable running version (e.g. a hand-set "dev") means we
		// cannot prove the build is current: offer the update rather than
		// hiding it forever.
		cmp = -1
	}
	if cmp >= 0 {
		return nil, false, nil
	}
	if err := checkURL(asset.URL, cfg.AllowInsecureHTTP); err != nil {
		return nil, false, err
	}
	return &Release{Version: manifest.Version, Notes: manifest.Notes, Asset: asset}, true, nil
}

// pickAsset returns the asset matching goos/goarch (exact match).
func pickAsset(assets []Asset, goos, goarch string) (Asset, bool) {
	for _, a := range assets {
		if strings.EqualFold(a.GOOS, goos) && strings.EqualFold(a.GOARCH, goarch) {
			return a, true
		}
	}
	return Asset{}, false
}

// checkURL enforces the https-only policy (http:// is allowed only for tests).
func checkURL(raw string, allowInsecure bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: invalid URL %q", ErrInsecureURL, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if allowInsecure {
			return nil
		}
		return fmt.Errorf("%w: %q", ErrInsecureURL, raw)
	default:
		return fmt.Errorf("%w: unsupported scheme in %q", ErrInsecureURL, raw)
	}
}

// Download streams the release asset into <DataDir>/updates, verifies its
// SHA-256 and returns the staged file.
func Download(ctx context.Context, cfg Config, rel Release) (*Staged, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, fmt.Errorf("%w: data dir is required", ErrManifest)
	}
	if err := checkURL(rel.Asset.URL, cfg.AllowInsecureHTTP); err != nil {
		return nil, err
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	dir := stagingDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: create staging dir: %v", ErrInstallFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.Asset.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build asset request: %v", ErrManifest, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("updater: download asset: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read error wins
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: asset status %s", ErrManifest, resp.Status)
	}

	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return nil, fmt.Errorf("%w: create temp file: %v", ErrInstallFailed, err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("updater: read asset: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("%w: close download: %v", ErrInstallFailed, err)
	}
	if n > maxAssetBytes {
		return nil, fmt.Errorf("%w: asset too large", ErrManifest)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(sum, strings.TrimSpace(rel.Asset.SHA256)) {
		return nil, fmt.Errorf("%w: want %s, got %s", ErrChecksumMismatch, rel.Asset.SHA256, sum)
	}
	if rel.Asset.Size > 0 && n != rel.Asset.Size {
		return nil, fmt.Errorf("%w: size %d, manifest says %d", ErrChecksumMismatch, n, rel.Asset.Size)
	}

	goos, goarch := rel.Asset.GOOS, rel.Asset.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	finalName := fmt.Sprintf("zeokeel-%s-%s-%s%s", sanitizeVersion(rel.Version), goos, goarch, exeSuffix(goos))
	finalPath := filepath.Join(dir, finalName)
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return nil, fmt.Errorf("%w: chmod staged binary: %v", ErrInstallFailed, err)
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		return nil, fmt.Errorf("%w: rename staged binary: %v", ErrInstallFailed, err)
	}
	removeTemp = false
	return &Staged{Version: rel.Version, Path: finalPath, SHA256: sum}, nil
}

// ScheduleInstall records a verified download as pending for the next start.
func ScheduleInstall(cfg Config, staged Staged) error {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return fmt.Errorf("%w: data dir is required", ErrInstallFailed)
	}
	dir := stagingDir(cfg)
	if err := ensureInside(dir, staged.Path); err != nil {
		return err
	}
	if strings.TrimSpace(staged.SHA256) == "" {
		return fmt.Errorf("%w: staged sha256 is required", ErrInstallFailed)
	}
	if info, err := os.Stat(staged.Path); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: staged file %q is missing", ErrInstallFailed, staged.Path)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w: create staging dir: %v", ErrInstallFailed, err)
	}
	pending := pendingInstall{Staged: staged, ScheduledAt: nowOf(cfg)}
	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode pending marker: %v", ErrInstallFailed, err)
	}
	marker := pendingPath(cfg)
	tmp := marker + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("%w: write pending marker: %v", ErrInstallFailed, err)
	}
	if err := os.Rename(tmp, marker); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: replace pending marker: %v", ErrInstallFailed, err)
	}
	return nil
}

// ApplyPendingInstall replaces the running executable with the pending staged
// update through cfg.Replacer, then removes the staging files.
//
// Crash recovery: the staged file is renamed to "<path>.installing" before the
// replace. If the process dies mid-replace, the next start finds the marker
// pointing at a missing file but the ".installing" sibling present and resumes
// from it. A marker whose file is gone entirely (or a corrupt marker) is
// removed so a broken update can never block startup.
func ApplyPendingInstall(cfg Config) (bool, error) {
	if cfg.Replacer == nil {
		return false, fmt.Errorf("%w: no replacer configured", ErrInstallFailed)
	}
	marker := pendingPath(cfg)
	data, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: read pending marker: %v", ErrInstallFailed, err)
	}
	var pending pendingInstall
	if err := json.Unmarshal(data, &pending); err != nil {
		// Self-heal: a corrupt marker must not brick every future start.
		_ = os.Remove(marker)
		return false, nil
	}
	dir := stagingDir(cfg)
	stagedPath := pending.Path
	if err := ensureInside(dir, stagedPath); err != nil {
		_ = os.Remove(marker)
		return false, err
	}
	installingPath := stagedPath + installingSuffix
	if _, statErr := os.Stat(stagedPath); statErr != nil {
		if _, recErr := os.Stat(installingPath); recErr == nil {
			// Resume an interrupted replace.
			stagedPath = installingPath
		} else {
			// The download disappeared: nothing to install, do not block startup.
			_ = os.Remove(marker)
			return false, nil
		}
	}
	if stagedPath != installingPath {
		if err := os.Rename(stagedPath, installingPath); err != nil {
			return false, fmt.Errorf("%w: mark install in progress: %v", ErrInstallFailed, err)
		}
	}
	current, err := cfg.Replacer.CurrentExecutable()
	if err != nil {
		_ = os.Rename(installingPath, stagedPath)
		return false, fmt.Errorf("%w: locate current executable: %v", ErrInstallFailed, err)
	}
	if err := cfg.Replacer.Replace(current, installingPath); err != nil {
		// Keep the download for the next start; restore its staged name.
		_ = os.Rename(installingPath, stagedPath)
		return false, fmt.Errorf("%w: %v", ErrInstallFailed, err)
	}
	// The replace succeeded. Cleanup failures are harmless (stale files are
	// ignored by future runs) but must not mask a successful install.
	_ = os.Remove(installingPath)
	_ = os.Remove(marker)
	return true, nil
}

// ensureInside reports ErrInstallFailed when path escapes dir.
func ensureInside(dir, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: empty staged path", ErrInstallFailed)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("%w: resolve staging dir: %v", ErrInstallFailed, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: resolve staged path: %v", ErrInstallFailed, err)
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: staged path %q is outside %q", ErrInstallFailed, path, dir)
	}
	return nil
}

// sanitizeVersion makes a version safe as a file-name component.
func sanitizeVersion(version string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(version) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

// exeSuffix returns the staged binary suffix for goos.
func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// nowOf is the injectable clock for the pending marker.
func nowOf(cfg Config) time.Time {
	if cfg.Now != nil {
		return cfg.Now()
	}
	return time.Now()
}

// ---- version comparison -------------------------------------------------

type version struct {
	major, minor, patch int
	pre                 []string
}

// CompareVersions compares two semver-ish versions.
func CompareVersions(a, b string) (int, error) {
	va, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	return compareVersion(va, vb), nil
}

// parseVersion parses MAJOR[.MINOR[.PATCH]][-prerelease][+build].
func parseVersion(s string) (version, error) {
	var v version
	raw := strings.TrimSpace(s)
	if raw == "" {
		return v, fmt.Errorf("empty version")
	}
	// Build metadata is ignored for ordering.
	if i := strings.IndexByte(raw, '+'); i >= 0 {
		raw = raw[:i]
	}
	core := raw
	if i := strings.IndexByte(raw, '-'); i >= 0 {
		core = raw[:i]
		pre := raw[i+1:]
		if pre == "" {
			return v, fmt.Errorf("empty prerelease in %q", s)
		}
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return v, fmt.Errorf("empty prerelease identifier in %q", s)
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("too many version components in %q", s)
	}
	nums := [3]int{}
	for i, part := range parts {
		if part == "" {
			return v, fmt.Errorf("empty version component in %q", s)
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return v, fmt.Errorf("non-numeric version component %q in %q", part, s)
			}
			n = n*10 + int(r-'0')
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, nil
}

func compareVersion(a, b version) int {
	if c := cmpInt(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpInt(a.minor, b.minor); c != 0 {
		return c
	}
	if c := cmpInt(a.patch, b.patch); c != 0 {
		return c
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1 // release > prerelease
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := comparePrereleaseID(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.pre), len(b.pre))
}

func comparePrereleaseID(a, b string) int {
	an, aNum := atoiStrict(a)
	bn, bNum := atoiStrict(b)
	switch {
	case aNum && bNum:
		return cmpInt(an, bn)
	case aNum:
		return -1 // numeric identifiers sort before alphanumeric
	case bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func atoiStrict(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
