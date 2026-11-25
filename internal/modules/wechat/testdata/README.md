# WeChat (iLink) fixture provenance

These files are recorded **response bodies** of the three iLink endpoints the
adapter talks to, replayed verbatim by `fixtures_test.go` (the fake server
writes the file bytes as-is, so nothing is re-encoded or re-shaped on the way
in).

Sources, in order of authority for this adapter:

- `docs/research/channel-capabilities.md` §1 — the only checked-in description
  of the channel: `getupdates` long-poll in, `sendmessage` out, "missing fields
  → HTTP 200 but **not delivered**", single-chat text only.
- `docs/plans/grokbot-open-source-plan.md` §6 4 — the field set every
  `sendmessage` must carry (`from_user_id` / `to_user_id` / `client_id` /
  `message_type` / `message_state` / `context_token` / `item_list`, plus
  `base_info.channel_version`), and again "HTTP 200 but not delivered".
- The public community description of the iLink wire format
  (<https://github.com/codeenxi/weixin-ClawBot-API>, linked from the research
  note above) for field names and nesting of `getupdates`, `sendmessage` and
  `getloginqrcode`.

**Honest limitation:** there is no real-device trace in this repository yet. Only
ids, tokens and message text were replaced with values of the same kind; field
names, types and nesting follow the public descriptions. Where the public
description does not pin a detail (the numeric id of a non-text `item_list`
type, whether `getupdates` is ever wrapped in `{"data": …}`), the fixture pins
the *contract-relevant property* and says so in the table below — it does not
claim to be a byte-for-byte device capture.

| Fixture | Shape | Adapter must |
|---|---|---|
| `getupdates_text_message.json` | One text message: `msg_id`, `from_user_id`, `to_user_id`, `context_token`, `item_list[0] = {type: 1, text_item: {text}}`, plus ignored metadata (`create_time`, `message_type`). | Ingest exactly one item: `Source = "wechat"`, `Channel = Sender = from_user_id`, `DedupKey = "wechat:" + msg_id`, `Body = Title = ` the trimmed `text_item.text`, `Ref` empty. Record `context_token` as the reply token for that user and move `Status.LastInbound`/`WindowClosesAt` to now / now+24h. |
| `getupdates_empty.json` | Documented "nothing new" answer: `{"msgs": []}`. | Return `(0, nil)`: no inbox row, and **no** reply-window movement (an empty poll is not an interaction). |
| `getupdates_media_only.json` | One message whose `item_list` has only a non-text item (`type: 3`, `image_item`). The channel is text-only, so media cannot be represented. (Public descriptions do not pin the numeric id of non-text items; the property that matters is `type != 1` with no `text_item`.) | Skip: `(0, nil)`, no inbox row — **not** an empty-text row — and do not record the message's `context_token`, so the 24h window is not opened by a message we never showed anyone. |
| `getupdates_data_envelope.json` | The same text message wrapped in `{"data": {"msgs": [...]}}` instead of a flat `{"msgs": [...]}`. Some deployments of other iLink endpoints use this envelope (`login.go` already tolerates it). | **Known gap, pinned not fixed:** the adapter decodes only the flat shape, so this body is read as "no messages" — `(0, nil)`, no inbox row, no error, `LastInbound` untouched. The messages are lost silently. See "Documented gaps". |
| `getupdates_error_retcode.json` | HTTP 200 whose body is `{"ret_code": -1, "errmsg": "invalid bot token"}` — the shape an iLink build uses to reject a token without changing the status code. | **Known gap, pinned not fixed:** read as "no messages" (`(0, nil)`), so the poll loop keeps reporting success and never surfaces the rejected token. See "Documented gaps". |
| `sendmessage_ok.json` | HTTP 200, `{"ok": true}` — the delivered answer. | `Send` returns `nil`. |
| `sendmessage_error_retcode.json` | HTTP 200, `{"ret_code": -1, "errmsg": "invalid context token"}`. The research note already says a refused send can come back as a 200; a body-level error code is the same failure. | `Send` returns an error wrapping `ErrWeChatUnavailable` and carrying the platform's `errmsg`. **This was a real bug and is fixed in `adapter.go`:** before the fix any 200 counted as delivered, so a swallowed message was reported as success. Pinned by `TestReplaySendMessageErrorRetCodeIsRefused`. |
| `sendmessage_ok_false.json` | HTTP 200, `{"ok": false, "errmsg": "send too frequently"}` — the explicit-negative spelling of the same verdict (plausible for the community-reported low rate limit; the limit itself is unpublished). | Same refusal path as a non-zero `ret_code`: `ErrWeChatUnavailable` + `errmsg`. |
| `sendmessage_no_verdict.json` | HTTP 200, `{}` — no verdict fields at all. | Accepted: the request already got a 200 and the body contradicts nothing. Pinned so the new check cannot become "reject anything unexpected" and fail every terse platform answer. (A completely empty body is accepted the same way.) |
| `login_qrcode.json` | `getloginqrcode` answer: `{"qrcode", "qrcode_url", "expires_in": 60}`. | `StartLogin` returns both values and sets `ExpiresAt = now + 60s` — the platform TTL wins over the local `loginDefaultTTL` (5m). |

## Documented gaps (updated: §1 and §2 are fixed)

1. **`{"data": …}` envelope is not unwrapped on `getupdates`.** `login.go` decodes
   both the flat and the `data` shape for the two login endpoints; `PollOnce`
   does not. If any deployment wraps `getupdates` answers, every message is
   dropped with no error. Proposed (not done here, no device evidence to size
   the risk): decode `{"msgs", "data": {"msgs"}}` and prefer the flat list when
   both are present, exactly like `loginQRCodePayload.fillFrom` — a superset of
   today's behaviour, so it cannot lose a message it currently ingests.
2. **`ret_code`/`errmsg` on `getupdates` is ignored.** `sendmessage` now checks
   the body (see above), `PollOnce` does not, so a 200-with-error token
   rejection looks like an empty poll: `Run` resets its backoff and keeps
   polling forever, and `Status` keeps reporting connected. Proposed: apply the
   same body check to `getupdates` and map a non-zero `ret_code` to
   `ErrWeChatUnavailable` (or `ErrWeChatUnauthorized` for the platform's
   auth codes, once those are known from a device).
3. **Non-text `item_list` types are skipped, not surfaced.** Skipping is the
   intended text-only policy, but a user who sends a voice note and then a text
   message within the same poll gets only the text ingested; the skipped media
   leaves no trace beyond the returned count. Acceptable while the channel is
   text-only; revisit together with media support.
4. **Only `ret_code` and `ok` are read as the send verdict.** Some iLink builds
   spell the code `ret` (no `_code`); such a refusal would still be reported as
   delivered. Not pinned by a fixture because no public description shows the
   field name, and guessing wrong would reject valid sends — the fix belongs
   with the device trace. Accepting `ret` as an alias is a two-line change once
   the real spelling is known.
5. **`sendmessage` has no delivery reconciliation.** The research note
   (`grokbot-open-source-plan.md` §6) proposes checking that the next
   `getupdates` reflects the message; the adapter stops at the platform's
   verdict. A body saying "ok" while the platform later drops the message (e.g.
   an undisclosed rate limit) is still invisible.