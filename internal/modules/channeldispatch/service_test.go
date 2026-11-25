package channeldispatch_test

// TDD for the send half of routing. The router already says where a message
// should go; these tests are about what happens when a channel refuses: fall
// back in order, never claim delivery that did not happen, and always end at the
// in-app terminal.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/channeldispatch"
)

type fakeRouter struct {
	plan contracts.DeliveryPlan
}

func (f *fakeRouter) Routes(context.Context) ([]contracts.NotifyRoute, error) { return nil, nil }
func (f *fakeRouter) Upsert(context.Context, contracts.NotifyRoute) error     { return nil }
func (f *fakeRouter) Plan(context.Context, string, time.Time, map[string]bool) (contracts.DeliveryPlan, error) {
	return f.plan, nil
}

type fakeSender struct {
	name     string
	err      error
	sent     []string
	failN    int
	attempts int
}

func (f *fakeSender) Name() string { return f.name }

func (f *fakeSender) Send(_ context.Context, conversation, text string) error {
	f.attempts++
	if f.failN > 0 {
		f.failN--
		return errors.New("refused")
	}
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, conversation+":"+text)
	return nil
}

func newService(t *testing.T, plan contracts.DeliveryPlan, senders ...*fakeSender) (*channeldispatch.Service, map[string]string) {
	t.Helper()
	registry := make([]contracts.ChannelSender, 0, len(senders))
	targets := map[string]string{}
	for _, sender := range senders {
		registry = append(registry, sender)
		targets[sender.name] = sender.name + "-dm"
	}
	service := channeldispatch.New(&fakeRouter{plan: plan}, registry, targets, nil)
	service.RetryDelay = time.Nanosecond // keep the suite fast; the retry itself is what is under test
	return service, targets
}

func TestPrimaryChannelTakesIt(t *testing.T) {
	wechat := &fakeSender{name: "wechat"}
	service, _ := newService(t, contracts.DeliveryPlan{Channels: []string{"wechat", "inapp"}}, wechat)

	receipt, err := service.Deliver(context.Background(), contracts.NotifyCategoryApproval, "有一条要你批")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if receipt.Channel != "wechat" || len(receipt.Attempts) != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(wechat.sent) != 1 || wechat.sent[0] != "wechat-dm:有一条要你批" {
		t.Fatalf("sent = %v", wechat.sent)
	}
}

func TestFallbackIsUsedWhenThePrimaryRefuses(t *testing.T) {
	wechat := &fakeSender{name: "wechat", err: errors.New("window closed")}
	dingtalk := &fakeSender{name: "dingtalk"}
	service, _ := newService(t, contracts.DeliveryPlan{Channels: []string{"wechat", "dingtalk", "inapp"}}, wechat, dingtalk)

	receipt, err := service.Deliver(context.Background(), contracts.NotifyCategoryApproval, "x")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if receipt.Channel != "dingtalk" {
		t.Fatalf("receipt = %+v, want the fallback to take it", receipt)
	}
	if len(receipt.Attempts) != 2 || receipt.Attempts[0] != "wechat" || receipt.Attempts[1] != "dingtalk" {
		t.Fatalf("attempts = %v", receipt.Attempts)
	}
	if len(receipt.Errors) != 1 {
		t.Fatalf("errors = %v, want the refusal recorded", receipt.Errors)
	}
}

func TestEveryChannelFailingEndsInAppInsteadOfLying(t *testing.T) {
	wechat := &fakeSender{name: "wechat", err: errors.New("down")}
	service, _ := newService(t, contracts.DeliveryPlan{Channels: []string{"wechat", "inapp"}}, wechat)

	receipt, err := service.Deliver(context.Background(), contracts.NotifyCategoryApproval, "x")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if receipt.Channel != "inapp" {
		t.Fatalf("receipt = %+v, want the in-app terminal channel, not a false success", receipt)
	}
	if len(receipt.Errors) != 1 {
		t.Fatalf("errors = %v, want the attempt recorded", receipt.Errors)
	}
}

func TestUnconfiguredChannelIsSkippedNotFatal(t *testing.T) {
	// The plan names wechat but this product has no wechat sender configured.
	service, _ := newService(t, contracts.DeliveryPlan{Channels: []string{"wechat", "inapp"}})

	receipt, err := service.Deliver(context.Background(), contracts.NotifyCategoryApproval, "x")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if receipt.Channel != "inapp" || len(receipt.Errors) != 1 {
		t.Fatalf("receipt = %+v, want an in-app fallback with the reason recorded", receipt)
	}
}

func TestQuietHoursDeferTheMessage(t *testing.T) {
	wechat := &fakeSender{name: "wechat"}
	service, _ := newService(t, contracts.DeliveryPlan{
		Channels: []string{"inapp"}, Deferred: true, Reason: "quiet hours 22:30-08:00",
	}, wechat)

	receipt, err := service.Deliver(context.Background(), contracts.NotifyCategoryInboxDigest, "x")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !receipt.Deferred || receipt.Channel != "inapp" {
		t.Fatalf("receipt = %+v, want a deferred in-app only delivery", receipt)
	}
	if len(wechat.sent) != 0 {
		t.Fatal("a deferred message reached a channel")
	}
}

func TestOneRetryBeforeFallingBack(t *testing.T) {
	wechat := &fakeSender{name: "wechat", failN: 1}
	dingtalk := &fakeSender{name: "dingtalk"}
	service, _ := newService(t, contracts.DeliveryPlan{Channels: []string{"wechat", "dingtalk", "inapp"}}, wechat, dingtalk)

	receipt, err := service.Deliver(context.Background(), contracts.NotifyCategoryApproval, "x")
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if receipt.Channel != "wechat" {
		t.Fatalf("receipt = %+v, want the retry to keep it on the primary", receipt)
	}
	if wechat.attempts != 2 {
		t.Fatalf("attempts = %d, want exactly one retry", wechat.attempts)
	}
}
