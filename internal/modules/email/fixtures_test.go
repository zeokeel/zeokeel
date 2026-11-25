package email_test

// Platform-shaped replay fixtures for the inbound (IMAP) half of the mail
// adapter.
//
// Every file in testdata/ is a raw RFC 5322 message as it sits in a mailbox; the
// loader at the top of this file is the IMAP-client seam: it turns a file into the
// email.RawMessage the adapter declares — header map, body still in its transfer
// encoding, Content-Transfer-Encoding lifted out — and nothing else. It
// deliberately does not unfold continuation lines, because a client that hands the
// header section through verbatim does exactly that; the folded-Subject fixture
// therefore reaches the adapter folded.
//
// testdata/README.md lists each fixture with the outcome it pins and where the
// shape comes from (RFC + the mail facts in
// docs/research/channel-capabilities.md §4).
//
// Each case asserts the exact ingested fields — Source, Channel, Sender, DedupKey,
// Title, Body — or one specific typed error plus "nothing reached the inbox".
// "It did not panic" is not an assertion.

import (
	"bytes"
	"context"
	"errors"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/email"
)

// readMailFixture returns the fixture bytes untouched: these files are the wire
// shape, so the replay must not normalise line endings or re-encode anything.
func readMailFixture(t *testing.T, fixture string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	return raw
}

// loadMail parses a fixture the way an IMAP client hands a message to the adapter:
// the header section becomes the map (continuation lines kept in place, so a
// folded value stays folded) and the body stays byte-identical.
func loadMail(t *testing.T, fixture string) email.RawMessage {
	t.Helper()
	raw := readMailFixture(t, fixture)
	split := bytes.Index(raw, []byte("\r\n\r\n"))
	if split < 0 {
		t.Fatalf("fixture %s has no blank line after the headers, so it is not a mail message", fixture)
	}

	header := make(map[string][]string)
	current := ""
	for _, line := range strings.Split(string(raw[:split]), "\r\n") {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if current == "" {
				t.Fatalf("fixture %s starts with a continuation line %q", fixture, line)
			}
			header[current][0] += "\r\n" + line
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			t.Fatalf("fixture %s has a malformed header line %q", fixture, line)
		}
		current = line[:colon]
		header[current] = append(header[current], strings.TrimSpace(line[colon+1:]))
	}

	return email.RawMessage{
		Header:   header,
		Body:     raw[split+4:],
		Encoding: headerFirst(header, "Content-Transfer-Encoding"),
	}
}

// headerFirst is the test-side twin of the adapter's own case-insensitive lookup.
func headerFirst(header map[string][]string, name string) string {
	for key, values := range header {
		if !strings.EqualFold(key, name) {
			continue
		}
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	return ""
}

// replayMail polls once over the named fixtures and returns the adapter and the
// inbox it wrote to.
func replayMail(t *testing.T, fixtures ...string) (*email.Adapter, *fakeInbox) {
	t.Helper()
	messages := make([]email.RawMessage, 0, len(fixtures))
	for _, fixture := range fixtures {
		messages = append(messages, loadMail(t, fixture))
	}
	client := &fakeIMAPClient{}
	client.setMessages(messages...)
	adapter, inbox := pollFixture(t, client)
	return adapter, inbox
}

// onlyItem returns the single inbox item, failing if the poll filed any other
// number of items.
func onlyItem(t *testing.T, inbox *fakeInbox) contracts.InboxItem {
	t.Helper()
	items := inbox.snapshot()
	if len(items) != 1 {
		t.Fatalf("inbox holds %d items (%+v), want exactly 1", len(items), items)
	}
	return items[0]
}

// assertNothingIngested pins the refusal/skip cases: no item, no Add call at all,
// and no inbound timestamp — a skipped or refused message must not look like mail
// that arrived.
func assertNothingIngested(t *testing.T, adapter *email.Adapter, inbox *fakeInbox) {
	t.Helper()
	if items := inbox.snapshot(); len(items) != 0 {
		t.Fatalf("inbox holds %+v, want nothing", items)
	}
	if adds := inbox.addCount(); adds != 0 {
		t.Fatalf("inbox Add called %d times, want 0", adds)
	}
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.LastInbound.IsZero() {
		t.Fatalf("last inbound = %v, want zero: nothing was ingested", status.LastInbound)
	}
}

// assertFiled pins the identity fields every ingested mail shares: the source,
// the From header (Sender and Channel), and the dedupe key the Message-ID produces.
func assertFiled(t *testing.T, item contracts.InboxItem, from, messageID string) {
	t.Helper()
	if item.Source != contracts.EmailSource {
		t.Fatalf("source = %q, want %q", item.Source, contracts.EmailSource)
	}
	if item.Sender != from || item.Channel != from {
		t.Fatalf("sender/channel = (%q, %q), want the From header %q", item.Sender, item.Channel, from)
	}
	if want := "email:" + messageID; item.DedupKey != want {
		t.Fatalf("dedup key = %q, want %q", item.DedupKey, want)
	}
}

// assertNoMIME checks that transport machinery stayed out of the text the triage
// path will read: boundaries, part headers, base64 payloads and HTML markup are
// how a normal mail turns into an unreadable inbox row.
func assertNoMIME(t *testing.T, item contracts.InboxItem, forbidden map[string]string) {
	t.Helper()
	for what, needle := range forbidden {
		if strings.Contains(item.Body, needle) || strings.Contains(item.Title, needle) {
			t.Fatalf("%s (%q) reached the inbox: title=%q body=%q", what, needle, item.Title, item.Body)
		}
	}
}

// A raw UTF-8 Subject with no RFC 2047 word, and a From carrying a display name.
// This is what a same-language mailer sends, and it is the shape the product's
// own SMTP writer produces.
func TestReplayPlainUTF8SubjectAndBody(t *testing.T) {
	adapter, inbox := replayMail(t, "plain_utf8_chinese_subject.eml")

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 1 {
		t.Fatalf("ingested = %d, want 1", ingested)
	}
	item := onlyItem(t, inbox)
	// Channel and Sender are the From header exactly as it arrived, display name
	// included. Extracting the addr-spec is the consumer's job; the adapter must
	// not silently drop who wrote the mail.
	assertFiled(t, item, "张伟 <owner@example.com>", "<plain-utf8-0001@example.com>")
	if want := "本周退款率 3.1%"; item.Title != want {
		t.Fatalf("title = %q, want the Subject as written %q", item.Title, want)
	}
	if want := "本周退款率 3.1%，环比上周下降 0.4 个百分点。\r\n明细见附表。\r\n"; item.Body != want {
		t.Fatalf("body = %q, want %q", item.Body, want)
	}
	if item.Count != 1 {
		t.Fatalf("count = %d, want 1", item.Count)
	}
}

// RFC 2047 §2 "B" encoding (base64 word) in the Subject: the raw word must never
// reach the title.
func TestReplayRFC2047SubjectB(t *testing.T) {
	adapter, inbox := replayMail(t, "rfc2047_subject_b.eml")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	item := onlyItem(t, inbox)
	// Subject: =?UTF-8?B?5pys5ZGo6YCA5qy+546H?=
	if want := "本周退款率"; item.Title != want {
		t.Fatalf("title = %q, want the decoded B word %q", item.Title, want)
	}
	assertNoMIME(t, item, map[string]string{"encoded word": "=?UTF-8?B?", "base64 payload": "5pys5ZGo6YCA"})
	if item.Body != "B encoded subject.\r\n" {
		t.Fatalf("body = %q", item.Body)
	}
	assertFiled(t, item, "owner@example.com", "<rfc2047-b-0002@example.com>")
}

// RFC 2047 §4.2 "Q" encoding (quoted-printable word), lower-case charset label,
// next to plain text. Underscores are spaces; the plain spans around the word are
// preserved verbatim.
func TestReplayRFC2047SubjectQ(t *testing.T) {
	adapter, inbox := replayMail(t, "rfc2047_subject_q.eml")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	item := onlyItem(t, inbox)
	// Subject: Re: =?utf-8?Q?=E9=80=80=E6=AC=BE=E7=8E=87_=E5=91=8A=E8=AD=A6?= (fwd)
	if want := "Re: 退款率 告警 (fwd)"; item.Title != want {
		t.Fatalf("title = %q, want the decoded Q word %q", item.Title, want)
	}
	assertNoMIME(t, item, map[string]string{"encoded word": "=?utf-8?Q?", "qp payload": "=E9=80=80"})
	assertFiled(t, item, "owner@example.com", "<rfc2047-q-0003@example.com>")
}

// RFC 2045 §6.7 quoted-printable: soft line breaks (a trailing '=' plus a line
// break) are wrapping, not content. They must vanish, and the escapes must be
// undone, leaving no '=' residue anywhere.
func TestReplayQuotedPrintableSoftBreaks(t *testing.T) {
	fixture := readMailFixture(t, "quoted_printable_soft_breaks.eml")
	// The fixture only proves something if it really carries soft breaks and
	// hex escapes; guard it against a future re-generation that flattens them.
	if !bytes.Contains(fixture, []byte("=\r\n")) {
		t.Fatal("fixture has no quoted-printable soft line break, so this test would pass vacuously")
	}
	if !bytes.Contains(fixture, []byte("=E9=80=80")) {
		t.Fatal("fixture has no quoted-printable hex escape, so this test would pass vacuously")
	}

	adapter, inbox := replayMail(t, "quoted_printable_soft_breaks.eml")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	item := onlyItem(t, inbox)
	want := "退款率 3.1%（环比 -0.4pp），明细见附表。这是一段被故意写得很长的中文，用来穿过 76 列的软换行边界。\r\n" +
		"第二行：软换行不得在正文里留下换行符，也不得留下等号。\r\n"
	if item.Body != want {
		t.Fatalf("body = %q, want %q", item.Body, want)
	}
	// A soft break left in place would show up as '=' or as a newline inside the
	// first logical line; the text itself contains neither.
	if strings.Contains(item.Body, "=") {
		t.Fatalf("body still contains a quoted-printable escape: %q", item.Body)
	}
	if firstLine, _, _ := strings.Cut(item.Body, "\r\n"); strings.Contains(firstLine, "\n") {
		t.Fatalf("a soft line break survived into the first line: %q", firstLine)
	}
	assertFiled(t, item, "owner@example.com", "<qp-soft-breaks-0004@example.com>")
}

// RFC 2045 §6.8 base64: the body arrives wrapped at 76 columns and its final
// quantum may be emitted without padding. Both are transport detail; neither the
// wrapping nor the alphabet may survive into the text.
func TestReplayBase64MultilineUnpadded(t *testing.T) {
	fixture := readMailFixture(t, "base64_multiline_unpadded.eml")
	body := string(fixture[bytes.Index(fixture, []byte("\r\n\r\n")):])
	if lines := strings.Count(body, "\r\n"); lines < 3 {
		t.Fatalf("fixture base64 body has %d line breaks, want a multiline body", lines)
	}

	adapter, inbox := replayMail(t, "base64_multiline_unpadded.eml")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	item := onlyItem(t, inbox)
	want := "base64 正文：本周退款率 3.1%，环比下降 0.4 个百分点。\r\n第二行：多行 base64 不得把换行带进正文。\r\n"
	if item.Body != want {
		t.Fatalf("body = %q, want %q", item.Body, want)
	}
	assertNoMIME(t, item, map[string]string{"base64 payload": "YmFzZTY0IOato", "encoded subject": "=?"})
	assertFiled(t, item, "owner@example.com", "<base64-multiline-0005@example.com>")
}

// RFC 2046 §5.1.4 multipart/alternative: the parts are the same message in two
// renditions. The adapter ingests the text/plain rendition — and it must be the
// *decoded* rendition, not the envelope: boundaries, part headers, per-part
// content-transfer-encoding and HTML are all transport.
func TestReplayMultipartAlternativeIngestsTheTextPlainPart(t *testing.T) {
	adapter, inbox := replayMail(t, "multipart_alternative.eml")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	item := onlyItem(t, inbox)
	if want := "本周退款率 3.1%"; item.Title != want {
		t.Fatalf("title = %q, want the decoded Subject %q", item.Title, want)
	}
	// The text/plain part is itself quoted-printable, so this also pins that the
	// part is decoded with its own Content-Transfer-Encoding.
	if want := "退款率 3.1%，环比下降 0.4pp。\r\n请以纯文本部分为准。"; item.Body != want {
		t.Fatalf("body = %q, want the decoded text/plain part %q", item.Body, want)
	}
	assertNoMIME(t, item, map[string]string{
		"boundary":         "--=-zk-alt-0006",
		"part header":      "Content-Type: text/plain",
		"html rendition":   "<html",
		"base64 payload":   "PGh0bWw+PGJvZHk+",
		"mime boilerplate": "This is a multi-part message",
	})
	assertFiled(t, item, "owner@example.com", "<multipart-alt-0006@example.com>")
}

// RFC 2046 §5.1.5 multipart/mixed: the usual envelope for a mail with an
// attachment, whose readable text sits one level deeper in a nested
// multipart/alternative. The text must be found there; the attachment must not
// become the body.
func TestReplayMultipartMixedFindsTheNestedTextPlainPart(t *testing.T) {
	adapter, inbox := replayMail(t, "multipart_mixed_nested_alternative.eml")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	item := onlyItem(t, inbox)
	if want := "周末数据快照（附表）"; item.Title != want {
		t.Fatalf("title = %q, want %q", item.Title, want)
	}
	if want := "附表在附件里。\r\n正文只读这一段。"; item.Body != want {
		t.Fatalf("body = %q, want the nested text/plain part %q", item.Body, want)
	}
	assertNoMIME(t, item, map[string]string{
		"outer boundary":  "--=-zk-mix-0007",
		"nested boundary": "--=-zk-mix-0007-alt",
		"attachment":      "JVBERi0xLjQg", // the faked PDF payload
		"attachment type": "application/pdf",
		"html rendition":  "<html",
	})
	assertFiled(t, item, "owner@example.com", "<multipart-mixed-0007@example.com>")
}

// A multipart/alternative whose only rendition is HTML. The adapter does not
// strip markup into text (that is a rendering decision, and a wrong one to make
// silently), so this is a typed refusal — pinned so the gap stays visible instead
// of turning into "<html><body>…" in the inbox.
func TestReplayMultipartWithoutTextPlainIsRefused(t *testing.T) {
	adapter, inbox := replayMail(t, "multipart_html_only.eml")

	ingested, err := adapter.PollOnce(context.Background())
	if !errors.Is(err, contracts.ErrEmailBadPayload) {
		t.Fatalf("err = %v, want ErrEmailBadPayload", err)
	}
	if !strings.Contains(err.Error(), "multipart/alternative") || !strings.Contains(err.Error(), "no text/plain part") {
		t.Fatalf("err = %v, want it to name the envelope and the missing part", err)
	}
	if ingested != 0 {
		t.Fatalf("ingested = %d, want 0", ingested)
	}
	assertNothingIngested(t, adapter, inbox)
}

// RFC 5322 §2.2.3 folding: a long Subject is wrapped onto continuation lines that
// start with whitespace. Unfolding drops the line break and keeps the word break,
// so the encoded word on the first line still decodes and the plain text on the
// continuation line still follows it.
func TestReplayFoldedSubject(t *testing.T) {
	message := loadMail(t, "folded_subject.eml")
	// Guard the fixture: if the fold is ever lost, this test proves nothing.
	if !strings.Contains(message.Header["Subject"][0], "\r\n ") {
		t.Fatalf("fixture Subject is not folded: %q", message.Header["Subject"][0])
	}

	client := &fakeIMAPClient{}
	client.setMessages(message)
	adapter, inbox := pollFixture(t, client)
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	item := onlyItem(t, inbox)
	if want := "本周退款率 3.1% (2026-11-25 08:00 数据快照)"; item.Title != want {
		t.Fatalf("title = %q, want the unfolded subject %q", item.Title, want)
	}
	if strings.ContainsAny(item.Title, "\r\n") {
		t.Fatalf("title = %q, want a single line (the fold was not undone)", item.Title)
	}
	if item.Body != "folded subject body.\r\n" {
		t.Fatalf("body = %q", item.Body)
	}
	assertFiled(t, item, "owner@example.com", "<folded-subject-0009@example.com>")

	// A client that unfolds before handing the header over (net/mail does) must
	// land on the same title: folding is the wire's business, not the client's.
	t.Run("a client that unfolds first", func(t *testing.T) {
		parsed, err := mail.ReadMessage(bytes.NewReader(readMailFixture(t, "folded_subject.eml")))
		if err != nil {
			t.Fatalf("read fixture with net/mail: %v", err)
		}
		unfolded := email.RawMessage{
			Header:   parsed.Header,
			Body:     message.Body,
			Encoding: headerFirst(map[string][]string(parsed.Header), "Content-Transfer-Encoding"),
		}
		client := &fakeIMAPClient{}
		client.setMessages(unfolded)
		adapter, inbox := pollFixture(t, client)
		if _, err := adapter.PollOnce(context.Background()); err != nil {
			t.Fatalf("PollOnce: %v", err)
		}
		if got := onlyItem(t, inbox).Title; got != "本周退款率 3.1% (2026-11-25 08:00 数据快照)" {
			t.Fatalf("title = %q, want the same title as the folded header", got)
		}
	})
}

// A mail with no Message-ID has no dedupe identity: filing it would duplicate the
// same mail on every poll, so it is skipped — and skipping one message must not
// stop the others in the same poll.
func TestReplayMissingMessageIDIsSkipped(t *testing.T) {
	message := loadMail(t, "missing_message_id.eml")
	if got := headerFirst(message.Header, "Message-ID"); got != "" {
		t.Fatalf("fixture carries a Message-ID (%q); it exists to have none", got)
	}

	adapter, inbox := replayMail(t, "missing_message_id.eml")
	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 0 {
		t.Fatalf("ingested = %d, want 0 (no Message-ID, nothing to dedupe on)", ingested)
	}
	assertNothingIngested(t, adapter, inbox)

	// Same poll, one skippable and one good message: the good one is still filed.
	adapter, inbox = replayMail(t, "missing_message_id.eml", "plain_utf8_chinese_subject.eml")
	ingested, err = adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce with a skippable message: %v", err)
	}
	if ingested != 1 {
		t.Fatalf("ingested = %d, want 1", ingested)
	}
	item := onlyItem(t, inbox)
	if item.DedupKey != "email:<plain-utf8-0001@example.com>" {
		t.Fatalf("dedup key = %q, want only the message that has an id", item.DedupKey)
	}
}

// An encoding that is neither MIME nor supported here is a typed refusal, not a
// best-effort guess: the body would reach triage as garbage. The poll aborts so
// the caller retries with backoff instead of silently dropping the mail.
func TestReplayUnknownTransferEncodingIsRefused(t *testing.T) {
	adapter, inbox := replayMail(t, "unknown_transfer_encoding.eml")

	ingested, err := adapter.PollOnce(context.Background())
	if !errors.Is(err, contracts.ErrEmailBadPayload) {
		t.Fatalf("err = %v, want ErrEmailBadPayload", err)
	}
	if !strings.Contains(err.Error(), `unsupported encoding "x-uuencode"`) {
		t.Fatalf("err = %v, want it to name the refused encoding", err)
	}
	if ingested != 0 {
		t.Fatalf("ingested = %d, want 0", ingested)
	}
	assertNothingIngested(t, adapter, inbox)
}

// RFC 2047 words in a charset the standard library cannot decode (GB2312 is the
// common one in Chinese mail). There is no charset converter in the standard
// library, so the word is kept verbatim: a broken Subject must not fail the poll
// and lose the message. Documented gap, pinned here.
func TestReplayUnhandledCharsetSubjectStaysVerbatim(t *testing.T) {
	adapter, inbox := replayMail(t, "rfc2047_unknown_charset.eml")
	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 1 {
		t.Fatalf("ingested = %d, want the message ingested despite the Subject", ingested)
	}

	item := onlyItem(t, inbox)
	if want := "=?gb2312?B?6YCA5qy+?="; item.Title != want {
		t.Fatalf("title = %q, want the undecodable word kept verbatim %q", item.Title, want)
	}
	if item.Body != "gb2312 subject body.\r\n" {
		t.Fatalf("body = %q, want the body decoded", item.Body)
	}
	assertFiled(t, item, "owner@example.com", "<rfc2047-charset-0011@example.com>")
}

// A mailbox with one unreadable mail must still file the readable ones: the bad
// message stays unseen for the next poll, and the refusal surfaces once so the
// loop can back off without the rest of the mailbox rotting behind it.
func TestOneUnreadableMessageDoesNotBlockTheMailbox(t *testing.T) {
	good := email.RawMessage{
		Header: map[string][]string{
			"Message-Id":   {"<good@example.com>"},
			"Subject":      {"fine"},
			"From":         {"owner@example.com"},
			"Content-Type": {"text/plain; charset=utf-8"},
		},
		Body: []byte("body"),
	}
	bad := email.RawMessage{
		Header: map[string][]string{
			"Message-Id":   {"<bad@example.com>"},
			"Subject":      {"bad"},
			"From":         {"owner@example.com"},
			"Content-Type": {`multipart/alternative; boundary="x"`},
		},
		Body: []byte("--x\r\nContent-Type: text/html\r\n\r\n<p>only html</p>\r\n--x--\r\n"),
	}
	inboxSvc := newFakeInbox()
	adapter := email.NewAdapter(inboxSvc, email.Config{
		Username: "owner@example.com", Password: "pw", From: "owner@example.com",
		IMAPClient: &fakeIMAPClient{messages: []email.RawMessage{bad, good}},
	})

	ingested, err := adapter.PollOnce(context.Background())
	if ingested != 1 {
		t.Fatalf("ingested = %d, want the readable message filed despite the bad one", ingested)
	}
	if err == nil {
		t.Fatal("the refusal was swallowed: the loop would never know a mail is stuck")
	}
	items, listErr := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if listErr != nil || len(items) != 1 || items[0].DedupKey != "email:<good@example.com>" {
		t.Fatalf("inbox = (%+v, %v), want exactly the good message", items, listErr)
	}
}
