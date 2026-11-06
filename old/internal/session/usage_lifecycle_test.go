package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// blockingTestTool never returns until the turn is cancelled, so a test can
// hold a tool call open and observe the Stop path close it.
type blockingTestTool struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingTestTool) Name() string        { return "block" }
func (b *blockingTestTool) Description() string { return "Blocks until cancelled." }
func (b *blockingTestTool) Invoke(ctx context.Context, _ json.RawMessage) (string, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return "", ctx.Err()
}

// usageRespond answers every model call with one text chunk that reports usage,
// so a completed turn advances the cumulative counters by one turn and one
// step.
func usageRespond(_ context.Context, _ runtime.Request, _ int) []runtime.Chunk {
	return []runtime.Chunk{{
		Text: "answer",
		Usage: &runtime.Usage{
			InputTokens: 7, OutputTokens: 3, TotalTokens: 10, CachedInputTokens: 2,
		},
		Done: true,
	}}
}

// TestUsageIsCumulativeLiveAndAfterReopen pins the live footer contract and its
// replay: usage is session-cumulative across turns on the live snapshot and the
// reopened snapshot that rebuilds from the persisted deltas.
func TestUsageIsCumulativeLiveAndAfterReopen(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: usageRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "usage")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, prompt := range []string{"first", "second"} {
		if err := h.ctrl.Submit(ctx, id, prompt); err != nil {
			t.Fatalf("Submit(%q): %v", prompt, err)
		}
		waitIdleAndDrain(t, h.ctrl)
	}
	live := h.ctrl.Snapshot().Usage
	if live.Turns != 2 || live.Steps != 2 || live.In != 14 || live.Out != 6 || live.CacheHit != 4 {
		t.Fatalf("live cumulative usage = %+v, want 2 turns / 2 steps / 14 in / 6 out / 4 cached", live)
	}

	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if snap.Usage != live {
		t.Fatalf("reopened usage = %+v, want live cumulative %+v", snap.Usage, live)
	}
	if snap.Phase != state.PhaseIdle || snap.TurnReason != state.ReasonNone {
		t.Fatalf("reopened ended session = phase %q reason %q", snap.Phase, snap.TurnReason)
	}
}

// TestStopClosesPendingToolAndKeepsNoEmptyDraft pins the BUG-6/Stop fix: the
// cancelled turn closes its pending tool call (no spinning card, no dangling
// tool_calls row), persists the stopped reason, and does not leave a
// reasoning-only assistant row behind. A reopen replays the same state.
func TestStopClosesPendingToolAndKeepsNoEmptyDraft(t *testing.T) {
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if call == 0 {
			return []runtime.Chunk{
				{Reasoning: "I should inspect the workspace first."},
				{
					ToolCalls: []runtime.ToolCall{{
						ID: "call-blocked", Name: "block", Arguments: json.RawMessage(`{}`),
					}},
					Done: true,
				},
			}
		}
		return []runtime.Chunk{{Text: "done", Done: true}}
	}}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		if err := cfg.Tools.Register(&blockingTestTool{started: make(chan struct{})}); err != nil {
			t.Fatalf("register block tool: %v", err)
		}
	})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "stop-clean")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "keep going"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitPhase(t, h.ctrl, state.PhaseRunning)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.ctrl.Snapshot().Tools["call-blocked"]; ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.ctrl.Stop()
	h.ctrl.Wait()

	snap := h.ctrl.Snapshot()
	if snap.Phase != state.PhaseIdle || snap.TurnReason != state.ReasonStopped {
		t.Fatalf("after stop = phase %q reason %q, want idle/stopped", snap.Phase, snap.TurnReason)
	}
	tool, ok := snap.Tools["call-blocked"]
	if !ok || tool.Status != state.ToolCancelled || tool.IsError {
		t.Fatalf("stopped tool = %+v, ok=%v; want cancelled", tool, ok)
	}
	// The reasoning-only assistant draft must not be persisted as an empty row.
	messages, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("messages after stop = %+v, want only the user prompt", messages)
	}
	calls, err := h.store.ToolCallsForSession(id)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) != 1 || calls[0].Status != "cancelled" || calls[0].Result == "" {
		t.Fatalf("tool_calls rows after stop = %+v, want one closed cancelled call", calls)
	}
	events, err := h.store.ListEvents(id)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var sawStopped, sawCancelled bool
	for _, ev := range events {
		if ev.Type != session.EventStatus && ev.Type != session.EventToolEnd {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(ev.Payload, &payload) != nil {
			continue
		}
		if payload["reason"] == string(state.ReasonStopped) {
			sawStopped = true
		}
		if cancelled, _ := payload["cancelled"].(bool); cancelled {
			sawCancelled = true
		}
	}
	if !sawStopped || !sawCancelled {
		t.Fatalf("persisted events missing stopped reason (%v) or cancelled tool end (%v)", sawStopped, sawCancelled)
	}

	// Reopen: no crash recovery is needed (the turn closed cleanly), but the
	// reason and the cancelled card must come back from the persisted events.
	h.restart()
	reopened, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after stop: %v", err)
	}
	if reopened.Phase != state.PhaseIdle || reopened.TurnReason != state.ReasonStopped {
		t.Fatalf("reopened stopped turn = phase %q reason %q", reopened.Phase, reopened.TurnReason)
	}
	if got := reopened.Tools["call-blocked"].Status; got != state.ToolCancelled {
		t.Fatalf("reopened stopped tool status = %q, want cancelled", got)
	}
}

// TestOpenSessionRecoversInterruptedRunningSession simulates a process that
// died mid-turn (session row left running, an open tool call and no terminal
// status event) and pins the crash repair: the session opens idle with an
// interrupted marker and a closed tool call, and the repair is persisted.
func TestOpenSessionRecoversInterruptedRunningSession(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: usageRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "crash")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// A tool start event and its tool_calls row, as a crashed turn leaves them.
	payload, err := json.Marshal(map[string]any{"call_id": "call-open", "name": "echo", "args": map[string]any{}})
	if err != nil {
		t.Fatalf("marshal tool_start: %v", err)
	}
	if _, err := h.store.AppendEvent(id, store.Event{Type: session.EventToolStart, Payload: payload}); err != nil {
		t.Fatalf("AppendEvent(tool_start): %v", err)
	}
	if err := h.store.RecordToolCall(store.ToolCall{
		ID: "call-open", SessionID: id, Name: "echo", Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}
	if _, err := h.store.UpdateSessionStatus(id, store.SessionRunning); err != nil {
		t.Fatalf("UpdateSessionStatus(running): %v", err)
	}

	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after crash: %v", err)
	}
	if snap.Phase != state.PhaseIdle || snap.TurnReason != state.ReasonInterrupted {
		t.Fatalf("recovered session = phase %q reason %q, want idle/interrupted", snap.Phase, snap.TurnReason)
	}
	tool, ok := snap.Tools["call-open"]
	if !ok || tool.Status != state.ToolError || tool.Result == "" {
		t.Fatalf("recovered tool = %+v, ok=%v; want a closed error result", tool, ok)
	}
	sess, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Status != store.SessionIdle {
		t.Fatalf("persisted session status = %q, want idle", sess.Status)
	}
	calls, err := h.store.ToolCallsForSession(id)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) != 1 || calls[0].Status != "error" || calls[0].Result == "" {
		t.Fatalf("recovered tool_calls row = %+v, want closed error row", calls)
	}
	// The repair events are durable: a second open is a no-op and replays the
	// same state.
	h.restart()
	again, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("second OpenSession: %v", err)
	}
	if again.TurnReason != state.ReasonInterrupted || again.Tools["call-open"].Status != state.ToolError {
		t.Fatalf("second open = reason %q tool %+v", again.TurnReason, again.Tools["call-open"])
	}
}

// TestSubmitRecoversInterruptedSessionBeforeNewTurn covers the submit path: a
// session left "running" by a crash is repaired before the new turn starts, so
// its history sees the interrupted tool as closed and the view never shows a
// phantom running phase.
func TestSubmitRecoversInterruptedSessionBeforeNewTurn(t *testing.T) {
	provider := &fakeProvider{respond: usageRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "crash-submit")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"call_id": "call-open", "name": "echo", "args": map[string]any{}})
	if _, err := h.store.AppendEvent(id, store.Event{Type: session.EventToolStart, Payload: payload}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := h.store.RecordToolCall(store.ToolCall{
		ID: "call-open", SessionID: id, Name: "echo", Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}
	if _, err := h.store.UpdateSessionStatus(id, store.SessionRunning); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	h.restart()

	if err := h.ctrl.Submit(ctx, id, "after crash"); err != nil {
		t.Fatalf("Submit after crash: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	snap := h.ctrl.Snapshot()
	if snap.Phase != state.PhaseIdle {
		t.Fatalf("phase after recovery submit = %q, want idle", snap.Phase)
	}
	if got := snap.Tools["call-open"].Status; got != state.ToolError {
		t.Fatalf("tool after recovery submit = %q, want error", got)
	}
	// The next turn's model request must not carry the dangling call as an
	// empty tool result; the recovered row is a real error result.
	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider saw no request")
	}
	for _, msg := range req.Messages {
		if msg.Role == runtime.RoleTool && msg.ToolCallID == "call-open" {
			if strings.TrimSpace(msg.Content) == "" {
				t.Fatalf("recovered tool result is empty: %+v", msg)
			}
		}
	}
}

// TestStopCallsTeamStopper pins the non-UI Stop hook: a session's asynchronous
// team members outlive the turn slot, so every Stop request for the current
// session calls the registered stopper even when no turn is in flight. The app
// wires it to the agent-team manager so API/automation stops behave like the
// shell's Stop.
func TestStopCallsTeamStopper(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: usageRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "team-stop")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	calls := make(chan string, 4)
	h.ctrl.SetTeamStopper(func(sessionID string) { calls <- sessionID })

	h.ctrl.Stop() // no active turn: a lingering async team still stops
	select {
	case got := <-calls:
		if got != id {
			t.Fatalf("team stopper session = %q, want %q", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not call the team stopper")
	}

	// A Stop during a running turn calls it too, alongside the stopped reason.
	h.ctrl.Stop()
	select {
	case got := <-calls:
		if got != id {
			t.Fatalf("second team stopper session = %q, want %q", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Stop did not call the team stopper")
	}
}
