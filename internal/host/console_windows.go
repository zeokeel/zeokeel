//go:build windows

package host

// Windows console inheritance for child processes.
//
// The desktop product links as a GUI-subsystem executable: it has no console.
// When a console program (pwsh, bash, a background job, the terminal shell) is
// started from a process without a console, Windows allocates a NEW console
// window for it — the flashing cmd/pwsh windows users see during tool calls.
// CREATE_NO_WINDOW would suppress that, but the shell/job/terminal commands are
// created inside the rig module and we do not fork it. Allocating one hidden
// console for the GUI process makes every child inherit that console instead of
// opening its own window; the window is hidden immediately and never removed.
//
// A process that already has a console (for example the headless build launched
// from a terminal) is left untouched: the visible console is then the user's
// own choice and child output stays there.

import "golang.org/x/sys/windows"

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	user32                     = windows.NewLazySystemDLL("user32.dll")
	procAllocConsole           = kernel32.NewProc("AllocConsole")
	procGetConsoleWindow       = kernel32.NewProc("GetConsoleWindow")
	procShowWindow             = user32.NewProc("ShowWindow")
	showWindowHide       int32 = 0 // SW_HIDE
)

// hideWindowsConsoleForChildren gives the GUI process one hidden console (only
// when it has none) so console children inherit it instead of popping cmd
// windows. Failures are non-fatal: the shell tools keep working, they may just
// flash a window on that machine.
func hideWindowsConsoleForChildren() {
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
		return
	}
	if ok, _, _ := procAllocConsole.Call(); ok == 0 {
		return
	}
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, uintptr(showWindowHide))
}
