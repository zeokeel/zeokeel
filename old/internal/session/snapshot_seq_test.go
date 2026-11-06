package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// seqToolRespond scripts two turns, each with one tool call followed by its
// final answer, so a reopened snapshot has messages and tool calls whose Seq
// fields can be compared.
func seqToolRespond(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
	switch call {
	case 0:
		return []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-1",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"first.md"}`),
			}},
			Done: true,
		}}
	case 1:
		return []runtime.Chunk{{Text: "first answer", Done: true}}
	case 2:
		return []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-2",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"second.md"}`),
			}},
			Done: true,
		}}
	default:
		return []runtime.Chunk{{Text: "second answer", Done: true}}
	}
}

// runTwoToolTurns submits two prompts and waits for each turn to finish.
func runTwoToolTurns(t *testing.T, ctrl *session.Controller, id string) {
	t.Helper()
	ctx := context.Background()
	for _, prompt := range []string{"first question", "second question"} {
		if err := ctrl.Submit(ctx, id, prompt); err != nil {
			t.Fatalf("Submit(%q): %v", prompt, err)
		}
		waitIdleAndDrain(t, ctrl)
	}
}

// eventSeqWith returns the seq of the first store event of type typ whose JSON
// payload has key == value (for example tool_start with call_id "call-1").
func eventSeqWith(t *testing.T, events []store.Event, typ, key, value string) int64 {
	t.Helper()
	for _, ev := range events {
		if ev.Type != typ {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(ev.Payload, &payload) != nil {
			continue
		}
		if payload[key] == value {
			return ev.Seq
		}
	}
	t.Fatalf("no %s event with %s=%q in %d events", typ, key, value, len(events))
	return 0
}

// assertMessagesAndToolsShareAxis checks the ordering invariant the Chat view
// will rely on: messages are strictly increasing on the Seq axis, and each
// turn's tool call starts after that turn's user message, before its final
// assistant answer, and before the next turn's user message.
func assertMessagesAndToolsShareAxis(t *testing.T, snap *state.Snapshot) {
	t.Helper()
	if len(snap.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (two turns)", len(snap.Messages))
	}
	if len(snap.Tools) != 2 {
		t.Fatalf("tools = %d, want 2 (one per turn)", len(snap.Tools))
	}
	for i := 1; i < len(snap.Messages); i++ {
		if snap.Messages[i].Seq <= snap.Messages[i-1].Seq {
			t.Fatalf("message Seqs not strictly increasing: %+v", snap.Messages)
		}
	}
	user1, answer1 := snap.Messages[0], snap.Messages[1]
	user2, answer2 := snap.Messages[2], snap.Messages[3]
	tool1, ok := snap.Tools["call-1"]
	if !ok {
		t.Fatalf("turn-1 tool call-1 missing: %+v", snap.Tools)
	}
	tool2, ok := snap.Tools["call-2"]
	if !ok {
		t.Fatalf("turn-2 tool call-2 missing: %+v", snap.Tools)
	}

	if user1.Role != state.RoleUser || user2.Role != state.RoleUser ||
		answer1.Role != state.RoleAssistant || answer2.Role != state.RoleAssistant {
		t.Fatalf("message roles = %q,%q,%q,%q", user1.Role, answer1.Role, user2.Role, answer2.Role)
	}
	// The full turn interleaving on one axis:
	//   user1 < turn-1 tool < answer1 < user2 < turn-2 tool < answer2.
	if !(user1.Seq < tool1.StartedSeq && tool1.StartedSeq < answer1.Seq &&
		answer1.Seq < user2.Seq && user2.Seq < tool2.StartedSeq &&
		tool2.StartedSeq < answer2.Seq) {
		t.Fatalf("seq axis not interleaved: user1=%d tool1=%d answer1=%d user2=%d tool2=%d answer2=%d",
			user1.Seq, tool1.StartedSeq, answer1.Seq, user2.Seq, tool2.StartedSeq, answer2.Seq)
	}

	// Turn grouping derived only from the user messages' Seq: the first tool
	// belongs to turn 1, the second to turn 2.
	var turn1Tools, turn2Tools []string
	for callID, tool := range snap.Tools {
		if tool.StartedSeq < user2.Seq {
			turn1Tools = append(turn1Tools, callID)
		} else {
			turn2Tools = append(turn2Tools, callID)
		}
	}
	if len(turn1Tools) != 1 || turn1Tools[0] != "call-1" || len(turn2Tools) != 1 || turn2Tools[0] != "call-2" {
		t.Fatalf("turn grouping = %v / %v, want [call-1] / [call-2]", turn1Tools, turn2Tools)
	}
}

// logSeqAxis prints the concrete Seq values so a reviewer can check the axis
// without rerunning the harness by hand (the report quotes these numbers).
func logSeqAxis(t *testing.T, label string, snap *state.Snapshot) {
	t.Helper()
	t.Logf("%s snapshot Seq=%d", label, snap.Seq)
	for _, m := range snap.Messages {
		t.Logf("%s message %s %s Seq=%d", label, m.Role, m.MsgID, m.Seq)
	}
	for callID, tool := range snap.Tools {
		t.Logf("%s tool %s StartedSeq=%d UpdatedSeq=%d status=%s",
			label, callID, tool.StartedSeq, tool.UpdatedSeq, tool.Status)
	}
}

// TestSnapshotSeqAxisAfterReopen pins the data-model half of the Chat ordering
// fix: messages and tool calls share one Seq axis both while live and after the
// session is reopened from the store, where the axis is the persisted event seq.
func TestSnapshotSeqAxisAfterReopen(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: seqToolRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "seq axis")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	runTwoToolTurns(t, h.ctrl, id)

	// Live: Fold assigns Message.Seq on first appearance and ToolState.
	// StartedSeq on ToolStart, both from the same Snapshot.Seq counter.
	live := h.ctrl.Snapshot()
	assertMessagesAndToolsShareAxis(t, live)
	if got := live.Messages[0].Seq; got != 1 {
		t.Fatalf("live first user message Seq = %d, want the addUserMessage fold Seq 1", got)
	}
	logSeqAxis(t, "live", live)

	// Reopen over the same store: Seq must now be the persisted event seq and
	// the recovered message Seq must match the events that created each row.
	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	events, err := h.ctrl.Events(ctx, id)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	assertMessagesAndToolsShareAxis(t, snap)

	// User messages: Seq is the "message" event seq of that message id.
	// Assistant messages: Seq is the first token event seq of that msg id.
	user1, answer1 := snap.Messages[0], snap.Messages[1]
	user2, answer2 := snap.Messages[2], snap.Messages[3]
	if want := uint64(eventSeqWith(t, events, session.EventMessage, "id", user1.MsgID)); user1.Seq != want {
		t.Fatalf("turn-1 user Seq = %d, want its message event seq %d", user1.Seq, want)
	}
	if want := uint64(eventSeqWith(t, events, session.EventMessage, "id", user2.MsgID)); user2.Seq != want {
		t.Fatalf("turn-2 user Seq = %d, want its message event seq %d", user2.Seq, want)
	}
	if want := uint64(eventSeqWith(t, events, session.EventToken, "msg_id", answer1.MsgID)); answer1.Seq != want {
		t.Fatalf("turn-1 answer Seq = %d, want its first token seq %d", answer1.Seq, want)
	}
	if want := uint64(eventSeqWith(t, events, session.EventToken, "msg_id", answer2.MsgID)); answer2.Seq != want {
		t.Fatalf("turn-2 answer Seq = %d, want its first token seq %d", answer2.Seq, want)
	}
	// Tool calls: StartedSeq is the persisted tool_start event seq, not a
	// replay counter (the old behavior was 1,2,... across the replay).
	for _, callID := range []string{"call-1", "call-2"} {
		want := uint64(eventSeqWith(t, events, session.EventToolStart, "call_id", callID))
		if got := snap.Tools[callID].StartedSeq; got != want {
			t.Fatalf("tool %s StartedSeq = %d, want store tool_start seq %d", callID, got, want)
		}
	}
	var eventSummary []string
	for _, ev := range events {
		eventSummary = append(eventSummary, fmt.Sprintf("%d:%s", ev.Seq, ev.Type))
	}
	t.Logf("reopened store events: %v", eventSummary)
	logSeqAxis(t, "reopened", snap)
}

// TestSnapshotSeqFallbackForOrphanMessage pins the monotonic fallback: a
// message row with no matching event (a legacy/crash row) still gets a Seq
// after the previous message instead of 0.
func TestSnapshotSeqFallbackForOrphanMessage(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: seqToolRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "seq fallback")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	runTwoToolTurns(t, h.ctrl, id)

	// A row written without a message/token event: replay finds no seq for it.
	orphan, err := h.store.AppendMessage(store.Message{
		SessionID: id,
		Role:      string(state.RoleAssistant),
		Content:   "orphan row",
		TurnID:    "legacy-turn",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if len(snap.Messages) != 5 {
		t.Fatalf("messages after orphan row = %d, want 5", len(snap.Messages))
	}
	last, prev := snap.Messages[4], snap.Messages[3]
	if last.MsgID != orphan.ID || last.Text != "orphan row" {
		t.Fatalf("last message = %+v, want the orphan row %q", last, orphan.ID)
	}
	if last.Seq != prev.Seq+1 {
		t.Fatalf("orphan message Seq = %d, want previous Seq %d + 1", last.Seq, prev.Seq)
	}
	t.Logf("fallback Seq: previous %s Seq=%d, orphan %s Seq=%d",
		prev.MsgID, prev.Seq, last.MsgID, last.Seq)
	for i := 1; i < len(snap.Messages); i++ {
		if snap.Messages[i].Seq <= snap.Messages[i-1].Seq {
			t.Fatalf("message Seqs not strictly increasing: %+v", snap.Messages)
		}
	}
}

// reasoningFirstRespond scripts two turns whose assistant message is created by
// the Reasoning event that precedes the turn's tool call and finished by the
// text that follows it. The runtime publishes one msgID per turn, so this is
// the real shape of a "think, call a tool, then answer" turn.
func reasoningFirstRespond(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
	switch call {
	case 0:
		return []runtime.Chunk{{
			Reasoning: "thinking before the first call",
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-1",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"first.md"}`),
			}},
			Done: true,
		}}
	case 1:
		return []runtime.Chunk{{Text: "first answer", Done: true}}
	case 2:
		return []runtime.Chunk{{
			Reasoning: "thinking before the second call",
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-2",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"second.md"}`),
			}},
			Done: true,
		}}
	default:
		return []runtime.Chunk{{Text: "second answer", Done: true}}
	}
}

// eventLastSeqWith returns the seq of the last store event of type typ whose
// JSON payload has key == value (the visible-text end of an assistant message).
func eventLastSeqWith(t *testing.T, events []store.Event, typ, key, value string) int64 {
	t.Helper()
	var last int64
	for _, ev := range events {
		if ev.Type != typ {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(ev.Payload, &payload) != nil {
			continue
		}
		if payload[key] == value {
			last = ev.Seq
		}
	}
	if last == 0 {
		t.Fatalf("no %s event with %s=%q in %d events", typ, key, value, len(events))
	}
	return last
}

// assertLastSeqInterleaving pins the data-model half of the reasoning-first
// fix: each assistant message's Seq (its pre-tool reasoning) is before its
// call, while its LastSeq (its visible text end) is after it. So a turn is
// recorded interleaved as user < answer.Seq < tool < answer.LastSeq < next
// user, and the Chat view can place the message by LastSeq.
func assertLastSeqInterleaving(t *testing.T, snap *state.Snapshot) {
	t.Helper()
	if len(snap.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (two turns)", len(snap.Messages))
	}
	user1, answer1, user2, answer2 := snap.Messages[0], snap.Messages[1], snap.Messages[2], snap.Messages[3]
	if user1.Role != state.RoleUser || answer1.Role != state.RoleAssistant ||
		user2.Role != state.RoleUser || answer2.Role != state.RoleAssistant {
		t.Fatalf("message roles = %q,%q,%q,%q", user1.Role, answer1.Role, user2.Role, answer2.Role)
	}
	for i := 1; i < len(snap.Messages); i++ {
		if snap.Messages[i].Seq <= snap.Messages[i-1].Seq {
			t.Fatalf("message Seqs not strictly increasing: %+v", snap.Messages)
		}
	}
	tool1, ok := snap.Tools["call-1"]
	if !ok {
		t.Fatalf("turn-1 tool call-1 missing: %+v", snap.Tools)
	}
	tool2, ok := snap.Tools["call-2"]
	if !ok {
		t.Fatalf("turn-2 tool call-2 missing: %+v", snap.Tools)
	}
	if !(user1.Seq < answer1.Seq && answer1.Seq < tool1.StartedSeq && tool1.StartedSeq < answer1.LastSeq) {
		t.Fatalf("turn 1 not interleaved: user=%d answer.Seq=%d tool=%d answer.LastSeq=%d",
			user1.Seq, answer1.Seq, tool1.StartedSeq, answer1.LastSeq)
	}
	if !(answer1.LastSeq < user2.Seq && user2.Seq < answer2.Seq &&
		answer2.Seq < tool2.StartedSeq && tool2.StartedSeq < answer2.LastSeq) {
		t.Fatalf("turn 2 not interleaved: answer1.LastSeq=%d user2=%d answer2.Seq=%d tool2=%d answer2.LastSeq=%d",
			answer1.LastSeq, user2.Seq, answer2.Seq, tool2.StartedSeq, answer2.LastSeq)
	}
	// A user message is its own visible row: LastSeq equals its Seq.
	if user1.LastSeq != user1.Seq || user2.LastSeq != user2.Seq {
		t.Fatalf("user LastSeq = %d/%d, want Seq %d/%d",
			user1.LastSeq, user2.LastSeq, user1.Seq, user2.Seq)
	}
	for _, m := range []state.Message{answer1, answer2} {
		if m.Text == "" {
			t.Fatalf("assistant answer %q has no visible text", m.MsgID)
		}
	}
}

// TestMessageLastSeqAfterReopen pins the LastSeq recovery on both the live fold
// axis and the reopened store-event axis: a reopened snapshot must remember
// where each assistant message's visible text ended, not just where it was
// created by the pre-tool reasoning.
func TestMessageLastSeqAfterReopen(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: reasoningFirstRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "reasoning first")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	runTwoToolTurns(t, h.ctrl, id)

	live := h.ctrl.Snapshot()
	assertLastSeqInterleaving(t, live)
	if got := live.Messages[1].Seq; got != 3 {
		t.Fatalf("live turn-1 answer Seq = %d, want the pre-tool reasoning seq 3", got)
	}
	t.Logf("live: user1 Seq=%d answer1 Seq=%d LastSeq=%d tool1=%d",
		live.Messages[0].Seq, live.Messages[1].Seq, live.Messages[1].LastSeq, live.Tools["call-1"].StartedSeq)

	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	assertLastSeqInterleaving(t, snap)
	events, err := h.ctrl.Events(ctx, id)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	for i, text := range []string{"first answer", "second answer"} {
		answer := snap.Messages[1+2*i]
		if answer.Text != text {
			t.Fatalf("answer %d text = %q, want %q", i+1, answer.Text, text)
		}
		if want := uint64(eventLastSeqWith(t, events, session.EventToken, "msg_id", answer.MsgID)); answer.LastSeq != want {
			t.Fatalf("answer %q LastSeq = %d, want its last token event seq %d", text, answer.LastSeq, want)
		}
		if want := uint64(eventSeqWith(t, events, session.EventReasoning, "msg_id", answer.MsgID)); answer.Seq != want {
			t.Fatalf("answer %q Seq = %d, want its first reasoning event seq %d", text, answer.Seq, want)
		}
	}
	if snap.Seq < snap.Messages[3].LastSeq {
		t.Fatalf("reopened snapshot Seq = %d is behind the last visible text %d; a continued turn would reuse the axis",
			snap.Seq, snap.Messages[3].LastSeq)
	}
	t.Logf("reopened: Seq=%d messages=%+v", snap.Seq, snap.Messages)
}
