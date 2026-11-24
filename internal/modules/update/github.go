package update

// The keyless GitHub Releases source: the rig update package reads the newest
// non-draft, non-prerelease release, selects this platform's asset by the
// release workflow's naming scheme and resolves its SHA-256 from the checksums
// asset (or the asset digest). This file only maps that host-agnostic source
// onto contracts.UpdateInfo and the product's localized sentinel errors.

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	rigupdate "github.com/zeokeel/zeokeel-rig/update"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// defaultGitHubAPIBaseURL is the public GitHub REST API (keyless).
	defaultGitHubAPIBaseURL = rigupdate.DefaultGitHubAPIBaseURL
	// defaultChecksumsAsset is the release asset holding SHA-256 lines.
	defaultChecksumsAsset = rigupdate.DefaultChecksumsAsset
	// githubAPIUserAgent identifies the client to the GitHub REST API, which
	// rejects requests without a User-Agent.
	githubAPIUserAgent = "zeokeel-update/" + Version
)

// githubSource builds the SDK source from the module config. NewGitHubSource
// validates the owner/name pair, so an invalid config surfaces as ErrManifest.
func (s *service) githubSource() (*rigupdate.GitHubSource, error) {
	return rigupdate.NewGitHubSource(rigupdate.GitHubConfig{
		Repo:              s.githubRepo,
		BaseURL:           s.apiBaseURL,
		Client:            s.client,
		ChecksumsAsset:    s.checksumsAsset,
		UserAgent:         githubAPIUserAgent,
		AllowInsecureHTTP: s.allowInsecureHTTP,
	})
}

// checkGitHub implements the GitHub Releases half of Check.
func (s *service) checkGitHub(ctx context.Context) (contracts.UpdateInfo, error) {
	source, err := s.githubSource()
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	result, err := source.Check(ctx, s.appInfo.Version, rigupdate.DefaultPlatform(runtime.GOOS, runtime.GOARCH))
	if err != nil {
		return contracts.UpdateInfo{}, translateCheckError(err)
	}
	return contracts.UpdateInfo{
		CurrentVersion: result.CurrentVersion,
		LatestVersion:  result.LatestVersion,
		Available:      result.Available,
		Notes:          result.Notes,
		URL:            result.Asset.URL,
		SHA256:         result.Asset.SHA256,
		Size:           result.Asset.Size,
	}, nil
}

// translateCheckError maps the SDK's error classification onto the product's
// sentinels while preserving the original error for errors.Is (notably
// context.Canceled/DeadlineExceeded).
func translateCheckError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, rigupdate.ErrInsecureURL):
		return fmt.Errorf("%w: %w", ErrInsecureURL, err)
	case errors.Is(err, rigupdate.ErrNoAsset):
		return fmt.Errorf("%w: %w", ErrNoAsset, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// A cancelled check is not a malformed manifest: the caller needs the
		// cancellation back so the UI can say "cancelled" instead of "the update
		// manifest is invalid" (AUDIT-2026-11-24 B-15). download.go already
		// preserves it this way.
		return err
	default:
		return fmt.Errorf("%w: %w", ErrManifest, err)
	}
}
