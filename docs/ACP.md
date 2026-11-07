# ACP mode (editor integration)

ZeoKeel can run as an Agent Client Protocol (ACP) backend over stdio, the same
role the old shell's `internal/acp` adapter played. It loads the `headless`
assembly and exposes the conversation backend as newline-framed JSON-RPC 2.0.

```sh
# one JSON-RPC frame per line on stdin, responses/notifications on stdout
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}' |
  ./zeokeel --acp --data-dir ~/.zeokeel
```

Supported methods (minimal surface):

- `initialize` → `{"protocolVersion":1,"agentCapabilities":{...}}`
- `session/new` → `{"sessionId":"<id>"}` (creates a normal product session)
- `session/prompt` → streams `session/update` notifications with
  `agent_message_chunk` text, then returns `{"stopReason":"end_turn"}`;
  `session/cancel` aborts with `{"stopReason":"cancelled"}`
- `session/cancel` → cancels the in-flight prompt
- unknown methods → JSON-RPC `-32601`; malformed lines → `-32700` and the loop
  keeps serving

The ACP backend is the `zeokeel/acp` module (`internal/modules/acp`), which
requires the product `ChatService`; the host starts it when `--acp` is passed
(`RunACP`). It uses the same settings/data directory as the desktop product, so
provider configuration and sessions are shared.
