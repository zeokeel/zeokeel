package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// InstanceLockInfo is the read-only view of the owner record a running ZeoKeel
// wrote into <data dir>/instance.lock (see singleinstance.go).
type InstanceLockInfo struct {
	// PID is the owning process id, or 0 when the record could not be read.
	PID int
	// Since is the RFC3339 start time recorded by the owner, if any.
	Since string
}

// ProbeInstanceLock reports whether another process currently holds the
// data-directory single-instance lock, without blocking and without keeping the
// lock. It is used by `zeokeel import` to refuse a real import while the desktop
// app runs (residual PD-04 item: a running app may be writing the same data
// directory/database).
//
// A nil info with a nil error means no running instance: the data directory is
// missing, has no lock file, or the lock file is stale (the OS released the lock
// when its owner died). The probe momentarily takes and immediately releases the
// lock to test it, so it never blocks a starting app; a probe racing an app
// startup can only observe "not running" for that instant, and the CLI still
// opens the store through its normal single-writer path.
func ProbeInstanceLock(dir string) (*InstanceLockInfo, error) {
	path := filepath.Join(dir, instanceLockFileName)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("probe instance lock %q: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // probe only
	locked, err := lockFileExclusive(f)
	if err != nil {
		return nil, fmt.Errorf("probe instance lock %q: %w", path, err)
	}
	if locked {
		// Nobody holds the lock: a stale file left by a dead process. Release
		// immediately; the probe must never keep a lock it does not own.
		if err := unlockFile(f); err != nil {
			return nil, fmt.Errorf("probe instance lock %q: %w", path, err)
		}
		return nil, nil
	}
	info, _ := readInstanceLockInfo(path)
	out := &InstanceLockInfo{}
	if info != nil {
		out.PID = info.PID
		out.Since = info.Since
	}
	return out, nil
}
