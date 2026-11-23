//go:build !windows

package gateway

import (
	"os"
	"syscall"
)

// fileLock is the gateway's single-instance lock: an exclusive, non-blocking
// flock on <data-dir>/gateway.lock (docs/TRANSPORT.md §2.4).
//
// The desktop product uses mygo's RequestSingleInstanceLock, which needs the
// GUI event loop; the gateway has no window, so it uses the OS file lock
// instead. The lock is released by the kernel if the process dies, which is
// what makes the stale gateway.json handling safe.
type fileLock struct {
	file *os.File
}

// acquireLock takes the lock. It returns errAlreadyRunning (wrapped in
// *alreadyRunningError when gateway.json is readable) if another process holds
// it.
func acquireLock(dataDir string) (*fileLock, error) {
	path := infoPath(dataDir, LockFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if info, readErr := readInfo(dataDir); readErr == nil {
			return nil, &alreadyRunningError{Info: info}
		}
		return nil, errAlreadyRunning
	}
	return &fileLock{file: file}, nil
}

// Close releases the lock.
func (l *fileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
