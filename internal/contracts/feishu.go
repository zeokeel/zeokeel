package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Feishu (Lark) open-platform adapter. Unlike WeChat the platform pushes events
// to us: the adapter is an HTTP ingress (signature-checked event callback, url
// verification handshake, card action callback) plus one outbound call. It never
// decides *what* to say and it never resolves an approval — a card click only
// becomes a (approvalId, decision) pair that the caller hands to the approval
// service.
const (
	// FeishuSource is the inbox source value every Feishu event carries.
	FeishuSource = "feishu"
	// FeishuMaxClockSkew is how far the X-Lark-Request-Timestamp header may
	// drift from our clock before the request is treated as a replay.
	FeishuMaxClockSkew = 5 * time.Minute
	// FeishuReceiveIDTypeChatID is the receive_id_type used for every outbound
	// message: the adapter answers a conversation, never an ad-hoc open id.
	FeishuReceiveIDTypeChatID = "chat_id"
	// FeishuEventMessageReceive is the only event type the adapter ingests.
	FeishuEventMessageReceive = "im.message.receive_v1"
)

// Feishu card decisions, as they appear in a card action's value payload. The
// adapter accepts only these two: a card button that means something else is a
// card we do not own.
const (
	FeishuDecisionAllow = "allow"
	FeishuDecisionDeny  = "deny"
)

// FeishuStatus is the account state the settings page shows.
type FeishuStatus struct {
	// Connected reports whether app credentials (or a tenant token) are set.
	Connected bool
	// LastInbound is the last time the platform pushed a message (zero = never).
	LastInbound time.Time
}

// FeishuService is the adapter surface.
type FeishuService interface {
	// VerifyURL answers the platform's url_verification handshake: it returns
	// the challenge string to echo when body is a challenge, and ok=false for
	// every other payload. It writes nothing to the inbox.
	VerifyURL(body []byte) (challenge string, ok bool)
	// Receive verifies the signature headers, rejects stale requests, and
	// normalizes an im.message.receive_v1 text message into the inbox. merged
	// reports a platform retry being folded into the existing item. A zero item
	// with a nil error means "not a message" (challenge, unrelated event type).
	Receive(ctx context.Context, headers map[string]string, body []byte) (item InboxItem, merged bool, err error)
	// CardAction extracts the approval decision a card button carries. ok=false
	// when the payload has no usable action.value. It never calls approvals.
	CardAction(body []byte) (approvalID, decision string, ok bool)
	// Send delivers one plain-text message to a chat.
	Send(ctx context.Context, receiveID, text string) error
	// Status reports the account state.
	Status(ctx context.Context) (FeishuStatus, error)
}

// Feishu errors are typed so the HTTP layer can answer the platform with the
// right status code instead of a generic 500.
var (
	// ErrFeishuBadSignature means the signature headers are missing or do not
	// match the body (wrong encrypt key, tampered payload, wrong scheme).
	ErrFeishuBadSignature = errors.New("feishu: bad signature")
	// ErrFeishuStale means the request timestamp is outside FeishuMaxClockSkew;
	// the platform retries with a fresh timestamp, so we refuse the replay.
	ErrFeishuStale = errors.New("feishu: stale request")
	// ErrFeishuUnauthorized means the app credentials or tenant token were
	// rejected.
	ErrFeishuUnauthorized = errors.New("feishu: unauthorized")
	// ErrFeishuUnavailable means the platform call failed (network, 5xx, or a
	// non-auth error code).
	ErrFeishuUnavailable = errors.New("feishu: unavailable")
	// ErrFeishuBadPayload means the body is not the shape we expect (unparseable
	// JSON, missing message/chat/sender identity).
	ErrFeishuBadPayload = errors.New("feishu: bad payload")
	// ErrFeishuEmptyMessage means there was nothing to send.
	ErrFeishuEmptyMessage = errors.New("feishu: empty message")
)

// KeyFeishu is provided by the feishu module.
var KeyFeishu = cordis.NewServiceKey[FeishuService]("zeokeel/feishu")

// FeishuRequestVerifier is implemented by the adapter so the HTTP layer can
// authenticate callbacks that are not messages (card buttons) without reaching
// into the adapter's internals.
type FeishuRequestVerifier interface {
	VerifyRequest(headers map[string]string, body []byte) error
}

// FeishuCardActionReader extracts the approval id, decision and one-time token a
// card button carries.
type FeishuCardActionReader interface {
	CardActionWithToken(body []byte) (approvalID, decision, token string, ok bool)
}
