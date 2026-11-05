//go:build !windows

// Package winproc keeps child processes from flashing console windows on
// Windows.
//
// The desktop app is built as a GUI-subsystem executable. When such a process
// starts a console program (cmd.exe, pwsh.exe, git, an MCP server, ...) without
// the CREATE_NO_WINDOW creation flag, Windows allocates a new console and shows
// it as a window for every launch. The tools call Hide on every exec.Cmd they
// create so their output stays in the tool result and the UI instead of
// popping cmd dialogs. On other platforms Hide is a no-op.
package winproc

import "os/exec"

// Hide configures cmd so Windows starts its console child without a visible
// console window. It is a no-op on non-Windows platforms, callers therefore do
// not need platform build tags. A nil cmd is ignored.
//
// Hide must be called after the command is constructed and before Start/Run.
// It is safe to combine with a platform-specific SysProcAttr already set on the
// command (for example the POSIX process group in internal/tools).
func Hide(cmd *exec.Cmd) {}
