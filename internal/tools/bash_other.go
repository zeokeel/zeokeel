//go:build !unix

package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// shellCommand builds the platform shell invocation. Windows has no sh by
// default, so cmd.exe is used there.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// configureProcess has no portable process-group equivalent here; the direct
// child is killed on timeout.
func configureProcess(cmd *exec.Cmd) {}

// killProcessGroup kills the command process on platforms without POSIX
// process groups.
func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	err = p.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
