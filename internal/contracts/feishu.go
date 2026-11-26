package contracts

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Feishu (Lark) open-platform adapter — **outbound only**.
//
// Feishu is not an instruction entry: an event subscription callback is a public
// HTTP endpoint and anybody in the tenant who can post to a chat we are in could
// otherwise drive the personal agent that holds this machine's data. The adapter
// keeps the sender surface only — one plain-text message plus the account state
// the settings page reads. No event callback, no url_verification handshake, no
// card action: a card button must never resolve an approval (see
// docs/research/channel-capabilities.md §6 for the decision record).
const (
	// FeishuReceiveIDTypeChatID is the receive_id_type used for every outbound
	// message: the adapter answers a conversation, never an ad-hoc open id.
	FeishuReceiveIDTypeChatID = "chat_id"
)

// FeishuStatus is the account state the settings page shows.
type FeishuStatus struct {
	// Connected reports whether app credentials (or a tenant token) are set.
	Connected bool
}

// FeishuService is the adapter surface: a sender and its status.
type FeishuService interface {
	// Send delivers one plain-text message to a chat.
	Send(ctx context.Context, receiveID, text string) error
	// Status reports the account state.
	Status(ctx context.Context) (FeishuStatus, error)
}

// Feishu errors are typed so the caller can fall back to another channel.
var (
	// ErrFeishuUnauthorized means the app credentials or tenant token were
	// rejected.
	ErrFeishuUnauthorized = errors.New("feishu: unauthorized")
	// ErrFeishuUnavailable means the platform call failed (network, 5xx, or a
	// non-auth error code).
	ErrFeishuUnavailable = errors.New("feishu: unavailable")
	// ErrFeishuBadPayload means the outbound request is malformed (no receive
	// id) or the platform answered with a shape we cannot read.
	ErrFeishuBadPayload = errors.New("feishu: bad payload")
	// ErrFeishuEmptyMessage means there was nothing to send.
	ErrFeishuEmptyMessage = errors.New("feishu: empty message")
)

// KeyFeishu is provided by the feishu module.
var KeyFeishu = cordis.NewServiceKey[FeishuService]("zeokeel/feishu")
