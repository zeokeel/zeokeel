// Package update implements ZeoKeel's update-check backend module: fetch a
// release manifest, compare versions and select this platform's asset. It is
// the "check" phase of the old shell's updater (no download or install).
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/update"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// DefaultTimeout bounds one manifest request (5s).
	DefaultTimeout = 5 * time.Second
	// maxManifestBytes caps the release manifest body (1 MiB).
	maxManifestBytes = 1 << 20
)

// Sentinel errors callers classify with errors.Is.
var (
	// ErrDisabled reports that no update feed is configured.
	ErrDisabled = errors.New("更新源未配置（update.feedURL）")
	// ErrInsecureURL reports a non-https feed/asset URL while insecure HTTP is
	// not explicitly allowed.
	ErrInsecureURL = errors.New("更新地址必须使用 https")
	// ErrManifest reports a malformed or unsupported release manifest.
	ErrManifest = errors.New("更新清单无效")
	// ErrNoAsset reports a manifest without an asset for this GOOS/GOARCH.
	ErrNoAsset = errors.New("没有适用于当前平台的更新包")
)

// Config is the module's typed assembly config.
type Config struct {
	// FeedURL is the release manifest URL (empty disables update checks).
	FeedURL string `json:"feedURL"`
	// AllowInsecureHTTP permits http:// feed/asset URLs (tests only).
	AllowInsecureHTTP bool `json:"allowInsecureHTTP"`
	// TimeoutMs bounds one manifest request (0 = 5000ms).
	TimeoutMs int `json:"timeoutMs"`
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

// Module is the update backend descriptor. It provides contracts.KeyUpdate.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		appInfo, err := ctx.Require(contracts.KeyAppInfo)
		if err != nil {
			return fmt.Errorf("%s: require app info: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		service := newService(cfg, appInfo, logger)
		if _, err := ctx.Provide(contracts.KeyUpdate, contracts.UpdateService(service)); err != nil {
			return fmt.Errorf("%s: provide update service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyUpdate)).
		AddRequires(
			module.Requires(contracts.KeyAppInfo),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel update checker").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if cfg.TimeoutMs < 0 {
		return fmt.Errorf("%s: timeoutMs must not be negative", ModuleID)
	}
	if strings.ContainsRune(cfg.FeedURL, 0) {
		return fmt.Errorf("%s: feedURL contains a NUL byte", ModuleID)
	}
	return nil
}

// service is the contracts.UpdateService implementation.
type service struct {
	feedURL           string
	allowInsecureHTTP bool
	appInfo           contracts.AppInfo
	logger            contracts.Logger
	client            *http.Client
}

var _ contracts.UpdateService = (*service)(nil)

// discardLogger drops update log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// newService builds the checker from the module config and app identity.
func newService(cfg Config, appInfo contracts.AppInfo, logger contracts.Logger) *service {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if cfg.TimeoutMs <= 0 {
		timeout = DefaultTimeout
	}
	if logger == nil {
		logger = discardLogger{}
	}
	return &service{
		feedURL:           strings.TrimSpace(cfg.FeedURL),
		allowInsecureHTTP: cfg.AllowInsecureHTTP,
		appInfo:           appInfo,
		logger:            logger,
		client:            &http.Client{Timeout: timeout},
	}
}

// Check implements contracts.UpdateService.
//
// A nil context is treated as context.Background(), like the other host-facing
// services (approvals, interaction, artifacts, plan, goal): callers such as a
// future CLI may not have a run context, and a nil dereference here would be a
// panic rather than a readable error.
func (s *service) Check(ctx context.Context) (contracts.UpdateInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return contracts.UpdateInfo{}, err
	}
	if s.feedURL == "" {
		return contracts.UpdateInfo{}, fmt.Errorf("%w", ErrDisabled)
	}
	if err := s.validateURL(s.feedURL); err != nil {
		return contracts.UpdateInfo{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.feedURL, nil)
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: build manifest request: %v", ErrManifest, err)
	}
	response, err := s.client.Do(request)
	if err != nil {
		s.logger.Printf("%s: fetch manifest: %v", ModuleID, err)
		return contracts.UpdateInfo{}, fmt.Errorf("update: fetch manifest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.logger.Printf("%s: manifest status %s", ModuleID, response.Status)
		return contracts.UpdateInfo{}, fmt.Errorf("%w: manifest status %s", ErrManifest, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxManifestBytes+1))
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("update: read manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: manifest larger than %d bytes", ErrManifest, maxManifestBytes)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return contracts.UpdateInfo{}, err
	}
	asset, ok := pickAsset(manifest.Assets, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: %s/%s", ErrNoAsset, runtime.GOOS, runtime.GOARCH)
	}
	if err := s.validateURL(asset.URL); err != nil {
		return contracts.UpdateInfo{}, err
	}
	currentDisplay, currentComparable := currentVersion(s.appInfo.Version)
	comparison, err := CompareVersions(currentComparable, manifest.Version)
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: version %q: %v", ErrManifest, manifest.Version, err)
	}
	return contracts.UpdateInfo{
		CurrentVersion: currentDisplay,
		LatestVersion:  manifest.Version,
		Available:      comparison < 0,
		Notes:          manifest.Notes,
		URL:            asset.URL,
		SHA256:         asset.SHA256,
		Size:           asset.Size,
	}, nil
}

// validateURL enforces the https-only policy (http is tests-only).
func (s *service) validateURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("%w: empty URL", ErrInsecureURL)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: invalid URL %q", ErrInsecureURL, raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if s.allowInsecureHTTP {
			return nil
		}
		return fmt.Errorf("%w: %q", ErrInsecureURL, raw)
	default:
		return fmt.Errorf("%w: unsupported scheme in %q", ErrInsecureURL, raw)
	}
}

// currentVersion returns the display version (empty -> "dev") and the value
// used for comparison (unparseable/dev builds compare as 0.0.0, so a release
// feed always offers the update).
func currentVersion(raw string) (display, comparable string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "dev", "0.0.0"
	}
	if _, err := parseVersion(raw); err != nil {
		return raw, "0.0.0"
	}
	return raw, raw
}

// ParseManifest decodes and validates a release manifest document.
func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return Manifest{}, fmt.Errorf("%w: missing version", ErrManifest)
	}
	if _, err := parseVersion(manifest.Version); err != nil {
		return Manifest{}, fmt.Errorf("%w: version %q: %v", ErrManifest, manifest.Version, err)
	}
	return manifest, nil
}

// pickAsset returns the asset matching goos/goarch (exact, case-insensitive).
func pickAsset(assets []Asset, goos, goarch string) (Asset, bool) {
	for _, asset := range assets {
		if strings.EqualFold(asset.GOOS, goos) && strings.EqualFold(asset.GOARCH, goarch) {
			return asset, true
		}
	}
	return Asset{}, false
}
