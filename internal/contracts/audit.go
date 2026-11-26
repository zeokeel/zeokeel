package contracts

// Audit contracts (M1): the automatic loop that turns "a task is waiting for a
// verdict" into "an auditor session was started", and "the auditor rejected it"
// into "the executor was dispatched again — or the human was told".
//
// The loop is deliberately a poller over a queue rather than a callback: the
// lease layer is durable and the process can restart between a submit and the
// verdict, so the source of truth must be "who is in the review state right
// now", not an in-memory event that a crash can lose.

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// KeyAudit is provided by the audit module.
var KeyAudit = cordis.NewServiceKey[AuditService]("zeokeel/audit")

// AuditInterval is how often the loop sweeps the review queue. Five seconds is
// the same order as the reply relay: fast enough that a person watching a task
// sees the auditor start, slow enough that an idle product does no work.
const AuditInterval = 5 * time.Second

// TaskReviewQueue is the read side the audit loop needs: every task whose lease
// is currently in the review state, oldest claim first.
//
// It is a separate interface from TaskLeaseService because the loop must not be
// able to claim, submit or decide: an auditor that could also start work would
// be the executor with extra steps.
type TaskReviewQueue interface {
	AwaitingReview(ctx context.Context) ([]TaskLease, error)
}

// AuditReport is one sweep's outcome. It is returned for logging and for the
// diagnostics surface; the counters are what an operator watches to see the
// loop working (and to see it stop when the rework ceiling is hit).
type AuditReport struct {
	// Queued is how many tasks were in the review state when the sweep ran.
	Queued int
	// AuditorsStarted is how many auditor sessions this sweep opened.
	AuditorsStarted int
	// Redispatches is how many rejected tasks were dispatched back to an
	// executor for another round.
	Redispatches int
	// Escalated is how many tasks hit the rework ceiling and became the human's.
	Escalated int
	// Errors are per-task failures; one failure never stops the sweep.
	Errors []string
}

// AuditService runs the loop. Sweep is idempotent: calling it twice in a row
// must not open a second auditor for the same lease attempt.
type AuditService interface {
	Sweep(ctx context.Context) (AuditReport, error)
}
