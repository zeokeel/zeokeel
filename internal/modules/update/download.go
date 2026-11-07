package update

// The download half of the update service: fetch the manifest's platform
// asset, verify its SHA-256 and size and stage it under <dataDir>/updates.
// Installing/replacing the running binary stays a separate host step (see
// contracts.UpdateDownloader).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Download failures are classified with errors.Is:
var (
	// ErrDownload reports a failed or unverifiable asset download (transport,
	// non-2xx status, missing checksum, checksum/size mismatch, staging I/O).
	ErrDownload = errors.New("更新包下载失败")
	// ErrNoDataDir reports that no data directory is available to stage into;
	// the process host provides contracts.KeyDataDir.
	ErrNoDataDir = errors.New("未配置数据目录，无法暂存更新包")
)

// updatesDirName is the staging directory below the product data directory.
const updatesDirName = "updates"

// compile-time check: the update service satisfies the optional download half.
var _ contracts.UpdateDownloader = (*service)(nil)

// Download implements contracts.UpdateDownloader: it runs the same check as
// Check (feed, manifest, platform asset, https policy), streams the asset into
// a temporary file under <dataDir>/updates while hashing it, verifies the
// manifest SHA-256 and size, and atomically renames it to
// <version>-<asset basename>. Same-version downloads replace the previous staged
// file; a failed download leaves no partial file behind.
func (s *service) Download(ctx context.Context) (contracts.UpdateDownload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return contracts.UpdateDownload{}, err
	}
	dataDir := strings.TrimSpace(s.dataDir)
	if dataDir == "" {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: %s", ErrNoDataDir, contracts.KeyDataDir.Name())
	}

	info, err := s.Check(ctx)
	if err != nil {
		return contracts.UpdateDownload{}, err
	}
	assetURL := strings.TrimSpace(info.URL)
	if assetURL == "" {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: manifest has no asset URL", ErrNoAsset)
	}
	// Check validated the feed; validate the asset URL again because it is the
	// one this method fetches (and Check's policy may be reused differently).
	if err := s.validateURL(assetURL); err != nil {
		return contracts.UpdateDownload{}, err
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(info.SHA256))
	if expectedSHA == "" {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: manifest asset has no sha256", ErrDownload)
	}

	stagedDir := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(stagedDir, 0o755); err != nil {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: create staging dir: %v", ErrDownload, err)
	}
	name := stagedFileName(info.LatestVersion, assetURL)
	finalPath := filepath.Join(stagedDir, name)
	// Defense in depth: stagedFileName only returns one safe path component,
	// and the final path must remain directly below the staging directory.
	if name == "" || name == "." || name == ".." || filepath.Dir(finalPath) != filepath.Clean(stagedDir) {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: unsafe staged file name %q", ErrDownload, name)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: build asset request: %v", ErrDownload, err)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: fetch asset: %v", ErrDownload, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: asset status %s", ErrDownload, response.Status)
	}

	tmp, err := os.CreateTemp(stagedDir, ".update-*.part")
	if err != nil {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: create temp file: %v", ErrDownload, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hasher), response.Body)
	if copyErr != nil {
		_ = tmp.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return contracts.UpdateDownload{}, ctxErr
		}
		return contracts.UpdateDownload{}, fmt.Errorf("%w: read asset: %v", ErrDownload, copyErr)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return contracts.UpdateDownload{}, fmt.Errorf("%w: sync download: %v", ErrDownload, err)
	}
	if err := tmp.Close(); err != nil {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: close download: %v", ErrDownload, err)
	}
	if info.Size > 0 && written != info.Size {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: size %d, manifest says %d", ErrDownload, written, info.Size)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(sum, expectedSHA) {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: want sha256 %s, got %s", ErrDownload, expectedSHA, sum)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: chmod staged file: %v", ErrDownload, err)
	}
	if err := replaceStagedFile(tmpName, finalPath); err != nil {
		return contracts.UpdateDownload{}, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	committed = true

	return contracts.UpdateDownload{
		Version: info.LatestVersion,
		Path:    finalPath,
		SHA256:  sum,
		Size:    written,
	}, nil
}

// stagedFileName derives one safe file name: <version>-<asset basename>. Both
// components are reduced to a single path component (letters, digits, '.', '-',
// '_'), so an asset URL or manifest version cannot escape the updates directory;
// the basename makes the staged file recognizable in the file manager.
func stagedFileName(version, rawURL string) string {
	base := "update.bin"
	if parsed, err := url.Parse(strings.TrimSpace(rawURL)); err == nil {
		if name := path.Base(parsed.Path); name != "" && name != "." && name != "/" && name != ".." {
			base = name
		}
	}
	version = sanitizeFileComponent(version)
	if version == "" {
		version = "update"
	}
	base = sanitizeFileComponent(base)
	if base == "" {
		base = "update.bin"
	}
	return version + "-" + base
}

// sanitizeFileComponent keeps [A-Za-z0-9._-] and trims leading/trailing dots
// and underscores, so the result is never "."/".." and cannot contain a path
// separator.
func sanitizeFileComponent(raw string) string {
	var builder strings.Builder
	builder.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "._")
}

// replaceStagedFile atomically replaces finalPath with tmpPath. Unix rename
// replaces an existing file directly; Windows cannot, so a leftover same-version
// file is removed and the rename retried.
func replaceStagedFile(tmpPath, finalPath string) error {
	if err := os.Rename(tmpPath, finalPath); err == nil {
		return nil
	}
	if err := os.Remove(finalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace staged file: %v", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename staged file: %v", err)
	}
	return nil
}
