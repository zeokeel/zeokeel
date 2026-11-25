package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// WeChat ClawBot (iLink) adapter. The channel is single-chat text only: the
// platform accepts plain text and replies ride the same conversation, and every
// outbound message must reuse the context token of the inbound message it
// answers. The adapter is deliberately narrow — it never decides *what* to say,
// it only delivers.
const (
	// WeChatWindow is how long after the user's last message the bot may reply.
	// Beyond it the platform drops outbound messages silently.
	WeChatWindow = 24 * time.Hour
	// WeChatChannelVersion is the base_info version iLink expects.
	WeChatChannelVersion = "1.0.2"
)

// WeChatStatus is the account state the settings page shows.
type WeChatStatus struct {
	// Connected reports whether a login token is configured.
	Connected bool
	// LastInbound is the last time the user messaged the bot (zero = never).
	LastInbound time.Time
	// WindowClosesAt is LastInbound + WeChatWindow (zero = no window yet).
	WindowClosesAt time.Time
	// BotID is the platform identity ("" before login). It changes on every QR
	// login, so nothing durable may key on it.
	BotID string
}

// WeChatService is the adapter surface.
type WeChatService interface {
	// PollOnce fetches pending messages (a short long-poll) and files them in
	// the inbox. It returns how many were ingested.
	PollOnce(ctx context.Context) (int, error)
	// Send delivers one plain-text reply to the user. It refuses when the
	// context token is missing or the 24h window is closed.
	Send(ctx context.Context, toUserID, text string) error
	// Status reports the account state.
	Status(ctx context.Context) (WeChatStatus, error)
}

// WeChat errors are typed so the caller can fall back to another channel.
var (
	// ErrWeChatWindowClosed means the user has not messaged within 24h; the
	// platform would drop the message, so we do not pretend to send it.
	ErrWeChatWindowClosed = errors.New("wechat: reply window closed")
	// ErrWeChatNoContext means there is no context token for that user (the
	// message cannot be threaded into the conversation).
	ErrWeChatNoContext = errors.New("wechat: no context token")
	// ErrWeChatUnauthorized means the login token was rejected.
	ErrWeChatUnauthorized = errors.New("wechat: unauthorized")
	// ErrWeChatUnavailable means the platform call failed.
	ErrWeChatUnavailable = errors.New("wechat: unavailable")
	// ErrWeChatEmptyMessage means there was nothing to send.
	ErrWeChatEmptyMessage = errors.New("wechat: empty message")
)

// KeyWeChat is provided by the wechat module.
var KeyWeChat = cordis.NewServiceKey[WeChatService]("zeokeel/wechat")
