package email_test

// TDD for the inbound half. The rules that matter: one mail becomes one inbox
// item with a Message-ID-based dedupe key (so a re-poll merges instead of
// duplicating), a mail without that header is skipped rather than duplicated, and
// a body we cannot decode is a typed error instead of garbage in the triage path.

import (
	"context"
	"encoding/base64"
	"errors"
	"golang.org/x/text/encoding/simplifiedchinese"
	"mime/quotedprintable"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/email"
)

// pollFixture wires an adapter over the fake IMAP client and its inbox.
func pollFixture(t *testing.T, client *fakeIMAPClient) (*email.Adapter, *fakeInbox) {
	t.Helper()
	inbox := newFakeInbox()
	cfg := email.Config{
		IMAPAddr:   "imap.example.com:993",
		Username:   "bot@example.com",
		Password:   "secret",
		From:       "bot@example.com",
		Now:        func() time.Time { return fixedNow },
		IMAPClient: client,
	}
	return email.NewAdapter(inbox, cfg), inbox
}

func qpEncode(t *testing.T, text string) string {
	t.Helper()
	var buffer strings.Builder
	writer := quotedprintable.NewWriter(&buffer)
	if _, err := writer.Write([]byte(text)); err != nil {
		t.Fatalf("encode quoted-printable fixture: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close quoted-printable fixture: %v", err)
	}
	return buffer.String()
}

func TestPollOnceIngestsTwoMessages(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(
		rawMail("<a@example.com>", "owner@example.com", "退款率", "本周退款率 3.1%", ""),
		rawMail("<b@example.com>", "boss@example.com", "Weekly numbers", "please review", ""),
	)
	adapter, inbox := pollFixture(t, client)

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 2 {
		t.Fatalf("PollOnce = %d, want 2", ingested)
	}
	items := inbox.snapshot()
	if len(items) != 2 {
		t.Fatalf("inbox holds %d items, want 2", len(items))
	}
	first := items[0]
	if first.Source != contracts.EmailSource {
		t.Fatalf("item source = %q, want %q", first.Source, contracts.EmailSource)
	}
	if first.Channel != "owner@example.com" || first.Sender != "owner@example.com" {
		t.Fatalf("item channel/sender = (%q, %q), want the From header", first.Channel, first.Sender)
	}
	if first.DedupKey != "email:<a@example.com>" {
		t.Fatalf("item dedupe key = %q, want email:<a@example.com>", first.DedupKey)
	}
	if first.Title != "退款率" {
		t.Fatalf("item title = %q, want the decoded subject", first.Title)
	}
	if first.Body != "本周退款率 3.1%" {
		t.Fatalf("item body = %q, want the message text", first.Body)
	}
	if items[1].DedupKey != "email:<b@example.com>" || items[1].Sender != "boss@example.com" {
		t.Fatalf("second item = %+v, want the second message", items[1])
	}

	// A re-poll of the same unseen mail must merge, not duplicate.
	ingested, err = adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}
	if ingested != 2 {
		t.Fatalf("second PollOnce = %d, want 2", ingested)
	}
	items = inbox.snapshot()
	if len(items) != 2 {
		t.Fatalf("inbox holds %d items after a re-poll, want 2 (deduped by Message-ID)", len(items))
	}
	for _, item := range items {
		if item.Count != 2 {
			t.Fatalf("item %q count = %d, want 2 arrivals merged", item.DedupKey, item.Count)
		}
	}
}

func TestPollOnceDedupesByMessageID(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(
		rawMail("<same@example.com>", "owner@example.com", "one", "first copy", ""),
		rawMail("<same@example.com>", "owner@example.com", "one", "second copy", ""),
	)
	adapter, inbox := pollFixture(t, client)

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 2 {
		t.Fatalf("PollOnce = %d, want 2", ingested)
	}
	items := inbox.snapshot()
	if len(items) != 1 {
		t.Fatalf("inbox holds %d items, want 1 (same Message-ID)", len(items))
	}
	if items[0].Count != 2 {
		t.Fatalf("merged item count = %d, want 2", items[0].Count)
	}
	if items[0].DedupKey != "email:<same@example.com>" {
		t.Fatalf("merged dedupe key = %q", items[0].DedupKey)
	}
}

func TestPollOnceSkipsMessagesWithoutMessageID(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(
		rawMail("", "owner@example.com", "no id", "body without an id", ""),
		rawMail("<kept@example.com>", "owner@example.com", "has id", "body with an id", ""),
		rawMail("   ", "owner@example.com", "blank id", "body with a blank id", ""),
	)
	adapter, inbox := pollFixture(t, client)

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 1 {
		t.Fatalf("PollOnce = %d, want 1 (messages without a Message-ID are skipped)", ingested)
	}
	if adds := inbox.addCount(); adds != 1 {
		t.Fatalf("inbox Add called %d times, want 1", adds)
	}
	items := inbox.snapshot()
	if len(items) != 1 || items[0].DedupKey != "email:<kept@example.com>" {
		t.Fatalf("inbox = %+v, want only the message with an id", items)
	}
}

func TestPollOnceDecodesTransferEncodings(t *testing.T) {
	quotedPrintable := qpEncode(t, "grüße aus München — 退款 3.1%")
	base64Body := base64.StdEncoding.EncodeToString([]byte("base64 says 你好"))

	client := &fakeIMAPClient{}
	client.setMessages(
		email.RawMessage{
			Header:   map[string][]string{"Message-ID": {"<qp@example.com>"}, "From": {"owner@example.com"}, "Subject": {"qp"}},
			Body:     []byte(quotedPrintable),
			Encoding: "quoted-printable",
		},
		email.RawMessage{
			Header: map[string][]string{"Message-ID": {"<b64@example.com>"}, "From": {"owner@example.com"}, "Subject": {"b64"}},
			// Real mail wraps base64 at 76 columns; the decoder must tolerate it.
			Body:     []byte(base64Body[:8] + "\r\n" + base64Body[8:]),
			Encoding: "BASE64",
		},
	)
	adapter, inbox := pollFixture(t, client)

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 2 {
		t.Fatalf("PollOnce = %d, want 2", ingested)
	}
	items := inbox.snapshot()
	if items[0].Body != "grüße aus München — 退款 3.1%" {
		t.Fatalf("quoted-printable body = %q", items[0].Body)
	}
	if items[1].Body != "base64 says 你好" {
		t.Fatalf("base64 body = %q", items[1].Body)
	}
}

func TestPollOnceRejectsBadPayload(t *testing.T) {
	for _, tc := range []struct {
		name     string
		encoding string
		body     string
	}{
		{"invalid base64", "base64", "!!!not base64!!!"},
		{"invalid quoted-printable", "quoted-printable", "broken =ZZ hex"},
		{"unknown encoding", "x-uuencode", "whatever"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeIMAPClient{}
			client.setMessages(email.RawMessage{
				Header:   map[string][]string{"Message-ID": {"<bad@example.com>"}, "From": {"owner@example.com"}, "Subject": {"bad"}},
				Body:     []byte(tc.body),
				Encoding: tc.encoding,
			})
			adapter, inbox := pollFixture(t, client)

			ingested, err := adapter.PollOnce(context.Background())
			if !errors.Is(err, contracts.ErrEmailBadPayload) {
				t.Fatalf("PollOnce = %v, want ErrEmailBadPayload", err)
			}
			if ingested != 0 {
				t.Fatalf("ingested = %d, want 0", ingested)
			}
			if adds := inbox.addCount(); adds != 0 {
				t.Fatalf("inbox Add called %d times, want 0", adds)
			}
		})
	}
}

func TestPollOnceTruncatesTheTitle(t *testing.T) {
	subject := strings.Repeat("邮", 80)
	client := &fakeIMAPClient{}
	client.setMessages(rawMail("<long@example.com>", "owner@example.com", subject, "body", ""))
	adapter, inbox := pollFixture(t, client)

	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	items := inbox.snapshot()
	if got := len([]rune(items[0].Title)); got != 60 {
		t.Fatalf("title has %d runes, want 60", got)
	}
	if items[0].Body != "body" {
		t.Fatalf("body = %q, want the full text untouched", items[0].Body)
	}
}

func TestPollOnceDecodesAnEncodedSubject(t *testing.T) {
	client := &fakeIMAPClient{}
	// =?utf-8?q?=E9=80=80=E6=AC=BE?= is "退款".
	client.setMessages(rawMail("<enc@example.com>", "owner@example.com", "=?utf-8?q?=E9=80=80=E6=AC=BE?=", "body", ""))
	adapter, inbox := pollFixture(t, client)

	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if title := inbox.snapshot()[0].Title; title != "退款" {
		t.Fatalf("title = %q, want the RFC 2047 decoded subject", title)
	}
}

func TestPollOnceFindsHeadersCaseInsensitively(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(email.RawMessage{
		Header: map[string][]string{
			"message-id": {"<lower@example.com>"},
			"from":       {"owner@example.com"},
			"SUBJECT":    {"lowercase headers"},
		},
		Body: []byte("text"),
	})
	adapter, inbox := pollFixture(t, client)

	ingested, err := adapter.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if ingested != 1 {
		t.Fatalf("PollOnce = %d, want 1", ingested)
	}
	items := inbox.snapshot()
	if items[0].DedupKey != "email:<lower@example.com>" || items[0].Sender != "owner@example.com" || items[0].Title != "lowercase headers" {
		t.Fatalf("item = %+v, want headers found regardless of case", items[0])
	}
}

func TestPollOnceFallsBackToTheBodyForAnEmptySubject(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(rawMail("<nosubject@example.com>", "owner@example.com", "", "  \n  first real line\nsecond", ""))
	adapter, inbox := pollFixture(t, client)

	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if title := inbox.snapshot()[0].Title; title != "first real line" {
		t.Fatalf("title = %q, want the first body line", title)
	}
}

func TestPollOnceRequiresAnIMAPClient(t *testing.T) {
	inbox := newFakeInbox()
	adapter := email.NewAdapter(inbox, email.Config{Username: "bot@example.com", Password: "secret", From: "bot@example.com"})
	ingested, err := adapter.PollOnce(context.Background())
	if !errors.Is(err, contracts.ErrEmailUnauthorized) {
		t.Fatalf("PollOnce without a client = %v, want ErrEmailUnauthorized", err)
	}
	if ingested != 0 {
		t.Fatalf("ingested = %d, want 0", ingested)
	}
}

func TestPollOnceClassifiesClientErrors(t *testing.T) {
	t.Run("rejected login", func(t *testing.T) {
		client := &fakeIMAPClient{}
		client.setError(errors.New("imap: LOGIN failed: [AUTHENTICATIONFAILED] Invalid credentials"))
		adapter, _ := pollFixture(t, client)
		if _, err := adapter.PollOnce(context.Background()); !errors.Is(err, contracts.ErrEmailUnauthorized) {
			t.Fatalf("PollOnce = %v, want ErrEmailUnauthorized", err)
		}
	})
	t.Run("unreachable server", func(t *testing.T) {
		client := &fakeIMAPClient{}
		client.setError(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")})
		adapter, _ := pollFixture(t, client)
		if _, err := adapter.PollOnce(context.Background()); !errors.Is(err, contracts.ErrEmailUnavailable) {
			t.Fatalf("PollOnce = %v, want ErrEmailUnavailable", err)
		}
	})
}

func TestPollOncePropagatesInboxFailures(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(rawMail("<x@example.com>", "owner@example.com", "subject", "body", ""))
	adapter, inbox := pollFixture(t, client)
	sentinel := errors.New("inbox exploded")
	inbox.setAddError(sentinel)

	ingested, err := adapter.PollOnce(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("PollOnce = %v, want the inbox error", err)
	}
	if ingested != 0 {
		t.Fatalf("ingested = %d, want 0", ingested)
	}
}

func TestPollOnceHonorsACanceledContext(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(rawMail("<x@example.com>", "owner@example.com", "subject", "body", ""))
	adapter, _ := pollFixture(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := adapter.PollOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("PollOnce on a canceled context = %v, want context.Canceled", err)
	}
	if calls := client.callCount(); calls != 0 {
		t.Fatalf("IMAP client called %d times after cancellation, want 0", calls)
	}
}

// Mail from Chinese mail clients is routinely GBK and declares it. Without the
// charset step the inbox shows mojibake and triage receives it.
func TestPollOnceDecodesDeclaredCharsets(t *testing.T) {
	gbk := func(text string) []byte {
		encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatalf("encode gbk: %v", err)
		}
		return encoded
	}
	client := &fakeIMAPClient{}
	client.setMessages(
		email.RawMessage{
			Header: map[string][]string{
				"Message-ID":   {"<gbk@example.com>"},
				"From":         {"owner@example.com"},
				"Subject":      {"gbk"},
				"Content-Type": {"text/plain; charset=GBK"},
			},
			Body:     gbk("请确认本期退款名单"),
			Encoding: "8bit",
		},
	)
	adapter, inbox := pollFixture(t, client)

	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	items := inbox.snapshot()
	if items[0].Body != "请确认本期退款名单" {
		t.Fatalf("gbk body = %q, want the decoded Chinese text", items[0].Body)
	}
}

// A wrong or unknown label must never corrupt text that is already valid UTF-8.
func TestPollOnceKeepsValidUTF8WithAnUnknownCharset(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(email.RawMessage{
		Header: map[string][]string{
			"Message-ID":   {"<unknown@example.com>"},
			"From":         {"owner@example.com"},
			"Subject":      {"unknown"},
			"Content-Type": {"text/plain; charset=x-user-defined"},
		},
		Body:     []byte("退款 3.1%"),
		Encoding: "8bit",
	})
	adapter, inbox := pollFixture(t, client)

	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	items := inbox.snapshot()
	if items[0].Body != "退款 3.1%" {
		t.Fatalf("body = %q, want the original text", items[0].Body)
	}
}
