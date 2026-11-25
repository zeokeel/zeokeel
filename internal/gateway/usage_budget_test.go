package gateway

// A bound task must be stoppable by spend. The run stream is the only place that
// knows what a turn cost, so metering hangs off it — quietly: a metering failure
// never turns a finished answer into an error.

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeChatWithBinder is the plain fake plus the optional SessionTaskBinder half,
// which is what the real chat service also provides.
type fakeChatWithBinder struct {
	*fakeChat
	taskID string
}

func (f *fakeChatWithBinder) BindSessionTask(_ context.Context, _, taskID string) error {
	f.taskID = taskID
	return nil
}

func (f *fakeChatWithBinder) SessionTask(string) (string, error) { return f.taskID, nil }

type fakeConsumeLease struct {
	contracts.TaskLeaseService
	mu       sync.Mutex
	consumed map[string]int
	err      error
}

func (f *fakeConsumeLease) Consume(_ context.Context, taskID string, tokens int) (contracts.TaskBudget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumed == nil {
		f.consumed = map[string]int{}
	}
	f.consumed[taskID] += tokens
	return contracts.TaskBudget{TaskID: taskID, UsedTokens: f.consumed[taskID]}, f.err
}

func TestRunUsageReachesTheBoundTaskBudget(t *testing.T) {
	chat := &fakeChatWithBinder{fakeChat: newFakeChat(), taskID: "task-9"}
	root := testRoot(t, chat)
	if _, err := root.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(&fakeConsumeLease{})); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	gw, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The fake chat also carries the binder half, so the metering path can be
	// proven without a real agent.

	gw.consumeRunUsage("s-1", 42)
	leases, _ := gw.root.Get(contracts.KeyTaskLease)
	used := leases.(*fakeConsumeLease)
	used.mu.Lock()
	defer used.mu.Unlock()
	if used.consumed["task-9"] != 42 {
		t.Fatalf("consumed = %v, want 42 for task-9", used.consumed)
	}
}

func TestUnboundOrCheapRunsDoNotConsume(t *testing.T) {
	chat := &fakeChatWithBinder{fakeChat: newFakeChat()}
	root := testRoot(t, chat)
	lease := &fakeConsumeLease{}
	if _, err := root.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(lease)); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	gw, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gw.consumeRunUsage("s-1", 42) // unbound
	gw.consumeRunUsage("s-1", 0)  // nothing spent
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if len(lease.consumed) != 0 {
		t.Fatalf("consumed = %v, want nothing", lease.consumed)
	}
}
