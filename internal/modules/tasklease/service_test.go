package tasklease_test

// TDD for the claim layer. The dangerous cases are the boring ones: a second
// claim on a live task, a heartbeat after expiry, a submit without evidence, and
// a rejected task that must become work again instead of disappearing.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

type fakeBoard struct {
	mu     sync.Mutex
	status map[string]string
	owner  map[string]string
}

func newFakeBoard(tasks ...string) *fakeBoard {
	b := &fakeBoard{status: map[string]string{}, owner: map[string]string{}}
	for _, id := range tasks {
		b.status[id] = contracts.TaskStatusPending
	}
	return b
}

func (b *fakeBoard) List(context.Context) ([]contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]contracts.BoardTask, 0, len(b.status))
	for id, status := range b.status {
		out = append(out, contracts.BoardTask{ID: id, Status: status, Owner: b.owner[id]})
	}
	return out, nil
}

func (b *fakeBoard) Create(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, errors.New("not used")
}

func (b *fakeBoard) SetStatus(_ context.Context, id, status string) (contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.status[id]; !ok {
		return contracts.BoardTask{}, errors.New("fake board: unknown task")
	}
	b.status[id] = status
	return contracts.BoardTask{ID: id, Status: status, Owner: b.owner[id]}, nil
}

func (b *fakeBoard) Delete(context.Context, string) error { return nil }

func (b *fakeBoard) Assign(_ context.Context, id, owner string) (contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.owner[id] = owner
	return contracts.BoardTask{ID: id, Status: b.status[id], Owner: owner}, nil
}

func (b *fakeBoard) get(id string) (string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status[id], b.owner[id]
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)} }
func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newService(t *testing.T, board *fakeBoard, tune func(*tasklease.Config)) (*tasklease.Service, *clock) {
	t.Helper()
	c := newClock()
	cfg := tasklease.Config{Now: c.now, TTL: time.Minute}
	if tune != nil {
		tune(&cfg)
	}
	return tasklease.NewService(board, cfg), c
}

func TestClaimSetsBoardAndLease(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	ctx := context.Background()

	lease, err := svc.Claim(ctx, "task-1", "bot-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if lease.Owner != "bot-1" || lease.State != contracts.LeaseStateClaimed {
		t.Fatalf("lease = %+v, want bot-1/claimed", lease)
	}
	if !lease.Until.Equal(clock.now().Add(time.Minute)) {
		t.Fatalf("Until = %v, want %v", lease.Until, clock.now().Add(time.Minute))
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusInProgress || owner != "bot-1" {
		t.Fatalf("board = %s/%s, want in_progress/bot-1", status, owner)
	}
}

func TestClaimRejectsLiveLease(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-2"); !errors.Is(err, contracts.ErrLeaseHeld) {
		t.Fatalf("second claim err = %v, want ErrLeaseHeld", err)
	}
}

func TestClaimAfterExpiryDropsStaleLease(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, func(c *tasklease.Config) { c.TTL = time.Minute })
	ctx := context.Background()

	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	clock.advance(2 * time.Minute)
	if _, err := svc.Claim(ctx, "task-1", "bot-2"); !errors.Is(err, contracts.ErrLeaseExpired) {
		t.Fatalf("claim after expiry err = %v, want ErrLeaseExpired", err)
	}
	if _, ok, _ := svc.Lease(ctx, "task-1"); ok {
		t.Fatal("stale lease survived the takeover attempt")
	}
	lease, err := svc.Claim(ctx, "task-1", "bot-2")
	if err != nil || lease.Owner != "bot-2" {
		t.Fatalf("retry claim = (%+v, %v), want bot-2", lease, err)
	}
}

func TestHeartbeatExtendsAndChecksOwner(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, func(c *tasklease.Config) { c.TTL = time.Minute })
	ctx := context.Background()

	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	clock.advance(30 * time.Second)
	lease, err := svc.Heartbeat(ctx, "task-1", "bot-1")
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !lease.Until.Equal(clock.now().Add(time.Minute)) {
		t.Fatalf("Until = %v, want extended", lease.Until)
	}
	if _, err := svc.Heartbeat(ctx, "task-1", "bot-2"); !errors.Is(err, contracts.ErrLeaseNotHeld) {
		t.Fatalf("wrong owner heartbeat err = %v, want ErrLeaseNotHeld", err)
	}
}

func TestSubmitRequiresEvidence(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.Submit(ctx, "task-1", "bot-1", nil); !errors.Is(err, contracts.ErrLeaseEvidenceRequired) {
		t.Fatalf("empty evidence err = %v, want ErrLeaseEvidenceRequired", err)
	}
	lease, err := svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "https://x"}})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if lease.State != contracts.LeaseStateReview {
		t.Fatalf("state = %q, want review", lease.State)
	}
	if len(lease.Evidence) != 1 {
		t.Fatalf("evidence = %v, want one", lease.Evidence)
	}
}

func TestAcceptCompletesTheBoardTask(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	_, _ = svc.Claim(ctx, "task-1", "bot-1")
	_, _ = svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "file", Ref: "a.xlsx"}})
	lease, err := svc.Accept(ctx, "task-1", "lead", "looks good")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if lease.State != contracts.LeaseStateDone || lease.ReviewNote != "looks good" {
		t.Fatalf("lease = %+v, want done with note", lease)
	}
	if status, _ := board.get("task-1"); status != contracts.TaskStatusCompleted {
		t.Fatalf("board status = %q, want completed", status)
	}
}

func TestRejectReturnsTaskToTheBoard(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	_, _ = svc.Claim(ctx, "task-1", "bot-1")
	_, _ = svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "x"}})
	lease, err := svc.Reject(ctx, "task-1", "lead", "缺来源")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if lease.State != contracts.LeaseStateRejected || lease.ReviewNote != "缺来源" {
		t.Fatalf("lease = %+v, want rejected with reason", lease)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("board = %s/%q, want pending and unassigned", status, owner)
	}
	if _, ok, _ := svc.Lease(ctx, "task-1"); ok {
		t.Fatal("rejected lease still held")
	}
}

func TestExpireReleasesOnlyDeadLeases(t *testing.T) {
	board := newFakeBoard("task-1", "task-2")
	svc, clock := newService(t, board, func(c *tasklease.Config) { c.TTL = time.Minute })
	ctx := context.Background()

	_, _ = svc.Claim(ctx, "task-1", "bot-1")
	clock.advance(40 * time.Second)
	_, _ = svc.Claim(ctx, "task-2", "bot-2")
	clock.advance(30 * time.Second) // task-1 expired, task-2 still live

	expired, err := svc.Expire(ctx)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if len(expired) != 1 || expired[0] != "task-1" {
		t.Fatalf("expired = %v, want [task-1]", expired)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("task-1 board = %s/%q, want pending/unassigned", status, owner)
	}
	if _, ok, _ := svc.Lease(ctx, "task-2"); !ok {
		t.Fatal("live lease expired too")
	}
}

func TestReleaseChecksOwner(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	_, _ = svc.Claim(ctx, "task-1", "bot-1")
	if err := svc.Release(ctx, "task-1", "bot-2"); !errors.Is(err, contracts.ErrLeaseNotHeld) {
		t.Fatalf("wrong owner release err = %v, want ErrLeaseNotHeld", err)
	}
	if err := svc.Release(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("board = %s/%q after release, want pending/unassigned", status, owner)
	}
}
