package memory

// BE-09 / VC-01 (2026-11 audit): load() quarantined DECODE errors but left
// non-NotExist READ errors in place — empty start, then the next persist
// overwrote the path entry and lost the original bytes. A read error now
// quarantines exactly like a decode error (the load comment's FR-06 promise
// becomes true). Fault injection: a self-referential symlink (ELOOP), the
// root-uid-safe stand-in for a transient EIO/ESTALE.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestMemoryReadErrorQuarantined(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, DefaultFileName)
	if err := os.Symlink(path, path); err != nil {
		t.Fatalf("inject symlink loop: %v", err)
	}
	if _, err := os.ReadFile(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fault injection invalid: ReadFile err = %v, want a non-NotExist read error", err)
	}
	h := mustLoadMemory(t, dataDir, nil, nil)

	entries, err := h.store.List(context.Background())
	if err != nil || len(entries) != 0 {
		t.Fatalf("store after read-error quarantine = %+v/%v, want empty", entries, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("BE-09: unreadable memory entry still at %s (err=%v); the next write would overwrite it", path, err)
	}
	backups, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("quarantine backups = %v/%v, want exactly one", backups, err)
	}
	if !h.logger.contains("could not be read") || !h.logger.contains("quarantine") {
		t.Fatalf("read-error recovery was not logged: %v", h.logger.lines)
	}
	reporter, ok := h.store.(contracts.MemoryRecoveryReporter)
	if !ok || strings.TrimSpace(reporter.RecoveryNotice()) == "" {
		t.Fatalf("store does not expose a recovery notice: %T", h.store)
	}
	// The store keeps working after the quarantine.
	if _, err := h.store.Add(context.Background(), contracts.Memory{Text: "after recovery", Scope: contracts.MemoryScopeGlobal}); err != nil {
		t.Fatalf("Add after recovery: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("memory file not rewritten after recovery: %v", statErr)
	}
}
