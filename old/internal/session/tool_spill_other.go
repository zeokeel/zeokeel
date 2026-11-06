//go:build !linux

package session

import (
	"fmt"
	"os"
	"path/filepath"
)

// createSpillFile creates dir/name with content and verifies afterwards that the
// validated directory still resolves to itself. Non-Linux platforms lack the
// unix openat seam used by the Linux implementation, so this version keeps the
// original O_CREAT|O_EXCL create and adds an identity re-check: the window is
// narrowed (the resolved directory must still be the validated directory) but
// not eliminated like the openat descriptor chain. A failed check removes the
// file and refuses the spill; the caller keeps the full result.
func createSpillFile(dir, name, content string) error {
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("tool-result spill: write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("tool-result spill: close %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(dir) {
		_ = os.Remove(path)
		return fmt.Errorf("tool-result spill: directory %q changed while writing; refusing the spill", dir)
	}
	return nil
}
