package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// requirePOSIXModes skips the assertions on Windows, where os.Chmod only
// toggles the read-only attribute and os.Stat does not report Unix mode bits.
func requirePOSIXModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes are not reported on Windows")
	}
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

// TestOpenSecuresLooseDataDirectory is the RT-11 regression: the index stores
// plaintext provider API keys and the full history, so Open must tighten a
// pre-existing 0755 data directory to 0700 and the database (plus WAL/-shm
// sidecars) to 0600, instead of relying on the directory mode alone.
func TestOpenSecuresLooseDataDirectory(t *testing.T) {
	requirePOSIXModes(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close() //nolint:errcheck // test cleanup
	// Force WAL pages so -wal/-shm exist while the connection is open.
	if _, err := s.CreateWorkspace("secure", dir); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	requireMode(t, dir, 0o700)
	requireMode(t, filepath.Join(dir, dbFileName), 0o600)
	requireMode(t, filepath.Join(dir, dbFileName+"-wal"), 0o600)
	requireMode(t, filepath.Join(dir, dbFileName+"-shm"), 0o600)
}

// TestOpenRestoresModesOnExistingDatabase covers an install whose database was
// created by a build that left the files 0644 (or a backup/sync tool): opening
// it again repairs the modes.
func TestOpenRestoresModesOnExistingDatabase(t *testing.T) {
	requirePOSIXModes(t)
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.CreateWorkspace("w", dir); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Simulate a loosened install.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{dbFileName, dbFileName + "-wal", dbFileName + "-shm"} {
		target := filepath.Join(dir, name)
		if err := os.Chmod(target, 0o644); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("chmod %s: %v", name, err)
		}
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close() //nolint:errcheck // test cleanup
	requireMode(t, dir, 0o700)
	requireMode(t, filepath.Join(dir, dbFileName), 0o600)
}
