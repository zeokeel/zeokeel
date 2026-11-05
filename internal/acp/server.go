package acp

import (
	"context"
	"errors"
	"io"
	"log"
)

// ErrNotImplemented marks the TDD RED contract of this round: the wire types
// and the Agent seam are designed (see doc.go), the JSON-RPC loop is not
// implemented yet. The tests in this package assert the real wire behavior and
// fail while this error is returned.
var ErrNotImplemented = errors.New("acp: JSON-RPC server loop is not implemented yet (TDD RED); see doc.go for the next-round plan")

// Agent is the headless execution seam the ACP server drives. It is
// intentionally small and transport-agnostic so the JSON-RPC loop stays
// testable with a fake, and so internal/app can provide the real
// implementation without importing this package's wire details.
//
// Contract for the next round (implemented by internal/app on top of
// session.Controller; see doc.go "Integration requirement"):
//   - NewSession creates one headless agent session bound to req.Cwd (absolute)
//     and returns its stable id; it must not write to stdout.
//   - Prompt runs exactly one prompt for the session and streams semantic
//     updates through updates (serially, before returning); it blocks until the
//     turn reaches a terminal state and maps that state to a stop reason.
//     A second concurrent Prompt for the same session must be rejected.
//   - Cancel stops the in-flight turn of sessionID; unknown ids are a no-op.
//   - Close releases every session and the runtime (idempotent).
type Agent interface {
	NewSession(ctx context.Context, req NewSessionRequest) (NewSessionResponse, error)
	Prompt(ctx context.Context, req PromptRequest, updates UpdateSink) (PromptResponse, error)
	Cancel(ctx context.Context, sessionID string) error
	Close(ctx context.Context) error
}

// UpdateSink delivers one session/update notification. The returned error
// aborts the prompt (the client connection is gone / write failed).
type UpdateSink func(SessionUpdateNotification) error

// Config configures a Server.
type Config struct {
	// Agent is the headless runtime the protocol drives. Required.
	Agent Agent
	// ServerName/Version are advertised in initialize's agentInfo.
	// Defaults: "zeokeel" / "0.1.0".
	ServerName string
	Version    string
	// Logf receives diagnostics (always stderr or a file, never stdout: stdout
	// is reserved for protocol frames). Nil means log.Printf.
	Logf func(format string, args ...any)
}

// Server is the ACP v1 JSON-RPC server over one newline-framed stream pair.
// One connection may serve many sessions concurrently.
type Server struct {
	agent      Agent
	serverName string
	version    string
	logf       func(format string, args ...any)
}

// NewServer validates cfg and returns a server. The wire loop itself is
// implemented in Serve next round.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Agent == nil {
		return nil, errors.New("acp: NewServer: Config.Agent is required")
	}
	name := cfg.ServerName
	if name == "" {
		name = "zeokeel"
	}
	version := cfg.Version
	if version == "" {
		version = "0.1.0"
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Server{agent: cfg.Agent, serverName: name, version: version, logf: logf}, nil
}

// Serve runs the connection until in reaches EOF or ctx is cancelled:
//
//   - each newline-terminated line is one JSON-RPC 2.0 frame;
//   - requests are dispatched concurrently and responses correlate by id, so
//     out-of-order completion is allowed;
//   - notifications (session/cancel, session/update) carry no id and are
//     fire-and-forget;
//   - stdout (out) carries protocol frames only, diagnostics go to logf.
//
// See doc.go for the full next-round implementation plan. This round returns
// ErrNotImplemented so the TDD tests stay RED.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	// TODO(next round): implement the newline-framed JSON-RPC loop per doc.go.
	// Planned shape (kept here so the implementation cannot drift from the
	// tests):
	//   scanner over in -> decode request/notification/bad JSON
	//   initialized gate for session/prompt
	//   dispatch: initialize | session/new | session/prompt | session/cancel
	//   -32601 unknown method, -32602 invalid params, -32603 handler error,
	//   -32700 malformed line (null id), responses via out with a write mutex
	//   per-session one in-flight prompt; prompt settles after ordered updates
	//   ctx cancellation closes prompts with stopReason "cancelled"
	_ = ctx
	_ = in
	_ = out
	return ErrNotImplemented
}
