package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ReplyRelayReport is what one relay pass did: how many new channel messages it
// looked at, and which of them answered an approval.
type ReplyRelayReport struct {
	Scanned int
	Applied int
	// ApprovalIDs are the requests the replies answered (for the log line the
	// Lead writes, not for a second state machine).
	ApprovalIDs []string
	// Errors names replies that could not be parsed far enough to decide.
	Errors []string
}

// ReplyRelayService is the bridge that makes "reply 同意 in the chat" work: it
// reads the inbox items channels produced, asks the approval action service to
// interpret each one as a reply, and archives the item once it answered a
// request. It never creates work and never sends anything back to the channel —
// the adapter already owns delivery.
type ReplyRelayService interface {
	// Run performs one pass immediately.
	Relay(ctx context.Context) (ReplyRelayReport, error)
}

// KeyReplyRelay is provided by the relay module.
var KeyReplyRelay = cordis.NewServiceKey[ReplyRelayService]("zeokeel/replyrelay")

// ReplyRelayInterval is how often the relay looks at the inbox.
const ReplyRelayInterval = 5 * time.Second
