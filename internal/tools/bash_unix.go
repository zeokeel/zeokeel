//go:build unix

package tools

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
)

// shellCommand builds an sh -c command for Unix-like systems.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// configureProcess starts the command in its own process group so the whole
// tree can be signalled on timeout.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the process group led by pid. A group that
// has already exited is not an error.
func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
