package agent

// BE-09 / VC-01 (2026-11 audit): the child-session store's load() quarantined
// DECODE errors but left non-NotExist READ errors in place — empty start, then
// the next upsert overwrote the path entry and lost the durable child records.
// A read error now quarantines exactly like a decode error. Fault injection: a
// self-referential symlink (ELOOP), the root-uid-safe stand-in for EIO/ESTALE.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestChildSessionReadErrorQuarantined(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, childSessionFileName)
	if err := os.Symlink(path, path); err != nil {
		t.Fatalf("inject symlink loop: %v", err)
	}
	if _, err := os.ReadFile(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fault injection invalid: ReadFile err = %v, want a non-NotExist read error", err)
	}
	logger := &fakeLogger{}
	store := newChildSessionStore(dataDir, logger)

	if records := store.records(); len(records) != 0 {
		t.Fatalf("records after read-error quarantine = %+v, want empty", records)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("BE-09: unreadable child-session entry still at %s (err=%v); the next write would overwrite it", path, err)
	}
	backups, globErr := filepath.Glob(path + ".corrupt-*")
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("quarantine backups = %v/%v, want exactly one", backups, globErr)
	}
	if !logger.contains("could not be read") || !logger.contains("quarantine") {
		t.Fatalf("read-error recovery was not logged: %v", logger.lines)
	}
	// The store keeps working: an upsert persists to the canonical path.
	store.upsert(childSessionRecord{ID: "child-recovered", Kind: "teammate", Name: "x"})
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("child-session file not rewritten after recovery: %v", statErr)
	}
}
