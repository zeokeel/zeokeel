package tasklease_test

// Held sends: a before_send task's message waits for a human and leaves once.

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

func TestHeldSendLeavesOnce(t *testing.T) {
	service, _ := newService(t, newFakeBoard("task-1"), nil)
	ctx := context.Background()

	if err := service.Hold(ctx, "task-1", "本期退款名单已确认：3 人"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	text, found, err := service.Held(ctx, "task-1")
	if err != nil || !found || text != "本期退款名单已确认：3 人" {
		t.Fatalf("Held = (%q, %v, %v)", text, found, err)
	}
	sent, err := service.ApproveSend(ctx, "task-1")
	if err != nil || sent != text {
		t.Fatalf("ApproveSend = (%q, %v)", sent, err)
	}
	if _, found, _ := service.Held(ctx, "task-1"); found {
		t.Fatal("the message is still held after it was approved")
	}
	if _, err := service.ApproveSend(ctx, "task-1"); !errors.Is(err, contracts.ErrHeldSendUnknown) {
		t.Fatalf("second ApproveSend = %v, want ErrHeldSendUnknown", err)
	}
}

func TestHeldSendSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	service, err := tasklease.Open(dir, board, tasklease.Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := service.Hold(context.Background(), "task-1", "还没发出去的话"); err != nil {
		t.Fatalf("Hold: %v", err)
	}

	again, err := tasklease.Open(dir, board, tasklease.Config{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	text, found, err := again.Held(context.Background(), "task-1")
	if err != nil || !found || text != "还没发出去的话" {
		t.Fatalf("after reopen Held = (%q, %v, %v)", text, found, err)
	}
}

func TestHeldSendRejectsAnEmptyMessage(t *testing.T) {
	service, _ := newService(t, newFakeBoard("task-1"), nil)
	if err := service.Hold(context.Background(), "task-1", "   "); !errors.Is(err, contracts.ErrLeaseInvalid) {
		t.Fatalf("Hold(blank) = %v, want ErrLeaseInvalid", err)
	}
}
