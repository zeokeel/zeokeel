//go:build !windows

package host

import (
	"os/exec"
	"runtime"
	"strings"
	"time"

	"path/filepath"
)

// updateHelperDelay is the seam tests replace: the detached helper must start
// its target only after the old process released the single-instance lock.
var updateHelperDelay = func() { time.Sleep(3 * time.Second) }

// waitForUpdateHelperDelay waits for the parent process to finish quitting.
func waitForUpdateHelperDelay() { updateHelperDelay() }

// detachedProcess builds the launch for a helper target. A macOS .app bundle is
// opened through LaunchServices; every other target is an executable/AppImage
// and is executed directly. The launch is detached and its handle released so
// the helper (which exits immediately) cannot leave a zombie.
func platformDetachedProcess(target string, hideWindow bool) (func() error, error) {
	target = strings.TrimSpace(target)
	if runtime.GOOS == "darwin" && strings.EqualFold(filepath.Ext(target), ".app") {
		return func() error {
			cmd := exec.Command("open", target)
			if err := cmd.Start(); err != nil {
				return err
			}
			return cmd.Process.Release()
		}, nil
	}
	return func() error {
		cmd := exec.Command(target)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}, nil
}
