// Package acp implements the minimum Agent Client Protocol (ACP v1) server
// that lets trusted out-of-process clients drive a ZeoKeel headless agent over
// a newline-framed JSON-RPC 2.0 stdio transport.
//
// # Scope (minimum ACP v1, implemented)
//
// Only the minimum automation surface is provided:
//
//	initialize     -> protocol version + agent capabilities
//	session/new    -> create a headless session bound to an absolute cwd
//	session/prompt -> run one prompt, stream session/update notifications,
//	                  settle with a stopReason
//	session/cancel -> cancel the in-flight prompt of one session
//
// Plus the JSON-RPC 2.0 error handling the transport needs (parse error
// -32700, invalid request -32600, method not found -32601, invalid params
// -32602, internal error -32603) and request/response correlation on one
// stdio connection.
//
// # Implementation
//
//   - Serve reads newline-framed JSON-RPC 2.0 from in. A malformed line answers
//     -32700 with a null id and keeps the connection alive.
//   - Control requests (initialize / session/new / session/cancel) are handled
//     in arrival order so the initialize gate and session state stay
//     consistent; session/prompt runs concurrently per request, so a slow turn
//     never blocks other traffic. Responses correlate by id and may complete
//     out of order.
//   - initialize opens the prompt gate; session/prompt before it answers
//     -32600. session/new requires an absolute, existing cwd (-32602
//     otherwise). Unknown methods answer -32601; agent handler failures answer
//     -32603.
//   - session/prompt streams Agent updates as session/update notifications
//     before its own response (ordered delivery) and allows one in-flight
//     prompt per session. session/cancel cancels the in-flight prompt's context
//     and calls Agent.Cancel; the prompt then settles with stopReason
//     "cancelled". Unknown session ids are a no-op.
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
// Preferred backend: internal/app.NewHeadlessRuntime (owned by the backend
// fixer); until it exists the adapter can wrap BuildServicesWith +
// session.Controller directly (CreateSessionInWorkspace / Submit / Stop /
// OnUpdate / SetOnTurnFinished / SetOnApprovalRequested / SetSessionPermission).
//
// Adapter requirements (minimum):
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
//  5. Approval requests: without a UI the adapter must auto-deny pending
//     approvals (Controller.Deny) so a prompt never hangs; a later round may
//     project them as ACP session/request_permission.
//  6. Close: release the runtime/controller and the store (idempotent).
//
// CLI wiring (cmd/zeokeel, for the CLI owner): add an `acp` subcommand that
// creates the adapter, builds acp.NewServer(Config{Agent: adapter, Version:
// buildinfo.Version}), routes all logs to stderr, runs srv.Serve(ctx,
// os.Stdin, os.Stdout) with a SIGINT/SIGTERM context, and exits 0 on EOF/normal
// shutdown, 1 on fatal startup errors. Do not print banners on stdout in this
// path. Use `Use "github.com/zeokeel/zeokeel/internal/acp"` from the adapter.
//
// # Not in the minimum surface (documented non-goals)
//
// session/load or resume, session/list, authenticate, session/set_config_option,
// client fs/terminal methods, image prompts, MCP server startup from
// session/new, and session/request_permission. Provider/model selection stays
// in ZeoKeel settings; the ACP client is a trusted local controller.
package acp
