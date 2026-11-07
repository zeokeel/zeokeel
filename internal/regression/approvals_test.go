package regression

// Adversarial audit: the approvals backend module. Negative/fault cases:
// resolving unknown or already-answered IDs, invalid decisions, cancellation
// races, panicking subscribers, mode never, and concurrent request/resolve
// interleavings. The invariant under test: bad input never corrupts the queue,
// never changes an answer and never deadlocks.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
)

// startApprovals boots the real approvals module in mode and returns its
// service.
func startApprovals(t *testing.T, mode string) *harness {
	t.Helper()
	h := newHarness(t)
	activate(t, h.ctx, approvals.Module(), approvals.Config{Mode: mode})
	return h
}

// approvalResult is one Request outcome.
type approvalResult struct {
	decision contracts.ApprovalDecision
	err      error
}

// requestAsync issues one blocking Request on a goroutine.
func requestAsync(ctx context.Context, service contracts.Approvals, request contracts.ApprovalRequest) <-chan approvalResult {
	results := make(chan approvalResult, 1)
	go func() {
		decision, err := service.Request(ctx, request)
		results <- approvalResult{decision: decision, err: err}
	}()
	return results
}

// awaitApproval receives one Request result with a short timeout.
func awaitApproval(t *testing.T, results <-chan approvalResult) approvalResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return within 2s (deadlock)")
		return approvalResult{}
	}
}

// TestApprovalsBadInputLeavesQueueUntouched covers the negative resolve paths:
// an unknown ID, an invalid decision and an empty ID must not remove, resolve or
// reorder a pending request.
func TestApprovalsBadInputLeavesQueueUntouched(t *testing.T) {
	h := startApprovals(t, approvals.ModeAsk)
	service := requireService(t, h.ctx, contracts.KeyApprovals)
	ctx := context.Background()

	results := requestAsync(ctx, service, contracts.ApprovalRequest{Tool: "bash", Summary: "audit"})
	waitFor(t, "pending request", func() bool { return len(service.Pending()) == 1 })
	pending := service.Pending()[0]
	if pending.ID == "" {
		t.Fatal("request ID was not generated")
	}

	// Resolve an ID that was never created.
	if err := service.Resolve("appr-does-not-exist", contracts.ApprovalAllow); !errors.Is(err, approvals.ErrUnknownRequest) {
		t.Fatalf("Resolve(unknown) error = %v, want ErrUnknownRequest", err)
	}
	// Resolve the empty ID (a common UI bug: unset click target).
	if err := service.Resolve("", contracts.ApprovalAllow); !errors.Is(err, approvals.ErrUnknownRequest) {
		t.Fatalf("Resolve(empty) error = %v, want ErrUnknownRequest", err)
	}
	// Invalid decisions are rejected before touching the queue.
	for _, decision := range []contracts.ApprovalDecision{"", "yes", "ALLOW"} {
		if err := service.Resolve(pending.ID, decision); !errors.Is(err, approvals.ErrInvalidDecision) {
			t.Fatalf("Resolve(%q) error = %v, want ErrInvalidDecision", decision, err)
		}
	}
	if got := service.Pending(); len(got) != 1 || got[0].ID != pending.ID {
		t.Fatalf("Pending() = %+v after bad resolves, want the original request untouched", got)
	}
	select {
	case result := <-results:
		t.Fatalf("the waiting Request returned %+v after only bad resolves, want it still blocked", result)
	default:
	}

	// A valid answer with surrounding whitespace still resolves the request.
	if err := service.Resolve("  "+pending.ID+"  ", contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve(trimmed pending ID): %v", err)
	}
	if result := awaitApproval(t, results); result.err != nil || result.decision != contracts.ApprovalAllow {
		t.Fatalf("Request = %+v, want allow", result)
	}
	if got := service.Pending(); len(got) != 0 {
		t.Fatalf("Pending() = %+v after the answer, want empty", got)
	}
	// Unknown IDs are not remembered: resolving again is still an error.
	if err := service.Resolve("appr-does-not-exist", contracts.ApprovalAllow); !errors.Is(err, approvals.ErrUnknownRequest) {
		t.Fatalf("Resolve(unknown) after resolution error = %v, want ErrUnknownRequest", err)
	}
}

// TestApprovalsDuplicateResolveKeepsFirstAnswer covers idempotent resolve after
// the first answer, and the cancelled-request case where the ID becomes unknown.
func TestApprovalsDuplicateResolveKeepsFirstAnswer(t *testing.T) {
	h := startApprovals(t, approvals.ModeAsk)
	service := requireService(t, h.ctx, contracts.KeyApprovals)
	ctx := context.Background()

	results := requestAsync(ctx, service, contracts.ApprovalRequest{Tool: "bash"})
	waitFor(t, "pending request", func() bool { return len(service.Pending()) == 1 })
	id := service.Pending()[0].ID
	if err := service.Resolve(id, contracts.ApprovalAllow); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	// A second answer is accepted (nil) and cannot change the delivered answer.
	if err := service.Resolve(id, contracts.ApprovalDeny); err != nil {
		t.Fatalf("duplicate Resolve returned %v, want nil (idempotent)", err)
	}
	if result := awaitApproval(t, results); result.decision != contracts.ApprovalAllow {
		t.Fatalf("Request decision = %q after duplicate Resolve, want the first answer allow", result.decision)
	}

	// A cancelled Request drops its pending entry; resolving its ID later is
	// unknown (not remembered) and must not panic.
	cancelCtx, cancel := context.WithCancel(context.Background())
	results = requestAsync(cancelCtx, service, contracts.ApprovalRequest{Tool: "write_file"})
	waitFor(t, "second pending request", func() bool { return len(service.Pending()) == 1 })
	cancelledID := service.Pending()[0].ID
	cancel()
	if result := awaitApproval(t, results); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancelled Request error = %v, want context.Canceled", result.err)
	}
	waitFor(t, "pending queue drained", func() bool { return len(service.Pending()) == 0 })
	if err := service.Resolve(cancelledID, contracts.ApprovalAllow); !errors.Is(err, approvals.ErrUnknownRequest) {
		t.Fatalf("Resolve(cancelled ID) error = %v, want ErrUnknownRequest", err)
	}
}

// TestApprovalsAlreadyCanceledContext covers a caller that passes a context
// which is already cancelled: Request must not leak a pending entry.
func TestApprovalsAlreadyCanceledContext(t *testing.T) {
	h := startApprovals(t, approvals.ModeAsk)
	service := requireService(t, h.ctx, contracts.KeyApprovals)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := requestAsync(ctx, service, contracts.ApprovalRequest{Tool: "bash"})
	if result := awaitApproval(t, results); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Request with a cancelled ctx error = %v, want context.Canceled", result.err)
	}
	if got := service.Pending(); len(got) != 0 {
		t.Fatalf("Pending() = %+v after a cancelled request, want empty", got)
	}
}

// TestApprovalsSubscriberPanicIsContained proves a panicking subscriber cannot
// break the queue handshake: the panic is recovered and the remaining
// subscribers still run.
func TestApprovalsSubscriberPanicIsContained(t *testing.T) {
	h := startApprovals(t, approvals.ModeAsk)
	service := requireService(t, h.ctx, contracts.KeyApprovals)

	var calls counter
	if _, err := service.Subscribe(func() { panic("subscriber boom") }); err != nil {
		t.Fatalf("Subscribe(panicking): %v", err)
	}
	if _, err := service.Subscribe(func() { calls.inc() }); err != nil {
		t.Fatalf("Subscribe(counting): %v", err)
	}

	results := requestAsync(context.Background(), service, contracts.ApprovalRequest{Tool: "bash"})
	waitFor(t, "pending request", func() bool { return len(service.Pending()) == 1 })
	before := calls.load()
	if err := service.Resolve(service.Pending()[0].ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve after panicking subscriber: %v", err)
	}
	if result := awaitApproval(t, results); result.err != nil || result.decision != contracts.ApprovalAllow {
		t.Fatalf("Request = %+v, want allow", result)
	}
	if calls.load() <= before {
		t.Fatalf("the good subscriber did not run after the panicking one (calls %d -> %d)", before, calls.load())
	}
}

// TestApprovalsModeNeverAndAuto covers the non-interactive policies: never
// denies without queueing, auto allows without queueing, neither leaves state.
func TestApprovalsModeNeverAndAuto(t *testing.T) {
	for _, testCase := range []struct {
		mode string
		want contracts.ApprovalDecision
	}{
		{approvals.ModeNever, contracts.ApprovalDeny},
		{approvals.ModeAuto, contracts.ApprovalAllow},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			h := startApprovals(t, testCase.mode)
			service := requireService(t, h.ctx, contracts.KeyApprovals)
			if got := service.Mode(); got != testCase.mode {
				t.Fatalf("Mode() = %q, want %q", got, testCase.mode)
			}
			decision, err := service.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"})
			if err != nil || decision != testCase.want {
				t.Fatalf("Request = (%q, %v), want %q", decision, err, testCase.want)
			}
			if got := service.Pending(); len(got) != 0 {
				t.Fatalf("Pending() = %+v in %s mode, want empty", got, testCase.mode)
			}
			if err := service.Resolve("appr-1", contracts.ApprovalAllow); !errors.Is(err, approvals.ErrUnknownRequest) {
				t.Fatalf("Resolve in %s mode error = %v, want ErrUnknownRequest", testCase.mode, err)
			}
		})
	}
}

// TestApprovalsInvalidModeIsRejected covers configuration input: an unknown
// policy must fail module activation instead of silently defaulting.
func TestApprovalsInvalidModeIsRejected(t *testing.T) {
	h := newHarness(t)
	plugin := &descriptorPlugin[approvals.Config]{
		descriptor: approvals.Module(),
		config:     approvals.Config{Mode: "yolo"},
	}
	inject := make([]string, 0)
	for _, requirement := range approvals.Module().Requires {
		if !requirement.Optional {
			inject = append(inject, requirement.Name)
		}
	}
	plugin.inject = inject
	fiber, err := h.ctx.Plugin(plugin, nil)
	if err == nil && fiber != nil && fiber.State() == cordis.Active {
		t.Fatal("the approvals module activated with an invalid mode, want rejection")
	}
	if err == nil && fiber != nil && fiber.ErrorDetail() == nil {
		t.Fatal("activation failed without a diagnostic error")
	}
}

// TestApprovalsConcurrentResolveAndRequest hammers Request/Resolve from several
// goroutines: every request must get exactly one answer, duplicates must be
// harmless and the queue must drain without deadlock (-race).
func TestApprovalsConcurrentResolveAndRequest(t *testing.T) {
	h := startApprovals(t, approvals.ModeAsk)
	service := requireService(t, h.ctx, contracts.KeyApprovals)
	const requests = 12

	var waiters sync.WaitGroup
	results := make(chan approvalResult, requests)
	for i := 0; i < requests; i++ {
		waiters.Add(1)
		go func(index int) {
			defer waiters.Done()
			decision, err := service.Request(context.Background(), contracts.ApprovalRequest{
				Tool: fmt.Sprintf("tool-%d", index),
			})
			results <- approvalResult{decision: decision, err: err}
		}(i)
	}
	waitFor(t, "all pending requests", func() bool { return len(service.Pending()) == requests })

	// Resolve every pending ID from several goroutines, twice each: the first
	// answer wins and the duplicate returns nil.
	ids := make([]string, 0, requests)
	for _, pending := range service.Pending() {
		ids = append(ids, pending.ID)
	}
	var resolvers sync.WaitGroup
	for _, id := range ids {
		id := id
		for copyIndex := 0; copyIndex < 2; copyIndex++ {
			resolvers.Add(1)
			go func() {
				defer resolvers.Done()
				// Both answers are valid; only the first must reach the waiter.
				if err := service.Resolve(id, contracts.ApprovalAllow); err != nil && !errors.Is(err, approvals.ErrUnknownRequest) {
					t.Errorf("concurrent Resolve(%q): %v", id, err)
				}
			}()
		}
	}
	resolvers.Wait()
	waiters.Wait()
	close(results)

	answered := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("Request error = %v, want nil", result.err)
		}
		if result.decision != contracts.ApprovalAllow {
			t.Fatalf("Request decision = %q, want allow", result.decision)
		}
		answered++
	}
	if answered != requests {
		t.Fatalf("answered %d requests, want %d", answered, requests)
	}
	if got := service.Pending(); len(got) != 0 {
		t.Fatalf("Pending() = %d after the race, want 0", len(got))
	}
}
