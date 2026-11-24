//go:build windows

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// lockRegistryFile takes the registry's cross-process write lock on Windows.
//
// There is no flock here; the equivalent is opening the lock file with an empty
// share mode, which makes a second open fail with a sharing violation while the
// handle is held. Retried briefly so two processes serialize instead of one
// failing outright, and the kernel closes the handle if the process dies.
func lockRegistryFile(path string) (func(), error) {
	// The registry's parent directory may not exist yet (the SDK creates it on
	// first persist, after this lock must already exist).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("workspace: registry directory: %w", err)
	}
	name := path + ".lock"
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("workspace: registry lock path: %w", err)
	}
	deadline := time.Now().Add(registryLockTimeout)
	for {
		handle, err := windows.CreateFile(
			pointer,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, // no sharing: a second writer cannot open it
			nil,
			windows.OPEN_ALWAYS,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err == nil {
			return func() { _ = windows.CloseHandle(handle) }, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %v", ErrRegistryBusy, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
