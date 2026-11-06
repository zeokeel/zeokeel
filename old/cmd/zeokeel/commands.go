package main

// Subcommand dispatch table.
//
// runCLI stays a thin dispatcher: version/help/unknown handling plus desktop as
// the default command. Every subcommand keeps its own file, flags and help
// text, so adding one is a table row here plus one file — no edits to the
// dispatch logic (AR-20 platform split).

import (
	"fmt"
	"io"
)

// subcommand is one CLI subcommand. stdout carries the command's result (or
// protocol frames for `acp`); diagnostics never go to stdout.
type subcommand struct {
	run func(args []string, stdout, stderr io.Writer) int
}

// subcommands maps the first CLI argument to its handler.
var subcommands = map[string]subcommand{
	"run": {run: func(args []string, stdout, stderr io.Writer) int {
		if headlessRun == nil {
			fmt.Fprintln(stderr, "zeokeel: the headless 'run' command is not wired yet (internal/app.RunHeadless is pending; see docs/BUGS.md FP-18)")
			return 2
		}
		return headlessRun(args, stdout, stderr)
	}},
	"import": {run: runImportCLI},
	"update": {run: runUpdateCLI},
	"acp": {run: func(args []string, stdout, _ io.Writer) int {
		// The ACP transport writes JSON-RPC frames to stdout only; runACP owns
		// all diagnostics through the std logger.
		return runACP(args, stdout)
	}},
}
