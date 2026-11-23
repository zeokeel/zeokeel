//go:build windows

package gateway

import (
	"os"

	"golang.org/x/sys/windows"
)

// fileLock is the gateway's single-instance lock on Windows.
//
// There is no flock in the Windows API; the equivalent is opening the lock file
// with an empty share mode, which makes every later open fail with a sharing
// violation while this process holds the handle. The kernel closes the handle
// if the process dies, so a crashed gateway never leaves the data directory
// locked.
type fileLock struct {
	handle windows.Handle
	path   string
}

// acquireLock takes the lock. It returns errAlreadyRunning (wrapped in
// *alreadyRunningError when gateway.json is readable) if another process holds
// it.
func acquireLock(dataDir string) (*fileLock, error) {
	path := infoPath(dataDir, LockFileName)
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // no sharing: a second gateway cannot open it
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if info, readErr := readInfo(dataDir); readErr == nil {
			return nil, &alreadyRunningError{Info: info}
		}
		return nil, errAlreadyRunning
	}
	return &fileLock{handle: handle, path: path}, nil
}

// Close releases the lock and removes the lock file.
func (l *fileLock) Close() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(l.handle)
	l.handle = 0
	_ = os.Remove(l.path)
	return err
}
