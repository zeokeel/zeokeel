package update

// The install half of the update service: a verified download can be scheduled
// for install-on-restart. The plan is a small pending.json below
// <dataDir>/updates; the process host replaces the executable at the next start
// (see contracts.UpdateInstaller and the host's applyPendingUpdate). Keeping the
// plan in the data directory lets the separate process (not the module) read it
// before it loads any assembly.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// pendingInstallName is the scheduled-install file below the updates directory.
const pendingInstallName = "pending.json"

// ErrInstall classifies rejected or unwritable install plans.
var ErrInstall = errors.New("更新安装计划失败")

// compile-time check: the update service satisfies the optional install half.
var _ contracts.UpdateInstaller = (*service)(nil)

// pendingInstallFile is the on-disk pending.json schema (shared with the host).
type pendingInstallFile struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// ScheduleInstall validates a verified download and records it as the pending
// install: the staged file must live below <dataDir>/updates, its content must
// still hash to download.SHA256 and a version is required. The plan is written
// atomically (temporary file + rename), so a crash never leaves half a JSON and
// a repeated schedule simply replaces the previous plan.
func (s *service) ScheduleInstall(download contracts.UpdateDownload) (contracts.UpdateInstallPlan, error) {
	dataDir := strings.TrimSpace(s.dataDir)
	if dataDir == "" {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: %s", ErrNoDataDir, contracts.KeyDataDir.Name())
	}
	version := strings.TrimSpace(download.Version)
	if version == "" {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: staged version is empty", ErrInstall)
	}
	staged, err := stagedPathBelowUpdates(dataDir, download.Path)
	if err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: %v", ErrInstall, err)
	}
	info, err := os.Stat(staged)
	if err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: stat staged asset: %v", ErrInstall, err)
	}
	if !info.Mode().IsRegular() {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: staged asset %q is not a regular file", ErrInstall, staged)
	}
	expected := strings.ToLower(strings.TrimSpace(download.SHA256))
	if expected == "" {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: staged asset has no checksum", ErrInstall)
	}
	actual, err := fileSHA256(staged)
	if err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: hash staged asset: %v", ErrInstall, err)
	}
	if !strings.EqualFold(actual, expected) {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: want sha256 %s, staged file has %s", ErrInstall, expected, actual)
	}

	updatesDir := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(updatesDir, 0o755); err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: create updates dir: %v", ErrInstall, err)
	}
	body, err := json.Marshal(pendingInstallFile{Version: version, Path: staged, SHA256: actual})
	if err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: encode pending plan: %v", ErrInstall, err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(updatesDir, ".pending-*.json")
	if err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: create pending temp: %v", ErrInstall, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: write pending plan: %v", ErrInstall, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: sync pending plan: %v", ErrInstall, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: chmod pending plan: %v", ErrInstall, err)
	}
	if err := tmp.Close(); err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: close pending plan: %v", ErrInstall, err)
	}
	if err := replaceStagedFile(tmpName, filepath.Join(updatesDir, pendingInstallName)); err != nil {
		return contracts.UpdateInstallPlan{}, fmt.Errorf("%w: %v", ErrInstall, err)
	}
	committed = true

	return contracts.UpdateInstallPlan{Version: version, Path: staged, SHA256: actual}, nil
}

// PendingInstall reports the scheduled install. A missing, unreadable or
// structurally invalid plan reports ok=false and no error (there is simply no
// install to apply); the host re-validates before touching the executable.
func (s *service) PendingInstall() (contracts.UpdateInstallPlan, bool) {
	dataDir := strings.TrimSpace(s.dataDir)
	if dataDir == "" {
		return contracts.UpdateInstallPlan{}, false
	}
	body, err := os.ReadFile(filepath.Join(dataDir, updatesDirName, pendingInstallName))
	if err != nil {
		return contracts.UpdateInstallPlan{}, false
	}
	var pending pendingInstallFile
	if err := json.Unmarshal(body, &pending); err != nil {
		return contracts.UpdateInstallPlan{}, false
	}
	version := strings.TrimSpace(pending.Version)
	sha := strings.TrimSpace(pending.SHA256)
	if version == "" || sha == "" {
		return contracts.UpdateInstallPlan{}, false
	}
	staged, err := stagedPathBelowUpdates(dataDir, pending.Path)
	if err != nil {
		return contracts.UpdateInstallPlan{}, false
	}
	return contracts.UpdateInstallPlan{Version: version, Path: staged, SHA256: sha}, true
}

// stagedPathBelowUpdates resolves path and verifies it lives inside
// <dataDir>/updates (containment, so "../" cannot point at another file).
func stagedPathBelowUpdates(dataDir, path string) (string, error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		return "", errors.New("staged asset path is empty")
	}
	updatesDir, err := filepath.Abs(filepath.Join(dataDir, updatesDirName))
	if err != nil {
		return "", fmt.Errorf("resolve updates dir: %w", err)
	}
	staged, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve staged asset path: %w", err)
	}
	relative, err := filepath.Rel(updatesDir, staged)
	if err != nil {
		return "", fmt.Errorf("relativize staged asset path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("staged asset %q is outside %q", staged, updatesDir)
	}
	return staged, nil
}

// fileSHA256 returns the lowercase hex SHA-256 of a regular file.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
