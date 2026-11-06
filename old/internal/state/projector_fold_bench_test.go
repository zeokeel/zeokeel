package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkProjectorFoldEvent measures the per-event cost of the projector's
// own event stream (channel receive + fold + immutable snapshot publish). The
// production UI does not read this stream, so this is the duplicated work AR-05
// removes from the application wiring.
func BenchmarkProjectorFoldEvent(b *testing.B) {
	const events = 512
	broker := NewBrokerWithBuffer(events + 1)
	projector := NewProjector(context.Background(), broker, WithBatchSize(events+1))
	b.Cleanup(projector.Close)
	b.ReportAllocs()
	b.ResetTimer()
	published := 0
	for i := 0; i < b.N; i++ {
		broker.Publish(Token{SessionID: "bench", MsgID: "m", Text: "x"})
		published++
		if published%events == 0 {
			waitProjected(b, projector, published)
		}
	}
	waitProjected(b, projector, published)
}

// BenchmarkApplyFoldEvent measures folding the same event directly through the
// shared fold function that the controller uses for the UI snapshot.
func BenchmarkApplyFoldEvent(b *testing.B) {
	var snap *Snapshot
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if snap == nil {
			snap = NewSnapshot()
		}
		snap = Apply(snap, Token{SessionID: "bench", MsgID: "m", Text: "x"})
	}
}

// waitProjected blocks until the projector applied want events or fails the
// benchmark after a bounded wait.
func waitProjected(b *testing.B, p *Projector, want int) {
	b.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.Snapshot().Seq >= uint64(want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	b.Fatalf("projector did not fold %s events", fmt.Sprint(want))
}
