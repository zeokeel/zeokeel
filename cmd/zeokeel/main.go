// Command zeokeel is ZeoKeel: a pure-Go desktop AI agent built with mygo's
// native UI. Its visual system is internal/brand + internal/design.
//
// Startup order matters for diagnostics: logging is configured before
// anything else, the runtime crash sink is installed next, and the panic
// recovery is deferred so a startup failure (or a panic in the UI thread)
// leaves a crash report in <data dir>/logs even when the build has no console
// (the Windows GUI subsystem).
package main

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"time"

	"github.com/zeokeel/zeokeel/internal/app"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is main's testable body: it returns the process exit code instead of
// calling os.Exit so deferred cleanup (log close, crash write) always runs.
func run(args []string) (code int) {
	// --version/--help are pure IO and must not create a data directory or a
	// log file. Everything else gets the diagnostics set up first.
	if isInfoCommand(args) {
		return runCLI(args, os.Stdout, os.Stderr)
	}

	dataDir := app.DataDir()

	logger, logErr := setupLogging(dataDir)
	if logErr != nil {
		// A broken data directory must not prevent the app from starting, but
		// the user (and support) should see why the file log is missing.
		log.Printf("zeokeel: file logging unavailable: %v", logErr)
	}
	if logger != nil {
		// Registered first so it runs last: the crash/recover handler needs a
		// live log when it writes, and the log must be flushed before exit.
		defer func() {
			if err := logger.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "zeokeel: close log: %v\n", err)
			}
		}()
		if err := installCrashOutput(logger); err != nil {
			log.Printf("zeokeel: install crash output: %v", err)
		}
		log.Printf("zeokeel: %s", versionString())
		log.Printf("zeokeel: data dir %s; log %s", dataDir, logger.Path())
	}

	// Recover panics on this goroutine (startup and the main loop). Panics on
	// other goroutines are covered by installCrashOutput above; this handler
	// additionally writes the version/platform header into a crash file next
	// to the log so a bug report is one file.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		path, err := writeCrashReport(dataDir, r, debug.Stack(), time.Now())
		if err != nil {
			log.Printf("zeokeel: panic: %v (crash report failed: %v)", r, err)
			fmt.Fprintf(os.Stderr, "zeokeel: panic: %v (crash report failed: %v)\n", r, err)
		} else {
			log.Printf("zeokeel: panic: %v (crash report: %s)", r, path)
			fmt.Fprintf(os.Stderr, "zeokeel: panic: %v (crash report: %s)\n", r, path)
		}
		code = 2
	}()

	return runCLI(args, os.Stdout, os.Stderr)
}
