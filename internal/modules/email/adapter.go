// Package email is the mail channel adapter: SMTP out, IMAP in, plain text only.
// It polls a mailbox for unseen messages, normalizes them into the inbox, and
// delivers one plain-text reply per Send. Nothing here decides *what* to say and
// nothing here treats a mail body as an instruction: the body is data, and the
// only structured identity it contributes is the Message-ID (dedupe + thread).
// See docs/research/channel-capabilities.md §4 for the mail facts this follows.
package email

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/backoff"
)

// DefaultPollInterval is the pause between successful polls. Mail is not a chat:
// minutes, not seconds.
const DefaultPollInterval = 5 * time.Minute

// maxTitleRunes keeps an inbox title scannable; the full text stays in Body.
const maxTitleRunes = 60

// Config is the module's typed assembly config. The password is a secret and is
// excluded from JSON; the clock, transport and IMAP client are unserializable and
// are injected by code (tests, or the host that owns the mailbox).
type Config struct {
	// SMTPAddr is the submission server ("host:port") Send dials.
	SMTPAddr string `json:"smtpAddr,omitempty"`
	// IMAPAddr is the mailbox server ("host:port") the injected IMAPClient
	// talks to.
	IMAPAddr string `json:"imapAddr,omitempty"`
	// Username is the mailbox login ("" = not connected).
	Username string `json:"username,omitempty"`
	// Password is the mailbox secret.
	Password string `json:"-"`
	// From is the sender address every message is sent from.
	From string `json:"from,omitempty"`
	// Now overrides the clock (tests).
	Now func() time.Time `json:"-"`
	// SendFunc overrides the SMTP transport (tests / custom dialer).
	SendFunc SendFunc `json:"-"`
	// IMAPClient is the injected inbox reader (the product ships none).
	IMAPClient IMAPClient `json:"-"`
}

// normalized fills the defaults. Anything still missing (a transport, an IMAP
// client, mailbox credentials) is a configuration state the adapter reports
// instead of guessing.
func (c Config) normalized() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.SendFunc == nil {
		c.SendFunc = defaultSendFunc(c.Username, c.Password)
	}
	return c
}

// Adapter implements contracts.EmailService.
type Adapter struct {
	inbox contracts.InboxService
	// cfg is immutable after NewAdapter; the mutex below guards lastInbound only.
	cfg Config

	mu sync.Mutex
	// lastInbound is the newest ingestion time (zero = never).
	lastInbound time.Time
}

// NewAdapter wires the adapter.
func NewAdapter(inboxService contracts.InboxService, cfg Config) *Adapter {
	return &Adapter{inbox: inboxService, cfg: cfg.normalized()}
}

// Status implements contracts.EmailService. Connected means "the mailbox is
// configured", not "the server answered a ping": a status call must not touch the
// network.
func (a *Adapter) Status(ctx context.Context) (contracts.EmailStatus, error) {
	if err := ctx.Err(); err != nil {
		return contracts.EmailStatus{}, err
	}
	a.mu.Lock()
	lastInbound := a.lastInbound
	a.mu.Unlock()
	return contracts.EmailStatus{Connected: a.configured(), LastInbound: lastInbound}, nil
}

// configured reports whether the mailbox has everything a login needs.
func (a *Adapter) configured() bool {
	return strings.TrimSpace(a.cfg.Username) != "" &&
		strings.TrimSpace(a.cfg.Password) != "" &&
		strings.TrimSpace(a.cfg.From) != ""
}

// Run polls the mailbox until ctx is canceled: it polls immediately, then waits
// interval between polls; while the server is unavailable it backs off on the
// shared backoff schedule (2s doubling, capped at 1m) so an outage does not turn
// into a login flood. A rejected login is terminal and returned immediately.
// Cancellation returns nil — stopping on request is not a failure.
func (a *Adapter) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, err := a.PollOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case err == nil:
			attempt = 0
			if !backoff.Sleep(ctx, interval) {
				return nil
			}
		case errors.Is(err, contracts.ErrEmailUnauthorized):
			return err
		default:
			// Transient: unreachable server, inbox hiccup, undecodable message.
			// All of them are worth retrying, slowly.
			attempt++
			if !backoff.Sleep(ctx, backoff.Delay(attempt)) {
				return nil
			}
		}
	}
}

var _ contracts.EmailService = (*Adapter)(nil)
