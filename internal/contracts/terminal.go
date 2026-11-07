package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// TerminalOutput is the result of one terminal command run.
type TerminalOutput struct {
	// Output is the combined stdout/stderr text (bounded by the service).
	Output string
	// ExitCode is the process exit code (0 for success).
	ExitCode int
}

// TerminalService is the product's simple command runner (the old shell's
// terminal panel): one shell process per Run in the current workspace root,
// with no PTY and no persistent shell.
type TerminalService interface {
	// WorkingDir returns the directory commands run in: the current
	// workspace root, or the process working directory when none is open.
	WorkingDir(ctx context.Context) string
	// Shell is the shell executable commands run with (e.g. "bash").
	Shell() string
	// Shells lists the shells available on this machine, in preference order.
	Shells() []string
	// SetShell selects the shell used by later runs.
	SetShell(name string) error
	// Run executes command with the selected shell in WorkingDir and returns
	// the combined output. ctx cancellation/timeout kills the process.
	Run(ctx context.Context, command string) (TerminalOutput, error)
}

// KeyTerminal is provided by the terminal backend module and consumed by the
// right inspector's 终端 tab.
var KeyTerminal = cordis.NewServiceKey[TerminalService]("zeokeel/terminal")
