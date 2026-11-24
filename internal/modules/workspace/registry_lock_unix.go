//go:build !windows

package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockRegistryFile takes the registry's cross-process write lock: an exclusive
// flock on "<registry>.lock", retried briefly so two processes writing at once
// serialize instead of corrupting or overwriting each other's registry.
//
// The kernel releases the lock if the process dies, so a crash never leaves the
// registry locked. Returns an unlock function.
func lockRegistryFile(path string) (func(), error) {
	// The registry's parent directory may not exist yet: the SDK creates it when
	// it first persists, and the lock has to be there before that write.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("workspace: registry directory: %w", err)
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("workspace: open registry lock: %w", err)
	}
	deadline := time.Now().Add(registryLockTimeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("workspace: lock registry: %w", err)
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, ErrRegistryBusy
		}
		time.Sleep(5 * time.Millisecond)
	}
}
