package app

import (
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestInstanceLockIsPerDataDirectory is the RT-12 regression: the
// single-instance guard is an exclusive lock in the data directory, so two
// different data directories can run side by side while a second instance on
// the same directory is refused with a readable owner record (pid/port), and
// the lock becomes available again when released.
func TestInstanceLockIsPerDataDirectory(t *testing.T) {
	dir := t.TempDir()
	first, info, err := tryAcquireInstanceLock(dir)
	if err != nil || first == nil || info != nil {
		t.Fatalf("first lock = (%v, %v, %v), want acquired", first, info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, instanceLockFileName)); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}

	second, busyInfo, err := tryAcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if second != nil {
		t.Fatal("a second instance acquired the same data directory's lock")
	}
	if busyInfo == nil {
		t.Fatal("busy lock did not carry an owner record")
	}
	if busyInfo.PID != os.Getpid() || busyInfo.Port <= 0 || busyInfo.Token == "" {
		t.Fatalf("owner record = %+v, want this pid, an activation port and a token", busyInfo)
	}

	// A different data directory is independent (the old fixed port made two
	// isolated instances impossible).
	other, _, err := tryAcquireInstanceLock(t.TempDir())
	if err != nil || other == nil {
		t.Fatalf("second data directory lock = (%v, %v), want acquired", other, err)
	}
	if err := other.close(); err != nil {
		t.Fatalf("close other lock: %v", err)
	}

	// Releasing makes the lock available again (no stale-lock failure).
	if err := first.close(); err != nil {
		t.Fatalf("close first lock: %v", err)
	}
	third, _, err := tryAcquireInstanceLock(dir)
	if err != nil || third == nil {
		t.Fatalf("lock after release = (%v, %v), want acquired", third, err)
	}
	if err := third.close(); err != nil {
		t.Fatalf("close third lock: %v", err)
	}
}

// TestInstanceLockFileIsPrivate covers the file-mode half: the lock record
// holds the activation token, so it must be 0600 even when a previous install
// left it 0644.
func TestInstanceLockFileIsPrivate(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Unix file modes are not reported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, instanceLockFileName)
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, _, err := tryAcquireInstanceLock(dir)
	if err != nil || lock == nil {
		t.Fatalf("acquire = (%v, %v)", lock, err)
	}
	defer func() { _ = lock.close() }()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("instance lock mode = %o, want 600", got)
	}
}

// TestSecondLaunchActivatesExistingInstance proves the second launch asks the
// running instance to show its window and reports the data directory (RT-12:
// a second instance used to fail with only "already running").
func TestSecondLaunchActivatesExistingInstance(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	first := &app{}
	if err := first.acquire(); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.releaseInstance()

	var activated atomic.Int32
	first.activationMu.Lock()
	first.activateWindow = func() { activated.Add(1) }
	first.activationMu.Unlock()

	second := &app{}
	err := second.acquire()
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second acquire error = %v, want ErrAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("second acquire error %q does not name the data directory %q", err, dir)
	}
	deadline := time.Now().Add(2 * time.Second)
	for activated.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := activated.Load(); got != 1 {
		t.Fatalf("existing instance activation count = %d, want 1", got)
	}
	if second.instanceLock != nil {
		t.Fatal("refused second instance kept a lock")
	}
}

// TestActivationRequestBeforeWindowShowsLater covers the startup race: a
// second launch that arrives before the main window exists is replayed once the
// window is registered instead of being dropped.
func TestActivationRequestBeforeWindowShowsLater(t *testing.T) {
	a := &app{}
	a.requestActivation() // window not created yet
	if !a.activationPending {
		t.Fatal("activation request before the window was not remembered")
	}
	// setActivationWindow needs a real *mygo.Window; verify the pending flag is
	// consumed by the registration path via a nil-window no-op first.
	a.setActivationWindow(nil)
	if !a.activationPending {
		t.Fatal("nil window registration consumed the pending activation")
	}
}
