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

- `initialize` → `{"protocolVersion":1,"agentCapabilities":{...}}`. The
  advertised `promptCapabilities` are `image:true`, `audio:false`,
  `embeddedContext:false`: the server accepts image prompt blocks and forwards
  them to the product's attachment-capable chat backend.
- `session/new` → `{"sessionId":"<id>","model":"<effective model>"}` (creates a
  normal product session; `model` is the configured chat model the new session
  will run with)
- `session/prompt` accepts text and image content blocks. Text blocks become the
  prompt; image blocks (`{"type":"image","data":"<base64>","mimeType":"image/png"}`)
  are forwarded to the agent run as image attachments, and a prompt may be
  image-only. Validation: every image needs a non-empty base64 `data` payload
  (at most 10 MiB decoded) and an `image/*` `mimeType`; violations return
  JSON-RPC `-32602` (`invalid params`). A chat backend that cannot accept
  attachments returns `-32603` (`internal error`) for image prompts. Audio and
  embedded-context blocks are not advertised.
- `session/prompt` → streams `session/update` notifications with
  `agent_message_chunk` text, then returns `{"stopReason":"end_turn"}`;
  `session/cancel` aborts with `{"stopReason":"cancelled"}`
- `session/cancel` → cancels the in-flight prompt
- `session/list` → `{"sessions":[...]}` (newest updated first); `session/load` →
  `{"sessionId","title","model","messages"}` (read-only text history replay)
- unknown methods → JSON-RPC `-32601`; malformed lines → `-32700` and the loop
  keeps serving

The ACP backend is the `zeokeel/acp` module (`internal/modules/acp`), which
requires the product `ChatService`; the host starts it when `--acp` is passed
(`RunACP`). It uses the same settings/data directory as the desktop product, so
provider configuration and sessions are shared.
