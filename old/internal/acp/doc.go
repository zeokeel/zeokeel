// Package acp implements the Agent Client Protocol (ACP v1) server that lets
// trusted out-of-process clients drive a ZeoKeel headless agent over a
// newline-framed JSON-RPC 2.0 stdio transport.
//
// # Scope (ACP v1 surface implemented)
//
// Client -> agent methods:
//
//	initialize     -> protocol version, agent capabilities, auth methods;
//	                  records the client capabilities used to gate fs/terminal
//	authenticate   -> validate an advertised method (no-op by default; a
//	                  configured authenticator gates the session methods with
//	                  -32000 until it succeeds)
//	session/new    -> create a headless session bound to an absolute cwd
//	session/list   -> list persisted sessions of this data directory
//	                  (id/title/updatedAt/cwd, most recently updated first)
//	session/load   -> restore an existing session and replay its history as
//	                  session/update notifications before the response
//	session/prompt -> run one prompt, stream session/update notifications,
//	                  settle with a stopReason
//	session/cancel -> cancel the in-flight prompt of one session
//
// Server -> client methods (the agent asks the client):
//
//	fs/read_text_file, fs/write_text_file (gated on clientCapabilities.fs)
//	terminal/create|output|wait_for_exit|kill|release (gated on
//	                  clientCapabilities.terminal)
//	session/request_permission -> approval prompt; the selected
//	                  allow_once/allow_always/reject_once/reject_always option
//	                  maps onto the Controller's ApprovalQueue
//
// Plus the JSON-RPC 2.0 error handling the transport needs (parse error
// -32700, invalid request -32600, method not found -32601, invalid params
// -32602, internal error -32603, authentication required -32000, resource not
// found -32002) and request/response correlation in both directions on one
// stdio connection.
//
// # Implementation
//
//   - Serve reads newline-framed JSON-RPC 2.0 from in. A malformed line answers
//     -32700 with a null id and keeps the connection alive.
//   - Control requests (initialize / authenticate / session/new / session/list /
//     session/load / session/cancel) are handled in arrival order so the
//     initialize gate, authentication state and session state stay consistent;
//     session/prompt runs concurrently per request, so a slow turn never blocks
//     other traffic. Responses correlate by id and may complete out of order.
//   - initialize opens the prompt gate; session/prompt before it answers
//     -32600. session/new and session/load require an absolute, existing cwd
//     (-32602 otherwise). Unknown methods answer -32601; agent handler failures
//     answer -32603, except explicit *RPCError values (for example an unknown
//     session id from session/load is -32002) which are passed through.
//   - session/prompt streams Agent updates as session/update notifications
//     before its own response (ordered delivery) and allows one in-flight
//     prompt per session. session/cancel cancels the in-flight prompt's context
//     and calls Agent.Cancel; the prompt then settles with stopReason
//     "cancelled". Unknown session ids are a no-op.
//   - session/list / session/load / authenticate are served when the Agent
//     opts into them by implementing SessionLister / SessionLoader; initialize
//     advertises exactly the capabilities the configured Agent implements, so
//     the baseline Agent keeps its historical advertisement (no loadSession,
//     no session list). session/load replay is delivered before its response
//     and registers the session for later prompts.
//   - Client requests: the Agent reaches the ACP client through the Client
//     value carried on the request context (ClientFromContext). The connection
//     assigns ids, correlates the client's response frames, and bounds each
//     call with Config.ClientRequestTimeout. A method whose capability the
//     client did not advertise at initialize fails with a method-not-found
//     JSON-RPC error and sends nothing on the wire; an unresponsive client
//     yields a timeout error, never a panic or a hung turn. Config.ClientRequest
//     can replace the transport seam for embedders/tests.
//     Venue contract: server->client requests may only be issued from an
//     asynchronous handler (session/prompt, which runs on its own goroutine).
//     A synchronous control handler (initialize, session/new, session/list,
//     session/load, session/cancel, ...) executes on the connection's read loop,
//     so its request could never be answered; the seam rejects it immediately
//     with an explicit internal error instead of blocking to the timeout.
//   - stdout (out) carries protocol frames only. Diagnostics go to Config.Logf
//     (stderr or the app log file), never stdout.
//
// The package has no dependencies beyond the standard library and does not
// import internal/app (the adapter lives app-side; see "Adapter wiring").
//
// # Adapter wiring (internal/app + cmd)
//
// internal/app owns the adapter that implements Agent on top of the headless
// runtime; this package only defines the seam. There is no import cycle
// because internal/acp imports nothing from the repository:
//
//	internal/app/acp_adapter.go   package app; imports internal/acp
//	cmd/zeokeel                   wires app adapter + acp.NewServer(Serve(os.Stdin, os.Stdout))
//
// Adapter requirements:
//  1. Never write to stdout: stdout is owned by the ACP frames. Log to stderr
//     or the app log file; pass Config.Logf accordingly. Do not take the
//     desktop single-instance lock.
//  2. NewSession: create a ZeoKeel session in the workspace given by req.Cwd
//     (CreateSessionInWorkspace/bind), return its id; keep a session-id map.
//  3. Prompt: run exactly one turn and project its semantic events to
//     UpdateSink, in this mapping:
//     state.Token                -> agent_message_chunk (text content)
//     state.Reasoning            -> agent_thought_chunk
//     state.ToolStart            -> tool_call (toolCallId, title, pending)
//     state.ToolEnd              -> tool_call_update (completed/failed)
//     state.Usage                -> usage_update (Usage counters)
//     Terminal turn status maps to PromptResponse.StopReason:
//     completed -> end_turn, stopped/cancel -> cancelled,
//     failed -> refusal (and keep the agent error in server logs).
//     Deliver updates before returning; return early when Prompt's context is
//     cancelled.
//  4. Cancel: stop the controller turn (Controller.Stop) and let Prompt return
//     so it settles as cancelled. Unknown ids are a no-op.
//  5. ListSessions/LoadSession: implement the SessionLister/SessionLoader
//     interfaces over the existing controller/store API (no new DB tables);
//     replay persisted user/assistant messages as user_message_chunk /
//     agent_message_chunk updates.
//  6. Approvals: when the client seam is on the prompt context, ask it via
//     Client.RequestPermission and map the selected option onto the
//     Controller's ApprovalQueue (Approve / ApproveAlways / Deny; reject_always
//     recorded adapter-side). Without a client, deny pending approvals so a
//     prompt never hangs.
//  7. Close: release the runtime/controller and the store (idempotent).
//
// CLI wiring (cmd/zeokeel): the `acp` subcommand creates the adapter, builds
// acp.NewServer(Config{Agent: adapter, Version: buildinfo.Version}), routes all
// logs to stderr, runs srv.Serve(ctx, os.Stdin, os.Stdout) with a
// SIGINT/SIGTERM context, exits 0 on EOF/normal shutdown and 1 on fatal startup
// errors. Do not print banners on stdout in this path.
//
// # Not in the surface (documented non-goals)
//
// session/set_config_option, session/close/resume/fork/delete, image/audio
// prompts (text only), MCP server startup from session/new (the declarations
// are accepted and ignored), client elicitation, and provider/model selection
// from the client (that stays in ZeoKeel settings). The ACP client is a trusted
// local controller.
package acp
