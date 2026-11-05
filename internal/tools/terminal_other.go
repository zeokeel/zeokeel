//go:build !unix

package tools

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

// startTerminalProcess starts the shell with plain stdio pipes on platforms
// without PTY support. The PTY master is always nil and the returned bool is
// false, so terminal_open/terminal_list surface pty=false.
func startTerminalProcess(newCmd func() *exec.Cmd, out *outputRing, touch func()) (*exec.Cmd, io.WriteCloser, *os.File, bool, error) {
	cmd, stdin, err := startTerminalPipes(newCmd, out, touch)
	if err != nil {
		return nil, nil, nil, false, err
	}
	return cmd, stdin, nil, false, nil
}

// terminalForegroundPGID has no meaning for pipe sessions.
func terminalForegroundPGID(*terminalSession) int { return 0 }

// init installs the non-Unix platform defaults. Terminal sessions on these
// systems use cmd.exe (through COMSPEC when present), and signals are delivered
// to the shell process directly because POSIX process-group addressing does not
// exist here. The tool surface and error reporting stay identical.
func init() {
	terminalShellDefaults = func() (string, []string) {
		if comspec := os.Getenv("COMSPEC"); comspec != "" {
			return comspec, nil
		}
		return "cmd.exe", nil
	}
	terminalDeliverSignal = func(pid int, sig syscall.Signal) error {
		if pid <= 0 {
			return os.ErrInvalid
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		return p.Signal(sig)
	}
}
