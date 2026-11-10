//go:build windows

package host

import (
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// updateHelperDelay is the seam tests replace: the detached helper must start
// its target only after the old process released the single-instance lock and
// its executable file handle.
var updateHelperDelay = func() { time.Sleep(3 * time.Second) }

// waitForUpdateHelperDelay waits for the parent process to finish quitting.
func waitForUpdateHelperDelay() { updateHelperDelay() }

// detachedProcess starts one GUI/hidden target without a shell: plain argv lets
// os/exec quote paths with spaces, '%' and '&' exactly; no console window is
// created for the helper's child.
func platformDetachedProcess(target string, hideWindow bool) (func() error, error) {
	cmd := newDetachedCommand(target, hideWindow)
	return func() error {
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}, nil
}

// newDetachedCommand builds the helper's child process. Windows-tagged tests
// inspect it to pin the window-visibility contract without starting a process.
func newDetachedCommand(target string, hideWindow bool) *exec.Cmd {
	target = strings.TrimSpace(target)
	cmd := exec.Command(target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: hideWindow}
	return cmd
}
