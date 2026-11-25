package email_test

// TDD for the account state and the poll loop. The loop is the only thing that
// touches the mailbox on a schedule, so the tests pin the behaviours that keep a
// long-running product sane: it stops when told to, it does not hammer a dead
// server, and a rejected login is terminal instead of being retried forever.

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/email"
)

func TestStatusReportsWhetherTheMailboxIsConfigured(t *testing.T) {
	client := &fakeIMAPClient{}
	adapter, _ := pollFixture(t, client)

	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected {
		t.Fatalf("status = %+v, want Connected for a full configuration", status)
	}
	if !status.LastInbound.IsZero() {
		t.Fatalf("LastInbound = %v, want zero before any poll", status.LastInbound)
	}

	for _, mutate := range []func(*email.Config){
		func(cfg *email.Config) { cfg.Username = "" },
		func(cfg *email.Config) { cfg.Password = "" },
		func(cfg *email.Config) { cfg.From = "" },
	} {
		cfg := email.Config{Username: "bot@example.com", Password: "secret", From: "bot@example.com"}
		mutate(&cfg)
		partial := email.NewAdapter(newFakeInbox(), cfg)
		status, err := partial.Status(context.Background())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if status.Connected {
			t.Fatalf("status = %+v, want disconnected for an incomplete configuration", status)
		}
	}
}

func TestStatusRecordsTheLastInboundTime(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(rawMail("<s@example.com>", "owner@example.com", "subject", "body", ""))
	adapter, _ := pollFixture(t, client)

	if _, err := adapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.LastInbound.Equal(fixedNow) {
		t.Fatalf("LastInbound = %v, want %v", status.LastInbound, fixedNow)
	}

	// Nothing ingested (all messages skipped): the timestamp must not move.
	empty := &fakeIMAPClient{}
	emptyAdapter, _ := pollFixture(t, empty)
	if _, err := emptyAdapter.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if status, err := emptyAdapter.Status(context.Background()); err != nil || !status.LastInbound.IsZero() {
		t.Fatalf("status = (%+v, %v), want no inbound timestamp", status, err)
	}
}

// waitForItems waits until the inbox holds at least want items.
func waitForItems(t *testing.T, inbox *fakeInbox, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(inbox.snapshot()) >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("inbox never reached %d items", want)
}

func TestRunIngestsAndStopsOnCancel(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setMessages(
		rawMail("<run-1@example.com>", "owner@example.com", "first", "body one", ""),
		rawMail("<run-2@example.com>", "owner@example.com", "second", "body two", ""),
	)
	adapter, inbox := pollFixture(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Run(ctx, 5*time.Millisecond) }()

	waitForItems(t, inbox, 2)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was canceled")
	}
	items := inbox.snapshot()
	if len(items) != 2 {
		t.Fatalf("inbox holds %d items, want 2", len(items))
	}
	for _, item := range items {
		if item.Source != contracts.EmailSource {
			t.Fatalf("item source = %q, want email", item.Source)
		}
	}
	// The loop polls; it must never send mail on its own.
	if status, err := adapter.Status(context.Background()); err != nil || status.LastInbound.IsZero() {
		t.Fatalf("status = (%+v, %v), want an inbound timestamp", status, err)
	}
}

func TestRunReturnsImmediatelyWhenTheLoginIsRejected(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setError(errors.New("imap: LOGIN failed: [AUTHENTICATIONFAILED]"))
	adapter, _ := pollFixture(t, client)

	start := time.Now()
	err := adapter.Run(context.Background(), time.Millisecond)
	if !errors.Is(err, contracts.ErrEmailUnauthorized) {
		t.Fatalf("Run = %v, want ErrEmailUnauthorized", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run took %v to reject a rejected login", elapsed)
	}
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("IMAP client called %d times, want 1 (auth failure must not retry)", calls)
	}
}

func TestRunBacksOffWhileUnavailableAndStopsOnCancel(t *testing.T) {
	client := &fakeIMAPClient{}
	client.setError(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})
	adapter, _ := pollFixture(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Run(ctx, time.Millisecond) }()

	// Give the loop time to fail once and enter its 2s backoff, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored the cancellation while backing off")
	}
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("IMAP client called %d times during the backoff window, want 1", calls)
	}
}

func TestRunTreatsAnEmptyPollAsSuccess(t *testing.T) {
	client := &fakeIMAPClient{} // no messages: nothing to ingest, no error
	adapter, inbox := pollFixture(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Run(ctx, 2*time.Millisecond) }()

	deadline := time.Now().Add(2 * time.Second)
	for client.callCount() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if calls := client.callCount(); calls < 3 {
		t.Fatalf("IMAP client called %d times, want >= 3 (an empty poll must not back off)", calls)
	}
	if len(inbox.snapshot()) != 0 {
		t.Fatalf("inbox holds %d items, want 0", len(inbox.snapshot()))
	}
}
