package state

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBrokerPreservesOrderAndFansOut(t *testing.T) {
	b := NewBrokerWithBuffer(32)
	ch1, cancel1 := b.Subscribe()
	defer cancel1()
	ch2, cancel2 := b.Subscribe()
	defer cancel2()

	const n = 8
	for i := 0; i < n; i++ {
		b.Publish(Token{MsgID: "m", Text: strconv.Itoa(i)})
	}
	if got := b.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}

	for _, ch := range []<-chan Event{ch1, ch2} {
		for i := 0; i < n; i++ {
			select {
			case ev := <-ch:
				tok, ok := ev.(Token)
				if !ok {
					t.Fatalf("event %d: %T, want Token", i, ev)
				}
				if tok.Text != strconv.Itoa(i) {
					t.Fatalf("event %d: text %q, want %q", i, tok.Text, strconv.Itoa(i))
				}
			default:
				t.Fatalf("missing event %d", i)
			}
		}
	}
}

func TestBrokerSlowSubscriberDropAccounting(t *testing.T) {
	b := NewBrokerWithBuffer(2)
	ch, cancel := b.Subscribe()
	defer cancel()

	// Buffer holds 2 events; the remaining 3 are dropped.
	for i := 0; i < 5; i++ {
		b.Publish(Token{MsgID: "m", Text: strconv.Itoa(i)})
	}
	if got, want := b.Dropped(), uint64(3); got != want {
		t.Fatalf("Dropped = %d, want %d", got, want)
	}

	// Draining makes room again; publishing up to capacity adds no drops.
	first := (<-ch).(Token)
	second := (<-ch).(Token)
	if first.Text != "0" || second.Text != "1" {
		t.Fatalf("delivered order = %q, %q", first.Text, second.Text)
	}
	b.Publish(Token{MsgID: "m", Text: "a"})
	b.Publish(Token{MsgID: "m", Text: "b"})
	if got := b.Dropped(); got != 3 {
		t.Fatalf("Dropped after refill = %d, want 3", got)
	}

	// One more event overflows the two-slot buffer.
	b.Publish(Token{MsgID: "m", Text: "c"})
	if got := b.Dropped(); got != 4 {
		t.Fatalf("Dropped after overflow = %d, want 4", got)
	}
}

func TestBrokerClose(t *testing.T) {
	b := NewBrokerWithBuffer(2)
	ch, cancel := b.Subscribe()

	b.Publish(Token{MsgID: "m", Text: "one"})
	if ev := <-ch; ev.(Token).Text != "one" {
		t.Fatalf("first event = %+v", ev)
	}

	b.Close()
	if _, ok := <-ch; ok {
		t.Fatal("subscriber channel not closed by Close")
	}
	b.Publish(Token{MsgID: "m", Text: "late"}) // must not panic
	b.Close()                                  // idempotent
	cancel()                                   // idempotent after Close

	lateCh, lateCancel := b.Subscribe()
	if _, ok := <-lateCh; ok {
		t.Fatal("subscription after Close should be closed")
	}
	lateCancel()

	if got := b.Dropped(); got != 0 {
		t.Fatalf("publish after Close counted as drop: %d", got)
	}
}

func TestBrokerCancelUnsubscribes(t *testing.T) {
	b := NewBrokerWithBuffer(4)
	ch, cancel := b.Subscribe()
	cancel()
	cancel() // idempotent

	if _, ok := <-ch; ok {
		t.Fatal("cancel should close the channel")
	}

	b.Publish(Token{MsgID: "m", Text: "x"})
	if got := b.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d after unsubscribe, want 0", got)
	}
	b.Close()
}

func TestBrokerZeroValue(t *testing.T) {
	var b Broker
	ch, cancel := b.Subscribe()
	defer cancel()
	b.Publish(Token{MsgID: "m", Text: "x"})
	if ev := <-ch; ev.(Token).Text != "x" {
		t.Fatalf("zero-value broker event = %+v", ev)
	}
	b.Close()
	// A second Close and subscriptions after it must stay safe.
	b.Close()
	ch2, cancel2 := b.Subscribe()
	cancel2()
	if _, ok := <-ch2; ok {
		t.Fatal("closed channel expected")
	}
}

// TestBrokerConcurrentPublish drives Publish from several goroutines while a
// consumer reads; with -race it also proves the bus is data-race free.
func TestBrokerConcurrentPublish(t *testing.T) {
	b := NewBrokerWithBuffer(64)
	ch, cancel := b.Subscribe()
	defer cancel()

	var delivered atomic.Int64
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for range ch {
			delivered.Add(1)
		}
	}()

	const publishers, perPublisher = 4, 100
	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perPublisher; j++ {
				b.Publish(Token{MsgID: "m", Text: "x"})
			}
		}()
	}
	wg.Wait()

	// Close ends the reader after it has drained the buffered events.
	b.Close()
	<-readerDone

	total := int64(publishers * perPublisher)
	if got := delivered.Load() + int64(b.Dropped()); got != total {
		t.Fatalf("delivered(%d) + dropped(%d) = %d, want %d",
			delivered.Load(), b.Dropped(), got, total)
	}
}
