package state

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitForSeq blocks until the projector has committed at least want events and
// returns that snapshot. It fails on overshoot or timeout.
func waitForSeq(t *testing.T, p *Projector, want uint64) *Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s := p.Snapshot()
		if s.Seq >= want {
			if s.Seq != want {
				t.Fatalf("seq = %d, want %d", s.Seq, want)
			}
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for seq %d; last seq %d", want, p.Snapshot().Seq)
	return nil
}

func TestProjectorPreservesEventOrder(t *testing.T) {
	b := NewBrokerWithBuffer(64)
	p := NewProjector(context.Background(), b)
	defer p.Close()

	var want string
	for i := 0; i < 10; i++ {
		text := strconv.Itoa(i)
		want += text
		b.Publish(Token{MsgID: "m", Text: text})
	}

	s := waitForSeq(t, p, 10)
	if len(s.Messages) != 1 || s.Messages[0].Text != want {
		t.Fatalf("messages = %+v, want text %q", s.Messages, want)
	}
	if got := b.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

func TestProjectorOnPublishPerBatch(t *testing.T) {
	const n = 8
	b := NewBrokerWithBuffer(32)
	p := NewProjector(context.Background(), b, WithBatchSize(1))
	defer p.Close()

	snaps := make(chan *Snapshot, n+8)
	p.OnPublish(func(s *Snapshot) { snaps <- s })
	for i := 0; i < n; i++ {
		b.Publish(Token{MsgID: "m", Text: "x"})
	}

	var seqs []uint64
	deadline := time.After(10 * time.Second)
	for len(seqs) < n {
		select {
		case s := <-snaps:
			seqs = append(seqs, s.Seq)
		case <-deadline:
			t.Fatalf("timed out; got %d callbacks, want %d", len(seqs), n)
		}
	}
	for i, seq := range seqs {
		if want := uint64(i + 1); seq != want {
			t.Fatalf("callback %d seq = %d, want %d (seqs %v)", i, seq, want, seqs)
		}
	}
	if got := p.Snapshot().Seq; got != n {
		t.Fatalf("final seq = %d, want %d", got, n)
	}
}

func TestProjectorOnPublishNilDisables(t *testing.T) {
	b := NewBrokerWithBuffer(8)
	p := NewProjector(context.Background(), b)
	defer p.Close()

	var calls atomic.Int64
	p.OnPublish(func(*Snapshot) { calls.Add(1) })
	p.OnPublish(nil)

	b.Publish(Token{MsgID: "m", Text: "x"})
	waitForSeq(t, p, 1)
	if got := calls.Load(); got != 0 {
		t.Fatalf("callbacks after OnPublish(nil) = %d, want 0", got)
	}
}

func TestProjectorSessionID(t *testing.T) {
	b := NewBrokerWithBuffer(8)
	p := NewProjector(context.Background(), b, WithSessionID("session-a"))
	defer p.Close()

	if got := p.Snapshot().SessionID; got != "session-a" {
		t.Fatalf("initial SessionID = %q, want session-a", got)
	}
	b.Publish(Token{MsgID: "m", Text: "a"})
	if s := waitForSeq(t, p, 1); s.SessionID != "session-a" {
		t.Fatalf("SessionID after token without SessionID = %q", s.SessionID)
	}
	b.Publish(Token{SessionID: "session-b", MsgID: "m", Text: "b"})
	if s := waitForSeq(t, p, 2); s.SessionID != "session-b" {
		t.Fatalf("SessionID after token with SessionID = %q", s.SessionID)
	}
}

func TestProjectorCloseStopsProjection(t *testing.T) {
	b := NewBrokerWithBuffer(8)
	p := NewProjector(context.Background(), b)

	b.Publish(Token{MsgID: "m", Text: "a"})
	waitForSeq(t, p, 1)

	p.Close()
	p.Close() // idempotent

	if s := p.Snapshot(); s.Seq != 1 || s.Messages[0].Text != "a" {
		t.Fatalf("snapshot after Close = %+v", s)
	}

	// After Close returns the consume goroutine is gone: a later publish can
	// never be projected.
	b.Publish(Token{MsgID: "m", Text: "b"})
	if s := p.Snapshot(); s.Seq != 1 || len(s.Messages[0].Text) != 1 {
		t.Fatalf("projection continued after Close: %+v", s)
	}
}

func TestProjectorContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := NewBrokerWithBuffer(8)
	p := NewProjector(ctx, b)

	cancel()
	p.Close() // must not deadlock even though the goroutine exits via ctx

	if s := p.Snapshot(); s.Seq != 0 {
		t.Fatalf("snapshot after context cancel = %+v", s)
	}
	b.Publish(Token{MsgID: "m", Text: "x"})
	if s := p.Snapshot(); s.Seq != 0 {
		t.Fatalf("projection continued after context cancel: %+v", s)
	}
}

// TestProjectorConcurrentPublish publishes from several goroutines and checks
// that the fold saw every event exactly once (bounds are big enough that drops
// are impossible, so Seq and text length must equal the publish count).
func TestProjectorConcurrentPublish(t *testing.T) {
	const publishers, perPublisher = 8, 200
	total := publishers * perPublisher

	b := NewBrokerWithBuffer(total + 16)
	p := NewProjector(context.Background(), b, WithBatchSize(64))
	defer p.Close()

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

	s := waitForSeq(t, p, uint64(total))
	if len(s.Messages) != 1 || len(s.Messages[0].Text) != total {
		t.Fatalf("message = %+v, want %d chars", s.Messages, total)
	}
	if got := b.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

// TestProjectorAtomicSnapshotConsistency hammers Snapshot from several reader
// goroutines while publishing; every snapshot must be internally consistent
// (Seq equals the number of folded tokens and never regresses), proving the
// atomic pointer swap exposes whole snapshots, never partial folds.
func TestProjectorAtomicSnapshotConsistency(t *testing.T) {
	const total = 600

	b := NewBrokerWithBuffer(total + 16)
	p := NewProjector(context.Background(), b, WithBatchSize(16))
	defer p.Close()

	const readers = 4
	errs := make(chan error, readers)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := p.Snapshot()
				if s == nil {
					errs <- fmt.Errorf("Snapshot returned nil")
					return
				}
				if s.Seq < last {
					errs <- fmt.Errorf("Seq regressed: %d -> %d", last, s.Seq)
					return
				}
				last = s.Seq
				if s.Seq == 0 {
					if len(s.Messages) != 0 {
						errs <- fmt.Errorf("seq 0 with %d messages", len(s.Messages))
						return
					}
				} else if len(s.Messages) != 1 ||
					s.Messages[0].MsgID != "m" ||
					uint64(len(s.Messages[0].Text)) != s.Seq {
					errs <- fmt.Errorf("inconsistent snapshot: seq=%d messages=%+v", s.Seq, s.Messages)
					return
				}
			}
		}()
	}

	for i := 0; i < total; i++ {
		b.Publish(Token{MsgID: "m", Text: "x"})
	}
	waitForSeq(t, p, total)
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestProjectorOnPublishCoalescesBatches(t *testing.T) {
	const n = 200
	b := NewBrokerWithBuffer(n + 16)
	p := NewProjector(context.Background(), b)
	defer p.Close()

	snaps := make(chan *Snapshot, n+8)
	p.OnPublish(func(s *Snapshot) { snaps <- s })
	for i := 0; i < n; i++ {
		b.Publish(Token{MsgID: "m", Text: "x"})
	}

	var seqs []uint64
	deadline := time.After(10 * time.Second)
	for {
		select {
		case s := <-snaps:
			seqs = append(seqs, s.Seq)
			if s.Seq == n {
				goto done
			}
		case <-deadline:
			t.Fatalf("timed out; callbacks %d, seqs %v", len(seqs), seqs)
		}
	}
done:
	if len(seqs) > n {
		t.Fatalf("%d callbacks for %d events", len(seqs), n)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("callback seqs not strictly increasing: %v", seqs)
		}
	}
	if last := seqs[len(seqs)-1]; last != n {
		t.Fatalf("last callback seq = %d, want %d", last, n)
	}
}
