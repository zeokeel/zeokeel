package agent

// BE-05 (2026-11 audit): handleEvent used to run synchronous SQLite
// transactions (store.AppendEvent) INSIDE the s.mu critical section, against
// the package's own discipline ("a store call must not run while holding
// s.mu"). On a contended/slow disk the busy_timeout wait blocks every reader
// of the whole read model. The durable writes now happen after the critical
// section, in emission order.
//
// The probe store tries to take s.mu from inside AppendEvent (TryLock): while
// the write runs under the lock the try fails — a deterministic, deadlock-free
// witness of the violation.

import (
	"context"
	"sync/atomic"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type lockProbeStore struct {
	*fakeStore
	svc        *service
	violations atomic.Int64
	probed     atomic.Int64
}

func (s *lockProbeStore) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	switch event.Type {
	case "tool_call", "tool_result", "spill":
		s.probed.Add(1)
		if s.svc.mu.TryLock() {
			s.svc.mu.Unlock()
		} else {
			s.violations.Add(1)
		}
	}
	return s.fakeStore.AppendEvent(ctx, event)
}

func TestHandleEventStoreWritesOutsideLock(t *testing.T) {
	probe := &lockProbeStore{fakeStore: newFakeStore()}
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, probe, fakeApprovals{})
	probe.svc = svc
	id, err := svc.NewSession("lock-probe")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	state := svc.sessions[id]
	state.messages = append(state.messages, contracts.ChatMessage{
		ID: "m-a", Role: contracts.ChatRoleAssistant, Streaming: true,
	})
	state.assistant = len(state.messages) - 1
	svc.mu.Unlock()

	ctx := context.Background()
	call := &rig.ToolCall{ID: "call-1", Name: "bash", Arguments: `{"command":"ls"}`}
	svc.handleEvent(ctx, id, "", rig.Event{Type: rig.EventToolCall, Iteration: 1, Call: call}, nil)
	// A spilled result exercises the third in-lock write path (spill event).
	spilled := rig.SpillSummaryPrefix + "\npath: /tmp/x/tool-results/call-1.txt\nbytes: 99999\npreview: head"
	svc.handleEvent(ctx, id, "", rig.Event{
		Type: rig.EventToolResult, Iteration: 1, Call: call,
		Result: &rig.ToolResult{Content: spilled},
	}, nil)

	if probe.probed.Load() != 3 {
		t.Fatalf("probed %d durable writes, want 3 (tool_call + tool_result + spill)", probe.probed.Load())
	}
	if got := probe.violations.Load(); got != 0 {
		t.Fatalf("BE-05: %d store writes ran while s.mu was held, want 0 (move the transactions out of the critical section)", got)
	}
	// The events still land durably, in order.
	events, err := probe.fakeStore.Events(ctx, id, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	want := []string{"tool_call", "tool_result", "spill"}
	if len(types) != len(want) {
		t.Fatalf("event types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event order = %v, want %v", types, want)
		}
	}
}
