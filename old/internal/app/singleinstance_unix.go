//go:build !windows

package app

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFileExclusive takes an exclusive, non-blocking flock on f. It reports
// (false, nil) when another process holds the lock. The kernel releases the
// lock when the process exits, so a crash never leaves a permanently held
// single-instance lock.
func lockFileExclusive(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
		return false, nil
	default:
		return false, err
	}
}

// unlockFile releases the flock taken by lockFileExclusive.
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
