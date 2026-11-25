package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// maxMultipartDepth bounds how deep a multipart body may nest (mixed →
// alternative is depth 1) so a crafted message cannot make the walk recurse
// forever.
const maxMultipartDepth = 4

// RawMessage is one message as the IMAP client delivers it: headers already
// parsed into a map, body still in its transfer encoding (as declared by the
// Content-Transfer-Encoding header). Keeping the wire shape this thin is what
// lets the tests drive PollOnce without a mail server.
type RawMessage struct {
	// Header is the parsed header map. Lookups are case-insensitive, so a client
	// may deliver either "Message-ID" or "Message-Id". Content-Type is read to
	// find a multipart envelope's text/plain part; a folded value may arrive with
	// its continuation lines still in it (see headerValue).
	Header map[string][]string
	// Body is the raw body bytes, still encoded.
	Body []byte
	// Encoding is the transfer encoding ("quoted-printable", "base64", "7bit",
	// "8bit", "binary" or "" for raw). For a multipart message it describes the
	// envelope; each part carries its own.
	Encoding string
}

// IMAPClient is the inbound half of the channel. The product ships no IMAP
// implementation of its own (the standard library has none); the operator
// injects one, and the tests inject a fake.
type IMAPClient interface {
	// Unseen returns the messages currently flagged unseen. It must not mark
	// anything as read: the adapter owns the decision to ingest.
	Unseen(ctx context.Context) ([]RawMessage, error)
}

// PollOnce implements contracts.EmailService: it pulls the unseen messages,
// normalizes each one into an inbox item and reports how many were ingested.
//
// A message without a Message-ID is skipped (not counted): without that header
// there is no dedupe identity, and a re-poll would file the same mail again
// under a fresh key. One undecodable message aborts the poll with
// ErrEmailBadPayload and reports what was already ingested, so the caller can
// retry with backoff instead of silently dropping mail.
func (a *Adapter) PollOnce(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.cfg.IMAPClient == nil {
		return 0, fmt.Errorf("%w: no imap client configured", contracts.ErrEmailUnauthorized)
	}

	messages, err := a.cfg.IMAPClient.Unseen(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, classifyMailError(err)
	}

	ingested := 0
	// One unreadable mail must not block the mailbox: the message stays unseen
	// (so the next poll retries it) while every other message is still filed.
	// The first refusal is returned at the end, so the loop still backs off and
	// the failure is visible instead of a silent skip.
	var firstRefusal error
	for _, message := range messages {
		messageID := strings.TrimSpace(headerValue(message.Header, "Message-ID"))
		if messageID == "" {
			continue // no dedupe identity: filing it would duplicate on re-poll
		}
		text, err := messageText(message)
		if err != nil {
			if firstRefusal == nil {
				firstRefusal = fmt.Errorf("email: message %s: %w", messageID, err)
			}
			continue
		}
		from := strings.TrimSpace(headerValue(message.Header, "From"))
		item := contracts.InboxItem{
			Source: contracts.EmailSource,
			// Mail has no conversation id beyond the counterpart: the thread is
			// carried by Message-ID/In-Reply-To, which we keep as the dedupe key.
			Channel:  from,
			Sender:   from,
			DedupKey: "email:" + messageID,
			Title:    titleFor(headerValue(message.Header, "Subject"), text),
			Body:     text,
		}
		if _, _, err := a.inbox.Add(ctx, item); err != nil {
			return ingested, err
		}
		a.markInbound()
		ingested++
	}
	return ingested, firstRefusal
}

// markInbound records the ingestion time the settings page shows.
func (a *Adapter) markInbound() {
	a.mu.Lock()
	a.lastInbound = a.cfg.Now()
	a.mu.Unlock()
}

// messageText returns the readable text of one message: the decoded body of a
// single-part message, or the decoded text/plain part of a multipart one.
//
// A multipart envelope carries no text of its own. Its boundaries, part headers,
// per-part transfer encodings and alternative HTML rendition are all transport,
// and passing the envelope through is how an ordinary mail arrives in the inbox as
// unreadable MIME (see testdata/README.md).
func messageText(message RawMessage) (string, error) {
	mediaType, params := parseContentType(headerValue(message.Header, "Content-Type"))
	if !strings.HasPrefix(mediaType, "multipart/") {
		return decodeRawBody(message.Body, message.Encoding)
	}
	envelope, err := decodeTransferEncoding(message.Body, message.Encoding)
	if err != nil {
		return "", err
	}
	return textPlainPart(envelope, mediaType, params["boundary"], 0)
}

// textPlainPart walks one multipart body looking for its text/plain part,
// descending into nested multiparts (mixed → alternative is what a mail with an
// attachment looks like). Non-text parts are skipped rather than converted: HTML is
// never stripped into text, so an HTML-only mail is refused and stays visible
// instead of arriving as markup.
func textPlainPart(body []byte, mediaType, boundary string, depth int) (string, error) {
	if boundary == "" {
		return "", fmt.Errorf("%w: %s without a boundary", contracts.ErrEmailBadPayload, mediaType)
	}
	if depth > maxMultipartDepth {
		return "", fmt.Errorf("%w: %s nested deeper than %d levels", contracts.ErrEmailBadPayload, mediaType, maxMultipartDepth)
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var nestedErr error
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			if nestedErr != nil {
				return "", nestedErr
			}
			return "", fmt.Errorf("%w: %s has no text/plain part", contracts.ErrEmailBadPayload, mediaType)
		}
		if err != nil {
			return "", fmt.Errorf("%w: %s: %v", contracts.ErrEmailBadPayload, mediaType, err)
		}
		partType, partParams := parseContentType(part.Header.Get("Content-Type"))
		// NextRawPart keeps the part bytes exactly as transmitted, so the part's
		// own transfer encoding is decoded once, below, and never twice.
		raw, readErr := io.ReadAll(part)
		part.Close()
		if readErr != nil {
			return "", fmt.Errorf("%w: %s: %v", contracts.ErrEmailBadPayload, mediaType, readErr)
		}
		if strings.HasPrefix(partType, "multipart/") {
			text, err := textPlainPart(raw, partType, partParams["boundary"], depth+1)
			if err == nil {
				return text, nil
			}
			nestedErr = err // a nested multipart without text may still have a sibling
			continue
		}
		if partType != "" && partType != "text/plain" {
			continue
		}
		// RFC 2046 §5.1.1: a part with no Content-Type defaults to text/plain.
		return decodeRawBody(raw, part.Header.Get("Content-Transfer-Encoding"))
	}
}

// parseContentType splits a Content-Type value; a missing or broken one is "".
func parseContentType(value string) (string, map[string]string) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return "", nil
	}
	return mediaType, params
}

// decodeRawBody returns the message text according to its transfer encoding.
func decodeRawBody(body []byte, encoding string) (string, error) {
	decoded, err := decodeTransferEncoding(body, encoding)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// decodeTransferEncoding undoes the transfer encoding and returns the bytes.
func decodeTransferEncoding(body []byte, encoding string) ([]byte, error) {
	switch encoding := strings.ToLower(strings.TrimSpace(encoding)); encoding {
	case "", "7bit", "8bit", "binary":
		return body, nil
	case "quoted-printable", "quotedprintable", "qprint":
		if err := validateQuotedPrintable(body); err != nil {
			return nil, fmt.Errorf("%w: quoted-printable: %v", contracts.ErrEmailBadPayload, err)
		}
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		if err != nil {
			return nil, fmt.Errorf("%w: quoted-printable: %v", contracts.ErrEmailBadPayload, err)
		}
		return decoded, nil
	case "base64":
		decoded, err := decodeBase64Body(body)
		if err != nil {
			return nil, fmt.Errorf("%w: base64: %v", contracts.ErrEmailBadPayload, err)
		}
		return decoded, nil
	default:
		// An encoding we cannot decode is not something to guess at: the body
		// would reach the triage path as garbage.
		return nil, fmt.Errorf("%w: unsupported encoding %q", contracts.ErrEmailBadPayload, encoding)
	}
}

// validateQuotedPrintable rejects an '=' that is not followed by two hex digits
// or a soft line break. The standard-library reader is deliberately lenient: it
// copies an invalid escape through verbatim, so without this check corrupt text
// would reach triage looking decoded.
func validateQuotedPrintable(body []byte) error {
	for i := 0; i < len(body); i++ {
		if body[i] != '=' {
			continue
		}
		if i+1 >= len(body) {
			return fmt.Errorf("truncated escape at offset %d", i)
		}
		next := body[i+1]
		switch {
		case next == '\n':
			i++
		case next == '\r' && i+2 < len(body) && body[i+2] == '\n':
			i += 2
		case isHexDigit(next) && i+2 < len(body) && isHexDigit(body[i+2]):
			i += 2
		default:
			end := i + 3
			if end > len(body) {
				end = len(body)
			}
			return fmt.Errorf("invalid escape %q at offset %d", string(body[i:end]), i)
		}
	}
	return nil
}

func isHexDigit(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'f':
		return true
	case b >= 'A' && b <= 'F':
		return true
	}
	return false
}

// decodeBase64Body decodes a base64 body, tolerating the line breaks real mail
// carries and an unpadded final quantum.
func decodeBase64Body(body []byte) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, string(body))
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err == nil {
		return decoded, nil
	}
	if paddingless, rawErr := base64.RawStdEncoding.DecodeString(strings.TrimRight(compact, "=")); rawErr == nil {
		return paddingless, nil
	}
	return nil, err
}

// headerValue looks a header up case-insensitively; the first value wins. Folding
// is undone here: an RFC 5322 §2.2.3 folded header reaches the adapter as one value
// with continuation lines still on it when the client hands the header section
// through verbatim, and an encoded Subject split across the fold would otherwise
// carry a line break into the inbox title.
func headerValue(header map[string][]string, name string) string {
	for key, values := range header {
		if !strings.EqualFold(key, name) {
			continue
		}
		if len(values) == 0 {
			return ""
		}
		return unfoldHeader(values[0])
	}
	return ""
}

// unfoldHeader removes the line breaks of a folded header value, leaving the
// continuation line's leading whitespace in place: that whitespace is the word
// break unfolding is defined to preserve.
func unfoldHeader(value string) string {
	if !strings.ContainsAny(value, "\r\n") {
		return value
	}
	value = strings.ReplaceAll(value, "\r\n", "")
	return strings.ReplaceAll(value, "\n", "")
}

// titleFor builds the one-line inbox title: the decoded subject, truncated to
// maxTitleRunes, falling back to the first body line when the subject is empty.
func titleFor(subject, body string) string {
	title := strings.TrimSpace(decodeHeaderText(subject))
	if title == "" {
		for _, line := range strings.Split(body, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				title = line
				break
			}
		}
	}
	runes := []rune(title)
	if len(runes) > maxTitleRunes {
		runes = runes[:maxTitleRunes]
	}
	return string(runes)
}

// decodeHeaderText undoes RFC 2047 word encoding when the header carries it; an
// undecodable header is kept verbatim (a broken subject must not fail the poll).
func decodeHeaderText(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}
