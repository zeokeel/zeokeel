package agent

// BE-13 (2026-11 audit): teamTrackers was a PACKAGE-LEVEL sync.Map with no
// Delete — every disposed service's tracker (notify closure + child-session
// store reference) lived for the process lifetime, violating the architecture
// rule "state belongs to the module instance; no package-level mutable state".
// The tracker now lives on the service instance: it is garbage-collected with
// the service, and the manager/adapter/tools still share the one tracker.

import (
	"runtime"
	"testing"
	"time"
	"weak"
)

func TestTeamTrackerIsInstanceState(t *testing.T) {
	svcA := newQueueService(t, nil, nil, nil)
	svcB := newQueueService(t, nil, nil, nil)
	trackerA := teamTrackerForService(svcA)
	trackerB := teamTrackerForService(svcB)
	if trackerA == nil || trackerB == nil {
		t.Fatal("tracker missing")
	}
	if trackerA == trackerB {
		t.Fatal("two services share one tracker")
	}
	if again := teamTrackerForService(svcA); again != trackerA {
		t.Fatal("tracker identity is unstable across calls")
	}
	// The tracker lives on the instance, not in a package-level table.
	svcA.mu.Lock()
	stored := svcA.teamTracker
	svcA.mu.Unlock()
	if stored != trackerA {
		t.Fatal("service does not own its tracker (BE-13: instance state)")
	}
}

// The manager, the team adapter and the wrapped model tools share the service's
// one tracker (the original purpose of the lookup table, now instance-owned).
func TestTeamManagerAdapterToolsShareTracker(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	manager := svc.teamManagerService()
	shared := teamTrackerForService(svc)
	adapter := newTeamService(svc)
	if adapter.tracker != shared {
		t.Fatal("team adapter does not share the service tracker")
	}
	tools := newTeamTools(svc, manager)
	wrapped := 0
	for _, tool := range tools {
		if wrapper, ok := tool.(*trackedTeamTool); ok {
			wrapped++
			if wrapper.tracker != shared {
				t.Fatalf("tool %q wraps a different tracker", tool.Name())
			}
		}
	}
	if wrapped == 0 {
		t.Fatal("no team tool was wrapped by the tracker")
	}
}

// A dropped service's tracker must be collectable: no package-level table may
// retain it (the old sync.Map had no Delete). A weak pointer observes
// collection without keeping the object alive — finalizers cannot be used
// here because the tracker↔service reference cycle suppresses them.
func TestTeamTrackerNotRetainedAfterServiceDrop(t *testing.T) {
	var wp weak.Pointer[teamTracker]
	func() {
		svc := newQueueService(t, nil, nil, nil)
		tracker := teamTrackerForService(svc)
		wp = weak.Make(tracker)
	}()
	for i := 0; i < 30 && wp.Value() != nil; i++ {
		runtime.GC()
		time.Sleep(2 * time.Millisecond)
	}
	if wp.Value() != nil {
		t.Fatal("BE-13: the tracker survived its service — a package-level table retains it (no Delete)")
	}
}
