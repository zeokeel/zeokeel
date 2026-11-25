package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// DingTalk channel adapter. DingTalk delivers chat-bot messages by HTTP
// callback, signed with the app secret, so the adapter is a *verified receiver*
// plus a thin outbound call: it never decides what to reply, it only files the
// inbound text in the inbox and delivers the answer back to the conversation.
const (
	// DingTalkMaxSkew is how far the callback timestamp may sit from local now.
	// Beyond it the request is a replay and is refused.
	DingTalkMaxSkew = time.Hour
)

// DingTalkStatus is the account state the settings page shows.
type DingTalkStatus struct {
	// Connected reports whether an access token is configured.
	Connected bool
	// LastInbound is the last time a verified callback arrived (zero = never).
	LastInbound time.Time
}

// DingTalkService is the adapter surface.
type DingTalkService interface {
	// Receive verifies a chat-bot callback, normalizes the text message into an
	// inbox item and records it. headers carries the raw callback headers (the
	// adapter looks the signature headers up case-insensitively). merged reports
	// that the platform retried a message already in the inbox.
	Receive(ctx context.Context, headers map[string]string, body []byte) (item InboxItem, merged bool, err error)
	// Send delivers one plain-text reply to the conversation.
	Send(ctx context.Context, chatID, text string) error
	// Status reports the account state.
	Status(ctx context.Context) (DingTalkStatus, error)
}

// DingTalk errors are typed so the HTTP layer can pick a status code and the
// caller can fall back to another channel.
var (
	// ErrDingTalkBadSignature means the callback signature did not match.
	ErrDingTalkBadSignature = errors.New("dingtalk: bad signature")
	// ErrDingTalkStaleTimestamp means the callback timestamp is outside the
	// allowed skew (a replay or a badly skewed clock).
	ErrDingTalkStaleTimestamp = errors.New("dingtalk: stale timestamp")
	// ErrDingTalkInvalid means the callback body is malformed or misses the
	// identity fields needed to file it.
	ErrDingTalkInvalid = errors.New("dingtalk: invalid callback")
	// ErrDingTalkUnsupportedMessage means the callback is valid but carries a
	// message type the adapter does not ingest (images, rich text, ...).
	ErrDingTalkUnsupportedMessage = errors.New("dingtalk: unsupported message type")
	// ErrDingTalkUnauthorized means the access token was missing or rejected.
	ErrDingTalkUnauthorized = errors.New("dingtalk: unauthorized")
	// ErrDingTalkUnavailable means the platform call failed.
	ErrDingTalkUnavailable = errors.New("dingtalk: unavailable")
	// ErrDingTalkEmptyMessage means there was nothing to send.
	ErrDingTalkEmptyMessage = errors.New("dingtalk: empty message")
)

// KeyDingTalk is provided by the dingtalk module.
var KeyDingTalk = cordis.NewServiceKey[DingTalkService]("zeokeel/dingtalk")
