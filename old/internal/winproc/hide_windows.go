//go:build windows

package winproc

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows process creation flag that keeps a console
// child from allocating a console (and therefore from showing a window). It is
// defined here because syscall does not export it.
const createNoWindow = 0x08000000

// Hide sets the Windows creation flags that keep cmd's console hidden. The
// combination is intentional:
//
//   - CREATE_NO_WINDOW: the child gets no console at all, so nothing can flash
//     on screen even when the parent GUI process has no console to inherit;
//   - HideWindow: STARTF_USESHOWWINDOW + SW_HIDE covers launchers that ignore
//     CREATE_NO_WINDOW (and is harmless otherwise).
//
// Existing flags are preserved (the app never sets console-related ones today,
// but clearing them would be surprising).
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
