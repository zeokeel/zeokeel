package gateway

// A bound task must be stoppable by spend. The run stream is the only place that
// knows what a turn cost, so metering hangs off it — quietly: a metering failure
// never turns a finished answer into an error.

import (
	"context"
	"strings"
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

type fakeNotifyDispatch struct {
	mu       sync.Mutex
	category []string
	texts    []string
}

func (f *fakeNotifyDispatch) Deliver(_ context.Context, category, text string) (contracts.DispatchReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.category = append(f.category, category)
	f.texts = append(f.texts, text)
	return contracts.DispatchReceipt{Channel: contracts.NotifyChannelInApp}, nil
}

func TestBudgetStopTellsTheHumanToDecide(t *testing.T) {
	chat := &fakeChatWithBinder{fakeChat: newFakeChat(), taskID: "task-9"}
	root := testRoot(t, chat)
	lease := &fakeConsumeLease{err: contracts.ErrLeaseBudgetExhausted}
	if _, err := root.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(lease)); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	dispatcher := &fakeNotifyDispatch{}
	if _, err := root.Provide(contracts.KeyChannelDispatch, contracts.ChannelDispatchService(dispatcher)); err != nil {
		t.Fatalf("provide dispatch: %v", err)
	}
	gw, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gw.consumeRunUsage("s-1", 500)

	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.category) != 1 {
		t.Fatalf("notifications = %v, want one", dispatcher.category)
	}
	if dispatcher.category[0] != contracts.NotifyCategoryNeedsDecision {
		t.Fatalf("category = %q, want needs_decision", dispatcher.category[0])
	}
	if !strings.Contains(dispatcher.texts[0], "task-9") {
		t.Fatalf("text = %q, want the task named", dispatcher.texts[0])
	}
}

func TestMeteringNoiseIsNotAnnounced(t *testing.T) {
	chat := &fakeChatWithBinder{fakeChat: newFakeChat(), taskID: "task-9"}
	root := testRoot(t, chat)
	lease := &fakeConsumeLease{err: contracts.ErrLeaseInvalid}
	if _, err := root.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(lease)); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	dispatcher := &fakeNotifyDispatch{}
	if _, err := root.Provide(contracts.KeyChannelDispatch, contracts.ChannelDispatchService(dispatcher)); err != nil {
		t.Fatalf("provide dispatch: %v", err)
	}
	gw, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gw.consumeRunUsage("s-1", 500)
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.category) != 0 {
		t.Fatalf("a metering error was announced: %v", dispatcher.category)
	}
}

type fakeInboxRecorder struct {
	mu    sync.Mutex
	items []contracts.InboxItem
	seen  map[string]bool
}

func (f *fakeInboxRecorder) Add(_ context.Context, item contracts.InboxItem) (contracts.InboxItem, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[item.DedupKey] {
		return item, true, nil
	}
	f.seen[item.DedupKey] = true
	item.ID = "inbox-1"
	f.items = append(f.items, item)
	return item, false, nil
}

func (f *fakeInboxRecorder) List(context.Context, contracts.InboxFilter) ([]contracts.InboxItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.InboxItem(nil), f.items...), nil
}

func (f *fakeInboxRecorder) Stats(context.Context) (contracts.InboxStats, error) {
	return contracts.InboxStats{}, nil
}

func (f *fakeInboxRecorder) Get(context.Context, string) (contracts.InboxItem, error) {
	return contracts.InboxItem{}, contracts.ErrInboxUnknownItem
}

func (f *fakeInboxRecorder) Triage(context.Context, []string, string, []string) (int, error) {
	return 0, nil
}

func TestABudgetStopBecomesATodoOnce(t *testing.T) {
	chat := &fakeChatWithBinder{fakeChat: newFakeChat(), taskID: "task-9"}
	root := testRoot(t, chat)
	lease := &fakeConsumeLease{err: contracts.ErrLeaseBudgetExhausted}
	if _, err := root.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(lease)); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	inbox := &fakeInboxRecorder{}
	if _, err := root.Provide(contracts.KeyInbox, contracts.InboxService(inbox)); err != nil {
		t.Fatalf("provide inbox: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gateway.consumeRunUsage("s-1", 500)
	gateway.consumeRunUsage("s-1", 500)

	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if len(inbox.items) != 1 {
		t.Fatalf("inbox items = %d, want one deduped todo", len(inbox.items))
	}
	if inbox.items[0].Source != "event" || !strings.Contains(inbox.items[0].Title, "task-9") {
		t.Fatalf("item = %+v, want an event todo naming the task", inbox.items[0])
	}
}
