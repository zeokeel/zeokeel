package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SendFunc hands one pre-built RFC 5322 message to the transport. addr is the
// SMTP server address ("host:port"). The default implementation dials that server
// with net/smtp and authenticates with PLAIN; tests inject their own so no real
// socket is ever needed.
type SendFunc func(addr string, msg []byte) error

// Send implements contracts.EmailService: it validates the message, builds a
// minimal RFC 5322 body and hands it to the transport. Only the subject and the
// body carry user text; both headers are stripped of CR/LF so a crafted subject
// cannot inject headers.
func (a *Adapter) Send(ctx context.Context, to, subject, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(subject) == "" && strings.TrimSpace(body) == "" {
		return contracts.ErrEmailEmptyMessage
	}
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("%w: missing recipient", contracts.ErrEmailBadPayload)
	}
	// No sender or no login: refuse rather than send anonymously. SMTP would
	// reject it anyway, and a silent anonymous send is worse than an error.
	if strings.TrimSpace(a.cfg.From) == "" || strings.TrimSpace(a.cfg.Username) == "" {
		return fmt.Errorf("%w: mailbox is not configured", contracts.ErrEmailUnauthorized)
	}

	message, err := buildMessage(a.cfg.From, to, subject, body, a.cfg.Now())
	if err != nil {
		return err
	}
	if err := a.cfg.SendFunc(a.cfg.SMTPAddr, message); err != nil {
		return classifyMailError(err)
	}
	return nil
}

// buildMessage renders the minimal message this channel needs: plain text, utf-8,
// quoted-printable, CRLF line endings. The subject is RFC 2047 encoded only when
// it is not ASCII, so an English subject stays readable in the raw bytes.
func buildMessage(from, to, subject, body string, date time.Time) ([]byte, error) {
	subjectValue := sanitizeHeaderValue(subject)
	if !isASCII(subjectValue) {
		subjectValue = mime.QEncoding.Encode("utf-8", subjectValue)
	}

	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "From: %s\r\n", sanitizeHeaderValue(from))
	fmt.Fprintf(&buffer, "To: %s\r\n", sanitizeHeaderValue(to))
	fmt.Fprintf(&buffer, "Subject: %s\r\n", subjectValue)
	fmt.Fprintf(&buffer, "Date: %s\r\n", date.Format(time.RFC1123Z))
	buffer.WriteString("MIME-Version: 1.0\r\n")
	buffer.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buffer.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buffer.WriteString("\r\n")

	writer := quotedprintable.NewWriter(&buffer)
	if _, err := writer.Write([]byte(crlfLineEndings(body))); err != nil {
		return nil, fmt.Errorf("%w: encode body: %v", contracts.ErrEmailBadPayload, err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("%w: encode body: %v", contracts.ErrEmailBadPayload, err)
	}
	return buffer.Bytes(), nil
}

// defaultSendFunc is the production transport: connect, authenticate with PLAIN
// when a username is configured, then MAIL FROM / RCPT TO / DATA. The envelope
// addresses come from the message headers, so the transport needs nothing but
// the finished message.
//
// net/smtp has no context support, so a canceled Send is only observed before
// the dial; a caller that needs a hard deadline injects its own SendFunc.
func defaultSendFunc(username, password string) SendFunc {
	return func(addr string, msg []byte) error {
		parsed, err := mail.ReadMessage(bytes.NewReader(msg))
		if err != nil {
			return fmt.Errorf("email: parse outgoing message: %w", err)
		}
		from := parsed.Header.Get("From")
		to := parsed.Header.Get("To")

		client, err := smtp.Dial(addr)
		if err != nil {
			return err
		}
		defer client.Close()

		if username != "" {
			host, _, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				host = addr
			}
			if supported, _ := client.Extension("AUTH"); supported {
				if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
					return err
				}
			}
		}
		if err := client.Mail(from); err != nil {
			return err
		}
		if err := client.Rcpt(to); err != nil {
			return err
		}
		writer, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := writer.Write(msg); err != nil {
			_ = writer.Close()
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		return client.Quit()
	}
}

// classifyMailError maps a transport or client failure onto the typed errors the
// poll loop and the caller switch on: a rejected login is terminal, everything
// else is worth retrying.
func classifyMailError(err error) error {
	if err == nil {
		return nil
	}
	if isAuthRejection(err) {
		return fmt.Errorf("%w: %v", contracts.ErrEmailUnauthorized, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %v", contracts.ErrEmailUnavailable, err)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("%w: %v", contracts.ErrEmailUnavailable, err)
	}
	return fmt.Errorf("%w: %v", contracts.ErrEmailUnavailable, err)
}

// isAuthRejection recognizes the ways SMTP and IMAP say "these credentials are
// wrong": the 530/534/535 reply codes (net/smtp reports them as plain text, not
// as a typed error) and the usual IMAP authentication phrases.
func isAuthRejection(err error) bool {
	text := err.Error()
	for _, code := range []string{"530", "534", "535"} {
		if containsSMTPCode(text, code) {
			return true
		}
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "authenticationfailed") ||
		strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "invalid credentials") ||
		strings.Contains(lower, "login failed")
}

// containsSMTPCode reports whether text carries code as a standalone reply code
// ("535 5.7.8 ...", "535-5.7.8 ..."), not as part of a longer number.
func containsSMTPCode(text, code string) bool {
	for _, field := range strings.Fields(text) {
		field = strings.TrimSuffix(field, ":")
		if strings.HasPrefix(field, code) {
			return true
		}
	}
	return false
}

// sanitizeHeaderValue removes CR/LF (header injection) and collapses the value to
// one line.
func sanitizeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

// isASCII reports whether every byte is 7-bit, i.e. the value needs no RFC 2047
// encoding.
func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}

// crlfLineEndings converts every line ending to CRLF (RFC 5322 requires it and
// quoted-printable leaves line endings literal).
func crlfLineEndings(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}
