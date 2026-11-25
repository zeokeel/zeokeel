package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ReplyOutcome reports what one chat reply did to the approval queue.
type ReplyOutcome struct {
	// Applied is false when the reply was understood but nothing was waiting,
	// or when it was not understood at all (Pending stays truthful either way).
	Applied bool
	// ApprovalID is the request that was answered.
	ApprovalID string
	// Decision is the answer that was sent.
	Decision ApprovalDecision
}

// ApprovalActionService is the single semantic behind every approval entry
// point: the desktop buttons, the mobile approval page and the chat reply all
// end up answering the same pending request through a one-time token.
type ApprovalActionService interface {
	// IssueToken mints a one-time token for one pending approval. The token is
	// bound to the approval id and an expiry; it is never stored in plain text.
	IssueToken(approvalID string) (token string, expiresAt time.Time, err error)
	// Apply answers a pending approval with a decision and its token.
	Apply(ctx context.Context, approvalID string, decision ApprovalDecision, token string) error
	// ApplyReply parses one chat message and answers the matching pending
	// approval (bare verbs only while exactly one request waits).
	ApplyReply(ctx context.Context, text string) (ReplyOutcome, error)
}

// Approval action errors are typed so the channel layer can say why the button
// or the reply did not work.
var (
	// ErrApprovalTokenInvalid means the token did not verify.
	ErrApprovalTokenInvalid = errors.New("approval: invalid token")
	// ErrApprovalTokenExpired means the token outlived its window.
	ErrApprovalTokenExpired = errors.New("approval: token expired")
	// ErrApprovalTokenUsed means this token already answered a request.
	ErrApprovalTokenUsed = errors.New("approval: token already used")
	// ErrApprovalNotPending means the request is unknown or already answered.
	ErrApprovalNotPending = errors.New("approval: request not pending")
	// ErrApprovalNoPending means a reply arrived while nothing was waiting.
	ErrApprovalNoPending = errors.New("approval: nothing pending")
)

// ApprovalTokenTTL is how long a minted token (and the chat short code that
// rides with it) stays valid.
const ApprovalTokenTTL = 30 * time.Minute

// KeyApprovalAction is provided by the approvals module next to KeyApprovals.
var KeyApprovalAction = cordis.NewServiceKey[ApprovalActionService]("zeokeel/approvalaction")
