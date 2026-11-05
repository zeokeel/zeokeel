package updater

// TDD scaffold: this file defines the public surface and behaviour contract of
// the auto-updater. Every exported function returns ErrNotImplemented in this
// round so `go test ./internal/updater` is RED by design while the rest of the
// repository keeps building. The tests in updater_test.go specify the intended
// behaviour and will turn green as the implementation lands.
//
// Do not relax the tests to match this scaffold: implement the functions
// instead (the package doc in doc.go is the contract).

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Sentinel errors. Callers classify failures with errors.Is.
var (
	// ErrNotImplemented reports that a function is part of the TDD scaffold and
	// has no implementation yet. It disappears as the package lands.
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

// Config is one update run's configuration. The zero value is not usable:
// CurrentVersion and DataDir are required; a missing FeedURL means auto-update
// is off (ErrDisabled).
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

// Check fetches the manifest, selects this platform's asset and reports whether
// a newer version is available. Same or older versions are not an error:
// (nil, false, nil). See the package doc for the security contract.
func Check(ctx context.Context, cfg Config) (*Release, bool, error) {
	return nil, false, ErrNotImplemented
}

// Download streams the release asset into <DataDir>/updates, verifies its
// SHA-256 and returns the staged file. A checksum mismatch discards the
// download (ErrChecksumMismatch) and leaves no partial file.
func Download(ctx context.Context, cfg Config, rel Release) (*Staged, error) {
	return nil, ErrNotImplemented
}

// ScheduleInstall records a verified download as pending for the next start.
// It rejects paths outside <DataDir>/updates.
func ScheduleInstall(cfg Config, staged Staged) error {
	return ErrNotImplemented
}

// ApplyPendingInstall is called during startup before the UI opens. It replaces
// the running executable through cfg.Replacer, removes the staging files and
// reports whether an update was applied. A failed replace keeps the pending
// marker for the next start (ErrInstallFailed).
func ApplyPendingInstall(cfg Config) (bool, error) {
	return false, ErrNotImplemented
}

// ParseManifest decodes and validates a release manifest document.
func ParseManifest(data []byte) (Manifest, error) {
	return Manifest{}, ErrNotImplemented
}

// CompareVersions compares two semver-ish versions: -1 when a < b, 0 when a ==
// b, +1 when a > b. Prereleases sort before their release. Invalid versions are
// an error rather than "not newer".
func CompareVersions(a, b string) (int, error) {
	return 0, ErrNotImplemented
}
