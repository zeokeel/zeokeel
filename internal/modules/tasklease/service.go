// Package tasklease adds the time-bounded claim, heartbeat, evidence and review
// on top of the shared task board. The board owns "is this task open/done"; the
// lease owns "who holds it right now, until when, and with what proof".
//
// Every transition writes both, so the board and the lease can never disagree:
// a claim moves the board to in_progress, a release/reject/expiry moves it back
// to pending, and an accept completes it.
package tasklease

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Config is the module's typed assembly config.
type Config struct {
	// Now overrides the clock (tests); nil means time.Now.
	Now func() time.Time `json:"-"`
	// TTL is the claim lifetime; heartbeats extend it.
	TTL time.Duration `json:"ttl"`
	// OnDirectClose is an optional observability hook, called after an unaudited
	// close was recorded (the module logs it). Never used for policy: the row is
	// already written, and a nil hook must not change behaviour.
	OnDirectClose func(taskID, actor, source string) `json:"-"`
	// MaxRework is the automatic rework ceiling (M1): how many rejections a task
	// may absorb before it stops being dispatched and becomes the human's.
	// Non-positive keeps contracts.DefaultMaxRework, so an assembly that does not
	// mention it still cannot loop forever.
	MaxRework int `json:"maxRework"`
}

func (c Config) normalized() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.TTL <= 0 {
		c.TTL = contracts.DefaultLeaseTTL
	}
	if c.MaxRework <= 0 {
		c.MaxRework = contracts.DefaultMaxRework
	}
	return c
}

// Service implements contracts.TaskLeaseService over a task board.
type Service struct {
	mu    sync.Mutex
	cfg   Config
	board contracts.TaskBoard
	// leases keeps the live claims by task id. Accepted/rejected leases are
	// dropped from the map (the board keeps the outcome); evidence and notes are
	// returned to the caller once.
	leases map[string]*contracts.TaskLease
	// tools keeps the per-task tool bindings (the M5 execution policy). They are
	// independent of the leases map: releasing or accepting a lease does not
	// drop the task's policy.
	tools map[string]*contracts.TaskTools
	// approvals keeps the per-task pre-start approval policy (G2). Like tools it
	// belongs to the task rather than to a lease: refusing or losing a claim
	// does not change what the work requires.
	approvals map[string]*contracts.TaskApproval
	// startRejections keeps the last refused start per task (G2): the note is
	// what explains why a task came back to the pool.
	startRejections map[string]*contracts.TaskStartRejection
	// budgets keeps the per-task cost ceiling (G11). Like tools and approvals it
	// belongs to the task rather than to a lease — a release/re-claim must not
	// reset the meter — and it is the row the stop reads.
	budgets map[string]*contracts.TaskBudget
	// heldSends parks the message of a before_send task until a human approves
	// it. It lives beside the other task-scoped rows so a restart cannot lose a
	// message nobody decided about yet.
	heldSends map[string]string
	// requesters is who asked for each task (M6 attribution): the board records
	// the work and the lease the executor, but neither says which human wanted it.
	requesters map[string]string
	// reviews is the recorded review decision of each task (accept/reject). It
	// is not part of a lease: the lease is dropped by the very decision it
	// describes, so the explanation has to live somewhere else or it is lost the
	// moment it is written (contracts.TaskReview).
	reviews map[string][]contracts.TaskReview
	// rework counts the automatic rework rounds of each task (M1). Task-scoped
	// like tools/approvals/budgets: a re-claim must not reset the budget of
	// automatic attempts, and the count is what stops the audit loop from
	// spending forever.
	rework map[string]int
	// path is the durable document (dir/leases.json); empty means in-memory only
	// (NewService, and unit tests that do not care about durability).
	path string
}

// NewService wires the lease layer.
func NewService(board contracts.TaskBoard, cfg Config) *Service {
	return &Service{
		cfg: cfg.normalized(), board: board,
		leases:          make(map[string]*contracts.TaskLease),
		tools:           make(map[string]*contracts.TaskTools),
		approvals:       make(map[string]*contracts.TaskApproval),
		startRejections: make(map[string]*contracts.TaskStartRejection),
		budgets:         make(map[string]*contracts.TaskBudget),
		heldSends:       make(map[string]string),
		requesters:      make(map[string]string),
		reviews:         make(map[string][]contracts.TaskReview),
		rework:          make(map[string]int),
	}
}

func cloneLease(lease *contracts.TaskLease) contracts.TaskLease {
	out := *lease
	out.Evidence = append([]contracts.Evidence(nil), lease.Evidence...)
	return out
}

// Claim implements contracts.TaskLeaseService.
func (s *Service) Claim(ctx context.Context, taskID, owner string) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	if taskID == "" || owner == "" {
		return contracts.TaskLease{}, contracts.ErrLeaseInvalid
	}

	s.mu.Lock()
	// The automatic loop's ceiling (M1): a task that exhausted its rework rounds
	// is not dispatched again by anyone until a human resets it. This is the one
	// place that can stop a paying loop, so it is checked before the stale-claim
	// branch: a ceiling that only applied to fresh tasks would be bypassed by
	// every expiry.
	if s.reworkExhaustedLocked(taskID) {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseReworkExhausted
	}
	now := s.cfg.Now()
	if existing, ok := s.leases[taskID]; ok {
		if now.Before(existing.Until) {
			s.mu.Unlock()
			return contracts.TaskLease{}, contracts.ErrLeaseHeld
		}
		// The previous owner is gone: drop the stale claim and report it so the
		// caller can decide to retry (never silently steal).
		delete(s.leases, taskID)
		persistErr := s.persistLocked()
		s.mu.Unlock()
		s.releaseBoard(ctx, taskID)
		if persistErr != nil {
			return contracts.TaskLease{}, persistErr
		}
		return contracts.TaskLease{}, contracts.ErrLeaseExpired
	}
	s.mu.Unlock()

	// Only write what changed: the board rejects a no-op update ("no changes"),
	// and triage may already have put the task in progress with this owner.
	current, err := s.findTask(ctx, taskID)
	if err != nil {
		return contracts.TaskLease{}, err
	}
	if current.Status != contracts.TaskStatusInProgress {
		if _, err := s.board.SetStatus(ctx, taskID, contracts.TaskStatusInProgress); err != nil {
			return contracts.TaskLease{}, fmt.Errorf("tasklease: claim board: %w", err)
		}
	}
	if assigner, ok := s.board.(contracts.TaskAssigner); ok && current.Owner != owner {
		if _, err := assigner.Assign(ctx, taskID, owner); err != nil {
			return contracts.TaskLease{}, fmt.Errorf("tasklease: assign: %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	lease := &contracts.TaskLease{
		TaskID: taskID, Owner: owner, Until: now.Add(s.cfg.TTL),
		Heartbeat: now, State: s.stateForClaimLocked(taskID),
	}
	s.leases[taskID] = lease
	if err := s.persistLocked(); err != nil {
		return contracts.TaskLease{}, err
	}
	return cloneLease(lease), nil
}

// Heartbeat implements contracts.TaskLeaseService.
func (s *Service) Heartbeat(ctx context.Context, taskID, owner string) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	s.mu.Lock()
	lease, err := s.liveLeaseLocked(taskID, owner)
	if err != nil {
		s.mu.Unlock()
		return contracts.TaskLease{}, err
	}
	now := s.cfg.Now()
	lease.Heartbeat = now
	lease.Until = now.Add(s.cfg.TTL)
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return contracts.TaskLease{}, err
	}
	s.mu.Unlock()
	return cloneLease(lease), nil
}

// Release implements contracts.TaskLeaseService.
func (s *Service) Release(ctx context.Context, taskID, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	lease, err := s.liveLeaseLocked(taskID, owner)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	lease.State = contracts.LeaseStateRejected
	delete(s.leases, taskID)
	persistErr := s.persistLocked()
	s.mu.Unlock()
	// The board goes back to pending even when the document could not be
	// rewritten: a stale file row is pruned on the next load, hidden work is not.
	s.releaseBoard(ctx, taskID)
	return persistErr
}

// Submit implements contracts.TaskLeaseService.
func (s *Service) Submit(ctx context.Context, taskID, owner string, evidence []contracts.Evidence) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	if len(evidence) == 0 {
		return contracts.TaskLease{}, contracts.ErrLeaseEvidenceRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Submitting past the ceiling would let the owner turn an escalated task into
	// "review" — i.e. back into the automatic loop — without the human decision
	// ResetRework exists to record.
	if s.reworkExhaustedLocked(taskID) {
		return contracts.TaskLease{}, contracts.ErrLeaseReworkExhausted
	}
	lease, err := s.liveLeaseLocked(taskID, owner)
	if err != nil {
		return contracts.TaskLease{}, err
	}
	// The gate (G2): a parked claim has not been approved to start, so evidence
	// must not be able to turn it into review. Approval is a separate decision,
	// not something the owner can assume by submitting.
	if lease.State == contracts.LeaseStateAwaitingApproval {
		return contracts.TaskLease{}, fmt.Errorf("%w: %s", contracts.ErrLeaseApprovalRequired, taskID)
	}
	// The stop (G11): a claim parked by its own budget is waiting for the Lead to
	// decide. Letting Submit through would make the ceiling advisory — the work
	// would keep being spent until the owner felt like stopping.
	if lease.State == contracts.LeaseStateBlocked {
		return contracts.TaskLease{}, fmt.Errorf("%w: %s is blocked", contracts.ErrLeaseBudgetExhausted, taskID)
	}
	lease.Evidence = append([]contracts.Evidence(nil), evidence...)
	lease.State = contracts.LeaseStateReview
	lease.Heartbeat = s.cfg.Now()
	if err := s.persistLocked(); err != nil {
		return contracts.TaskLease{}, err
	}
	return cloneLease(lease), nil
}

// Accept implements contracts.TaskLeaseService.
func (s *Service) Accept(ctx context.Context, taskID, reviewer, note string) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	if strings.TrimSpace(reviewer) == "" {
		return contracts.TaskLease{}, contracts.ErrLeaseInvalid
	}
	s.mu.Lock()
	lease, ok := s.leases[taskID]
	if !ok || lease.State != contracts.LeaseStateReview {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	// Auditing your own work is not an audit. The rule is role-shaped, not
	// person-shaped (single-user product): see contracts.ErrLeaseReviewerIsOwner.
	if sameIdentity(reviewer, lease.Owner) {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseReviewerIsOwner
	}
	// The audit role rule (M1): once the identity is known to differ, the role
	// still may not be the executor's. Hiding the review tool from the executor's
	// allowlist is the first line of defence; this is the second, and it is the
	// one an audit trail is checked against.
	if reviewerRoleIsExecutor(reviewer) {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseReviewerRoleMismatch
	}
	s.recordReviewLocked(taskID, reviewer, contracts.TaskReviewAccept, note)
	// A finished task carries no rework debt into its next life (M1).
	s.clearReworkLocked(taskID)
	lease.State = contracts.LeaseStateDone
	lease.ReviewNote = note
	done := cloneLease(lease)
	delete(s.leases, taskID)
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return contracts.TaskLease{}, err
	}
	s.mu.Unlock()

	if _, err := s.board.SetStatus(ctx, taskID, contracts.TaskStatusCompleted); err != nil {
		return contracts.TaskLease{}, fmt.Errorf("tasklease: accept board: %w", err)
	}
	return done, nil
}

// Reject implements contracts.TaskLeaseService.
func (s *Service) Reject(ctx context.Context, taskID, reviewer, reason string) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	if strings.TrimSpace(reviewer) == "" || reason == "" {
		return contracts.TaskLease{}, contracts.ErrLeaseInvalid
	}
	s.mu.Lock()
	lease, ok := s.leases[taskID]
	if !ok || lease.State != contracts.LeaseStateReview {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	if sameIdentity(reviewer, lease.Owner) {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseReviewerIsOwner
	}
	// The audit role rule (M1): once the identity is known to differ, the role
	// still may not be the executor's. Hiding the review tool from the executor's
	// allowlist is the first line of defence; this is the second, and it is the
	// one an audit trail is checked against.
	if reviewerRoleIsExecutor(reviewer) {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseReviewerRoleMismatch
	}
	s.recordReviewLocked(taskID, reviewer, contracts.TaskReviewReject, reason)
	// One rejection = one automatic rework round (M1). The bump lands in the same
	// atomic write as the review row, so a crash cannot lose the count that stops
	// the loop while keeping the rejection that caused it.
	s.bumpReworkLocked(taskID)
	lease.State = contracts.LeaseStateRejected
	lease.ReviewNote = reason
	rejected := cloneLease(lease)
	delete(s.leases, taskID)
	persistErr := s.persistLocked()
	s.mu.Unlock()

	s.releaseBoard(ctx, taskID)
	if persistErr != nil {
		return contracts.TaskLease{}, persistErr
	}
	return rejected, nil
}

// Lease implements contracts.TaskLeaseService.
func (s *Service) Lease(ctx context.Context, taskID string) (contracts.TaskLease, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[taskID]
	if !ok {
		return contracts.TaskLease{}, false, nil
	}
	return cloneLease(lease), true, nil
}

// Expire implements contracts.TaskLeaseService.
func (s *Service) Expire(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := s.cfg.Now()

	s.mu.Lock()
	var expired []string
	for id, lease := range s.leases {
		if now.Before(lease.Until) {
			continue
		}
		expired = append(expired, id)
		delete(s.leases, id)
	}
	s.mu.Unlock()

	for _, id := range expired {
		s.releaseBoard(ctx, id)
	}
	if len(expired) == 0 {
		return expired, nil
	}
	// The board is already released; now drop the rows from the document. A
	// failed write leaves stale rows that the next load prunes — never hidden
	// work.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.persistLocked(); err != nil {
		return expired, err
	}
	return expired, nil
}

// findTask reads one task from the board (the board API has no Get).
func (s *Service) findTask(ctx context.Context, taskID string) (contracts.BoardTask, error) {
	tasks, err := s.board.List(ctx)
	if err != nil {
		return contracts.BoardTask{}, fmt.Errorf("tasklease: list board: %w", err)
	}
	for _, task := range tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return contracts.BoardTask{}, fmt.Errorf("tasklease: task %q not found", taskID)
}

// liveLeaseLocked returns the lease the caller still owns, or a typed error.
func (s *Service) liveLeaseLocked(taskID, owner string) (*contracts.TaskLease, error) {
	lease, ok := s.leases[taskID]
	if !ok || lease.Owner != owner {
		return nil, contracts.ErrLeaseNotHeld
	}
	if !s.cfg.Now().Before(lease.Until) {
		delete(s.leases, taskID)
		// Best effort: the row is stale either way, and a load prunes it.
		_ = s.persistLocked()
		return nil, contracts.ErrLeaseNotHeld
	}
	return lease, nil
}

// releaseBoard puts the task back to pending and unassigns it, ignoring the
// board's absence of an owner API.
func (s *Service) releaseBoard(ctx context.Context, taskID string) {
	if s.board == nil {
		return // a service opened only for its persisted table (no board wired)
	}
	current, err := s.findTask(ctx, taskID)
	if err != nil {
		return
	}
	if current.Status != contracts.TaskStatusPending {
		if _, err := s.board.SetStatus(ctx, taskID, contracts.TaskStatusPending); err != nil {
			return
		}
	}
	if assigner, ok := s.board.(contracts.TaskAssigner); ok && current.Owner != "" {
		_, _ = assigner.Assign(ctx, taskID, "")
	}
}

var _ contracts.TaskLeaseService = (*Service)(nil)
