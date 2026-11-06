//go:build windows

package app

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockFileExclusive(f *os.File) (bool, error) {
	overlapped := windows.Overlapped{}
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped,
	)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return false, nil
	default:
		return false, err
	}
}

// unlockFile releases the LockFileEx range taken by lockFileExclusive.
func unlockFile(f *os.File) error {
	overlapped := windows.Overlapped{}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}
