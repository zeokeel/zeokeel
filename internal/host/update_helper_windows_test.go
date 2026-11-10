//go:build windows

package host

// Windows-tagged tests for the detached helper's child process. They are
// typechecked by `GOOS=windows go vet` on Linux and run on Windows CI/dev hosts;
// they never start a process.

import "testing"

// TestNewDetachedCommandWindowVisibility documents the HideWindow decision: the
// interactive NSIS setup must stay visible (installer helper passes false), while
// the relaunch helper's target can start hidden and then show its own window.
func TestNewDetachedCommandWindowVisibility(t *testing.T) {
	installer := newDetachedCommand(`C:\Users\Alice Smith\AppData\Roaming\ZeoKeel\updates\setup.exe`, false)
	if installer.SysProcAttr == nil || installer.SysProcAttr.HideWindow {
		t.Fatal("installer child must not set HideWindow: the NSIS setup is interactive")
	}
	relaunch := newDetachedCommand(`C:\Program Files\ZeoKeel\zeokeel.exe`, true)
	if relaunch.SysProcAttr == nil || !relaunch.SysProcAttr.HideWindow {
		t.Fatal("relaunch child should start hidden and let the app show its own window")
	}
	if relaunch.Path == "" {
		t.Fatal("relaunch command has no path")
	}
}
