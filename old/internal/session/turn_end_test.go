package session_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestTurnMarksPersistPerTurn pins the per-turn transcript markers: two turns
// in one session that end stopped and failed each keep their own TurnEnd mark
// (anchored to their user message), a normal completion adds none, and a reopen
// replays every mark.
func TestTurnMarksPersistPerTurn(t *testing.T) {
	var failing atomic.Bool
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if failing.Load() {
			return []runtime.Chunk{{Err: errors.New("provider exploded"), Done: true}}
		}
		if call == 0 {
			return []runtime.Chunk{{
				ToolCalls: []runtime.ToolCall{{ID: "call-blocked", Name: "block", Arguments: []byte(`{}`)}},
				Done:      true,
			}}
		}
		return []runtime.Chunk{{Text: "done", Done: true}}
	}}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		if err := cfg.Tools.Register(&blockingTestTool{started: make(chan struct{})}); err != nil {
			t.Fatalf("register block tool: %v", err)
		}
	})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "turn marks")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Turn 1: stop while the blocking tool runs.
	if err := h.ctrl.Submit(ctx, id, "first"); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.ctrl.Snapshot().Tools["call-blocked"]; ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.ctrl.Stop()
	h.ctrl.Wait()
	waitPhase(t, h.ctrl, state.PhaseIdle)

	// Turn 2: the provider fails.
	failing.Store(true)
	if err := h.ctrl.Submit(ctx, id, "second"); err != nil {
		t.Fatalf("Submit second: %v", err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && h.ctrl.Snapshot().Phase != state.PhaseFailed {
		time.Sleep(2 * time.Millisecond)
	}
	h.ctrl.Wait()

	messages, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var userIDs []string
	for _, m := range messages {
		if m.Role == string(state.RoleUser) {
			userIDs = append(userIDs, m.ID)
		}
	}
	if len(userIDs) != 2 {
		t.Fatalf("user messages = %d, want 2: %+v", len(userIDs), messages)
	}
	marks := h.ctrl.Snapshot().TurnMarks
	if got := marks[userIDs[0]]; got != state.ReasonStopped {
		t.Fatalf("turn 1 mark = %q, want stopped (marks %+v)", got, marks)
	}
	if got := marks[userIDs[1]]; got != state.ReasonFailed {
		t.Fatalf("turn 2 mark = %q, want failed (marks %+v)", got, marks)
	}

	// A normal completed turn must not add a marker.
	failing.Store(false)
	if err := h.ctrl.Submit(ctx, id, "third"); err != nil {
		t.Fatalf("Submit third: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	messages, err = h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages after third: %v", err)
	}
	var thirdUserID string
	for _, m := range messages {
		if m.Role == string(state.RoleUser) {
			thirdUserID = m.ID
		}
	}
	if _, ok := h.ctrl.Snapshot().TurnMarks[thirdUserID]; ok {
		t.Fatalf("completed turn was marked: %+v", h.ctrl.Snapshot().TurnMarks)
	}

	// Reopen: the TurnEnd events replay into the same per-turn marks.
	h.restart()
	reopened, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if got := reopened.TurnMarks[userIDs[0]]; got != state.ReasonStopped {
		t.Fatalf("reopened turn 1 mark = %q, want stopped (%+v)", got, reopened.TurnMarks)
	}
	if got := reopened.TurnMarks[userIDs[1]]; got != state.ReasonFailed {
		t.Fatalf("reopened turn 2 mark = %q, want failed (%+v)", got, reopened.TurnMarks)
	}
	// The persisted events carry the marker rows (no schema change: JSON payload).
	events, err := h.store.ListEvents(id)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	count := 0
	for _, ev := range events {
		if ev.Type == sessionEventTurnEnd {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("turn_end events = %d, want 2 (stopped + failed)", count)
	}
}

// sessionEventTurnEnd mirrors session.EventTurnEnd (the session_test package is
// external, so the wire name is repeated here).
const sessionEventTurnEnd = "turn_end"

// TestRecoverInterruptedSessionAddsTurnMark pins the crash-recovery marker: a
// session left running by a dead process records the last user message's
// interrupted marker, so the transcript can show it after a reopen.
func TestRecoverInterruptedSessionAddsTurnMark(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: usageRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "recover mark")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	user, err := h.store.AppendMessage(store.Message{SessionID: id, Role: string(state.RoleUser), Content: "crashed prompt"})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := h.store.RecordToolCall(store.ToolCall{ID: "call-open", SessionID: id, Name: "echo", Status: "running", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}
	if _, err := h.store.UpdateSessionStatus(id, store.SessionRunning); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got := snap.TurnMarks[user.ID]; got != state.ReasonInterrupted {
		t.Fatalf("recovered mark = %q, want interrupted (%+v)", got, snap.TurnMarks)
	}
}
