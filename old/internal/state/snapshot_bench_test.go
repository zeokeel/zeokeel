package state

// AR-22 micro-benchmarks: a long session (many messages) receiving a long
// streaming answer (many Token events). The before/after comparison lives in
// snapshot_batch_test.go's consistency tests plus these benchmarks:
//
//	apply_loop = one Apply per event (the historical path, kept as the reference)
//	batch      = ApplyBatch over the same events (incremental chunk builder)

import (
	"fmt"
	"testing"
)

// longSessionSnapshot builds a snapshot with nMessages messages, each carrying
// roughly one line of text, so the per-event fold has to walk a realistic
// message slice.
func longSessionSnapshot(nMessages int) *Snapshot {
	snap := Apply(nil, Token{SessionID: "bench", MsgID: "m-0", Text: "seed"})
	for i := 1; i < nMessages; i++ {
		snap = Apply(snap, Token{SessionID: "bench", MsgID: fmt.Sprintf("m-%d", i), Text: "line"})
	}
	return snap
}

// streamingTokenEvents appends n tokens (each 10 bytes) to one message id.
func streamingTokenEvents(msgID string, n int) []Event {
	events := make([]Event, 0, n)
	token := "abcdefghij"
	for i := 0; i < n; i++ {
		events = append(events, Token{SessionID: "bench", MsgID: msgID, Text: token})
	}
	return events
}

func benchmarkApplyLoop(b *testing.B, base *Snapshot, events []Event) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		snap := base
		for _, ev := range events {
			snap = Apply(snap, ev)
		}
		if len(snap.Messages) == 0 {
			b.Fatal("empty snapshot")
		}
	}
}

func benchmarkApplyBatch(b *testing.B, base *Snapshot, events []Event) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		snap := ApplyBatch(base, events)
		if len(snap.Messages) == 0 {
			b.Fatal("empty snapshot")
		}
	}
}

// benchmarkProjectorBursts folds the events the way Projector.run does: up to
// 256 events per batch (the projector batch limit), one ApplyBatch per burst.
func benchmarkProjectorBursts(b *testing.B, base *Snapshot, events []Event, burst int) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		snap := base
		for start := 0; start < len(events); start += burst {
			end := start + burst
			if end > len(events) {
				end = len(events)
			}
			snap = ApplyBatch(snap, events[start:end])
		}
		if len(snap.Messages) == 0 {
			b.Fatal("empty snapshot")
		}
	}
}

func BenchmarkApplyTokenStream(b *testing.B) {
	base := longSessionSnapshot(1000)
	events := streamingTokenEvents("m-999", 10000)
	b.Run("apply_loop_reference", func(b *testing.B) { benchmarkApplyLoop(b, base, events) })
	b.Run("batch_10k", func(b *testing.B) { benchmarkApplyBatch(b, base, events) })
	b.Run("projector_bursts_256", func(b *testing.B) { benchmarkProjectorBursts(b, base, events, 256) })
}
