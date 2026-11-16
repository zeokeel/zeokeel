package agent

// BE-08 (2026-11 audit): newSessionIDLocked busy-waited on the clock — when the
// generated nano collided with an existing session it re-read time.Now() in a
// loop, spinning under the caller-held s.mu until the clock left the occupied
// region (probe: 650 iterations / 500µs; under a frozen clock it never
// terminates). The nextSessionSeq counter was dead code. Collisions now
// resolve by advancing a monotonic sequence: one map lookup per step, no
// clock re-read, bounded by the number of occupied IDs.

import (
	"fmt"
	"testing"
	"time"
)

// TestNewSessionIDMonotonicUnderFrozenClock freezes the ID clock and pre-seeds
// a dense occupied nano region (the restored-session collision shape). The
// generator must walk past it immediately instead of spinning on the clock.
func TestNewSessionIDMonotonicUnderFrozenClock(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	const base = int64(1700000000000000000)
	svc.sessionIDNow = func() int64 { return base }
	svc.mu.Lock()
	for i := int64(0); i < 1000; i++ {
		id := fmt.Sprintf("sess-%d", base+i)
		svc.sessions[id] = &sessionState{id: id, assistant: -1}
	}
	svc.mu.Unlock()

	start := time.Now()
	svc.mu.Lock()
	id := svc.newSessionIDLocked()
	svc.mu.Unlock()
	elapsed := time.Since(start)
	if want := fmt.Sprintf("sess-%d", base+1000); id != want {
		t.Fatalf("ID = %q, want %q (monotonic escape past the occupied region)", id, want)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("BE-08: ID generation busy-waited %v under a frozen clock", elapsed)
	}
	// Further calls keep the sequence strictly increasing and unique even
	// though the clock never advances.
	previous := base + 1000
	for i := 0; i < 50; i++ {
		svc.mu.Lock()
		next := svc.newSessionIDLocked()
		svc.sessions[next] = &sessionState{id: next, assistant: -1}
		svc.mu.Unlock()
		var nano int64
		if _, err := fmt.Sscanf(next, "sess-%d", &nano); err != nil {
			t.Fatalf("ID %q is not sess-<nano>: %v", next, err)
		}
		if nano <= previous {
			t.Fatalf("ID %q not monotonic (previous %d)", next, previous)
		}
		previous = nano
	}
}

// Control: without collisions the ID is the clock value itself (the format and
// the fast path are unchanged).
func TestNewSessionIDNoCollisionUsesClock(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	const base = int64(1700000000000000000)
	svc.sessionIDNow = func() int64 { return base }
	svc.mu.Lock()
	id := svc.newSessionIDLocked()
	svc.mu.Unlock()
	if want := fmt.Sprintf("sess-%d", base); id != want {
		t.Fatalf("ID = %q, want %q", id, want)
	}
}

// A rewound clock never reissues an ID: the monotonic floor wins over the
// clock value.
func TestNewSessionIDRewoundClockStaysMonotonic(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	clock := int64(1700000000000000000)
	svc.sessionIDNow = func() int64 { return clock }
	svc.mu.Lock()
	first := svc.newSessionIDLocked()
	svc.sessions[first] = &sessionState{id: first, assistant: -1}
	clock -= 5_000_000_000 // five seconds backwards
	second := svc.newSessionIDLocked()
	svc.mu.Unlock()
	if second == first {
		t.Fatalf("rewound clock reissued %q", first)
	}
	var a, b int64
	fmt.Sscanf(first, "sess-%d", &a)
	fmt.Sscanf(second, "sess-%d", &b)
	if b <= a {
		t.Fatalf("IDs not monotonic across a clock rewind: %q then %q", first, second)
	}
}

// Real-clock regression: many sessions never collide.
func TestNewSessionIDsUnique(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := svc.NewSession("")
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate session ID %q", id)
		}
		seen[id] = true
	}
}
