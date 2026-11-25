package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Email adapter. Mail is the only channel that is symmetric and stateless: SMTP
// pushes one plain-text message out, IMAP polling pulls unread mail in. There is
// no card, no button and no push callback, so the only interactive surface is
// the message body — which means an email body is always data, never
// instructions (docs/research/channel-capabilities.md §4). Threading is by
// Message-ID / In-Reply-To, and the Message-ID is also the dedupe identity: a
// re-poll of the same unseen message must fold into the existing inbox item.
const (
	// EmailSource is the inbox source value every inbound mail carries.
	EmailSource = "email"
)

// EmailStatus is the account state the settings page shows.
type EmailStatus struct {
	// Connected reports whether the mailbox is configured (username, password
	// and sender address all set).
	Connected bool
	// LastInbound is the last time a message was ingested (zero = never).
	LastInbound time.Time
}

// EmailService is the adapter surface.
type EmailService interface {
	// PollOnce fetches the unseen messages, normalizes them into the inbox and
	// returns how many were ingested. Messages without a Message-ID cannot be
	// deduped, so they are skipped and not counted.
	PollOnce(ctx context.Context) (int, error)
	// Send delivers one plain-text mail. It refuses an empty message instead of
	// sending a blank one.
	Send(ctx context.Context, to, subject, body string) error
	// Status reports the account state.
	Status(ctx context.Context) (EmailStatus, error)
}

// Email errors are typed so the poll loop can tell a rejected login (terminal)
// from a transient outage (retry with backoff).
var (
	// ErrEmailUnauthorized means the mailbox credentials were rejected (or no
	// mailbox is configured).
	ErrEmailUnauthorized = errors.New("email: unauthorized")
	// ErrEmailUnavailable means the mail server could not be reached or the
	// transaction failed for a transient reason.
	ErrEmailUnavailable = errors.New("email: unavailable")
	// ErrEmailEmptyMessage means there was neither a subject nor a body to send.
	ErrEmailEmptyMessage = errors.New("email: empty message")
	// ErrEmailBadPayload means an inbound message could not be decoded (unknown
	// transfer encoding, corrupt quoted-printable or base64 body).
	ErrEmailBadPayload = errors.New("email: bad payload")
)

// KeyEmail is provided by the email module.
var KeyEmail = cordis.NewServiceKey[EmailService]("zeokeel/email")
