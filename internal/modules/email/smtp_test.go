package email_test

// TDD for the outbound half. What matters here is the wire message (headers a
// receiving server will parse) and the error classification the poll loop and
// the caller switch on: a rejected login must not look like a transient outage.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/email"
)

// fixedNow pins the Date header.
var fixedNow = time.Date(2026, 11, 25, 9, 30, 0, 0, time.FixedZone("CST", 8*3600))

// capturedSend is the injected transport: it records the message instead of
// dialing anything.
type capturedSend struct {
	mu    sync.Mutex
	addr  string
	msg   []byte
	calls int
	err   error
}

func (c *capturedSend) sendFunc() email.SendFunc {
	return func(addr string, msg []byte) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls++
		c.addr = addr
		c.msg = append([]byte(nil), msg...)
		return c.err
	}
}

func (c *capturedSend) message() (string, []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addr, c.msg
}

func (c *capturedSend) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// mailConfig is a fully configured mailbox with the clock and transport stubbed.
func mailConfig(transport *capturedSend) email.Config {
	return email.Config{
		SMTPAddr: "smtp.example.com:587",
		IMAPAddr: "imap.example.com:993",
		Username: "bot@example.com",
		Password: "secret",
		From:     "bot@example.com",
		Now:      func() time.Time { return fixedNow },
		SendFunc: transport.sendFunc(),
	}
}

// splitMessage separates the header block from the body and decodes the body.
func splitMessage(t *testing.T, msg []byte) (map[string]string, string) {
	t.Helper()
	head, rawBody, ok := strings.Cut(string(msg), "\r\n\r\n")
	if !ok {
		t.Fatalf("message has no header/body separator: %q", msg)
	}
	headers := make(map[string]string)
	for _, line := range strings.Split(head, "\r\n") {
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("malformed header line %q", line)
		}
		headers[strings.ToLower(name)] = value
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(rawBody)))
	if err != nil {
		t.Fatalf("decode quoted-printable body: %v", err)
	}
	return headers, string(decoded)
}

func TestSendBuildsMinimalRFC5322Message(t *testing.T) {
	transport := &capturedSend{}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))

	body := "第一行\nsecond line"
	if err := adapter.Send(context.Background(), "owner@example.com", "Weekly report", body); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if transport.callCount() != 1 {
		t.Fatalf("transport called %d times, want 1", transport.callCount())
	}
	addr, raw := transport.message()
	if addr != "smtp.example.com:587" {
		t.Fatalf("transport addr = %q, want the configured SMTP address", addr)
	}

	headers, decoded := splitMessage(t, raw)
	for _, want := range []struct{ name, value string }{
		{"from", "bot@example.com"},
		{"to", "owner@example.com"},
		{"subject", "Weekly report"},
		{"mime-version", "1.0"},
		{"content-type", "text/plain; charset=utf-8"},
		{"content-transfer-encoding", "quoted-printable"},
	} {
		if got := headers[want.name]; got != want.value {
			t.Fatalf("header %s = %q, want %q (message: %q)", want.name, got, want.value, raw)
		}
	}

	// Date must be RFC 1123 with a numeric zone and come from the injected clock.
	date, err := time.Parse(time.RFC1123Z, headers["date"])
	if err != nil {
		t.Fatalf("Date header %q is not RFC1123Z: %v", headers["date"], err)
	}
	if !date.Equal(fixedNow) {
		t.Fatalf("Date = %v, want %v", date, fixedNow)
	}

	// Line endings are CRLF on the wire and the text round-trips.
	if !strings.Contains(string(raw), "\r\nSubject: Weekly report\r\n") {
		t.Fatalf("message is not CRLF-framed: %q", raw)
	}
	if decoded != "第一行\r\nsecond line" {
		t.Fatalf("decoded body = %q, want the CRLF-normalized text", decoded)
	}
	// The non-ASCII body must actually be quoted-printable encoded.
	if bytes.Contains(raw, []byte("第一行")) {
		t.Fatalf("body was not quoted-printable encoded: %q", raw)
	}
	if !bytes.Contains(raw, []byte("=E7=AC=AC")) {
		t.Fatalf("quoted-printable encoding is missing from the body: %q", raw)
	}
}

func TestSendEncodesNonASCIISubject(t *testing.T) {
	transport := &capturedSend{}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))

	const subject = "退款率周报"
	if err := adapter.Send(context.Background(), "owner@example.com", subject, "body"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, raw := transport.message()
	headers, _ := splitMessage(t, raw)
	encoded := headers["subject"]
	if !strings.HasPrefix(encoded, "=?utf-8?q?") || !strings.HasSuffix(encoded, "?=") {
		t.Fatalf("Subject header = %q, want an RFC 2047 Q-encoded word", encoded)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(encoded)
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if decoded != subject {
		t.Fatalf("decoded subject = %q, want %q", decoded, subject)
	}
	// An ASCII subject must stay plain: no pointless encoding.
	transport2 := &capturedSend{}
	adapter2 := email.NewAdapter(newFakeInbox(), mailConfig(transport2))
	if err := adapter2.Send(context.Background(), "owner@example.com", "plain ascii", ""); err != nil {
		t.Fatalf("Send ASCII subject: %v", err)
	}
	_, raw2 := transport2.message()
	headers2, _ := splitMessage(t, raw2)
	if headers2["subject"] != "plain ascii" {
		t.Fatalf("ASCII subject = %q, want it unencoded", headers2["subject"])
	}
}

func TestSendRejectsEmptySubjectAndBody(t *testing.T) {
	transport := &capturedSend{}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))

	for _, tc := range []struct{ subject, body string }{
		{"", ""},
		{"   ", "\n\t "},
	} {
		if err := adapter.Send(context.Background(), "owner@example.com", tc.subject, tc.body); !errors.Is(err, contracts.ErrEmailEmptyMessage) {
			t.Fatalf("Send(%q, %q) = %v, want ErrEmailEmptyMessage", tc.subject, tc.body, err)
		}
	}
	if transport.callCount() != 0 {
		t.Fatalf("transport called %d times for empty messages, want 0", transport.callCount())
	}

	// Only *both* empty is refused: a subject-only or body-only mail is fine.
	if err := adapter.Send(context.Background(), "owner@example.com", "subject only", ""); err != nil {
		t.Fatalf("Send with subject only: %v", err)
	}
	if err := adapter.Send(context.Background(), "owner@example.com", "", "body only"); err != nil {
		t.Fatalf("Send with body only: %v", err)
	}
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	transport := &capturedSend{}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))

	if err := adapter.Send(context.Background(), "owner@example.com", "hello\r\nBcc: evil@example.com", "body"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, raw := transport.message()
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("subject injected a header: %q", raw)
	}
	headers, _ := splitMessage(t, raw)
	if headers["subject"] != "hello  Bcc: evil@example.com" {
		t.Fatalf("subject = %q, want the newlines flattened", headers["subject"])
	}
}

func TestSendMapsAuthenticationRejection(t *testing.T) {
	transport := &capturedSend{err: errors.New("535 5.7.8 Username and Password not accepted")}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))

	err := adapter.Send(context.Background(), "owner@example.com", "hi", "body")
	if !errors.Is(err, contracts.ErrEmailUnauthorized) {
		t.Fatalf("Send = %v, want ErrEmailUnauthorized", err)
	}
	if errors.Is(err, contracts.ErrEmailUnavailable) {
		t.Fatalf("Send error also matches ErrEmailUnavailable: %v", err)
	}
}

func TestSendMapsNetworkFailure(t *testing.T) {
	transport := &capturedSend{err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))

	err := adapter.Send(context.Background(), "owner@example.com", "hi", "body")
	if !errors.Is(err, contracts.ErrEmailUnavailable) {
		t.Fatalf("Send = %v, want ErrEmailUnavailable", err)
	}
}

func TestSendRefusesAnUnconfiguredMailbox(t *testing.T) {
	transport := &capturedSend{}
	for _, mutate := range []func(*email.Config){
		func(cfg *email.Config) { cfg.Username = "" },
		func(cfg *email.Config) { cfg.From = "  " },
	} {
		cfg := mailConfig(transport)
		mutate(&cfg)
		adapter := email.NewAdapter(newFakeInbox(), cfg)
		if err := adapter.Send(context.Background(), "owner@example.com", "hi", "body"); !errors.Is(err, contracts.ErrEmailUnauthorized) {
			t.Fatalf("Send with an unconfigured mailbox = %v, want ErrEmailUnauthorized", err)
		}
	}
	if transport.callCount() != 0 {
		t.Fatalf("transport called %d times, want 0", transport.callCount())
	}
}

func TestSendRequiresARecipient(t *testing.T) {
	transport := &capturedSend{}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))
	if err := adapter.Send(context.Background(), " ", "hi", "body"); !errors.Is(err, contracts.ErrEmailBadPayload) {
		t.Fatalf("Send without a recipient = %v, want ErrEmailBadPayload", err)
	}
	if transport.callCount() != 0 {
		t.Fatalf("transport called %d times, want 0", transport.callCount())
	}
}

func TestSendHonorsACanceledContext(t *testing.T) {
	transport := &capturedSend{}
	adapter := email.NewAdapter(newFakeInbox(), mailConfig(transport))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.Send(ctx, "owner@example.com", "hi", "body"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send on a canceled context = %v, want context.Canceled", err)
	}
	if transport.callCount() != 0 {
		t.Fatalf("transport called %d times, want 0", transport.callCount())
	}
}

// fakeSMTP is a loopback SMTP server that speaks just enough of the protocol for
// net/smtp: greeting, EHLO with AUTH PLAIN, MAIL, RCPT, DATA, QUIT. It keeps the
// default transport (the net/smtp path) under test without a real mail server.
type fakeSMTP struct {
	listener  net.Listener
	authReply string

	mu       sync.Mutex
	authLine string
	data     string
	closed   chan struct{}
}

func startFakeSMTP(t *testing.T, authReply string) (*fakeSMTP, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable in this sandbox: %v", err)
	}
	server := &fakeSMTP{listener: listener, authReply: authReply, closed: make(chan struct{})}
	go server.serve()
	t.Cleanup(func() {
		_ = listener.Close()
		<-server.closed
	})
	return server, listener.Addr().String()
}

func (s *fakeSMTP) serve() {
	defer close(s.closed)
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reply := func(line string) { _, _ = fmt.Fprintf(conn, "%s\r\n", line) }
	reply("220 fake ESMTP")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
			reply("250-fake")
			reply("250-AUTH PLAIN")
			reply("250 OK")
		case strings.HasPrefix(verb, "AUTH"):
			s.mu.Lock()
			s.authLine = strings.TrimSpace(line)
			s.mu.Unlock()
			reply(s.authReply)
		case strings.HasPrefix(verb, "MAIL FROM"):
			reply("250 2.1.0 OK")
		case strings.HasPrefix(verb, "RCPT TO"):
			reply("250 2.1.5 OK")
		case verb == "DATA":
			reply("354 End data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" || line == ".\n" {
					break
				}
				data.WriteString(line)
			}
			s.mu.Lock()
			s.data = data.String()
			s.mu.Unlock()
			reply("250 2.0.0 OK queued")
		case verb == "QUIT":
			reply("221 2.0.0 Bye")
			return
		default:
			reply("250 OK")
		}
	}
}

func (s *fakeSMTP) captured() (authLine, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authLine, s.data
}

func TestDefaultTransportSendsViaSMTP(t *testing.T) {
	server, addr := startFakeSMTP(t, "235 2.7.0 Authentication successful")

	cfg := email.Config{
		SMTPAddr: addr,
		Username: "bot@example.com",
		Password: "secret",
		From:     "bot@example.com",
		Now:      func() time.Time { return fixedNow },
	}
	adapter := email.NewAdapter(newFakeInbox(), cfg)
	if err := adapter.Send(context.Background(), "owner@example.com", "Hello", "world"); err != nil {
		t.Fatalf("Send through net/smtp: %v", err)
	}

	authLine, data := server.captured()
	if !strings.HasPrefix(authLine, "AUTH PLAIN ") {
		t.Fatalf("AUTH line = %q, want PLAIN authentication", authLine)
	}
	if !strings.Contains(data, "From: bot@example.com") || !strings.Contains(data, "To: owner@example.com") {
		t.Fatalf("delivered message headers = %q, want the resolved addresses", data)
	}
	if !strings.Contains(data, "Subject: Hello") {
		t.Fatalf("delivered message = %q, want the subject header", data)
	}
}

func TestDefaultTransportReportsSMTP535(t *testing.T) {
	_, addr := startFakeSMTP(t, "535 5.7.8 Username and Password not accepted")

	cfg := email.Config{
		SMTPAddr: addr,
		Username: "bot@example.com",
		Password: "wrong",
		From:     "bot@example.com",
	}
	adapter := email.NewAdapter(newFakeInbox(), cfg)
	err := adapter.Send(context.Background(), "owner@example.com", "Hello", "world")
	if !errors.Is(err, contracts.ErrEmailUnauthorized) {
		t.Fatalf("Send with a rejected login = %v, want ErrEmailUnauthorized", err)
	}
}
