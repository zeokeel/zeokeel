package approvals

// Tests for the approvals backend module: mode semantics, the blocking
// Request/Resolve handshake, pending ordering, subscriptions and concurrent
// interleavings. No UI or display is involved.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *fakeLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func (l *fakeLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// requestResult is one Request outcome.
type requestResult struct {
	decision contracts.ApprovalDecision
	err      error
}

// startRequest issues a Request on a goroutine and returns its result channel.
func startRequest(ctx context.Context, svc *service, request contracts.ApprovalRequest) <-chan requestResult {
	results := make(chan requestResult, 1)
	go func() {
		decision, err := svc.Request(ctx, request)
		results <- requestResult{decision: decision, err: err}
	}()
	return results
}

// waitFor polls condition until it holds or the timeout expires.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitResult receives one request result, failing the test on timeout.
func waitResult(t *testing.T, results <-chan requestResult) requestResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the approval result")
		return requestResult{}
	}
}

// newAskService returns a service in ask mode with a fake logger.
func newAskService(t *testing.T) (*service, *fakeLogger) {
	t.Helper()
	logger := &fakeLogger{}
	svc, err := newService(ModeAsk, logger)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	return svc, logger
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// --- tests -----------------------------------------------------------------

// TestModes covers the immediate auto/never decisions and the default mode.
func TestModes(t *testing.T) {
	auto, err := newService("auto", nil)
	if err != nil {
		t.Fatalf("newService(auto): %v", err)
	}
	if got := auto.Mode(); got != ModeAuto {
		t.Fatalf("Mode = %q, want auto", got)
	}
	if decision, err := auto.Request(context.Background(), contracts.ApprovalRequest{Tool: "echo"}); err != nil ||
		decision != contracts.ApprovalAllow {
		t.Fatalf("auto Request = %q/%v, want allow", decision, err)
	}
	if pending := auto.Pending(); len(pending) != 0 {
		t.Fatalf("auto pending = %+v, want none", pending)
	}

	never, err := newService("never", nil)
	if err != nil {
		t.Fatalf("newService(never): %v", err)
	}
	if decision, err := never.Request(context.Background(), contracts.ApprovalRequest{Tool: "echo"}); err != nil ||
		decision != contracts.ApprovalDeny {
		t.Fatalf("never Request = %q/%v, want deny", decision, err)
	}

	defaultMode, err := newService("", nil)
	if err != nil {
		t.Fatalf("newService(default): %v", err)
	}
	if got := defaultMode.Mode(); got != ModeWorkspace {
		t.Fatalf("default Mode = %q, want workspace", got)
	}
	// The default policy allows normal in-workspace file edits without queueing.
	if decision, err := defaultMode.Request(context.Background(),
		contracts.ApprovalRequest{Tool: "write_file"}); err != nil || decision != contracts.ApprovalAllow {
		t.Fatalf("default write_file = %q/%v, want allow", decision, err)
	}
	if pending := defaultMode.Pending(); len(pending) != 0 {
		t.Fatalf("default write_file pending = %+v, want none", pending)
	}
}

// TestModeNormalization covers whitespace/case handling and invalid modes.
func TestModeNormalization(t *testing.T) {
	for _, testCase := range []struct {
		mode string
		want string
	}{
		{"", ModeWorkspace},
		{"workspace", ModeWorkspace},
		{" WORKSPACE ", ModeWorkspace},
		{"ask", ModeAsk},
		{" ASK ", ModeAsk},
		{"Auto", ModeAuto},
		{" never\n", ModeNever},
	} {
		svc, err := newService(testCase.mode, nil)
		if err != nil {
			t.Fatalf("newService(%q): %v", testCase.mode, err)
		}
		if got := svc.Mode(); got != testCase.want {
			t.Fatalf("Mode(%q) = %q, want %q", testCase.mode, got, testCase.want)
		}
	}
	if _, err := newService("sometimes", nil); err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("newService(invalid) = %v, want a readable mode error", err)
	}
}

// TestAskBlocksThenResolve covers the core handshake and idempotent Resolve.
func TestAskBlocksThenResolve(t *testing.T) {
	svc, _ := newAskService(t)
	request := contracts.ApprovalRequest{
		Tool:      "write_file",
		Summary:   "Write the release notes",
		Arguments: `{"path":"CHANGELOG.md"}`,
	}
	results := startRequest(context.Background(), svc, request)
	waitFor(t, "the request to be pending", func() bool { return len(svc.Pending()) == 1 })

	pending := svc.Pending()[0]
	if pending.ID != "appr-1" {
		t.Fatalf("request ID = %q, want appr-1", pending.ID)
	}
	if pending.Tool != request.Tool || pending.Summary != request.Summary ||
		pending.Arguments != request.Arguments || pending.CreatedAt.IsZero() {
		t.Fatalf("pending request = %+v, want the submitted fields and a timestamp", pending)
	}

	if err := svc.Resolve(pending.ID, contracts.ApprovalDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	result := waitResult(t, results)
	if result.err != nil || result.decision != contracts.ApprovalDeny {
		t.Fatalf("Request result = %q/%v, want deny", result.decision, result.err)
	}
	if got := svc.Pending(); len(got) != 0 {
		t.Fatalf("pending after resolve = %+v, want none", got)
	}

	// Resolve is idempotent after the first answer and does not change it.
	if err := svc.Resolve(pending.ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("second Resolve: %v, want nil", err)
	}
	if got := svc.Pending(); len(got) != 0 {
		t.Fatalf("second Resolve re-opened the request: %+v", got)
	}
}

// TestResolveUnknownAndInvalidDecision covers Resolve validation.
func TestResolveUnknownAndInvalidDecision(t *testing.T) {
	svc, _ := newAskService(t)
	if err := svc.Resolve("appr-999", contracts.ApprovalAllow); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("Resolve(unknown) = %v, want ErrUnknownRequest", err)
	}
	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: "echo"})
	waitFor(t, "pending", func() bool { return len(svc.Pending()) == 1 })
	id := svc.Pending()[0].ID

	if err := svc.Resolve(id, contracts.ApprovalDecision("maybe")); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Resolve(invalid decision) = %v, want ErrInvalidDecision", err)
	}
	if got := svc.Pending(); len(got) != 1 {
		t.Fatalf("invalid Resolve changed the queue: %+v", got)
	}
	if err := svc.Resolve(id, contracts.ApprovalAllowAlways); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result := waitResult(t, results); result.decision != contracts.ApprovalAllowAlways {
		t.Fatalf("decision = %q, want allow_always", result.decision)
	}
}

// TestContextCancellationCleansPending covers a waiting request whose context
// ends before an answer.
func TestContextCancellationCleansPending(t *testing.T) {
	svc, _ := newAskService(t)
	var notifications atomic.Int32
	unsub, err := svc.Subscribe(func() { notifications.Add(1) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	results := startRequest(ctx, svc, contracts.ApprovalRequest{Tool: "echo"})
	waitFor(t, "pending", func() bool { return len(svc.Pending()) == 1 })
	id := svc.Pending()[0].ID
	if notifications.Load() == 0 {
		t.Fatal("no notification for the queued request")
	}

	cancel()
	result := waitResult(t, results)
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("Request error = %v, want context.Canceled", result.err)
	}
	waitFor(t, "pending to be cleared", func() bool { return len(svc.Pending()) == 0 })
	if err := svc.Resolve(id, contracts.ApprovalAllow); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("Resolve(cancelled id) = %v, want ErrUnknownRequest", err)
	}
}

// TestPendingOrderAndIDs covers oldest-first ordering and monotonically growing
// IDs.
func TestPendingOrderAndIDs(t *testing.T) {
	svc, _ := newAskService(t)
	results := make([]<-chan requestResult, 0, 3)
	for index := 0; index < 3; index++ {
		results = append(results, startRequest(context.Background(), svc,
			contracts.ApprovalRequest{Tool: fmt.Sprintf("tool-%d", index)}))
		waitFor(t, "next pending request", func() bool { return len(svc.Pending()) == index+1 })
	}
	pending := svc.Pending()
	if len(pending) != 3 {
		t.Fatalf("pending = %d, want 3", len(pending))
	}
	for index, request := range pending {
		want := fmt.Sprintf("appr-%d", index+1)
		if request.ID != want {
			t.Fatalf("pending[%d].ID = %q, want %q", index, request.ID, want)
		}
		if index > 0 && request.CreatedAt.Before(pending[index-1].CreatedAt) {
			t.Fatalf("pending order is not oldest-first: %+v", pending)
		}
		if err := svc.Resolve(request.ID, contracts.ApprovalAllow); err != nil {
			t.Fatalf("Resolve(%s): %v", request.ID, err)
		}
	}
	for _, channel := range results {
		if result := waitResult(t, channel); result.err != nil || result.decision != contracts.ApprovalAllow {
			t.Fatalf("result = %q/%v", result.decision, result.err)
		}
	}
	if got := svc.Pending(); len(got) != 0 {
		t.Fatalf("pending after all resolved = %+v", got)
	}
}

// TestSubscriptions covers notification fan-out, unsubscribe idempotency and
// panic isolation.
func TestSubscriptions(t *testing.T) {
	svc, logger := newAskService(t)
	var first, second atomic.Int32
	unsubFirst, err := svc.Subscribe(func() { first.Add(1) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := svc.Subscribe(func() { second.Add(1) }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := svc.Subscribe(nil); err != nil {
		t.Fatalf("Subscribe(nil): %v", err)
	}
	if _, err := svc.Subscribe(func() { panic("ui bug") }); err != nil {
		t.Fatalf("Subscribe(panic): %v", err)
	}

	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: "echo"})
	waitFor(t, "pending", func() bool { return len(svc.Pending()) == 1 })
	if first.Load() == 0 || second.Load() == 0 {
		t.Fatalf("notifications: first=%d second=%d", first.Load(), second.Load())
	}
	if !logger.contains("subscriber panicked") {
		t.Fatalf("panic was not logged: %v", logger.lines)
	}
	firstCount := first.Load()
	unsubFirst()
	unsubFirst() // idempotent
	id := svc.Pending()[0].ID
	if err := svc.Resolve(id, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	waitResult(t, results)
	if first.Load() != firstCount {
		t.Fatalf("unsubscribed callback fired: %d -> %d", firstCount, first.Load())
	}
	if second.Load() < 2 {
		t.Fatalf("remaining subscriber was not notified on resolve: %d", second.Load())
	}
}

// TestConcurrentRequestsAndResolve interleaves ten blocking requests with their
// resolutions.
func TestConcurrentRequestsAndResolve(t *testing.T) {
	svc, _ := newAskService(t)
	const requests = 10
	results := make([]<-chan requestResult, 0, requests)
	for index := 0; index < requests; index++ {
		results = append(results, startRequest(context.Background(), svc,
			contracts.ApprovalRequest{Tool: fmt.Sprintf("tool-%d", index)}))
	}
	waitFor(t, "all requests pending", func() bool { return len(svc.Pending()) == requests })

	pending := svc.Pending()
	ids := make(map[string]bool, requests)
	decisions := make(map[string]contracts.ApprovalDecision, requests)
	for index, request := range pending {
		if ids[request.ID] {
			t.Fatalf("duplicate request ID %q", request.ID)
		}
		ids[request.ID] = true
		decision := []contracts.ApprovalDecision{
			contracts.ApprovalAllow,
			contracts.ApprovalDeny,
			contracts.ApprovalAllowAlways,
		}[index%3]
		decisions[request.ID] = decision
	}

	var waitGroup sync.WaitGroup
	errs := make(chan error, requests)
	for _, request := range pending {
		waitGroup.Add(1)
		go func(id string, decision contracts.ApprovalDecision) {
			defer waitGroup.Done()
			if err := svc.Resolve(id, decision); err != nil {
				errs <- err
			}
		}(request.ID, decisions[request.ID])
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Requests are started concurrently, so the result channels are not in
	// pending order; compare the decision multiset instead.
	got := map[contracts.ApprovalDecision]int{}
	for index, channel := range results {
		result := waitResult(t, channel)
		if result.err != nil {
			t.Fatalf("request %d error: %v", index, result.err)
		}
		got[result.decision]++
	}
	want := map[contracts.ApprovalDecision]int{}
	for _, decision := range decisions {
		want[decision]++
	}
	if len(got) != len(want) {
		t.Fatalf("decisions = %v, want %v", got, want)
	}
	for decision, count := range want {
		if got[decision] != count {
			t.Fatalf("decisions = %v, want %v", got, want)
		}
	}
	if got := svc.Pending(); len(got) != 0 {
		t.Fatalf("pending after concurrent resolves = %+v", got)
	}
}

// TestConcurrentCancelAndResolve exercises the cancellation/resolve race.
func TestConcurrentCancelAndResolve(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		svc, _ := newAskService(t)
		ctx, cancel := context.WithCancel(context.Background())
		results := startRequest(ctx, svc, contracts.ApprovalRequest{Tool: "race"})
		waitFor(t, "pending", func() bool { return len(svc.Pending()) == 1 })
		id := svc.Pending()[0].ID

		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() { defer waitGroup.Done(); cancel() }()
		go func() { defer waitGroup.Done(); _ = svc.Resolve(id, contracts.ApprovalAllow) }()
		waitGroup.Wait()
		result := waitResult(t, results)
		if result.err == nil && result.decision != contracts.ApprovalAllow {
			t.Fatalf("result = %q/%v, want a decision or ctx error", result.decision, result.err)
		}
		if result.err != nil && !errors.Is(result.err, context.Canceled) {
			t.Fatalf("request error = %v, want context.Canceled", result.err)
		}
		waitFor(t, "pending to settle", func() bool { return len(svc.Pending()) == 0 })
	}
}

// TestActivation covers the module descriptor, Provide and invalid configs.
func TestActivation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/approvals" {
		t.Fatalf("module ID = %q", descriptor.ID)
	}
	if descriptor.Factory == nil {
		t.Fatal("module has a nil factory")
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyApprovals.Name() {
		t.Fatalf("provides = %+v, want KeyApprovals", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	if !required[contracts.KeyLogger.Name()] {
		t.Fatalf("module does not require KeyLogger; requires = %v", descriptor.Requires)
	}

	ctx := cordis.Root()
	defer func() { _ = ctx.DisposeAll() }()
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(&fakeLogger{}))
	if err := descriptor.Factory(ctx, Config{Mode: "auto"}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	approvals, err := ctx.Require(contracts.KeyApprovals)
	if err != nil {
		t.Fatalf("require KeyApprovals: %v", err)
	}
	if got := approvals.Mode(); got != ModeAuto {
		t.Fatalf("Mode = %q, want auto", got)
	}

	invalid := cordis.Root()
	defer func() { _ = invalid.DisposeAll() }()
	provide(t, invalid, contracts.KeyLogger, contracts.Logger(&fakeLogger{}))
	if err := descriptor.Factory(invalid, Config{Mode: "sometimes"}); err == nil {
		t.Fatal("activation accepted an invalid mode")
	}
	if _, err := invalid.Require(contracts.KeyApprovals); err == nil {
		t.Fatal("KeyApprovals was provided despite the invalid mode")
	}
}

// TestRequestContextAlreadyDone covers a pre-cancelled context.
func TestRequestContextAlreadyDone(t *testing.T) {
	svc, _ := newAskService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Request(ctx, contracts.ApprovalRequest{Tool: "echo"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Request(cancelled ctx) = %v, want context.Canceled", err)
	}
	// A pre-cancelled request must not be left pending.
	waitFor(t, "pending to be empty", func() bool { return len(svc.Pending()) == 0 })
}
