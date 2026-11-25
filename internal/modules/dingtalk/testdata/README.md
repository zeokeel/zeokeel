# DingTalk fixture provenance

All bodies were transcribed from the official **接收消息的消息协议** page
(<https://open.dingtalk.com/document/robots/receive-message>), which is the
contract for the HTTP-mode chat-bot callback this adapter receives. Only ids and
message text were replaced with values of the same kind; field names, types and
nesting are the documented ones.

Signature contract on the same page: the callback carries the headers
`timestamp` (unix **milliseconds**) and `sign`, where

```
stringToSign = timestamp + "\n" + appSecret
sign         = base64(HMAC-SHA256(key = appSecret, message = stringToSign))
```

and a timestamp more than 1 hour from local now is a replay.

| Fixture | Shape | Adapter must |
|---|---|---|
| `text_callback_group.json` | Documented group (`conversationType: "2"`) text message: `msgId`, `conversationId`, `senderStaffId`, `senderId`, `senderNick`, `conversationTitle`, `atUsers`, `isInAtList`, `sessionWebhook`, `sessionWebhookExpiredTime`, `robotCode`, `createAt`, `text.content` (with the documented leading space). | Ingest: `Channel = conversationId`, `Sender = senderStaffId`, `DedupKey = "dingtalk:" + msgId`, `Body` = `text.content` trimmed. Every other field is platform metadata and is not part of the body. |
| `text_callback_single.json` | Same protocol, 1:1 chat: `conversationType: "1"`, no `conversationTitle`, no `atUsers`, `isInAtList: false`. | Ingest the same way. `Channel` is `conversationId` again — the conversation id already names the address, so the adapter does not branch on `conversationType` and the group/1:1 distinction never reaches the inbox. |
| `text_with_session_fields.json` | Text message plus `sessionWebhook`, `sessionWebhookExpiredTime`, `robotCode` and a numeric `timeout`. | Ingest only `text.content`; the webhook URL, robot code and timeout must be ignored, never folded into `Body`/`Title`. |
| `markdown_callback.json` | `msgtype: "markdown"` with `markdown.title` / `markdown.text`. The documented inbound types are `text`/`richText`/`picture`/`audio`/`video`/`file`, so this is a defensive fixture. | Refuse with `ErrDingTalkUnsupportedMessage`. |
| `richtext_callback.json` | Documented inbound type `msgtype: "richText"`, content under `content.richText` (not `text.content`). | Refuse with `ErrDingTalkUnsupportedMessage` — not `ErrDingTalkInvalid`: the callback is well formed, it is just not a type we ingest. |
| `text_missing_msg_id.json` | Text callback without `msgId`. | Refuse with `ErrDingTalkInvalid`: without a message id there is no dedupe identity, so a platform retry would file a duplicate. |
| `text_missing_staff_id.json` | Text callback with `senderId` but no `senderStaffId`. The doc marks `senderStaffId` as returned only once the robot is published; an unpublished robot (or an external sender) produces exactly this. | Refuse with `ErrDingTalkInvalid`. Deliberate: `Sender` is the staff id used for owner binding, and the encrypted `senderId` is not a substitute for it. Pinned as a known limitation (see the test comment) rather than silently filing the wrong identity. |