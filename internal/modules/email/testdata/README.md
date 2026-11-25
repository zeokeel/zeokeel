# Email fixture provenance

Every file here is a raw **RFC 5322 message** as it sits in a mailbox — the wire
shape an IMAP client hands the adapter. `fixtures_test.go` replays them through
`PollOnce` after parsing the header section into a map (the `email.RawMessage`
seam) and leaving the body bytes untouched, which is exactly the division of
labour `imap.go` documents.

There are no `.json` fixtures here on purpose. The adapter's inbound seam is
`email.RawMessage` — a header map, the raw body bytes, and the transfer encoding —
not the IMAP protocol itself (the product ships no IMAP client; one is injected).
An IMAP `FETCH` response fixture would exercise that injected client, which this
package neither implements nor tests; the object-level shape this adapter actually
receives is what the loader builds from these files.

Sources for the shapes:

- **RFC 5322** (Internet Message Format): header fields, `Message-ID`, §2.2.3
  folding. <https://www.rfc-editor.org/rfc/rfc5322>
- **RFC 2045** (MIME part one): `Content-Type`, `Content-Transfer-Encoding`,
  §6.7 quoted-printable, §6.8 base64. <https://www.rfc-editor.org/rfc/rfc2045>
- **RFC 2046** (MIME part two): §5.1.1 default `text/plain`, §5.1.4
  `multipart/alternative`, §5.1.5 `multipart/mixed`. <https://www.rfc-editor.org/rfc/rfc2046>
- **RFC 2047** (non-ASCII header words): §2 "B" and §4.2 "Q" encodings.
  <https://www.rfc-editor.org/rfc/rfc2047>
- `docs/research/channel-capabilities.md` §4 — the mail facts this adapter
  follows: IMAP in, SMTP out, threading and dedupe by `Message-ID`, and the rule
  that a mail body is **data, never instructions**.

Message ids, addresses and text are invented; field names, encodings and nesting
are the standard ones. Bodies were produced by a real encoder (Python `quopri` /
`base64` at the mandated 76-column wrap), not retyped by hand, so the line
wrapping and padding are what a mail user agent actually emits.

Outcome the test pins for each file (`Title` is the decoded `Subject`, `Body` the
decoded text, `DedupKey` is `"email:" + Message-ID`, `Sender`/`Channel` are the
`From` header as it arrived):

| Fixture | Shape | Adapter must |
|---|---|---|
| `plain_utf8_chinese_subject.eml` | Raw UTF-8 `Subject` with **no** RFC 2047 word (`本周退款率 3.1%`), `Content-Transfer-Encoding: 8bit`, `From` with a display name (`张伟 <owner@example.com>`). | Ingest verbatim: bytes are not double-decoded. `Title = 本周退款率 3.1%`; `Body` = both lines with their CRLF; `Sender = Channel = 张伟 <owner@example.com>`; `DedupKey = email:<plain-utf8-0001@example.com>`. |
| `rfc2047_subject_b.eml` | RFC 2047 §2 **B** word in `Subject` (`=?UTF-8?B?5pys5ZGo6YCA5qy+546H?=` = `本周退款率`), 7bit body. | `Title = 本周退款率`. The encoded word must not survive: the title carries neither `=?UTF-8?B?` nor its base64 payload. |
| `rfc2047_subject_q.eml` | RFC 2047 §4.2 **Q** word, lower-case charset label, surrounded by plain text: `Re: =?utf-8?Q?=E9=80=80=E6=AC=BE=E7=8E=87_=E5=91=8A=E8=AD=A6?= (fwd)`. | `Title = Re: 退款率 告警 (fwd)` — `_` decodes to a space inside the word, the plain spans around it are preserved. |
| `quoted_printable_soft_breaks.eml` | RFC 2045 §6.7 body, 76-column wrapped by a real encoder: `=\r\n` soft breaks (some splitting a `=XX` escape), `=XX` escapes for every non-ASCII byte. | `Body` is the original text **exactly**: soft breaks are wrapping (no newline, no `=`), hard CRLFs are kept, no `=` residue at all. |
| `base64_multiline_unpadded.eml` | RFC 2045 §6.8 body wrapped at 76 columns whose final quantum is emitted **without** padding. | `Body` is the original text: wrapping and alphabet gone, unpadded tail decoded rather than refused. |
| `multipart_alternative.eml` | RFC 2046 §5.1.4 `multipart/alternative` with a preamble, a `text/plain` part (itself `quoted-printable`) and a `text/html` part (itself `base64`). This is the most common shape for a normal mail. | Ingest the **decoded `text/plain` part** — not the envelope and not the HTML rendition. `Title = 本周退款率 3.1%`; `Body = 退款率 3.1%，环比下降 0.4pp。\r\n请以纯文本部分为准。`; boundary, part headers, `<html` and base64 payload never appear in `Title`/`Body`. |
| `multipart_mixed_nested_alternative.eml` | RFC 2046 §5.1.5 `multipart/mixed` whose first part is a nested `multipart/alternative` (plain + HTML) and whose second part is a `application/pdf` attachment. The usual shape of "mail with an attachment". | Find the `text/plain` one level down: `Body = 附表在附件里。\r\n正文只读这一段。`. Neither the nested/outer boundary, the HTML rendition nor the PDF payload reaches the inbox. |
| `multipart_html_only.eml` | `multipart/alternative` with **no** `text/plain` part (HTML only). | Refuse with `ErrEmailBadPayload` ("multipart/alternative has no text/plain part") and ingest nothing. The adapter does not strip markup into text — see the gap below. |
| `folded_subject.eml` | RFC 5322 §2.2.3 folded `Subject`: an encoded word on the first line, the rest of the subject on a continuation line starting with a space. | `Title = 本周退款率 3.1% (2026-11-25 08:00 数据快照)`, one line — the fold contributes a word break, not a line break. The test also replays the same mail after a client-side unfold (`net/mail`) and requires the identical title. |
| `missing_message_id.eml` | Well-formed mail (Subject, From, body) with **no** `Message-ID` header. | Skip it: `ingested = 0`, no `Add` call, no inbound timestamp. Without that header there is no dedupe identity, so filing it would duplicate the mail on every poll. A poll that also contains a good message still files that one. |
| `unknown_transfer_encoding.eml` | `Content-Transfer-Encoding: x-uuencode` (neither MIME nor supported here) over a uuencoded-looking body. | Refuse with `ErrEmailBadPayload` naming the encoding, ingest nothing. No best-effort guess: the bytes would reach triage as garbage. |
| `rfc2047_unknown_charset.eml` | RFC 2047 word in a charset the standard library cannot convert (`=?gb2312?B?6YCA5qy+?=`). | Ingest the message, keep the word **verbatim** as the title. A broken `Subject` must not fail the poll and lose the mail. Documented gap. |

## Known gaps (pinned, not fixed)

1. **Non-UTF-8 charsets in headers.** `mime.WordDecoder` has no `CharsetReader`
   configured and the standard library ships no GB2312/GBK/Big5 converter, so an
   encoded word in those charsets stays verbatim (`rfc2047_unknown_charset.eml`).
   Fixing it needs a charset table (`golang.org/x/text/encoding`), which is out of
   scope under the stdlib-only rule. The body is unaffected.
2. **HTML-only multipart mail is refused.** Stripping HTML into text is a
   rendering decision with real failure modes (dropped line breaks, tags pasted
   into the triage prompt), so the adapter refuses instead. Note the seam: a
   **single-part** `Content-Type: text/html` message still reaches the inbox as
   its raw markup, because only the multipart walk looks at part types. No
   fixture pins that shape yet — it is the same gap, one branch over. But `PollOnce` aborts
   the whole poll on a decode error, which means one such mail keeps `Run`
   backing off — and with it every later message — until someone deletes it. A
   future "skip and record the refusal, keep polling" rule should change both this
   and gap 3 together.
3. **An unsupported transfer encoding is likewise a poll-aborting refusal**
   (`unknown_transfer_encoding.eml`). Same remedy as gap 2.
4. **`Sender`/`Channel` are the raw `From` header**, display name included
   (`张伟 <owner@example.com>`), because the adapter must not silently discard who
   wrote the mail. Consumers that need an address must parse it
   (`net/mail.ParseAddress`); no owner binding or allow-listing happens here.
5. **Body charsets are not converted either.** A `charset=iso-8859-1` (or
   `gb2312`) `8bit` body is ingested as-is. Same reason as gap 1.