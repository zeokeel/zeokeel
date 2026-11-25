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

// KeyWeChatLogin is the QR login surface, provided alongside KeyWeChat so the
// settings screen can own "connect a WeChat account" without the adapter leaking
// its concrete type into the gateway.
var KeyWeChatLogin = cordis.NewServiceKey[WeChatLoginService]("zeokeel/wechat/login")

// KeyWeChat is provided by the wechat module.
var KeyWeChat = cordis.NewServiceKey[WeChatService]("zeokeel/wechat")

// WeChat QR login. The adapter cannot obtain a bot token by itself: a human
// scans a code with the WeChat app. The flow is deliberately two calls, because
// a settings page must first render the code and then poll what the human did
// with it — a blocking call would hold a request open for minutes.
//
// Note on the QR id: WeChatLogin carries it explicitly (QRCodeID) so the caller
// can poll without knowing the platform's wire shape. The adapter also keeps the
// id's own deadline, so a confirmation that arrives after the code expired is
// refused instead of logging in with a stale QR code.

// WeChatLogin is one QR-login attempt.
type WeChatLogin struct {
	// QRCodeURL is what the QR code encodes: the settings page renders it as a
	// QR image, and the human scans it with WeChat.
	QRCodeURL string
	// QRCodeID identifies the attempt for LoginState.
	QRCodeID string
	// ExpiresAt is when the code stops being valid, in the adapter's clock.
	ExpiresAt time.Time
}

// WeChatLoginState is the lifecycle of one QR-login attempt.
type WeChatLoginState string

const (
	// WeChatLoginPending means the platform has not seen the code scanned yet.
	WeChatLoginPending WeChatLoginState = "pending"
	// WeChatLoginWaiting means the human scanned the code and still has to
	// confirm on the phone.
	WeChatLoginWaiting WeChatLoginState = "waiting"
	// WeChatLoginConfirmed means the login completed and the bot token is live
	// and persisted.
	WeChatLoginConfirmed WeChatLoginState = "confirmed"
	// WeChatLoginExpired means the attempt is dead: a new code is required.
	WeChatLoginExpired WeChatLoginState = "expired"
)

// WeChatLoginService is the human-in-the-loop half of the adapter. It is a
// separate interface from WeChatService so a settings page can drive a login
// without being handed PollOnce/Send.
type WeChatLoginService interface {
	// StartLogin asks the platform for a fresh QR code.
	StartLogin(ctx context.Context) (WeChatLogin, error)
	// LoginState reports the state of the attempt with this QR id. The second
	// result is the bot token, and it is set only on WeChatLoginConfirmed (by
	// then the token is already persisted, so a restart stays logged in).
	LoginState(ctx context.Context, qrID string) (WeChatLoginState, string, error)
}

// WeChat login errors.
var (
	// ErrWeChatLoginExpired means the attempt is dead: an id this adapter never
	// issued, a code past its deadline, or a failure the platform reported as
	// expired. LoginState returns it together with WeChatLoginExpired as the
	// state, so a caller that only checks err stops polling while one that reads
	// the state can still show the outcome. It is the only error that carries a
	// meaningful state.
	ErrWeChatLoginExpired = errors.New("wechat: login expired")
	// ErrWeChatLoginUnavailable means the QR-login endpoints failed, or answered
	// something unusable (no code, no token). Retrying later is reasonable.
	ErrWeChatLoginUnavailable = errors.New("wechat: login unavailable")
)
