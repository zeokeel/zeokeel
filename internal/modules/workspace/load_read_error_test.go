package workspace

// BE-09 / VC-01 (2026-11 audit): load() quarantined DECODE errors but left
// non-NotExist READ errors (EIO/ESTALE/ELOOP on a network or failing disk) in
// place: the service started empty and the next persist overwrote the path
// entry, losing the original bytes. A read error now quarantines exactly like
// a decode error — the load comment's FR-06 promise becomes true (VC-01).
//
// Fault injection: a self-referential symlink makes ReadFile fail with ELOOP
// (a non-NotExist read error) while the directory stays writable, so rename
// still works — the same decision branch a transient EIO produces. (chmod 000
// cannot inject EACCES under the root test uid.)

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryReadErrorQuarantined(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, DefaultFileName)
	if err := os.Symlink(path, path); err != nil {
		t.Fatalf("inject symlink loop: %v", err)
	}
	if _, err := os.ReadFile(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fault injection invalid: ReadFile err = %v, want a non-NotExist read error", err)
	}
	h, err := loadWorkspace(t, dataDir, nil)
	if err != nil {
		t.Fatalf("module activation with an unreadable registry = %v, want quarantine", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("BE-09: unreadable registry entry still at %s (err=%v); the next write would overwrite it", path, err)
	}
	backups, globErr := filepath.Glob(path + ".corrupt-*")
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("quarantine backups = %v/%v, want exactly one", backups, globErr)
	}
	if !h.logger.contains("could not be read") || !h.logger.contains("quarantine") {
		t.Fatalf("read-error recovery was not logged: %v", h.logger.lines)
	}
	workspaces, listErr := h.service.List(context.Background())
	if listErr != nil || len(workspaces) != 0 {
		t.Fatalf("List after read-error quarantine = %+v/%v, want empty", workspaces, listErr)
	}
	// The registry keeps working: a new workspace persists to the canonical path.
	root := makeDir(t, t.TempDir(), "recovered")
	if _, createErr := h.service.Create(context.Background(), "recovered", root); createErr != nil {
		t.Fatalf("Create after recovery: %v", createErr)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("registry not rewritten after recovery: %v", statErr)
	}
}
