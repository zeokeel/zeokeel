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
}

func (c Config) normalized() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.TTL <= 0 {
		c.TTL = contracts.DefaultLeaseTTL
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
}

// NewService wires the lease layer.
func NewService(board contracts.TaskBoard, cfg Config) *Service {
	return &Service{cfg: cfg.normalized(), board: board, leases: make(map[string]*contracts.TaskLease)}
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
	now := s.cfg.Now()
	if existing, ok := s.leases[taskID]; ok {
		if now.Before(existing.Until) {
			s.mu.Unlock()
			return contracts.TaskLease{}, contracts.ErrLeaseHeld
		}
		// The previous owner is gone: drop the stale claim and report it so the
		// caller can decide to retry (never silently steal).
		delete(s.leases, taskID)
		s.mu.Unlock()
		s.releaseBoard(ctx, taskID)
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
		Heartbeat: now, State: contracts.LeaseStateClaimed,
	}
	s.leases[taskID] = lease
	return cloneLease(lease), nil
}

// Heartbeat implements contracts.TaskLeaseService.
func (s *Service) Heartbeat(ctx context.Context, taskID, owner string) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, err := s.liveLeaseLocked(taskID, owner)
	if err != nil {
		return contracts.TaskLease{}, err
	}
	now := s.cfg.Now()
	lease.Heartbeat = now
	lease.Until = now.Add(s.cfg.TTL)
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
	s.mu.Unlock()
	s.releaseBoard(ctx, taskID)
	return nil
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
	lease, err := s.liveLeaseLocked(taskID, owner)
	if err != nil {
		return contracts.TaskLease{}, err
	}
	lease.Evidence = append([]contracts.Evidence(nil), evidence...)
	lease.State = contracts.LeaseStateReview
	lease.Heartbeat = s.cfg.Now()
	return cloneLease(lease), nil
}

// Accept implements contracts.TaskLeaseService.
func (s *Service) Accept(ctx context.Context, taskID, reviewer, note string) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	if reviewer == "" {
		return contracts.TaskLease{}, contracts.ErrLeaseInvalid
	}
	s.mu.Lock()
	lease, ok := s.leases[taskID]
	if !ok || lease.State != contracts.LeaseStateReview {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	lease.State = contracts.LeaseStateDone
	lease.ReviewNote = note
	done := cloneLease(lease)
	delete(s.leases, taskID)
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
	if reviewer == "" || reason == "" {
		return contracts.TaskLease{}, contracts.ErrLeaseInvalid
	}
	s.mu.Lock()
	lease, ok := s.leases[taskID]
	if !ok || lease.State != contracts.LeaseStateReview {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	lease.State = contracts.LeaseStateRejected
	lease.ReviewNote = reason
	rejected := cloneLease(lease)
	delete(s.leases, taskID)
	s.mu.Unlock()

	s.releaseBoard(ctx, taskID)
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
		return nil, contracts.ErrLeaseNotHeld
	}
	return lease, nil
}

// releaseBoard puts the task back to pending and unassigns it, ignoring the
// board's absence of an owner API.
func (s *Service) releaseBoard(ctx context.Context, taskID string) {
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
