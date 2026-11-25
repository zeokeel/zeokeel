# Feishu fixture provenance

Bodies transcribed from the official open-platform docs; only ids/message text
were replaced with values of the same kind.

Sources:

- v2 event (`schema` 2.0, `header` + `event.message` with `content` as a JSON
  **string**): <https://open.feishu.cn/document/server-docs/im-v1/message/events/receive>
  (事件体示例). The doc also states retries are delivered again under the same
  `message_id` — that is why the adapter dedupes on the message id.
- v1.0 event envelope (`type: "event_callback"`, `uuid`, `ts`, and the event
  fields flat under `event`): <https://open.feishu.cn/document/ukTMukTMukTM/uUTNz4SN1MjL1UzM>
  (事件结构 § v1.0 版本事件结构). The v1.0 *message* field names
  (`msg_type`/`message_id`/`open_chat_id`/`open_id`/`text`) come from the
  official SDK's `P1MessageReceiveV1` event type, because the standalone v1.0
  message event page is no longer published.
- Card action callback (v2, `event_type: "card.action.trigger"`, `event.action`
  with `value` as object **or** string): <https://open.feishu.cn/document/feishu-cards/card-callback-communication>
  (回调结构体示例).
- Legacy card callback (top-level `action.value`): <https://open.feishu.cn/document/ukTMukTMukTM/uYzM3QjL2MzN04iNzcDN/configuring-card-callbacks/card-callback-structure>.
- `url_verification` handshake and the encrypted push body (`{"encrypt": "..."}`,
  also used for the handshake when an Encrypt Key is configured):
  <https://open.feishu.cn/document/event-subscription-guide/event-subscriptions/event-subscription-configure-/choose-a-subscription-mode/send-notifications-to-developers-server>.
- Signature scheme (`hex(sha256(timestamp + nonce + encrypt_key + rawBody))`):
  <https://open.feishu.cn/document/server-docs/event-subscription-guide/event-subscription-configure-/encrypt-key-encryption-configuration-case>.

| Fixture | Shape | Adapter must |
|---|---|---|
| `event_v2_message.json` | Documented v2 `im.message.receive_v1` group text event: `header` (event_id/event_type/create_time/token/app_id/tenant_key), `event.sender.sender_id` (open/union/user id), `event.message` with `message_id`, `chat_id`, `chat_type`, `message_type: "text"`, `content` as the JSON string `{"text": "..."}`, `mentions`, `thread_id`, `user_agent`. | Ingest: `Source = "feishu"`, `Channel = chat_id`, `Sender = sender open_id`, `DedupKey = "feishu:" + message_id`, `Ref = message_id`, `Body` = decoded `content.text`. |
| `event_v1_callback_flat.json` | Documented v1.0 envelope: `ts`/`uuid`/`token`/`type: "event_callback"` with the message fields flat under `event` (`msg_type`, `message_id`, `open_chat_id`, `open_id`, `text` as a plain string). | Refuse with `ErrFeishuBadPayload`. **Known gap, reported:** the adapter's v1 branch only maps a v1 envelope whose `event` body is nested like a v2 event, which the platform does not send for v1 events, so a real v1 message event is refused (loudly) instead of ingested. |
| `card_action_v2.json` | Documented v2 `card.action.trigger` callback: `event.operator`, `event.token`, `event.action.value` as an object carrying `approvalId`/`decision`/`token`, `event.action.tag`, `event.context`. | `VerifyRequest` accepts the signed body; `CardAction` → `(ap_100, allow)`; `CardActionWithToken` → `(ap_100, allow, tok_100)`. |
| `card_action_legacy_string.json` | Documented legacy card callback: top-level `open_id`, `open_message_id`, `token`, `action.value` as a **JSON string**. | `CardAction` → `(ap_legacy_7, deny)`; `CardActionWithToken` → not ok (the value carries no approval token, so the button is not enough to approve). |
| `url_verification.json` | Documented handshake: `{"challenge", "token", "type": "url_verification"}`. | `VerifyURL` → the challenge; `Receive` → no inbox row and no error (the HTTP layer echoes the challenge). A forged handshake never reaches this because the signature is checked first. |
| `event_encrypted.json` | Documented encrypted push: `{"encrypt": "<base64 ciphertext>"}`. | Refuse with `ErrFeishuBadPayload` — never acknowledge it silently, because a 200 would look like a successful ingest while the event body was never read. We do not implement the decrypt path (see the test comment). |