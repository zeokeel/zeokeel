package tasklease_test

// TDD for durability: a lease is the product's answer to "who is working on
// this right now", so a restart must not silently free (or worse, keep) a claim
// nobody owns any more. The dangerous cases are the boring ones: the claim that
// survives a restart unchanged, the claim that outlived its TTL while the
// process was down (the board must get the work back), and a corrupt document
// that must fail loudly instead of pretending nobody holds anything.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

func openLeaseStore(t *testing.T, dir string, board *fakeBoard, cfg tasklease.Config) *tasklease.Service {
	t.Helper()
	svc, err := tasklease.Open(dir, board, cfg)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	return svc
}

func TestLeaseSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	claimed, err := first.Claim(ctx, "task-1", "bot-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	clock.advance(30 * time.Second) // still inside the TTL

	second := openLeaseStore(t, dir, board, cfg)
	restored, ok, err := second.Lease(ctx, "task-1")
	if err != nil {
		t.Fatalf("Lease after reopen: %v", err)
	}
	if !ok {
		t.Fatal("lease did not survive the restart")
	}
	if restored.Owner != "bot-1" || restored.State != contracts.LeaseStateClaimed {
		t.Fatalf("restored = %+v, want bot-1/claimed", restored)
	}
	if !restored.Until.Equal(claimed.Until) {
		t.Fatalf("restored Until = %v, want %v", restored.Until, claimed.Until)
	}

	// The reopened service owns the same claim: a heartbeat extends it and the
	// next reopen sees the extension (the document is rewritten by mutation).
	extended, err := second.Heartbeat(ctx, "task-1", "bot-1")
	if err != nil {
		t.Fatalf("Heartbeat after reopen: %v", err)
	}
	if !extended.Until.Equal(clock.now().Add(time.Minute)) {
		t.Fatalf("extended Until = %v, want %v", extended.Until, clock.now().Add(time.Minute))
	}
	third := openLeaseStore(t, dir, board, cfg)
	again, ok, err := third.Lease(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Lease after second reopen = (%+v, %v), want the heartbeat extension", again, err)
	}
	if !again.Until.Equal(extended.Until) {
		t.Fatalf("Until after second reopen = %v, want %v", again.Until, extended.Until)
	}
}

func TestExpiredLeaseIsDroppedAndBoardReleasedOnReopen(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1", "task-2")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	if _, err := first.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusInProgress || owner != "bot-1" {
		t.Fatalf("board before restart = %s/%q, want in_progress/bot-1", status, owner)
	}

	clock.advance(2 * time.Minute) // the claim outlived its TTL while down

	second := openLeaseStore(t, dir, board, cfg)
	if _, ok, err := second.Lease(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Lease(task-1) after reopen = (_, %v, %v), want gone", ok, err)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("board after reopen = %s/%q, want pending/unassigned", status, owner)
	}
	// The dead claim is already gone, so a pass over live leases finds nothing.
	expired, err := second.Expire(ctx)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("Expire after reopen = %v, want none", expired)
	}
	// The released task is immediately claimable by another owner.
	lease, err := second.Claim(ctx, "task-1", "bot-2")
	if err != nil || lease.Owner != "bot-2" {
		t.Fatalf("reclaim = (%+v, %v), want bot-2", lease, err)
	}
}

func TestCorruptLeaseStoreIsTypedAndEmpty(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "leases.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	svc, err := tasklease.Open(dir, board, cfg)
	if !errors.Is(err, tasklease.ErrLeaseStoreCorrupt) {
		t.Fatalf("Open err = %v, want ErrLeaseStoreCorrupt", err)
	}
	if svc == nil {
		t.Fatal("Open returned no service for a corrupt store; the caller cannot keep an empty table")
	}
	if _, ok, leaseErr := svc.Lease(ctx, "task-1"); leaseErr != nil || ok {
		t.Fatalf("Lease on corrupt open = (_, %v, %v), want an empty table", ok, leaseErr)
	}
	// A claim heals the document: the next open sees exactly that lease.
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim after corrupt open: %v", err)
	}
	reopened, err := tasklease.Open(dir, board, cfg)
	if err != nil {
		t.Fatalf("Open after heal: %v", err)
	}
	if lease, ok, err := reopened.Lease(ctx, "task-1"); err != nil || !ok || lease.Owner != "bot-1" {
		t.Fatalf("Lease after heal = (%+v, %v, %v), want bot-1", lease, ok, err)
	}
}

func TestLeaseStoreIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}

	svc := openLeaseStore(t, dir, board, cfg)
	if _, err := svc.Claim(context.Background(), "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	path := filepath.Join(dir, "leases.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("lease store mode = %o, want 600", perm)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestReleaseClearsThePersistedLease(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	if _, err := first.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := first.Release(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second := openLeaseStore(t, dir, board, cfg)
	if _, ok, err := second.Lease(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Lease after release+reopen = (_, %v, %v), want gone", ok, err)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("board = %s/%q, want pending/unassigned", status, owner)
	}
}

func TestReviewStateAndAcceptPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	if _, err := first.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := first.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "https://x"}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// A restart during review must keep the evidence and the review state: the
	// reviewer is not asked to re-collect the proof.
	second := openLeaseStore(t, dir, board, cfg)
	review, ok, err := second.Lease(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Lease after submit+reopen = (_, %v, %v), want review", ok, err)
	}
	if review.State != contracts.LeaseStateReview || len(review.Evidence) != 1 || review.Evidence[0].Ref != "https://x" {
		t.Fatalf("review = %+v, want review with the submitted evidence", review)
	}

	if _, err := second.Accept(ctx, "task-1", "lead", "looks good"); err != nil {
		t.Fatalf("Accept after reopen: %v", err)
	}
	if status, _ := board.get("task-1"); status != contracts.TaskStatusCompleted {
		t.Fatalf("board status = %q, want completed", status)
	}
	// The accepted lease is gone from the document too (the deadline has not
	// passed, so a stale row would be restored here).
	third := openLeaseStore(t, dir, board, cfg)
	if lease, ok, err := third.Lease(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Lease after accept+reopen = (%+v, %v, %v), want gone", lease, ok, err)
	}
	if status, _ := board.get("task-1"); status != contracts.TaskStatusCompleted {
		t.Fatalf("board status after reopen = %q, want completed", status)
	}
}

func TestRejectClearsThePersistedLease(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	if _, err := first.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := first.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "x"}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := first.Reject(ctx, "task-1", "lead", "缺来源"); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	second := openLeaseStore(t, dir, board, cfg)
	if lease, ok, err := second.Lease(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Lease after reject+reopen = (%+v, %v, %v), want gone", lease, ok, err)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("board = %s/%q, want pending/unassigned", status, owner)
	}
}
