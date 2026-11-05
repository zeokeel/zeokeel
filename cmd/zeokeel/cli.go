package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/zeokeel/zeokeel/internal/app"
)

// headlessRun is the seam for `zeokeel run`, wired to internal/app.RunHeadless
// (FP-18). It is a variable so cmd-level tests can stub the backend; a nil value
// (only possible in isolation) fails closed with a clear error instead of
// silently starting the desktop app.
var headlessRun func(args []string, stdout, stderr io.Writer) int

func init() { headlessRun = app.RunHeadless }

// isInfoCommand reports whether args only ask for version/help. Those are
// answered before logging is set up so they never create or touch the data
// directory (useful in scripts and CI).
func isInfoCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--version", "-version", "-v", "version", "--help", "-h", "-help", "help":
		return true
	}
	return false
}

// runCLI dispatches the command line and returns the process exit code.
// stdout/stderr are injected so the dispatch is testable without a process.
func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-version", "-v", "version":
			fmt.Fprintln(stdout, versionString())
			return 0
		case "--help", "-h", "-help", "help":
			fmt.Fprint(stdout, usageText)
			return 0
		}
		if cmd, ok := subcommands[args[0]]; ok {
			return cmd.run(args[1:], stdout, stderr)
		}
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "zeokeel: unknown flag %q\n\n", args[0])
		} else {
			fmt.Fprintf(stderr, "zeokeel: unknown command %q\n\n", args[0])
		}
		fmt.Fprint(stderr, usageText)
		return 2
	}

	// Default: the desktop app. Errors are logged and mirrored to stderr so a
	// launcher (or the service) sees them even when the log file is unwritable.
	if err := app.Run(); err != nil {
		if errors.Is(err, app.ErrAlreadyRunning) {
			log.Printf("zeokeel: already running (data dir: %s); not starting a second instance", app.DataDir())
			fmt.Fprintln(stderr, "ZeoKeel is already running")
			return 0
		}
		log.Printf("zeokeel: fatal: %v", err)
		fmt.Fprintf(stderr, "zeokeel: fatal: %v\n", err)
		return 1
	}
	return 0
}
