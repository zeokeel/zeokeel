package update

// The keyless GitHub Releases source: read the newest non-draft,
// non-prerelease release from the GitHub REST API, pick this platform's asset
// by the release workflow's naming scheme and resolve its SHA-256 from the
// release's checksums asset (or the asset's GitHub digest). No token or
// signature is required: the release body and asset digests are the trust
// anchor, and Download re-verifies the checksum after streaming the file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// defaultGitHubAPIBaseURL is the public GitHub REST API (keyless).
	defaultGitHubAPIBaseURL = "https://api.github.com"
	// defaultChecksumsAsset is the release asset holding SHA-256 lines.
	defaultChecksumsAsset = "SHA256SUMS.txt"
	// maxChecksumsBytes caps the checksums asset body (1 MiB).
	maxChecksumsBytes = 1 << 20
	// githubAPIUserAgent identifies the client to the GitHub REST API, which
	// rejects requests without a User-Agent.
	githubAPIUserAgent = "zeokeel-update/" + Version
)

// githubRelease is the subset of GET /repos/{owner}/{repo}/releases/latest the
// updater needs. That endpoint never returns a draft or prerelease.
type githubRelease struct {
	TagName string               `json:"tag_name"`
	Body    string               `json:"body"`
	Assets  []githubReleaseAsset `json:"assets"`
}

// githubReleaseAsset is one downloadable file attached to a release.
type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	// Digest is GitHub's optional content digest ("sha256:<hex>").
	Digest string `json:"digest"`
}

// checkGitHub implements the GitHub Releases half of Check.
func (s *service) checkGitHub(ctx context.Context) (contracts.UpdateInfo, error) {
	if err := s.validateURL(s.apiBaseURL); err != nil {
		return contracts.UpdateInfo{}, err
	}
	endpoint, err := s.githubLatestReleaseURL()
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: build release request: %v", ErrManifest, err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", githubAPIUserAgent)
	response, err := s.client.Do(request)
	if err != nil {
		s.logger.Printf("%s: fetch latest release: %v", ModuleID, err)
		return contracts.UpdateInfo{}, fmt.Errorf("update: fetch latest release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		s.logger.Printf("%s: latest release status %s", ModuleID, response.Status)
		return contracts.UpdateInfo{}, fmt.Errorf("%w: latest release status %s", ErrManifest, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxManifestBytes+1))
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("update: read latest release: %w", err)
	}
	if len(data) > maxManifestBytes {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: latest release larger than %d bytes", ErrManifest, maxManifestBytes)
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: latest release: %v", ErrManifest, err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if version == "" {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: latest release has no tag_name", ErrManifest)
	}
	if _, err := parseVersion(version); err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: tag_name %q: %v", ErrManifest, release.TagName, err)
	}
	currentDisplay, currentComparable := currentVersion(s.appInfo.Version)
	comparison, err := CompareVersions(currentComparable, version)
	if err != nil {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: version %q: %v", ErrManifest, version, err)
	}
	// An up-to-date release needs no downloadable asset: the previous release
	// (or a release built before this platform's asset naming) may lack one, and
	// "no update available" must not turn into a check error.
	if comparison >= 0 {
		return contracts.UpdateInfo{
			CurrentVersion: currentDisplay,
			LatestVersion:  version,
			Available:      false,
			Notes:          release.Body,
		}, nil
	}
	asset, ok := pickGitHubAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return contracts.UpdateInfo{}, fmt.Errorf("%w: %s/%s", ErrNoAsset, runtime.GOOS, runtime.GOARCH)
	}
	if err := s.validateURL(asset.BrowserDownloadURL); err != nil {
		return contracts.UpdateInfo{}, err
	}
	sha256, err := s.resolveGitHubSHA256(ctx, release.Assets, asset)
	if err != nil {
		return contracts.UpdateInfo{}, err
	}
	return contracts.UpdateInfo{
		CurrentVersion: currentDisplay,
		LatestVersion:  version,
		Available:      true,
		Notes:          release.Body,
		URL:            asset.BrowserDownloadURL,
		SHA256:         sha256,
		Size:           asset.Size,
	}, nil
}

// githubLatestReleaseURL builds {APIBaseURL}/repos/{owner}/{name}/releases/latest
// from the configured owner/name.
func (s *service) githubLatestReleaseURL() (string, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s.githubRepo), "/")
	if !ok || strings.TrimSpace(owner) == "" || strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("githubRepo must be \"owner/name\", got %q", s.githubRepo)
	}
	return strings.TrimRight(s.apiBaseURL, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/releases/latest", nil
}

// pickGitHubAsset selects exactly one release asset for goos/goarch by the
// release workflow's naming scheme (case-insensitive).
//
//	Windows: <slug>-setup-<version>-<arch>.exe — the NSIS installer the in-app
//	         updater runs. A portable <...>-windows-<arch>.exe is only a legacy
//	         fallback for releases that shipped no installer; a tar.gz archive is
//	         never selected (it cannot replace a per-user installed app).
//	Linux:   <...>-linux-<arch>.AppImage, then <...>-linux-<arch>.tar.gz (older
//	         releases only).
//	macOS:   <slug>-<version>-<arch>.dmg — the signed disk image the updater
//	         mounts; a universal <slug>-<version>.dmg is the fallback. tar.gz
//	         archives are never selected.
func pickGitHubAsset(assets []githubReleaseAsset, goos, goarch string) (githubReleaseAsset, bool) {
	arch := strings.ToLower(strings.TrimSpace(goarch))
	if arch == "" {
		return githubReleaseAsset{}, false
	}
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		return pickWindowsAsset(assets, arch)
	case "linux":
		return pickAssetBySuffixes(assets, []string{
			"-linux-" + arch + ".appimage",
			"-linux-" + arch + ".tar.gz",
		})
	case "darwin":
		return pickAssetBySuffixes(assets, []string{
			"-" + arch + ".dmg",
			// A darwin/universal build is published as <slug>-<version>.dmg,
			// without an architecture token.
			".dmg",
		})
	default:
		return githubReleaseAsset{}, false
	}
}

// pickWindowsAsset prefers the release's NSIS setup installer: it is the asset
// the in-app updater runs, and the only one that updates an installed app.
// Releases from before the installer existed carry a portable
// <...>-windows-<arch>.exe, which the binary layout can still replace.
func pickWindowsAsset(assets []githubReleaseAsset, arch string) (githubReleaseAsset, bool) {
	for _, asset := range assets {
		name := strings.ToLower(strings.TrimSpace(asset.Name))
		if strings.Contains(name, "setup") && strings.HasSuffix(name, "-"+arch+".exe") {
			return asset, true
		}
	}
	for _, asset := range assets {
		name := strings.ToLower(strings.TrimSpace(asset.Name))
		if strings.HasSuffix(name, "-windows-"+arch+".exe") {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

// pickAssetBySuffixes returns the first asset matching one suffix, trying the
// suffixes in preference order.
func pickAssetBySuffixes(assets []githubReleaseAsset, suffixes []string) (githubReleaseAsset, bool) {
	for _, suffix := range suffixes {
		for _, asset := range assets {
			if strings.HasSuffix(strings.ToLower(strings.TrimSpace(asset.Name)), suffix) {
				return asset, true
			}
		}
	}
	return githubReleaseAsset{}, false
}

// resolveGitHubSHA256 returns the lowercase hex SHA-256 of the chosen asset.
// It prefers the checksums asset (advertised checksums are signed by the
// release workflow), falls back to GitHub's own asset digest, and fails loudly
// when neither names the asset: an unverifiable update must not be installable.
func (s *service) resolveGitHubSHA256(ctx context.Context, assets []githubReleaseAsset, chosen githubReleaseAsset) (string, error) {
	var checksumErr error
	if checksumAsset, ok := findGitHubChecksumsAsset(assets, s.checksumsAsset); ok {
		sum, err := s.fetchChecksumForAsset(ctx, checksumAsset, chosen.Name)
		if err == nil {
			return sum, nil
		}
		// A URL-policy violation is not a "missing checksum": it must surface
		// as ErrInsecureURL rather than silently falling back to the digest.
		if errors.Is(err, ErrInsecureURL) {
			return "", err
		}
		checksumErr = err
	} else {
		checksumErr = fmt.Errorf("release has no %q asset", s.checksumsAsset)
	}
	if digest := githubDigestSHA256(chosen.Digest); digest != "" {
		s.logger.Printf("%s: using GitHub digest for %s", ModuleID, chosen.Name)
		return digest, nil
	}
	return "", fmt.Errorf("%w: no sha256 for asset %q: %v", ErrManifest, chosen.Name, checksumErr)
}

// findGitHubChecksumsAsset returns the release asset whose name matches the
// configured checksums asset (case-insensitive).
func findGitHubChecksumsAsset(assets []githubReleaseAsset, name string) (githubReleaseAsset, bool) {
	wanted := strings.TrimSpace(name)
	if wanted == "" {
		return githubReleaseAsset{}, false
	}
	for _, asset := range assets {
		if strings.EqualFold(strings.TrimSpace(asset.Name), wanted) {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

// fetchChecksumForAsset downloads the checksums asset (capped at 1 MiB) and
// returns the matching entry for the chosen asset name.
func (s *service) fetchChecksumForAsset(ctx context.Context, checksumAsset githubReleaseAsset, assetName string) (string, error) {
	if err := s.validateURL(checksumAsset.BrowserDownloadURL); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumAsset.BrowserDownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: build checksums request: %v", ErrManifest, err)
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", githubAPIUserAgent)
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: fetch checksums: %v", ErrManifest, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: checksums status %s", ErrManifest, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxChecksumsBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: read checksums: %v", ErrManifest, err)
	}
	if len(data) > maxChecksumsBytes {
		return "", fmt.Errorf("%w: checksums larger than %d bytes", ErrManifest, maxChecksumsBytes)
	}
	return parseChecksums(data, assetName)
}

// parseChecksums parses a sha256sum-style file (lines "<hex>  <filename>" or
// "<hex> *<filename>") and returns the lowercase hex sum for assetName.
func parseChecksums(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		sum, filename, ok := parseChecksumLine(line)
		if !ok {
			continue
		}
		if checksumNamesAsset(filename, assetName) {
			return sum, nil
		}
	}
	return "", fmt.Errorf("checksums have no entry for %q", assetName)
}

// parseChecksumLine parses one "<hex><two spaces or *><filename>" line.
func parseChecksumLine(line string) (sum, filename string, ok bool) {
	line = strings.TrimSpace(line)
	if len(line) < 66 || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	sum = strings.ToLower(line[:64])
	if !isSHA256Hex(sum) {
		return "", "", false
	}
	rest := line[64:]
	switch {
	case strings.HasPrefix(rest, "  "):
		filename = rest[2:]
	case strings.HasPrefix(rest, " *"):
		filename = rest[2:]
	default:
		return "", "", false
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "", "", false
	}
	return sum, filename, true
}

// checksumNamesAsset matches a checksums-file name against a release asset
// name. sha256sum output may carry a "./" prefix, and some tools record the
// basename only, so both are accepted (case-insensitively).
func checksumNamesAsset(filename, assetName string) bool {
	wanted := strings.TrimSpace(assetName)
	if wanted == "" {
		return false
	}
	name := strings.TrimSpace(filename)
	name = strings.TrimPrefix(name, "./")
	if strings.EqualFold(name, wanted) {
		return true
	}
	return strings.EqualFold(path.Base(name), path.Base(wanted))
}

// githubDigestSHA256 normalizes GitHub's "sha256:<hex>" digest field ("" when
// the digest is absent or not a SHA-256).
func githubDigestSHA256(digest string) string {
	trimmed := strings.ToLower(strings.TrimSpace(digest))
	const prefix = "sha256:"
	if !strings.HasPrefix(trimmed, prefix) {
		return ""
	}
	sum := trimmed[len(prefix):]
	if !isSHA256Hex(sum) {
		return ""
	}
	return sum
}

// isSHA256Hex reports whether raw is exactly 64 lowercase hex characters.
func isSHA256Hex(raw string) bool {
	if len(raw) != 64 {
		return false
	}
	for _, character := range raw {
		switch {
		case character >= '0' && character <= '9', character >= 'a' && character <= 'f':
		default:
			return false
		}
	}
	return true
}
