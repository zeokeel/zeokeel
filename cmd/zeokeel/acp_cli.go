package main

// `zeokeel acp` — Agent Client Protocol (ACP v1) server over stdio.
//
// stdout is reserved for JSON-RPC frames: this command prints NO banners on
// stdout, only protocol frames written by acp.Server. All diagnostics go
// through the standard logger (stderr + the app log file), never stdout.
//
// The command takes no arguments; SIGINT/SIGTERM cancel the server context and
// shut it down cleanly. EOF on stdin also exits 0. It never takes the desktop
// single-instance lock, so an editor can run the agent next to the GUI.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/zeokeel/zeokeel/internal/acp"
	"github.com/zeokeel/zeokeel/internal/app"
)

// acpUsage is `zeokeel acp --help`.
const acpUsage = `Usage: zeokeel acp

Runs the Agent Client Protocol (ACP v1) server on stdio: newline-framed
JSON-RPC 2.0 on stdin/stdout. An editor or other ACP client launches this
process and drives headless ZeoKeel sessions.

stdout carries protocol frames only; logs go to stderr and the app log file.
Signals: SIGINT/SIGTERM shut down cleanly; EOF on stdin exits 0.

Environment:
  ZEOKEEL_DATA_DIR   data directory for ACP sessions (default: ~/.zeokeel).
                     Prefer a dedicated directory for ACP clients so a running
                     desktop app does not share the SQLite store.
  DEEPSEEK_API_KEY   model API key when none is configured in Settings

Supported requests: initialize, session/new, session/prompt, session/cancel.
`

// runACP implements `zeokeel acp`. It never writes to stdout itself.
func runACP(args []string, stdout io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Fprint(stdout, acpUsage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "zeokeel acp: unexpected argument %q\n\n%s", args[0], acpUsage)
			return 2
		}
	}

	// The server writes only protocol frames to stdout; diagnostics stay on the
	// std logger (stderr + app log file configured by main).
	logf := func(format string, args ...any) { log.Printf(format, args...) }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	agent, err := app.NewACPAdapter(app.ACPAgentConfig{Logger: logf})
	if err != nil {
		log.Printf("zeokeel acp: %v", err)
		fmt.Fprintf(os.Stderr, "zeokeel acp: %v\n", err)
		return 1
	}
	defer func() {
		if err := agent.Close(context.Background()); err != nil {
			log.Printf("zeokeel acp: close: %v", err)
		}
	}()

	server, err := acp.NewServer(acp.Config{
		Agent:      agent,
		ServerName: "zeokeel",
		Version:    app.BuildVersion,
		Logf:       logf,
	})
	if err != nil {
		log.Printf("zeokeel acp: %v", err)
		fmt.Fprintf(os.Stderr, "zeokeel acp: %v\n", err)
		return 1
	}

	if err := server.Serve(ctx, os.Stdin, stdout); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
			return 0
		}
		log.Printf("zeokeel acp: serve: %v", err)
		fmt.Fprintf(os.Stderr, "zeokeel acp: serve: %v\n", err)
		return 1
	}
	return 0
}
