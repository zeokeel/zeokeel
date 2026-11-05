package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/state"
)

// TestTrySendDeliversTerminalEventWhenBufferFull is the RT-13 regression: a
// terminal event (the final Status that decides "stopped" vs "completed") must
// not be silently dropped just because the consumer was momentarily behind.
func TestTrySendDeliversTerminalEventWhenBufferFull(t *testing.T) {
	out := make(chan state.Event, 1)
	out <- state.Token{Text: "already queued"}
	sink := newEventSink(out)

	delivered := make(chan state.Event, 1)
	go func() {
		// Drain slowly enough that the non-blocking send cannot succeed.
		time.Sleep(20 * time.Millisecond)
		<-out
		delivered <- <-out
	}()
	sink.trySend(state.Status{Phase: state.PhaseIdle, Reason: state.ReasonStopped})

	select {
	case ev := <-delivered:
		status, ok := ev.(state.Status)
		if !ok || status.Reason != state.ReasonStopped {
			t.Fatalf("terminal event = %#v, want the stopped status", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal event was dropped although the consumer drained the channel")
	}
}

// TestTrySendBoundedWhenConsumerGone proves the reliability wait is bounded: a
// turn goroutine must not leak when the consumer really is gone.
func TestTrySendBoundedWhenConsumerGone(t *testing.T) {
	out := make(chan state.Event, 1)
	out <- state.Token{Text: "stuck"}
	sink := newEventSink(out)

	start := time.Now()
	sink.trySend(state.Status{Phase: state.PhaseIdle, Reason: state.ReasonStopped})
	elapsed := time.Since(start)
	if elapsed < terminalSendGrace {
		t.Fatalf("trySend returned after %v, want to wait up to %v for a full channel", elapsed, terminalSendGrace)
	}
	if elapsed > terminalSendGrace+3*time.Second {
		t.Fatalf("trySend blocked for %v, want a bounded wait near %v", elapsed, terminalSendGrace)
	}
	if len(out) != 1 {
		t.Fatalf("output buffer length = %d, want the original event only", len(out))
	}
}

// TestTrySendNoopAfterClose pins the close contract: a terminal send racing the
// end of the turn never blocks and never panics.
func TestTrySendNoopAfterClose(t *testing.T) {
	out := make(chan state.Event, 1)
	sink := newEventSink(out)
	done := make(chan struct{})
	go func() {
		sink.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sink.close blocked")
	}
	// The channel is closed; trySend must notice the closed flag instead of
	// attempting a send.
	sink.trySend(state.Status{Phase: state.PhaseIdle})
}

// TestApprovalEarlyDecisionExpires covers the unbounded `early` map: a decision
// buffered without a matching Wait is only trusted briefly, so a UI mistiming
// cannot permanently arm a call id.
func TestApprovalEarlyDecisionExpires(t *testing.T) {
	q := NewApprovalQueue(nil)
	if q.Approve("call-old") {
		t.Fatal("Approve without a waiter reported a blocked Wait")
	}
	q.mu.Lock()
	decision := q.early["call-old"]
	decision.at = time.Now().Add(-2 * earlyDecisionTTL)
	q.early["call-old"] = decision
	q.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if allow, err := q.Wait(ctx, "call-old"); allow || err == nil {
		t.Fatalf("Wait accepted a stale early decision: allow=%v err=%v", allow, err)
	} else if !errors.Is(err, ErrApprovalExpired) {
		t.Fatalf("Wait error = %v, want ErrApprovalExpired", err)
	}
	if _, ok := q.early["call-old"]; ok {
		t.Fatal("stale early decision was not removed")
	}
}

// TestApprovalEarlyBufferBounded caps the early buffer instead of letting a
// misbehaving caller grow it without bound.
func TestApprovalEarlyBufferBounded(t *testing.T) {
	q := NewApprovalQueue(nil)
	for i := 0; i < maxEarlyDecisions+50; i++ {
		q.Approve("call-" + string(rune('a'+i%26)) + "-" + time.Now().Format("150405.000000000") + string(rune('0'+i%10)))
	}
	q.mu.Lock()
	size := len(q.early)
	q.mu.Unlock()
	if size > maxEarlyDecisions {
		t.Fatalf("early buffer size = %d, want <= %d", size, maxEarlyDecisions)
	}
}
