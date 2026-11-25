package gateway

// approval=before_send: the work may start, the words wait. Nothing leaves the
// product until a human approves the message, and it leaves once.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeSendHolder struct {
	mu    sync.Mutex
	held  map[string]string
	sends int
}

func (f *fakeSendHolder) Hold(_ context.Context, taskID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[string]string{}
	}
	f.held[taskID] = text
	return nil
}

func (f *fakeSendHolder) Held(_ context.Context, taskID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text, ok := f.held[taskID]
	return text, ok, nil
}

func (f *fakeSendHolder) ApproveSend(_ context.Context, taskID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text, ok := f.held[taskID]
	if !ok {
		return "", contracts.ErrHeldSendUnknown
	}
	delete(f.held, taskID)
	f.sends++
	return text, nil
}

// beforeSendLease is a lease set whose policy is before_send for one task.
type beforeSendLease struct {
	fakeLeaseTools
	taskID string
}

func (f *beforeSendLease) Approval(_ context.Context, taskID string) (string, bool, error) {
	if taskID == f.taskID {
		return contracts.TaskApprovalBeforeSend, true, nil
	}
	return contracts.TaskApprovalNone, false, nil
}

func newSendGateway(t *testing.T, holder contracts.TaskSendHolder, leases contracts.TaskLeaseService) (*Gateway, *fakeDispatchRecorder) {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyTaskSendHolder, holder); err != nil {
		t.Fatalf("provide holder: %v", err)
	}
	if _, err := root.Provide(contracts.KeyTaskLease, leases); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	dispatcher := &fakeDispatchRecorder{}
	if _, err := root.Provide(contracts.KeyChannelDispatch, contracts.ChannelDispatchService(dispatcher)); err != nil {
		t.Fatalf("provide dispatch: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway, dispatcher
}

func TestBeforeSendHoldsTheMessageInsteadOfSending(t *testing.T) {
	holder := &fakeSendHolder{}
	gateway, dispatcher := newSendGateway(t, holder, &beforeSendLease{taskID: "task-7"})

	gateway.holdOutbound(context.Background(), "task-7", "accept", "证据齐全")
	held, found, _ := holder.Held(context.Background(), "task-7")
	if !found || held == "" {
		t.Fatal("nothing was held for the before_send task")
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.requests) != 0 {
		t.Fatalf("the message went out before approval: %v", dispatcher.requests)
	}
}

func TestApprovedHeldMessageGoesOutOnce(t *testing.T) {
	holder := &fakeSendHolder{}
	gateway, dispatcher := newSendGateway(t, holder, &beforeSendLease{taskID: "task-7"})
	gateway.holdOutbound(context.Background(), "task-7", "accept", "")

	raw, _ := json.Marshal(map[string]any{"id": "task-7", "approver": "lead"})
	if _, problem := gateway.taskSendApprove(context.Background(), raw); problem != nil {
		t.Fatalf("approve send: %+v", problem)
	}
	dispatcher.mu.Lock()
	if len(dispatcher.requests) != 1 {
		dispatcher.mu.Unlock()
		t.Fatalf("deliveries = %v, want one", dispatcher.requests)
	}
	dispatcher.mu.Unlock()

	if _, problem := gateway.taskSendApprove(context.Background(), raw); problem == nil {
		t.Fatal("the same message was sent twice")
	}
}

func TestApproveSendRequiresAnApprover(t *testing.T) {
	holder := &fakeSendHolder{}
	gateway, _ := newSendGateway(t, holder, &beforeSendLease{taskID: "task-7"})
	gateway.holdOutbound(context.Background(), "task-7", "accept", "")

	raw, _ := json.Marshal(map[string]any{"id": "task-7"})
	if _, problem := gateway.taskSendApprove(context.Background(), raw); problem == nil {
		t.Fatal("an approval without an approver was accepted")
	}
	if _, found, _ := holder.Held(context.Background(), "task-7"); !found {
		t.Fatal("the message left without an approver")
	}
}

func TestPolicyDecidesWhetherTheMessageIsHeld(t *testing.T) {
	holder := &fakeSendHolder{}
	gateway, _ := newSendGateway(t, holder, &beforeSendLease{taskID: "task-7"})
	if !gateway.reviewNeedsSendApproval(context.Background(), "task-7") {
		t.Fatal("before_send was not recognised")
	}
	if gateway.reviewNeedsSendApproval(context.Background(), "task-8") {
		t.Fatal("a task without the policy was treated as before_send")
	}
}

func TestSendPendingReportsTheHeldMessage(t *testing.T) {
	holder := &fakeSendHolder{}
	gateway, _ := newSendGateway(t, holder, &beforeSendLease{taskID: "task-7"})

	empty, problem := gateway.taskSendPending(context.Background(), json.RawMessage(`{"id":"task-7"}`))
	if problem != nil {
		t.Fatalf("pending: %+v", problem)
	}
	if payload, _ := empty.(map[string]any); payload["held"] != false {
		t.Fatalf("pending with nothing held = %+v", empty)
	}

	gateway.holdOutbound(context.Background(), "task-7", "accept", "证据齐全")
	held, problem := gateway.taskSendPending(context.Background(), json.RawMessage(`{"id":"task-7"}`))
	if problem != nil {
		t.Fatalf("pending: %+v", problem)
	}
	payload, _ := held.(map[string]any)
	if payload["held"] != true {
		t.Fatalf("pending = %+v, want held", held)
	}
	if text, _ := payload["text"].(string); !strings.Contains(text, "task-7") {
		t.Fatalf("text = %q, want the task named", text)
	}
}
