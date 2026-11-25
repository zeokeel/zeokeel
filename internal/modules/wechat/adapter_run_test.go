package wechat_test

// TDD for the poll loop and the 24h keepalive warning. The loop is the only
// place that touches the platform on a schedule, so the tests pin the three
// behaviours that keep a long-running product sane: it stops when told to, it
// does not hammer the platform when it is down, and it never pretends to be
// authorized when the token was rejected.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func waitForInbox(t *testing.T, svc contracts.InboxService, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		items, err := svc.List(context.Background(), contracts.InboxFilter{})
		if err == nil && len(items) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("inbox never reached %d items", want)
}

func TestRunIngestsMessagesAndStopsOnCancel(t *testing.T) {
	adapter, server, inboxSvc, _ := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "帮我看下昨天的退款率", "token-1")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Run(ctx, 10*time.Millisecond) }()

	waitForInbox(t, inboxSvc, 1)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was canceled")
	}
	items, err := inboxSvc.List(context.Background(), contracts.InboxFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("inbox = (%v, %v), want one ingested message", items, err)
	}
	// The loop polls; it must never send on its own (no keepalive chatter).
	if server.lastSend() != nil {
		t.Fatal("Run sent a message it was not asked to send")
	}
}

func TestRunStopsOnUnauthorized(t *testing.T) {
	adapter, server, _, _ := newFixture(t)
	server.status = http.StatusUnauthorized

	start := time.Now()
	err := adapter.Run(context.Background(), 10*time.Millisecond)
	if !errors.Is(err, contracts.ErrWeChatUnauthorized) {
		t.Fatalf("Run = %v, want ErrWeChatUnauthorized", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run took %v to reject an unauthorized token", elapsed)
	}
	if polls := server.pollCount(); polls != 1 {
		t.Fatalf("getupdates called %d times, want 1 (auth failure must not retry)", polls)
	}
}

func TestRunBacksOffWhileUnavailableAndStillStopsOnCancel(t *testing.T) {
	adapter, server, _, _ := newFixture(t)
	server.status = http.StatusInternalServerError

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Run(ctx, 10*time.Millisecond) }()

	// The first failure must schedule the 2s initial backoff, not the 10ms
	// poll interval: one or two attempts in this window, not dozens.
	time.Sleep(250 * time.Millisecond)
	if polls := server.pollCount(); polls > 2 {
		t.Fatalf("getupdates called %d times in 250ms, want the backoff to hold it to <=2", polls)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel during backoff = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored the context while backing off")
	}
}

func TestRunReturnsNilOnAlreadyCanceledContext(t *testing.T) {
	adapter, _, _, _ := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- adapter.Run(ctx, time.Hour) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return for an already-canceled context")
	}
}

func TestNeedsKeepaliveBoundaries(t *testing.T) {
	adapter, server, _, clk := newFixture(t)
	now := clk.now()
	if adapter.NeedsKeepalive(now) {
		t.Fatal("NeedsKeepalive = true before any inbound message")
	}

	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	anchor := clk.now()

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"fresh", anchor, false},
		{"19h", anchor.Add(19 * time.Hour), false},
		{"just under 20h", anchor.Add(20*time.Hour - time.Nanosecond), false},
		{"exactly 20h", anchor.Add(20 * time.Hour), false},
		{"20h + 1ns", anchor.Add(20*time.Hour + time.Nanosecond), true},
		{"23h", anchor.Add(23 * time.Hour), true},
		{"past the 24h window", anchor.Add(25 * time.Hour), true},
	}
	for _, testCase := range cases {
		if got := adapter.NeedsKeepalive(testCase.now); got != testCase.want {
			t.Errorf("NeedsKeepalive(%s) = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestNeedsKeepaliveUsesTheNewestInbound(t *testing.T) {
	adapter, server, _, clk := newFixture(t)
	server.pushMessage("m1", "user@im.wechat", "first", "token-1")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	clk.advance(15 * time.Hour)
	server.pushMessage("m2", "user@im.wechat", "second", "token-2")
	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	// 15h after the first message, but only 0s after the newest one.
	if adapter.NeedsKeepalive(clk.now().Add(2 * time.Hour)) {
		t.Fatal("NeedsKeepalive anchored on an older message instead of the newest")
	}
}
