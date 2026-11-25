package gateway

// The delivery methods are the only place a message actually leaves the product,
// so their mapping and their refusals are worth pinning: an empty text must be
// refused before anything is attempted, and the receipt must survive the wire
// shape intact (attempts, deferred, reason, errors).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeRouterOnly struct {
	plan contracts.DeliveryPlan
	err  error
	seen map[string]bool
}

func (f *fakeRouterOnly) Routes(context.Context) ([]contracts.NotifyRoute, error) { return nil, nil }
func (f *fakeRouterOnly) Upsert(context.Context, contracts.NotifyRoute) error     { return nil }
func (f *fakeRouterOnly) Plan(_ context.Context, _ string, _ time.Time, healthy map[string]bool) (contracts.DeliveryPlan, error) {
	f.seen = healthy
	return f.plan, f.err
}

type fakeDispatch struct {
	receipt contracts.DispatchReceipt
	err     error
	text    string
}

func (f *fakeDispatch) Deliver(_ context.Context, _, text string) (contracts.DispatchReceipt, error) {
	f.text = text
	return f.receipt, f.err
}

func newNotifyGateway(t *testing.T, router contracts.NotifyRouterService, dispatcher contracts.ChannelDispatchService) *Gateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if router != nil {
		if _, err := root.Provide(contracts.KeyNotifyRoute, router); err != nil {
			t.Fatalf("provide router: %v", err)
		}
	}
	if dispatcher != nil {
		if _, err := root.Provide(contracts.KeyChannelDispatch, dispatcher); err != nil {
			t.Fatalf("provide dispatch: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}

func TestNotifyPlanReportsTheOrderedChannels(t *testing.T) {
	router := &fakeRouterOnly{plan: contracts.DeliveryPlan{
		Channels: []string{"wechat", "dingtalk", "inapp"}, Deferred: true, Reason: "quiet hours",
	}}
	gw := newNotifyGateway(t, router, nil)

	result, problem := gw.notifyPlan(context.Background(), json.RawMessage(`{"category":"approval"}`))
	if problem != nil {
		t.Fatalf("notifyPlan: %+v", problem)
	}
	plan, ok := result.(notifyPlanResult)
	if !ok || len(plan.Channels) != 3 || plan.Channels[0] != "wechat" || !plan.Deferred || plan.Reason != "quiet hours" {
		t.Fatalf("plan = %+v", result)
	}
}

func TestNotifyDeliverReturnsTheReceipt(t *testing.T) {
	dispatcher := &fakeDispatch{receipt: contracts.DispatchReceipt{
		Channel: "dingtalk", Attempts: []string{"wechat", "dingtalk"}, Errors: []string{"wechat: window closed"},
	}}
	gw := newNotifyGateway(t, nil, dispatcher)

	result, problem := gw.notifyDeliver(context.Background(), json.RawMessage(`{"category":"approval","text":"hello"}`))
	if problem != nil {
		t.Fatalf("notifyDeliver: %+v", problem)
	}
	receipt, ok := result.(notifyDeliverResult)
	if !ok || receipt.Channel != "dingtalk" || len(receipt.Attempts) != 2 || len(receipt.Errors) != 1 {
		t.Fatalf("receipt = %+v", result)
	}
	if dispatcher.text != "hello" {
		t.Fatalf("dispatcher saw %q", dispatcher.text)
	}
}

func TestNotifyDeliverRefusesEmptyTextBeforeSending(t *testing.T) {
	dispatcher := &fakeDispatch{}
	gw := newNotifyGateway(t, nil, dispatcher)

	if _, problem := gw.notifyDeliver(context.Background(), json.RawMessage(`{"category":"approval","text":"   "}`)); problem == nil {
		t.Fatal("blank text was accepted")
	}
	if dispatcher.text != "" {
		t.Fatalf("the dispatcher was called with %q", dispatcher.text)
	}
}

func TestNotifyDeliverSurfacesADispatchFailure(t *testing.T) {
	dispatcher := &fakeDispatch{err: errors.New("router exploded")}
	gw := newNotifyGateway(t, nil, dispatcher)
	if _, problem := gw.notifyDeliver(context.Background(), json.RawMessage(`{"category":"approval","text":"hi"}`)); problem == nil {
		t.Fatal("a dispatch failure was reported as success")
	}
}
