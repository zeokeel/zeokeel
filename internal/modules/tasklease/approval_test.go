package tasklease_test

// TDD for the pre-start approval gate (G2). The gate is a state, not a workflow:
// a task whose policy is `before_start` still gets claimed (the owner is
// recorded, the heartbeat still keeps it alive), but the claim parks in
// `awaiting_approval` and Submit is refused until somebody approves. Nothing
// else in the lease layer learns a new transition, which is the point — the
// dangerous cases are the quiet ones: a gate that opens by default, an approval
// that is forgotten across a restart, a refused start that loses the task, and a
// policy value that is unknown in the store and must therefore fail closed.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

// claimState claims task-1 and asserts the lease reached the expected state.
func claimState(t *testing.T, svc *tasklease.Service, board *fakeBoard, taskID, owner, wantState, wantBoard string) contracts.TaskLease {
	t.Helper()
	lease, err := svc.Claim(context.Background(), taskID, owner)
	if err != nil {
		t.Fatalf("Claim(%s, %s): %v", taskID, owner, err)
	}
	if lease.State != wantState {
		t.Fatalf("Claim(%s) state = %q, want %q", taskID, lease.State, wantState)
	}
	if lease.Owner != owner {
		t.Fatalf("Claim(%s) owner = %q, want %q (a parked claim must keep its owner)", taskID, lease.Owner, owner)
	}
	if status, boardOwner := board.get(taskID); status != wantBoard || boardOwner != owner {
		t.Fatalf("board(%s) = %s/%q, want %s/%q", taskID, status, boardOwner, wantBoard, owner)
	}
	return lease
}

// TestDefaultApprovalIsNone pins the no-policy case to today's behaviour: the
// existing claim path is untouched, and a policy set while a task is already
// claimed does not retroactively gate that lease.
func TestDefaultApprovalIsNone(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	policy, set, err := svc.Approval(ctx, "task-1")
	if err != nil {
		t.Fatalf("Approval: %v", err)
	}
	if policy != contracts.TaskApprovalNone || set {
		t.Fatalf("Approval = (%q, %v), want (none, false) with no policy configured", policy, set)
	}

	// Exactly today: claimed, board in progress, submit accepted straight away.
	claimState(t, svc, board, "task-1", "bot-1", contracts.LeaseStateClaimed, contracts.TaskStatusInProgress)
	if _, err := svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "x"}}); err != nil {
		t.Fatalf("Submit without a policy: %v", err)
	}
}

func TestSetApprovalRoundTrip(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	stored, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart)
	if err != nil {
		t.Fatalf("SetApproval: %v", err)
	}
	if stored != contracts.TaskApprovalBeforeStart {
		t.Fatalf("SetApproval = %q, want %q", stored, contracts.TaskApprovalBeforeStart)
	}
	policy, set, err := svc.Approval(ctx, "task-1")
	if err != nil {
		t.Fatalf("Approval: %v", err)
	}
	if policy != contracts.TaskApprovalBeforeStart || !set {
		t.Fatalf("Approval = (%q, %v), want (before_start, true)", policy, set)
	}

	// An explicit `none` is a real answer ("configured, and it needs nothing"),
	// and it must be distinguishable from "never configured".
	if _, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalNone); err != nil {
		t.Fatalf("SetApproval(none): %v", err)
	}
	policy, set, err = svc.Approval(ctx, "task-1")
	if err != nil {
		t.Fatalf("Approval after none: %v", err)
	}
	if policy != contracts.TaskApprovalNone || !set {
		t.Fatalf("Approval after explicit none = (%q, %v), want (none, true)", policy, set)
	}
}

func TestSetApprovalValidation(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	// before_send is a real policy now (held outbound message), so the unknown
	// example has to be a value this build genuinely does not know.
	for _, value := range []string{"", "before_publish", "approve", "BEFORE_START", "none;"} {
		t.Run("policy="+value, func(t *testing.T) {
			if _, err := svc.SetApproval(ctx, "task-1", value); !errors.Is(err, contracts.ErrLeaseInvalid) {
				t.Fatalf("SetApproval(%q) err = %v, want ErrLeaseInvalid", value, err)
			}
			// A rejected request must leave no trace: the task stays ungated.
			if policy, set, err := svc.Approval(ctx, "task-1"); err != nil || set || policy != contracts.TaskApprovalNone {
				t.Fatalf("Approval after a rejected policy = (%q, %v, %v), want the default", policy, set, err)
			}
		})
	}
	if _, err := svc.SetApproval(ctx, "  ", contracts.TaskApprovalBeforeStart); !errors.Is(err, contracts.ErrLeaseInvalid) {
		t.Fatalf("SetApproval(empty task) err = %v, want ErrLeaseInvalid", err)
	}
}

// TestClaimUnderBeforeStartAwaitsApproval is the acceptance case of G2: the
// claim parks with the owner recorded, the heartbeat keeps it alive, and Submit
// is refused with the typed error instead of quietly starting the work.
func TestClaimUnderBeforeStartAwaitsApproval(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	ctx := context.Background()
	if _, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart); err != nil {
		t.Fatalf("SetApproval: %v", err)
	}

	lease := claimState(t, svc, board, "task-1", "bot-1", contracts.LeaseStateAwaitingApproval, contracts.TaskStatusInProgress)
	if !lease.Until.Equal(clock.now().Add(time.Minute)) {
		t.Fatalf("Until = %v, want the normal TTL window", lease.Until)
	}
	if lease.Heartbeat.IsZero() {
		t.Fatal("a parked claim must still carry a heartbeat")
	}

	// The board and the lease agree that the task is held, so nobody else may
	// take it while the approver is thinking.
	if _, err := svc.Claim(ctx, "task-1", "bot-2"); !errors.Is(err, contracts.ErrLeaseHeld) {
		t.Fatalf("second Claim err = %v, want ErrLeaseHeld", err)
	}

	// Heartbeat keeps a parked claim alive instead of letting it time out.
	clock.advance(45 * time.Second)
	beat, err := svc.Heartbeat(ctx, "task-1", "bot-1")
	if err != nil {
		t.Fatalf("Heartbeat on a parked claim: %v", err)
	}
	if beat.State != contracts.LeaseStateAwaitingApproval {
		t.Fatalf("heartbeat state = %q, want awaiting_approval", beat.State)
	}
	if !beat.Until.Equal(clock.now().Add(time.Minute)) {
		t.Fatalf("heartbeat Until = %v, want the extension", beat.Until)
	}

	// Starting without approval is refused, and the refusal changes nothing.
	if _, err := svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "x"}}); !errors.Is(err, contracts.ErrLeaseApprovalRequired) {
		t.Fatalf("Submit before approval err = %v, want ErrLeaseApprovalRequired", err)
	}
	held, ok, err := svc.Lease(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Lease after refused submit = (_, %v, %v), want the parked claim", ok, err)
	}
	if held.State != contracts.LeaseStateAwaitingApproval || len(held.Evidence) != 0 {
		t.Fatalf("lease after refused submit = %+v, want awaiting with no evidence", held)
	}
}

// TestApproveUnblocksSubmit is the other half of the acceptance case: approval
// moves the same claim to claimed (no re-claim, no lost owner) and the normal
// evidence → review → accept path then works.
func TestApproveUnblocksSubmit(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	ctx := context.Background()
	if _, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart); err != nil {
		t.Fatalf("SetApproval: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	clock.advance(30 * time.Second)
	approved, err := svc.Approve(ctx, "task-1", "lead")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.State != contracts.LeaseStateClaimed || approved.Owner != "bot-1" {
		t.Fatalf("approved = %+v, want claimed by bot-1", approved)
	}
	// The approval re-stamps the window: the approver's delay is not charged to
	// the worker.
	if !approved.Until.Equal(clock.now().Add(time.Minute)) {
		t.Fatalf("approved Until = %v, want %v", approved.Until, clock.now().Add(time.Minute))
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusInProgress || owner != "bot-1" {
		t.Fatalf("board after approve = %s/%q, want in_progress/bot-1", status, owner)
	}

	if _, err := svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "x"}}); err != nil {
		t.Fatalf("Submit after approval: %v", err)
	}
	if _, err := svc.Accept(ctx, "task-1", "lead", "ok"); err != nil {
		t.Fatalf("Accept after approval: %v", err)
	}
	if status, _ := board.get("task-1"); status != contracts.TaskStatusCompleted {
		t.Fatalf("board status = %q, want completed", status)
	}

	// Approving twice is a refusal, not a second transition.
	if _, err := svc.Approve(ctx, "task-1", "lead"); !errors.Is(err, contracts.ErrLeaseNotHeld) {
		t.Fatalf("second Approve err = %v, want ErrLeaseNotHeld", err)
	}
}

func TestApproveRequiresAParkedClaim(t *testing.T) {
	board := newFakeBoard("task-1", "task-2")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.Approve(ctx, "task-1", "lead"); !errors.Is(err, contracts.ErrLeaseNotHeld) {
		t.Fatalf("Approve without a lease err = %v, want ErrLeaseNotHeld", err)
	}
	if _, err := svc.Claim(ctx, "task-2", "bot-1"); err != nil {
		t.Fatalf("Claim task-2: %v", err)
	}
	if _, err := svc.Approve(ctx, "task-2", "lead"); !errors.Is(err, contracts.ErrLeaseNotHeld) {
		t.Fatalf("Approve of a claimed lease err = %v, want ErrLeaseNotHeld", err)
	}
	if _, err := svc.Approve(ctx, "task-2", ""); !errors.Is(err, contracts.ErrLeaseInvalid) {
		t.Fatalf("Approve without an approver err = %v, want ErrLeaseInvalid", err)
	}
}

// TestRejectStartReturnsTheTaskToThePool: a refused start must give the work
// back (board pending, owner cleared, note recorded) so the pool is not blocked
// by an approval nobody will give.
func TestRejectStartReturnsTheTaskToThePool(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()
	if _, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart); err != nil {
		t.Fatalf("SetApproval: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// A refusal without a note is a malformed decision and changes nothing.
	if err := svc.RejectStart(ctx, "task-1", "lead", "  "); !errors.Is(err, contracts.ErrLeaseInvalid) {
		t.Fatalf("RejectStart without a note err = %v, want ErrLeaseInvalid", err)
	}
	if lease, ok, err := svc.Lease(ctx, "task-1"); err != nil || !ok || lease.State != contracts.LeaseStateAwaitingApproval {
		t.Fatalf("lease after a rejected refusal = (%+v, %v, %v), want awaiting_approval", lease, ok, err)
	}

	if err := svc.RejectStart(ctx, "task-1", "lead", "先别动生产库"); err != nil {
		t.Fatalf("RejectStart: %v", err)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("board after reject-start = %s/%q, want pending/unassigned", status, owner)
	}
	if lease, ok, err := svc.Lease(ctx, "task-1"); err != nil || ok {
		t.Fatalf("lease after reject-start = (%+v, %v, %v), want gone", lease, ok, err)
	}
	// Refusing a start that is not parked is a typed error, not a no-op success.
	if err := svc.RejectStart(ctx, "task-1", "lead", "again"); !errors.Is(err, contracts.ErrLeaseNotHeld) {
		t.Fatalf("second RejectStart err = %v, want ErrLeaseNotHeld", err)
	}

	// The task is work again: the next claim works (and is gated again, because
	// the policy belongs to the task).
	claimState(t, svc, board, "task-1", "bot-2", contracts.LeaseStateAwaitingApproval, contracts.TaskStatusInProgress)
	if _, err := svc.Approve(ctx, "task-1", "lead"); err != nil {
		t.Fatalf("Approve after reject-start: %v", err)
	}
}

// TestStartRejectionIsPersisted pins that the refusal note survives: without a
// record the task reappears in the pool with no explanation of why it bounced.
func TestStartRejectionIsPersisted(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	svc := openLeaseStore(t, dir, board, cfg)
	if _, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart); err != nil {
		t.Fatalf("SetApproval: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := svc.RejectStart(ctx, "task-1", "lead", "先别动生产库"); err != nil {
		t.Fatalf("RejectStart: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatalf("read lease store: %v", err)
	}
	var document struct {
		Version         int `json:"version"`
		StartRejections []struct {
			TaskID   string `json:"TaskID"`
			Approver string `json:"Approver"`
			Note     string `json:"Note"`
		} `json:"startRejections"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode lease store: %v", err)
	}
	if document.Version != 1 {
		t.Fatalf("store version = %d, want 1 (additive keys never bump it)", document.Version)
	}
	if len(document.StartRejections) != 1 {
		t.Fatalf("startRejections = %+v, want the one refusal", document.StartRejections)
	}
	refusal := document.StartRejections[0]
	if refusal.TaskID != "task-1" || refusal.Approver != "lead" || !strings.Contains(refusal.Note, "生产库") {
		t.Fatalf("refusal = %+v, want task-1/lead with the note", refusal)
	}

	// The refusal is not a claim: it must not come back as a live lease.
	reopened := openLeaseStore(t, dir, board, cfg)
	if lease, ok, err := reopened.Lease(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Lease after reject-start+reopen = (%+v, %v, %v), want gone", lease, ok, err)
	}
	claimState(t, reopened, board, "task-1", "bot-2", contracts.LeaseStateAwaitingApproval, contracts.TaskStatusInProgress)
}

// TestApprovalPolicySurvivesReopen: a restart must not open the gate. The
// policy sits in the same document as the leases, so one atomic write covers it.
func TestApprovalPolicySurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1", "task-2")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	if _, err := first.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart); err != nil {
		t.Fatalf("SetApproval: %v", err)
	}
	if _, err := first.SetApproval(ctx, "task-2", contracts.TaskApprovalNone); err != nil {
		t.Fatalf("SetApproval(none): %v", err)
	}
	// The park itself is durable too: a restart during the approval wait keeps
	// the claim (and its owner), it does not silently free the task.
	if _, err := first.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	second := openLeaseStore(t, dir, board, cfg)
	policy, set, err := second.Approval(ctx, "task-1")
	if err != nil {
		t.Fatalf("Approval after reopen: %v", err)
	}
	if policy != contracts.TaskApprovalBeforeStart || !set {
		t.Fatalf("policy after reopen = (%q, %v), want (before_start, true)", policy, set)
	}
	policy, set, err = second.Approval(ctx, "task-2")
	if err != nil {
		t.Fatalf("Approval(task-2) after reopen: %v", err)
	}
	if policy != contracts.TaskApprovalNone || !set {
		t.Fatalf("explicit none after reopen = (%q, %v), want (none, true)", policy, set)
	}
	if lease, ok, err := second.Lease(ctx, "task-1"); err != nil || !ok || lease.State != contracts.LeaseStateAwaitingApproval {
		t.Fatalf("parked lease after reopen = (%+v, %v, %v), want awaiting_approval", lease, ok, err)
	}
	if _, err := second.Approve(ctx, "task-1", "lead"); err != nil {
		t.Fatalf("Approve after reopen: %v", err)
	}
}

// TestOldStoreWithoutApprovalStillLoads: a v1 document written before the gate
// existed must keep loading with the default (ungated) behaviour.
func TestOldStoreWithoutApprovalStillLoads(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()
	seed := `{"version":1,"leases":[{"TaskID":"task-1","Owner":"bot-1",` +
		`"Until":"2026-11-25T09:10:00Z","Heartbeat":"2026-11-25T09:00:00Z","State":"claimed"}]}`
	if err := writeLeaseFile(dir, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := openLeaseStore(t, dir, board, cfg)
	if policy, set, err := svc.Approval(ctx, "task-1"); err != nil || set || policy != contracts.TaskApprovalNone {
		t.Fatalf("Approval on a legacy store = (%q, %v, %v), want the default", policy, set, err)
	}
	if lease, ok, err := svc.Lease(ctx, "task-1"); err != nil || !ok || lease.State != contracts.LeaseStateClaimed {
		t.Fatalf("legacy lease = (%+v, %v, %v), want claimed", lease, ok, err)
	}
	// Gating after the legacy load writes a document that still reloads.
	if _, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart); err != nil {
		t.Fatalf("SetApproval on a legacy store: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatalf("read rewritten store: %v", err)
	}
	if !strings.Contains(string(raw), `"approvals"`) {
		t.Fatalf("rewritten store has no approvals key: %s", raw)
	}
	reopened := openLeaseStore(t, dir, board, cfg)
	if policy, set, err := reopened.Approval(ctx, "task-1"); err != nil || !set || policy != contracts.TaskApprovalBeforeStart {
		t.Fatalf("policy after legacy rewrite = (%q, %v, %v)", policy, set, err)
	}
	if lease, ok, err := reopened.Lease(ctx, "task-1"); err != nil || !ok || lease.Owner != "bot-1" {
		t.Fatalf("legacy lease after rewrite = (%+v, %v, %v), want bot-1 preserved", lease, ok, err)
	}
}

// TestUnknownPersistedPolicyFailsClosed: a store that names a policy this build
// does not know must not be read as "no approval needed" — an unknown gate is a
// gate. The rewrites are lenient everywhere else; an authorization decision is
// not the place for leniency.
func TestUnknownPersistedPolicyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()
	seed := `{"version":1,"leases":[],"approvals":[{"TaskID":"task-1","Policy":"before_publish"}]}`
	if err := writeLeaseFile(dir, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := openLeaseStore(t, dir, board, cfg)
	// Read back as the gate it is, not as the value the file happened to name.
	if policy, set, err := svc.Approval(ctx, "task-1"); err != nil || !set || policy != contracts.TaskApprovalBeforeStart {
		t.Fatalf("Approval of an unknown stored policy = (%q, %v, %v), want (before_start, true)", policy, set, err)
	}
	claimState(t, svc, board, "task-1", "bot-1", contracts.LeaseStateAwaitingApproval, contracts.TaskStatusInProgress)
}

// TestAwaitingApprovalExpiresLikeAnyClaim: a parked claim whose owner vanished
// must not pin the task in progress forever.
func TestAwaitingApprovalExpiresLikeAnyClaim(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	ctx := context.Background()
	if _, err := svc.SetApproval(ctx, "task-1", contracts.TaskApprovalBeforeStart); err != nil {
		t.Fatalf("SetApproval: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	clock.advance(2 * time.Minute)
	expired, err := svc.Expire(ctx)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if len(expired) != 1 || expired[0] != "task-1" {
		t.Fatalf("Expire = %v, want [task-1]", expired)
	}
	if status, owner := board.get("task-1"); status != contracts.TaskStatusPending || owner != "" {
		t.Fatalf("board after expiry = %s/%q, want pending/unassigned", status, owner)
	}
	// And the approver can no longer approve a claim that is gone.
	if _, err := svc.Approve(ctx, "task-1", "lead"); !errors.Is(err, contracts.ErrLeaseNotHeld) {
		t.Fatalf("Approve after expiry err = %v, want ErrLeaseNotHeld", err)
	}
}
