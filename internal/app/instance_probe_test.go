package app

import (
	"os"
	"path/filepath"
	"testing"
)

// ProbeInstanceLock is the read-only half of the single-instance guard used by
// `zeokeel import` (residual PD-04 item): the CLI must refuse a real import while
// the desktop app holds the data directory lock, without acquiring or blocking
// on the lock itself.

// TestProbeInstanceLockMissingDirectory: a fresh/absent data directory has no
// running instance and is not an error.
func TestProbeInstanceLockMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	info, err := ProbeInstanceLock(dir)
	if err != nil {
		t.Fatalf("ProbeInstanceLock(absent) error = %v", err)
	}
	if info != nil {
		t.Fatalf("ProbeInstanceLock(absent) = %+v, want nil", info)
	}
}

// TestProbeInstanceLockStaleFile: a lock file left behind by a dead process is
// stale. The kernel released the lock when the process died, so the probe must
// report "not running" instead of trusting the file's presence.
func TestProbeInstanceLockStaleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, instanceLockFileName)
	if err := os.WriteFile(path, []byte(`{"pid":999999,"since":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := ProbeInstanceLock(dir)
	if err != nil {
		t.Fatalf("ProbeInstanceLock(stale) error = %v", err)
	}
	if info != nil {
		t.Fatalf("ProbeInstanceLock(stale) = %+v, want nil", info)
	}
}

// TestProbeInstanceLockDetectsHeldLock is the core contract: while a probe-able
// owner holds the exclusive lock, the probe reports it with the owner pid, and
// stops reporting it once the lock is released.
func TestProbeInstanceLockDetectsHeldLock(t *testing.T) {
	dir := t.TempDir()
	lock, _, err := tryAcquireInstanceLock(dir)
	if err != nil || lock == nil {
		t.Fatalf("acquire instance lock = (%v, %v)", lock, err)
	}
	t.Cleanup(func() { _ = lock.close() })

	info, err := ProbeInstanceLock(dir)
	if err != nil {
		t.Fatalf("ProbeInstanceLock(held) error = %v", err)
	}
	if info == nil {
		t.Fatal("ProbeInstanceLock did not detect the held instance lock")
	}
	if info.PID != os.Getpid() {
		t.Fatalf("probe PID = %d, want this process %d", info.PID, os.Getpid())
	}
	// The probe must not have stolen or kept the lock: a second acquire attempt
	// still fails while the original lock is held.
	second, _, err := tryAcquireInstanceLock(dir)
	if err != nil || second != nil {
		t.Fatalf("probe disturbed the held lock: second acquire = (%v, %v)", second, err)
	}
	if second != nil {
		_ = second.close()
	}

	if err := lock.close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}
	info, err = ProbeInstanceLock(dir)
	if err != nil {
		t.Fatalf("ProbeInstanceLock(after release) error = %v", err)
	}
	if info != nil {
		t.Fatalf("ProbeInstanceLock(after release) = %+v, want nil", info)
	}
}

// TestProbeInstanceLockHeldWithCorruptRecord: a held lock whose owner record is
// corrupt still means "running"; only the pid detail is unavailable.
func TestProbeInstanceLockHeldWithCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	lock, _, err := tryAcquireInstanceLock(dir)
	if err != nil || lock == nil {
		t.Fatalf("acquire instance lock = (%v, %v)", lock, err)
	}
	defer func() { _ = lock.close() }()
	if err := os.WriteFile(filepath.Join(dir, instanceLockFileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := ProbeInstanceLock(dir)
	if err != nil {
		t.Fatalf("ProbeInstanceLock(corrupt held record) error = %v", err)
	}
	if info == nil {
		t.Fatal("held lock with a corrupt record must still report a running instance")
	}
	if info.PID != 0 {
		t.Fatalf("corrupt record probe PID = %d, want 0 (unknown)", info.PID)
	}
}
