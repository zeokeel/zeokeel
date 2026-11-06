//go:build unix

package tools

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// startTerminalProcess starts the shell for a persistent terminal. On Unix it
// prefers a real pseudoterminal and only falls back to stdio pipes when the
// PTY cannot be created or the command cannot be started under it. The
// returned bool reports which path was taken so the tool output can surface
// pty=true/false; the PTY master is returned only on the PTY path.
func startTerminalProcess(newCmd func() *exec.Cmd, out *outputRing, touch func()) (*exec.Cmd, io.WriteCloser, *os.File, bool, error) {
	master, cmd, err := startTerminalPTY(newCmd)
	if err == nil {
		return cmd, nil, master, true, nil
	}
	cmd, stdin, fallbackErr := startTerminalPipes(newCmd, out, touch)
	if fallbackErr != nil {
		return nil, nil, nil, false, fmt.Errorf("pty unavailable (%v); pipe fallback failed: %w", err, fallbackErr)
	}
	return cmd, stdin, nil, false, nil
}

// terminalOpenPTY is a test seam around pty.Open so the pipe fallback can be
// exercised without an environment that actually lacks PTY support.
var terminalOpenPTY = pty.Open

// startTerminalPTY allocates a PTY pair and starts cmd as a session leader
// with the slave as its controlling terminal. The slave is closed in the
// parent after Start; the returned master is the session's read/write handle.
func startTerminalPTY(newCmd func() *exec.Cmd) (*os.File, *exec.Cmd, error) {
	master, tty, err := terminalOpenPTY()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tty.Close() }()
	// A fresh PTY may report a 0x0 window, which confuses full-screen
	// programs. There is no resize tool yet, so start with a conventional
	// 80x24 default; the size stays fixed for the session's lifetime.
	_ = pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80})

	cmd := newCmd()
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	// Setsid gives the shell its own session and process group (pgid == pid);
	// Setctty makes the slave its controlling terminal. Ctty defaults to 0,
	// which is the child-side descriptor for tty (exec assigns Stdin first).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	return master, cmd, nil
}

// terminalForegroundPGID reports the foreground process group of the session's
// controlling terminal. Under job control that group may differ from the
// shell's own process group, so signals and close must address both. It
// returns 0 for pipe sessions or when the ioctl is unavailable.
func terminalForegroundPGID(s *terminalSession) int {
	master := s.ptyHandle()
	if master == nil {
		return 0
	}
	pgrp, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPGRP)
	if err != nil || pgrp <= 0 {
		return 0
	}
	return pgrp
}
