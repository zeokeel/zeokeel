// Package acp implements the minimum Agent Client Protocol (ACP v1) server
// that lets trusted out-of-process clients drive a ZeoKeel headless agent over
// a newline-framed JSON-RPC 2.0 stdio transport.
//
// # Scope (this round: TDD RED)
//
// Only the minimum automation surface is designed here:
//
//	initialize     -> protocol version + agent capabilities
//	session/new    -> create a headless session bound to an absolute cwd
//	session/prompt -> run one prompt, stream session/update notifications,
//	                  settle with a stopReason
//	session/cancel -> cancel the in-flight prompt of one session
//
// Plus the JSON-RPC 2.0 error handling the transport needs (parse error
// -32700, invalid request -32600, method not found -32601, invalid params
// -32602, internal error -32603) and concurrent request correlation on one
// stdio connection.
//
// STATUS: the wire types and the Agent seam below are the interface design;
// Server.Serve still returns ErrNotImplemented. The tests in this package are
// deliberately RED and are the acceptance criteria for the next round:
//
//	go test ./internal/acp    # RED: TestServerInitializeReturnsCapabilities,
//	                          #      TestServerNewSessionPromptAndCancel,
//	                          #      TestServerRejectsUnknownMethodAndBadJSON,
//	                          #      TestStdioLoopStreamsResponses
//
// # Next-round implementation plan
//
//  1. Serve: newline-delimited JSON scanner over in; decode each line into
//     request/notification; a malformed line answers -32700 with a null id and
//     keeps the connection alive.
//  2. Dispatch table initialized/session-new/session-prompt/session-cancel.
//     Unknown methods answer -32601; invalid params answer -32602; handler
//     errors answer -32603. Requests are handled concurrently (one goroutine
//     per request, responses may complete out of order because IDs correlate
//     them); notifications are fire-and-forget.
//  3. Canonical ACP lifecycle: session/prompt is rejected until initialize
//     succeeded; session/new validates an absolute cwd; session/cancel is a
//     notification (also accepted as a request for compatibility) and is a
//     no-op for unknown session ids; one in-flight prompt per session.
//  4. Prompt updates are delivered via Agent.Prompt's UpdateSink as
//     session/update notifications, serially per session, before the
//     session/prompt response (ordered delivery).
//  5. stdout carries protocol frames only; diagnostics go to the configured
//     logger (stderr), never stdout.
//
// # Integration requirement: app.RunHeadless
//
// The current internal/app.RunHeadless(args []string, stdout, stderr io.Writer)
// int is CLI-shaped: it parses argv, builds the service stack once, prints the
// final text to stdout and returns a process exit code. An ACP server needs a
// long-lived, reusable, event-streaming runtime instead. The next round needs
// one seam from internal/app (owned by the backend owner, not this package):
//
//	type HeadlessRuntime interface {
//	    NewSession(ctx context.Context, cwd string) (sessionID string, err error)
//	    Submit(ctx context.Context, sessionID, text string) error
//	    Cancel(ctx context.Context, sessionID string) error
//	    Subscribe(sessionID string) (<-chan state.Event, func(), error)
//	    Close(ctx context.Context) error
//	}
//	func NewHeadlessRuntime(cfg HeadlessRuntimeConfig) (HeadlessRuntime, error)
//
// Rules the ACP server relies on:
//   - NewHeadlessRuntime must not write to stdout (ACP owns stdout; logs to
//     stderr / the app log file only) and must not take the desktop
//     single-instance lock.
//   - One runtime serves many sessions concurrently, each bound to the cwd
//     supplied by session/new; the runtime may reuse internal/session.Controller
//     (CreateSession/Submit/Stop/Wait/Snapshot/OnUpdate/SetOnTurnFinished).
//   - Subscribe must stream the per-turn semantic events the ACP projection
//     needs: assistant text/reasoning chunks, tool start/end, usage, terminal
//     turn status with stop reason (cancelled vs failed vs completed).
//   - Approval requests must be resolvable without a UI: the ACP server either
//     auto-denies (default) or maps session/request_permission in a later
//     round; the runtime must expose the pending request (call id + args) and
//     allow/deny methods.
//   - Cancel must stop the in-flight turn (session.Controller.Stop today) and
//     the runtime should report the turn as cancelled so session/prompt settles
//     with stopReason "cancelled".
package acp
