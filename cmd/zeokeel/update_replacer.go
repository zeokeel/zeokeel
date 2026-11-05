package main

// exeReplacer is the production updater.Replacer: it swaps the running binary
// with the verified staged download.
//
// Strategy (works on both Unix and Windows, where a running executable can be
// renamed/moved but not deleted/overwritten):
//
//  1. rename the current executable to "<path>.old-<unix>" (kept for rollback);
//  2. move the staged binary into the original path;
//  3. restore the old binary if step 2 fails, so a failed install never leaves
//     the install directory without an executable.
//
// The replace runs inside `zeokeel update --install`; the currently running
// process keeps its open handle, so the new build is what starts next time.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zeokeel/zeokeel/internal/updater"
)

type exeReplacer struct{}

// Compile-time check: the production replacer satisfies the engine seam.
var _ updater.Replacer = exeReplacer{}

// CurrentExecutable implements updater.Replacer.
func (exeReplacer) CurrentExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
		path = resolved
	}
	return path, nil
}

// Replace implements updater.Replacer.
func (exeReplacer) Replace(currentPath, stagedPath string) error {
	if currentPath == "" || stagedPath == "" {
		return fmt.Errorf("replace: empty path")
	}
	backup := fmt.Sprintf("%s.old-%d", currentPath, time.Now().Unix())
	if err := os.Rename(currentPath, backup); err != nil {
		return fmt.Errorf("replace: move current binary aside: %w", err)
	}
	if err := os.Rename(stagedPath, currentPath); err != nil {
		// Roll back so the install directory keeps a working executable.
		if restoreErr := os.Rename(backup, currentPath); restoreErr != nil {
			return fmt.Errorf("replace: move staged binary: %w (rollback failed: %v; backup at %s)", err, restoreErr, backup)
		}
		return fmt.Errorf("replace: move staged binary: %w", err)
	}
	_ = os.Chmod(currentPath, 0o755)
	return nil
}
