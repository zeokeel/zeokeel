package contracts

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// DingTalk channel adapter — **outbound only**.
//
// DingTalk is not an instruction entry: a colleague @-ing the bot in a group
// must not be able to push a message into the personal agent that holds this
// machine's data. The adapter therefore keeps exactly the sender surface — one
// plain-text send plus the account state the settings page reads — and has no
// callback, signature or inbox path at all (see
// docs/research/channel-capabilities.md §6 for the decision record).

// DingTalkStatus is the account state the settings page shows.
//
// Connected answers the only question the caller needs — "would a send work?" —
// and every send body carries BOTH the access token and the robot code
// (`{"robotCode": ..., "openConversationId": ...}`). A row holding one of them
// cannot deliver anything, so it is not connected: reporting it as connected put
// a healthy-looking channel into the notification plan and turned every real
// send into a 400 from the platform.
type DingTalkStatus struct {
	// Connected reports whether both halves of the credential are configured:
	// an access token and a robot code.
	Connected bool
	// Reason is the human-readable answer for why Connected is false ("" when
	// connected). It names the missing field so a settings page can say what to
	// fill in instead of showing a bare "not connected".
	Reason string
	// MissingFields lists the credential field names that are absent, in the
	// order the form asks for them (`token`, `robotCode`). Empty when connected.
	MissingFields []string
}

// DingTalkService is the adapter surface: a sender and its status.
type DingTalkService interface {
	// Send delivers one plain-text reply to the conversation.
	Send(ctx context.Context, chatID, text string) error
	// Status reports the account state.
	Status(ctx context.Context) (DingTalkStatus, error)
}

// DingTalk errors are typed so the caller can fall back to another channel.
var (
	// ErrDingTalkInvalid means the outbound call is missing an identity field the
	// platform requires — no conversation id to send to, or no robot code to send
	// as. Both are refused locally: the request would only produce a 400.
	ErrDingTalkInvalid = errors.New("dingtalk: invalid request")
	// ErrDingTalkUnauthorized means the access token was missing or rejected.
	ErrDingTalkUnauthorized = errors.New("dingtalk: unauthorized")
	// ErrDingTalkUnavailable means the platform call failed.
	ErrDingTalkUnavailable = errors.New("dingtalk: unavailable")
	// ErrDingTalkEmptyMessage means there was nothing to send.
	ErrDingTalkEmptyMessage = errors.New("dingtalk: empty message")
)

// KeyDingTalk is provided by the dingtalk module.
var KeyDingTalk = cordis.NewServiceKey[DingTalkService]("zeokeel/dingtalk")
