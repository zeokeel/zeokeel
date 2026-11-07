package host

// Install-on-restart: the update module schedules a verified download in
// <dataDir>/updates/pending.json; at the next process start the host replaces
// the running executable with the staged file. The plan is kept if anything
// fails, so the next start can retry; a successful replace removes both the plan
// and the staged file. This runs before the assembly/window exist, so a bad plan
// can never take the UI down with it (errors are logged, never fatal).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Host-side half of the pending.json schema (the update module writes it).
const (
	updatesDirName     = "updates"
	pendingInstallName = "pending.json"
)

// pendingInstallFile mirrors the update module's pending.json.
type pendingInstallFile struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// ExecutableReplacer replaces the running executable with the staged update.
// The process uses defaultExecutableReplacer; tests inject fakes so neither the
// test binary nor the developer's binary is ever replaced.
type ExecutableReplacer interface {
	// CurrentExecutable reports the executable to replace.
	CurrentExecutable() (string, error)
	// Replace replaces that executable with the staged file. It returns an
	// error without touching the current executable when that is not possible.
	Replace(stagedPath string) error
}

// defaultExecutableReplacer replaces the executable in place: it copies the
// staged file to a temporary file next to the current executable and renames it
// over the target (atomic on the same filesystem). Linux/macOS allow renaming
// over a running executable; Windows cannot replace a running .exe, so Replace
// reports a readable error there and the pending plan stays for a future
// release with an external updater. The staged file was SHA-256 verified when
// it was scheduled (and re-verified by the download).
type defaultExecutableReplacer struct{}

// CurrentExecutable implements ExecutableReplacer.
func (defaultExecutableReplacer) CurrentExecutable() (string, error) {
	return os.Executable()
}

// Replace implements ExecutableReplacer.
func (defaultExecutableReplacer) Replace(stagedPath string) error {
	if runtime.GOOS == "windows" {
		// A running executable cannot be renamed over on Windows; installing on
		// restart needs a helper that runs before the app locks its binary. Keep
		// the plan so such a helper can pick it up later.
		return errors.New("cannot replace a running executable on Windows; install the package manually")
	}
	staged := strings.TrimSpace(stagedPath)
	if staged == "" {
		return errors.New("staged update path is empty")
	}
	info, err := os.Stat(staged)
	if err != nil {
		return fmt.Errorf("stat staged update: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged update %q is not a regular file", staged)
	}
	current, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running executable: %w", err)
	}
	// Resolve a symlinked executable so the rename replaces the real binary.
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}
	targetDir := filepath.Dir(current)
	tmp, err := os.CreateTemp(targetDir, ".zeokeel-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create executable temp file in %q: %w", targetDir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	source, err := os.Open(staged)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("open staged update: %w", err)
	}
	_, copyErr := io.Copy(tmp, source)
	closeSourceErr := source.Close()
	if copyErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy staged update: %w", copyErr)
	}
	if closeSourceErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("close staged update: %w", closeSourceErr)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod executable temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync executable temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close executable temp file: %w", err)
	}
	if err := os.Rename(tmpName, current); err != nil {
		return fmt.Errorf("replace executable %q: %w", current, err)
	}
	committed = true
	return nil
}

// applyPendingUpdate applies the scheduled install, if any. It returns
// applied=true only after the executable was replaced and the plan cleaned up.
// Every failure keeps pending.json (and the staged file) for a retry.
func applyPendingUpdate(dataDir string, replacer ExecutableReplacer, logger contracts.Logger) (bool, error) {
	if strings.TrimSpace(dataDir) == "" {
		return false, errors.New("no data directory for pending update")
	}
	updatesDir := filepath.Join(dataDir, updatesDirName)
	pendingPath := filepath.Join(updatesDir, pendingInstallName)
	body, err := os.ReadFile(pendingPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read pending update: %w", err)
	}
	var pending pendingInstallFile
	if err := json.Unmarshal(body, &pending); err != nil {
		return false, fmt.Errorf("parse pending update: %w", err)
	}
	version := strings.TrimSpace(pending.Version)
	staged := strings.TrimSpace(pending.Path)
	if version == "" || staged == "" {
		return false, errors.New("pending update is incomplete (version/path)")
	}
	staged, err = stagingPathBelowUpdates(updatesDir, staged)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(staged)
	if err != nil {
		return false, fmt.Errorf("stat staged update: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("staged update %q is not a regular file", staged)
	}
	if replacer == nil {
		return false, errors.New("no executable replacer")
	}
	if _, err := replacer.CurrentExecutable(); err != nil {
		return false, fmt.Errorf("locate running executable: %w", err)
	}
	if err := replacer.Replace(staged); err != nil {
		return false, fmt.Errorf("replace executable: %w", err)
	}
	// The executable is replaced; clean up best effort. A leftover plan would
	// try to re-apply an already installed file on the next start.
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		logInstallWarning(logger, "remove installed pending plan %q: %v", pendingPath, err)
	}
	if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
		logInstallWarning(logger, "remove installed staged file %q: %v", staged, err)
	}
	return true, nil
}

// stagingPathBelowUpdates resolves path and verifies it is below updatesDir.
func stagingPathBelowUpdates(updatesDir, path string) (string, error) {
	absUpdates, err := filepath.Abs(updatesDir)
	if err != nil {
		return "", fmt.Errorf("resolve updates dir: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve pending update path: %w", err)
	}
	relative, err := filepath.Rel(absUpdates, absPath)
	if err != nil {
		return "", fmt.Errorf("relativize pending update path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("pending update path %q is outside %q", absPath, absUpdates)
	}
	return absPath, nil
}

// applyPendingUpdateOnStartup runs at process start and never blocks startup:
// the outcome is logged and a failure simply keeps the plan for the next start.
func applyPendingUpdateOnStartup(dataDir string, logger contracts.Logger) {
	applied, err := applyPendingUpdate(dataDir, defaultExecutableReplacer{}, logger)
	if err != nil {
		if logger != nil {
			logger.Printf("%s: pending update install failed (plan kept): %v", appName, err)
		}
		return
	}
	if applied && logger != nil {
		logger.Printf("%s: staged update installed; it takes effect on the next start", appName)
	}
}

// logInstallWarning logs a best-effort cleanup warning when a logger is present.
func logInstallWarning(logger contracts.Logger, format string, args ...any) {
	if logger != nil {
		logger.Printf("%s: update cleanup: "+format, append([]any{appName}, args...)...)
	}
}
