package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// DispatchReceipt is what the routing plan actually did. The router decides
// where a message should go; the dispatcher is the part that tried, and this is
// its honest report — which channel took it, what was tried before, and whether
// it ended up in-app only.
type DispatchReceipt struct {
	// Channel is the channel that accepted the message ("inapp" when the plan
	// ended in the terminal channel without a send).
	Channel string
	// Attempts lists the channels tried before (or including) Channel, in order.
	Attempts []string
	// Deferred reports that quiet hours held the message back.
	Deferred bool
	// Reason carries the router's reason when the plan was shortened.
	Reason string
	// Errors are the per-channel failures that were fallen back from.
	Errors []string
}

// ChannelSender is one delivery path. Conversation is the product's configured
// destination for that channel (a DM, a chat id, a mailbox).
type ChannelSender interface {
	// Name is the channel vocabulary value (wechat, dingtalk, feishu, email).
	Name() string
	// Send delivers one plain-text message.
	Send(ctx context.Context, conversation, text string) error
}

// ChannelDispatchService is the send half of notification routing.
type ChannelDispatchService interface {
	// Deliver routes one message by category and tries the plan in order.
	Deliver(ctx context.Context, category, text string) (DispatchReceipt, error)
}

// KeyChannelDispatch is provided by the channeldispatch module.
var KeyChannelDispatch = cordis.NewServiceKey[ChannelDispatchService]("zeokeel/channeldispatch")
